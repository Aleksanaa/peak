package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/aleksana/peak/internal/wevent"
)

// colorSpan marks a rune range with a named syntax attribute.
type colorSpan struct {
	q0, q1 int
	attr   string
}

// ---- event subscription ----

// eventSub is one reader's subscription to a window's event stream.
// deliver sends chunks to a buffered channel; readAt drains the channel
// into a local accumulator and serves from it by offset. The accumulator
// is only ever accessed by the single 9P goroutine calling readAt.
type eventSub struct {
	ch   chan []byte
	buf  []byte // accumulated bytes; goroutine-local to the readAt caller
	once sync.Once
}

func newEventSub() *eventSub {
	return &eventSub{ch: make(chan []byte, 64)}
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
			if int64(len(s.buf)) <= off {
				return 0, io.EOF
			}
			break
		}
		s.buf = append(s.buf, chunk...)
	}
	if int64(len(s.buf)) <= off {
		return 0, io.EOF
	}
	n := copy(p, s.buf[off:])
	return n, nil
}

func (s *eventSub) close() {
	s.once.Do(func() { close(s.ch) })
}

// ---- globalEventBus ----

// globalEventBus fans out editor-wide lifecycle events to all open /event readers.
// Events are lines like "new 5\n", "close 3\n".
type globalEventBus struct {
	mu   sync.Mutex
	subs []*eventSub
}

func (b *globalEventBus) subscribe() *eventSub {
	s := newEventSub()
	b.mu.Lock()
	b.subs = append(b.subs, s)
	b.mu.Unlock()
	return s
}

func (b *globalEventBus) unsubscribe(s *eventSub) {
	b.mu.Lock()
	for i, sub := range b.subs {
		if sub == s {
			b.subs = append(b.subs[:i], b.subs[i+1:]...)
			break
		}
	}
	b.mu.Unlock()
}

func (b *globalEventBus) broadcast(line string) {
	msg := []byte(line)
	b.mu.Lock()
	subs := make([]*eventSub, len(b.subs))
	copy(subs, b.subs)
	b.mu.Unlock()
	for _, s := range subs {
		s.deliver(msg)
	}
}

// ---- winEventFile ----

type winEventFile struct {
	winStub
	win *Window
	sub *eventSub
}

func (f *winEventFile) Name() string { return "event" }

func (f *winEventFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: "event", mode: 0644}, nil
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
		win.editor.execCh <- execReq{col: win.parent, win: win, text: ev.Text, kind: 'x'}
	case 'l':
		win.editor.execCh <- execReq{win: win, text: ev.Text, kind: 'l'}
	}
	return nil
}

func (f *winEventFile) Write(p []byte) (int, error)       { return f.WriteAt(p, 0) }
func (f *winEventFile) WriteString(s string) (int, error) { return f.WriteAt([]byte(s), 0) }

func (f *winEventFile) Close() error {
	if f.sub != nil {
		f.win.unsubscribeEvent(f.sub)
		f.sub.close()
		f.win.lk.Lock()
		f.win.spans = nil
		f.win.spansVersion++
		f.win.lk.Unlock()
	}
	return nil
}

// ---- winAddrFile ----

type winAddrFile struct {
	winStub
	win    *Window
	snap   []byte
	writes []byte
}

func (f *winAddrFile) Name() string { return "addr" }

func (f *winAddrFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: "addr", mode: 0644, size: int64(len(f.snap))}, nil
}

func (f *winAddrFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(f.snap)) {
		return 0, io.EOF
	}
	n := copy(p, f.snap[off:])
	if off+int64(n) >= int64(len(f.snap)) {
		return n, io.EOF
	}
	return n, nil
}

func (f *winAddrFile) WriteAt(p []byte, off int64) (int, error) {
	end := int(off) + len(p)
	if end > len(f.writes) {
		f.writes = append(f.writes, make([]byte, end-len(f.writes))...)
	}
	copy(f.writes[off:], p)
	return len(p), nil
}

