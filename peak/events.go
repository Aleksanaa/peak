package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/wevent"
)

// colorSpan marks a rune range with a named syntax attribute.
type colorSpan struct {
	q0, q1 int
	attr   string
}

// ---- event subscription ----

// eventSub is one reader's subscription to an event stream.
// deliver sends chunks to a buffered channel; readAt drains the channel
// into a local accumulator and serves from it by offset. The accumulator
// is only ever accessed by the single 9P goroutine calling readAt.
type eventSub struct {
	ch  chan []byte
	buf []byte // accumulated bytes; goroutine-local to the readAt caller
}

// deliver sends a record chunk to the subscriber. Non-blocking: if the channel is
// full the chunk is dropped (peak-lsp will re-sync from the next event).
func (s *eventSub) deliver(chunk []byte) {
	select {
	case s.ch <- chunk:
	default:
	}
}

// readAt blocks until enough bytes have arrived to serve offset off,
// then copies into p. Returns io.EOF when the subscription is closed
// and no more data is available at off.
func (s *eventSub) readAt(p []byte, off int64) (int, error) {
	for int64(len(s.buf)) <= off {
		chunk, ok := <-s.ch
		if !ok {
			return 0, io.EOF
		}
		s.buf = append(s.buf, chunk...)
	}
	return copy(p, s.buf[off:]), nil
}

// ---- eventBus ----

// eventBus fans events out to its subscribers: the readers of /peak/event,
// or of a window's event file. Events come from the main goroutine and
// subscriptions from 9P's, so it keeps a lock of its own.
type eventBus struct {
	mu   sync.Mutex
	subs []*eventSub
}

func (b *eventBus) subscribe() *eventSub {
	s := &eventSub{ch: make(chan []byte, 64)}
	b.mu.Lock()
	b.subs = append(b.subs, s)
	b.mu.Unlock()
	return s
}

// unsubscribe removes s and ends its stream.
func (b *eventBus) unsubscribe(s *eventSub) {
	b.mu.Lock()
	b.subs = slices.DeleteFunc(b.subs, func(x *eventSub) bool { return x == s })
	b.mu.Unlock()
	close(s.ch)
}

func (b *eventBus) broadcast(event []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.subs {
		s.deliver(event)
	}
}

// ---- winEventFile ----

func newWinEventFile(win *Window, flag int) *winEventFile {
	f := &winEventFile{win: win}
	if flag&os.O_WRONLY == 0 {
		f.sub = win.events.subscribe()
	}
	return f
}

type winEventFile struct {
	vfs.FileStub
	win *Window
	sub *eventSub // nil when opened only to write
}

func (f *winEventFile) ReadAt(p []byte, off int64) (int, error) {
	if f.sub == nil {
		return 0, io.EOF
	}
	return f.sub.readAt(p, off)
}

// WriteAt handles event bounce-back. It accepts counted v2 records only.
func (f *winEventFile) WriteAt(p []byte, _ int64) (int, error) {
	r := bytes.NewReader(p)
	for {
		posBefore := len(p) - r.Len()
		ev, err := wevent.Read(r)
		if err == io.EOF {
			return len(p), nil
		}
		if err != nil {
			return posBefore, err
		}
		if err := f.dispatchWriteEvent(ev); err != nil {
			return posBefore, err
		}
	}
}

func (f *winEventFile) dispatchWriteEvent(ev wevent.Event) error {
	win := f.win
	switch ev.Type {
	case 'x':
		col, text := win.parent, ev.Text
		win.editor.callCh <- func() { win.editor.Execute(col, win, text) }
	case 'l':
		text := ev.Text
		win.editor.callCh <- func() { win.editor.Plumb(win, text) }
	}
	return nil
}

func (f *winEventFile) Close() error {
	if f.sub != nil {
		win := f.win
		win.events.unsubscribe(f.sub)
		win.editor.Call(func() { win.spans = nil })
	}
	return nil
}

// ---- winAddrFile ----

