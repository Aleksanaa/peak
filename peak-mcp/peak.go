package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
)

// peakEvent is one line of peak's global /event stream.
type peakEvent struct {
	Kind string // new, close, focus, get, put
	ID   int
	Name string
}

// peakConn is peak-mcp's view of a running peak: a 9P client connection plus
// the window state it tracks by following /event.
type peakConn struct {
	fs afero.Fs

	mu      sync.Mutex
	focused int
	subs    map[chan peakEvent]struct{}
}

// dialPeak connects to peak's 9P socket, retrying until wait elapses. peak-mcp
// starts alongside peak and can lose the race, so a refused connection is
// expected rather than fatal.
func dialPeak(socket string, wait time.Duration) (*peakConn, error) {
	deadline := time.Now().Add(wait)
	for {
		fs, err := vfs.NewNinePClientFs("unix", socket)
		if err == nil {
			return &peakConn{fs: fs, subs: make(map[chan peakEvent]struct{})}, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("connect to peak at %s: %w", socket, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// Watch follows /event until the stream ends, updating the focused window and
// fanning each event out to subscribers. It blocks; run it in a goroutine.
func (p *peakConn) Watch() error {
	f, err := p.fs.Open("/event")
	if err != nil {
		return fmt.Errorf("open /event: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.SplitN(strings.TrimSpace(sc.Text()), " ", 3)
		if len(fields) < 2 {
			continue
		}
		id, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		ev := peakEvent{Kind: fields[0], ID: id}
		if len(fields) == 3 {
			ev.Name = fields[2]
		}

		p.mu.Lock()
		switch ev.Kind {
		case "focus":
			p.focused = ev.ID
		case "close":
			if p.focused == ev.ID {
				p.focused = 0
			}
		}
		subs := make([]chan peakEvent, 0, len(p.subs))
		for ch := range p.subs {
			subs = append(subs, ch)
		}
		p.mu.Unlock()

		for _, ch := range subs {
			// Non-blocking: a subscriber that has stopped reading must not
			// stall the event stream for everyone else.
			select {
			case ch <- ev:
			default:
			}
		}
	}
	return sc.Err()
}

// Subscribe returns a channel of events and a function that stops it.
func (p *peakConn) Subscribe() (<-chan peakEvent, func()) {
	ch := make(chan peakEvent, 32)
	p.mu.Lock()
	p.subs[ch] = struct{}{}
	p.mu.Unlock()
	return ch, func() {
		p.mu.Lock()
		delete(p.subs, ch)
		p.mu.Unlock()
	}
}

// Focused returns the id of the window peak last reported as focused, or 0
// before any focus event has arrived.
func (p *peakConn) Focused() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.focused
}

// winInfo is one line of /index.
type winInfo struct {
	ID    int
	Tag   string
	Name  string
	Dirty bool
	IsDir bool
}

// indexTagOffset is where the tag starts on an /index line: five "%11d "
// fields, matching acme's format.
const indexTagOffset = 5 * 12

// Index returns every open window, in peak's own order.
func (p *peakConn) Index() ([]winInfo, error) {
	data, err := afero.ReadFile(p.fs, "/index")
	if err != nil {
		return nil, fmt.Errorf("read /index: %w", err)
	}
	var out []winInfo
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		id, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		tag := strings.TrimSpace(line[min(len(line), indexTagOffset):])
		out = append(out, winInfo{
			ID:    id,
			Tag:   tag,
			Name:  tagFilename(tag),
			IsDir: fields[3] == "1",
			Dirty: fields[4] == "1",
		})
	}
	return out, nil
}

// FindWindow returns the window editing path, or false when none is open.
func (p *peakConn) FindWindow(path string) (winInfo, bool) {
	wins, err := p.Index()
	if err != nil {
		return winInfo{}, false
	}
	for _, w := range wins {
		if w.Name == path {
			return w, true
		}
	}
	return winInfo{}, false
}

// tagFilename returns the filename a window tag starts with. peak tags read
// " <name> Get Put ... ", so the name is the first space-separated word.
func tagFilename(tag string) string {
	if i := strings.IndexByte(tag, '\n'); i >= 0 {
		tag = tag[:i]
	}
	if i := strings.Index(tag, " |"); i >= 0 {
		tag = tag[:i]
	}
	fields := strings.Fields(tag)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func (p *peakConn) path(id int, file string) string {
	return "/" + strconv.Itoa(id) + "/" + file
}

// Body returns a window's body text.
func (p *peakConn) Body(id int) (string, error) {
	b, err := afero.ReadFile(p.fs, p.path(id, "body"))
	return string(b), err
}

// SetBody replaces a window's body. The window is left dirty; peak writes
// nothing to disk until the file is Put.
func (p *peakConn) SetBody(id int, text string) error {
	return p.write(p.path(id, "body"), text)
}

// Tag returns a window's tag text.
func (p *peakConn) Tag(id int) (string, error) {
	b, err := afero.ReadFile(p.fs, p.path(id, "tag"))
	return string(b), err
}

// SetTag replaces a window's tag text.
func (p *peakConn) SetTag(id int, text string) error {
	return p.write(p.path(id, "tag"), text)
}

// Ctl runs an editor command in the window's context, as if it had been
// middle-clicked in that window's tag.
func (p *peakConn) Ctl(id int, cmd string) error {
	return p.write(p.path(id, "ctl"), cmd)
}

// Selection returns the text selected in a window, empty when there is none.
func (p *peakConn) Selection(id int) (string, error) {
	b, err := afero.ReadFile(p.fs, p.path(id, "rdsel"))
	return string(b), err
}

// Addr returns the window's current address as rune offsets.
func (p *peakConn) Addr(id int) (q0, q1 int, err error) {
	b, err := afero.ReadFile(p.fs, p.path(id, "addr"))
	if err != nil {
		return 0, 0, err
	}
	// "#q0,#q1"
	text := strings.TrimSpace(string(b))
	before, after, ok := strings.Cut(text, ",")
	if !ok {
		return 0, 0, fmt.Errorf("addr: unexpected %q", text)
	}
	q0, err0 := strconv.Atoi(strings.TrimPrefix(before, "#"))
	q1, err1 := strconv.Atoi(strings.TrimPrefix(after, "#"))
	if err0 != nil || err1 != nil {
		return 0, 0, fmt.Errorf("addr: unexpected %q", text)
	}
	return q0, q1, nil
}

// SetAddr sets the window's address range in rune offsets.
func (p *peakConn) SetAddr(id, q0, q1 int) error {
	return p.write(p.path(id, "addr"), fmt.Sprintf("#%d,#%d", q0, q1))
}

// NewWindow creates an empty window and returns its id. Walking /new is what
// creates it; reading ctl reports the id peak assigned.
func (p *peakConn) NewWindow() (int, error) {
	b, err := afero.ReadFile(p.fs, "/new/ctl")
	if err != nil {
		return 0, fmt.Errorf("create window: %w", err)
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0, fmt.Errorf("create window: empty ctl")
	}
	id, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, fmt.Errorf("create window: ctl says %q", fields[0])
	}
	return id, nil
}

// write replaces a control file's contents. peak applies the write on close,
// so the close error is the one that matters.
func (p *peakConn) write(path, text string) error {
	f, err := p.fs.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}