func (f *winAddrFile) Write(p []byte) (int, error)       { return f.WriteAt(p, 0) }
func (f *winAddrFile) WriteString(s string) (int, error) { return f.WriteAt([]byte(s), 0) }

func (f *winAddrFile) Close() error {
	if f.writes == nil {
		return nil
	}
	s := strings.TrimSpace(string(f.writes))
	f.win.lk.Lock()
	buf := f.win.body.GetBuffer()
	q0, q1, err := parseAddr(s, buf)
	if err == nil {
		f.win.addrQ0 = clampAddr(q0, buf)
		f.win.addrQ1 = clampAddr(q1, buf)
	}
	f.win.lk.Unlock()
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
	if buf == nil {
		return 0, nil
	}
	if n < 0 {
		n = 0
	}
	if n >= buf.LineCount() {
		n = buf.LineCount() - 1
	}
	return buf.RuneOffsetOfPos(n, 0), nil
}

func clampAddr(q int, buf *Buffer) int {
	if buf == nil {
		return 0
	}
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

type winDataFile struct {
	winStub
	win    *Window
	snap   []byte
	writes []byte
}

func (f *winDataFile) Name() string { return "data" }

func (f *winDataFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: "data", mode: 0644, size: int64(len(f.snap))}, nil
}

func (f *winDataFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(f.snap)) {
		return 0, io.EOF
	}
	n := copy(p, f.snap[off:])
	if off+int64(n) >= int64(len(f.snap)) {
		return n, io.EOF
	}
	return n, nil
}

func (f *winDataFile) WriteAt(p []byte, off int64) (int, error) {
	end := int(off) + len(p)
	if end > len(f.writes) {
		f.writes = append(f.writes, make([]byte, end-len(f.writes))...)
	}
	copy(f.writes[off:], p)
	return len(p), nil
}

func (f *winDataFile) Write(p []byte) (int, error)       { return f.WriteAt(p, 0) }
func (f *winDataFile) WriteString(s string) (int, error) { return f.WriteAt([]byte(s), 0) }

func (f *winDataFile) Close() error {
	if f.writes == nil {
		return nil
	}
	runes := []rune(string(f.writes))
	var modified bool
	f.win.lk.Lock()
	if buf := f.win.body.GetBuffer(); buf != nil {
		buf.ReplaceRangeRunes(f.win.addrQ0, f.win.addrQ1, runes)
		f.win.addrQ1 = f.win.addrQ0 + len(runes)
		modified = true
	}
	f.win.lk.Unlock()
	if modified {
		f.win.editor.Redraw()
	}
	return nil
}

// ---- winColorFile ----

// winColorFile accumulates all written bytes in buf and atomically replaces
// the window's color spans on Close. This avoids both partial-state visibility
// during chunked 9P writes and the white-flash caused by clearing spans on
// every mutation.
type winColorFile struct {
	winStub
	win *Window
	buf strings.Builder
}

func (f *winColorFile) Name() string { return "color" }

func (f *winColorFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: "color", mode: 0200}, nil
}

func (f *winColorFile) WriteAt(p []byte, _ int64) (int, error) {
	f.buf.Write(p)
	return len(p), nil
}

// Close parses the accumulated "q0 q1 attr\n" lines and replaces the
// window's spans atomically. An empty write clears all spans.
func (f *winColorFile) Close() error {
	var newSpans []colorSpan
	for _, line := range strings.Split(f.buf.String(), "\n") {
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
	f.win.lk.Lock()
	if f.win.mutSeq == f.win.bodySnapSeq {
		f.win.spans = newSpans
		f.win.spansVersion++
	}
	f.win.lk.Unlock()
	f.win.editor.Redraw()
	return nil
}

func (f *winColorFile) Write(p []byte) (int, error)       { return f.WriteAt(p, 0) }
func (f *winColorFile) WriteString(s string) (int, error) { return f.WriteAt([]byte(s), 0) }
