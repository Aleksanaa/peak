package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
)

// colorSpan marks a rune range with a named syntax attribute.
type colorSpan struct {
	q0, q1 int
	attr   string
}

// ---- event subscription ----

// eventSub is one reader's subscription to a window's event stream.
// ReadAt blocks until data arrives; deliveries never fail silently.
type eventSub struct {
	mu   sync.Mutex
	cond *sync.Cond
	buf  []byte
	done bool
}

func newEventSub() *eventSub {
	s := &eventSub{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *eventSub) deliver(line []byte) {
	s.mu.Lock()
	if !s.done {
		s.buf = append(s.buf, line...)
		s.cond.Signal()
	}
	s.mu.Unlock()
}

func (s *eventSub) readAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for int64(len(s.buf)) <= off && !s.done {
		s.cond.Wait()
	}
	if int64(len(s.buf)) <= off {
		return 0, io.EOF
	}
	n := copy(p, s.buf[off:])
	return n, nil
}

func (s *eventSub) close() {
	s.mu.Lock()
	s.done = true
	s.cond.Broadcast()
	s.mu.Unlock()
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
	return f.sub.readAt(p, off)
}

func (f *winEventFile) Close() error {
	f.win.unsubscribeEvent(f.sub)
	f.sub.close()
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
	f.win.editor.Call(func() {
		buf := f.win.body.GetBuffer()
		q0, q1, err := parseAddr(s, buf)
		if err == nil {
			f.win.addrQ0 = clampAddr(q0, buf)
			f.win.addrQ1 = clampAddr(q1, buf)
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
	text := string(f.writes)
	f.win.editor.Call(func() {
		buf := f.win.body.GetBuffer()
		if buf == nil {
			return
		}
		runes := []rune(text)
		buf.ReplaceRangeRunes(f.win.addrQ0, f.win.addrQ1, runes)
		f.win.addrQ1 = f.win.addrQ0 + len(runes)
	})
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
	f.win.spansMu.Lock()
	f.win.spans = newSpans
	f.win.spansMu.Unlock()
	return nil
}

func (f *winColorFile) Write(p []byte) (int, error)       { return f.WriteAt(p, 0) }
func (f *winColorFile) WriteString(s string) (int, error) { return f.WriteAt([]byte(s), 0) }