func newWinAddrFile(win *Window, flag int) *winAddrFile {
	f := &winAddrFile{win: win}
	if flag&os.O_WRONLY == 0 {
		win.editor.Call(func() { f.Data = fmt.Appendf(nil, "#%d,#%d\n", win.addrQ0, win.addrQ1) })
	}
	return f
}

type winAddrFile struct {
	vfs.ReadWriteFile
	win *Window
}

func (f *winAddrFile) Close() error {
	if f.Writes == nil {
		return nil
	}
	s := strings.TrimSpace(string(f.Writes))
	win := f.win
	win.editor.Call(func() {
		buf := win.body.GetBuffer()
		if q0, q1, err := parseAddr(s, buf); err == nil {
			win.addrQ0 = clampAddr(q0, buf)
			win.addrQ1 = clampAddr(q1, buf)
		}
	})
	return nil
}

// parseAddr parses an address expression like "#n", "#n,#n", or "n" (line number).
func parseAddr(s string, buf *Buffer) (q0, q1 int, err error) {
	parts := strings.SplitN(s, ",", 2)
	q0, err = parseAddrOne(strings.TrimSpace(parts[0]), buf)
	if err != nil {
		return
	}
	if len(parts) == 2 {
		q1, err = parseAddrOne(strings.TrimSpace(parts[1]), buf)
	} else {
		q1 = q0
	}
	return
}

func parseAddrOne(s string, buf *Buffer) (int, error) {
	if strings.HasPrefix(s, "#") {
		n, err := strconv.Atoi(s[1:])
		return n, err
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid address: %s", s)
	}
	// Line number (1-based) → rune offset of line start
	n-- // convert to 0-based
	if n < 0 {
		n = 0
	}
	if n >= len(buf.lines) {
		n = len(buf.lines) - 1
	}
	return buf.Offset(n, 0), nil
}

func clampAddr(q int, buf *Buffer) int {
	n := buf.Len()
	if q < 0 {
		return 0
	}
	if q > n {
		return n
	}
	return q
}

// ---- winDataFile ----

func newWinDataFile(win *Window, flag int) *winDataFile {
	f := &winDataFile{win: win}
	if flag&os.O_WRONLY == 0 {
		win.editor.Call(func() { f.Data = []byte(string(win.body.GetBuffer().RunesInRange(win.addrQ0, win.addrQ1))) })
	}
	return f
}

type winDataFile struct {
	vfs.ReadWriteFile
	win *Window
}

func (f *winDataFile) Close() error {
	if f.Writes == nil {
		return nil
	}
	win := f.win
	if _, ok := win.body.(*TermView); ok {
		return nil
	}
	runes := []rune(string(f.Writes))
	win.editor.Call(func() {
		win.body.GetBuffer().ReplaceRangeRunes(win.addrQ0, win.addrQ1, runes)
		win.addrQ1 = win.addrQ0 + len(runes)
	})
	return nil
}

// ---- winColorFile ----

// winColorFile accumulates all written bytes in buf and atomically replaces
// the window's color spans on Close. Buffering until Close avoids partial-state
// visibility during chunked 9P writes; adjustSpans keeps existing spans aligned
// with edits in the meantime, so late-arriving spans just overwrite cleanly.
type winColorFile struct {
	vfs.FileStub
	win *Window
	buf strings.Builder
}

func (f *winColorFile) WriteAt(p []byte, _ int64) (int, error) {
	f.buf.Write(p)
	return len(p), nil
}

// Close parses the accumulated "q0 q1 attr\n" lines and replaces the
// window's spans atomically. An empty write clears all spans.
func (f *winColorFile) Close() error {
	var newSpans []colorSpan
	for line := range strings.SplitSeq(f.buf.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		q0, err0 := strconv.Atoi(parts[0])
		q1, err1 := strconv.Atoi(parts[1])
		if err0 != nil || err1 != nil {
			continue
		}
		newSpans = append(newSpans, colorSpan{q0, q1, parts[2]})
	}
	sort.SliceStable(newSpans, func(i, j int) bool { return newSpans[i].q0 < newSpans[j].q0 })
	f.win.editor.Call(func() { f.win.spans = newSpans })
	return nil
}
