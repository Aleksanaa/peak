package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ---- helpers ----

func setupWindowTest(t *testing.T) (*Editor, *Column, *Window, tcell.SimulationScreen) {
	t.Helper()
	e, s := setupTest(t, 120, 30)
	col := NewColumn(0, 1, e.width, e.height-1, e, e.Execute)
	e.columns = append(e.columns, col)
	win := col.AddWindow(" /tmp/test.txt Get Put Del ", "hello world\n")
	e.ActivateWindow(win)
	e.Resize()
	return e, col, win, s
}

// readAll reads the entire content of an afero file opened on wfs.
func readAll(t *testing.T, wfs *windowFs, name string) string {
	t.Helper()
	f, err := wfs.Open(name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer f.Close()
	buf := new(bytes.Buffer)
	tmp := make([]byte, 512)
	var off int64
	for {
		n, err := f.ReadAt(tmp, off)
		if n > 0 {
			buf.Write(tmp[:n])
			off += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
	}
	return buf.String()
}

// writeClose opens name for writing, writes p, and closes.
func writeClose(t *testing.T, wfs *windowFs, name string, p string) {
	t.Helper()
	f, err := wfs.OpenFile(name, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s for write: %v", name, err)
	}
	if _, err := f.WriteString(p); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", name, err)
	}
}

// eventReader reads successive lines from an eventSub, tracking offset between calls.
type eventReader struct {
	sub *eventSub
	off int64
	acc strings.Builder
}

// ReadLine reads one newline-terminated line with a timeout.
// Returns ("", false) if the deadline expires before a full line arrives.
func (r *eventReader) ReadLine(timeout time.Duration) (string, bool) {
	lineCh := make(chan string, 1)
	startOff := r.off
	startAcc := r.acc.String()
	go func() {
		buf := make([]byte, 4096)
		off := startOff
		var acc strings.Builder
		acc.WriteString(startAcc)
		for {
			n, err := r.sub.readAt(buf, off)
			if n > 0 {
				acc.Write(buf[:n])
				off += int64(n)
				s := acc.String()
				if idx := strings.Index(s, "\n"); idx >= 0 {
					lineCh <- strings.TrimRight(s[:idx], "\r") + "|" + s[idx+1:]
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	select {
	case raw := <-lineCh:
		sep := strings.Index(raw, "|")
		line := raw[:sep]
		remainder := raw[sep+1:]
		r.off += int64(len(line)) + 1 // +1 for newline
		r.acc.Reset()
		r.acc.WriteString(remainder)
		return line, true
	case <-time.After(timeout):
		return "", false
	}
}

// ---- body ----

func TestWindowFsBodyRead(t *testing.T) {
	_, _, win, _ := setupWindowTest(t)
	wfs := &windowFs{win: win}
	got := readAll(t, wfs, "body")
	if got != "hello world\n" {
		t.Errorf("body = %q, want %q", got, "hello world\n")
	}
}

func TestWindowFsBodyWrite(t *testing.T) {
	e, _, win, _ := setupWindowTest(t)
	wfs := &windowFs{win: win}
	writeClose(t, wfs, "body", "new content")
	var got string
	e.Call(func() {
		got = win.body.GetBuffer().GetText()
	})
	if got != "new content" {
		t.Errorf("body after write = %q, want %q", got, "new content")
	}
}

// ---- tag ----

func TestWindowFsTagRead(t *testing.T) {
	_, _, win, _ := setupWindowTest(t)
	wfs := &windowFs{win: win}
	got := readAll(t, wfs, "tag")
	if !strings.Contains(got, "/tmp/test.txt") {
		t.Errorf("tag %q does not contain expected filename", got)
	}
}

func TestWindowFsTagWrite(t *testing.T) {
	e, _, win, _ := setupWindowTest(t)
	wfs := &windowFs{win: win}
	writeClose(t, wfs, "tag", " /tmp/other.txt Get Del ")
	var got string
	e.Call(func() {
		got = win.tag.buffer.GetText()
	})
	if got != " /tmp/other.txt Get Del " {
		t.Errorf("tag after write = %q", got)
	}
}

// ---- addr/data round-trip ----

func TestWindowFsAddrDataRoundTrip(t *testing.T) {
	e, _, win, _ := setupWindowTest(t)
	e.Call(func() {
		win.body.GetBuffer().SetText("abcde fghij\n")
	})
	wfs := &windowFs{win: win}

	// Set addr to rune offsets 0–5 ("abcde")
	writeClose(t, wfs, "addr", "#0,#5")

	var q0, q1 int
	e.Call(func() { q0, q1 = win.addrQ0, win.addrQ1 })
	if q0 != 0 || q1 != 5 {
		t.Errorf("addrQ0=%d addrQ1=%d, want 0,5", q0, q1)
	}

	got := readAll(t, wfs, "data")
	if got != "abcde" {
		t.Errorf("data = %q, want %q", got, "abcde")
	}
}

func TestWindowFsAddrLineNumber(t *testing.T) {
	e, _, win, _ := setupWindowTest(t)
	e.Call(func() {
		win.body.GetBuffer().SetText("line1\nline2\nline3\n")
	})
	wfs := &windowFs{win: win}

	// Address "2" means line 2, rune offset 6
	writeClose(t, wfs, "addr", "2")

	var q0 int
	e.Call(func() { q0 = win.addrQ0 })
	if q0 != 6 {
		t.Errorf("line 2 addr = %d, want 6", q0)
	}
}

func TestWindowFsAddrReadBack(t *testing.T) {
	e, _, win, _ := setupWindowTest(t)
	e.Call(func() {
		win.addrQ0 = 3
		win.addrQ1 = 7
	})
	wfs := &windowFs{win: win}
	got := strings.TrimSpace(readAll(t, wfs, "addr"))
	if got != "#3,#7" {
		t.Errorf("addr = %q, want %q", got, "#3,#7")
	}
}

// ---- ctl ----

func TestWindowFsCtlExec(t *testing.T) {
	_, col, win, _ := setupWindowTest(t)
	wfs := &windowFs{win: win}

	before := len(col.windows)
	writeClose(t, wfs, "ctl", "Del")
	after := len(col.windows)

	if after != before-1 {
		t.Errorf("after Del: %d windows, want %d", after, before-1)
	}
}

func TestWindowFsCtlRead(t *testing.T) {
	_, _, win, _ := setupWindowTest(t)
	wfs := &windowFs{win: win}

	// Direct windowFs path.
	got := readAll(t, wfs, "ctl")
	if got == "" {
		t.Fatal("ctl read returned empty string")
	}

	// Format: "<id> <taglen> <bodylen> <isdir> <isdirty> <width> terminal <maxtab>\n"
	var id, tagLen, bodyLen, isDir, isDirty, width, maxtab int
	var font string
	n, err := fmt.Sscanf(got, "%d %d %d %d %d %d %s %d",
		&id, &tagLen, &bodyLen, &isDir, &isDirty, &width, &font, &maxtab)
	if err != nil || n != 8 {
		t.Fatalf("ctl line %q: parsed %d fields, err %v", got, n, err)
	}
	if id != win.ID {
		t.Errorf("id: got %d, want %d", id, win.ID)
	}
	if font != "terminal" {
		t.Errorf("font: got %q, want %q", font, "terminal")
	}
	if maxtab != 4 {
		t.Errorf("maxtab: got %d, want %d", maxtab, 4)
	}
	if isDir != 0 {
		t.Errorf("isdir: got %d, want 0", isDir)
	}

	// Via readWinPath (the fast path peak uses when navigating to /peak/<id>/ctl internally).
	vfsPath := fmt.Sprintf("/peak/%d/ctl", win.ID)
	viaWin, _, err := readFileOrDir(vfsPath)
	if err != nil {
		t.Fatalf("readFileOrDir(%q): %v", vfsPath, err)
	}
	if viaWin != got {
		t.Errorf("readWinPath returned %q, want %q", viaWin, got)
	}
}

// ---- errors file ----

func TestWindowFsErrorsCreatesWindow(t *testing.T) {
	e, col, win, s := setupWindowTest(t)
	wfs := &windowFs{win: win}

	f, err := wfs.OpenFile("errors", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open errors: %v", err)
	}
	f.WriteString("something went wrong\n")
	f.Close()

	var errWin *Window
	waitFor(t, e, s, func() bool {
		for _, w := range col.windows {
			if strings.HasSuffix(w.GetFilename(), "+Errors") && w.bodyTextView() != nil {
				errWin = w
				return true
			}
		}
		return false
	})

	got := errWin.bodyTextView().buffer.GetText()
	if !strings.Contains(got, "something went wrong") {
		t.Errorf("errors window body = %q", got)
	}
}

func TestWindowFsErrorsAppends(t *testing.T) {
	e, col, win, s := setupWindowTest(t)
	wfs := &windowFs{win: win}

	write := func(msg string) {
		f, _ := wfs.OpenFile("errors", os.O_WRONLY, 0)
		f.WriteString(msg)
		f.Close()
	}

	write("first\n")
	waitFor(t, e, s, func() bool {
		for _, w := range col.windows {
			if strings.HasSuffix(w.GetFilename(), "+Errors") && w.bodyTextView() != nil {
				return true
			}
		}
		return false
	})

	write("second\n")
	waitFor(t, e, s, func() bool {
		for _, w := range col.windows {
			if strings.HasSuffix(w.GetFilename(), "+Errors") && w.bodyTextView() != nil {
				return strings.Contains(w.bodyTextView().buffer.GetText(), "second")
			}
		}
		return false
	})

	var got string
	for _, w := range col.windows {
		if strings.HasSuffix(w.GetFilename(), "+Errors") && w.bodyTextView() != nil {
			got = w.bodyTextView().buffer.GetText()
		}
	}
	if !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Errorf("errors window body = %q, want both 'first' and 'second'", got)
	}
}

func TestWindowFsErrorsSkipsTerminalWindow(t *testing.T) {
	e, col, _, s := setupWindowTest(t)

	// Create a terminal +Errors window for the same dir as /tmp/test.txt
	termWin, err := col.AddTermWindow(" /tmp/+Errors Zerox Del ", "sh", "/tmp")
	if err != nil {
		t.Skipf("cannot create term window: %v", err)
	}
	e.Resize()

	// Create a text window in /tmp to write errors from
	textWin := col.AddWindow(" /tmp/src.txt Get Put Del ", "content")

	wfs := &windowFs{win: textWin}
	f, _ := wfs.OpenFile("errors", os.O_WRONLY, 0)
	f.WriteString("error output\n")
	f.Close()

	var errWin *Window
	waitFor(t, e, s, func() bool {
		for _, w := range col.windows {
			if w != termWin && strings.HasSuffix(w.GetFilename(), "+Errors") && w.bodyTextView() != nil {
				errWin = w
				return true
			}
		}
		return false
	})

	if errWin == nil {
		t.Error("expected a text +Errors window to be created, got none")
	}
}

// ---- window event file ----

func TestWindowFsEventIDEvents(t *testing.T) {
	e, _, win, _ := setupWindowTest(t)
	wfs := &windowFs{win: win}

	f, err := wfs.OpenFile("event", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("open event: %v", err)
	}
	defer f.Close()

	ef := f.(*winEventFile)
	er := &eventReader{sub: ef.sub}

	// Trigger an insert via buffer edit on the main goroutine
	e.Call(func() {
		win.body.GetBuffer().SetTextInRange(
			win.body.GetBuffer().cursor,
			win.body.GetBuffer().cursor,
			"X",
		)
	})

	line, ok := er.ReadLine(2 * time.Second)
	if !ok {
		t.Fatal("timeout waiting for I event")
	}
	if !strings.HasPrefix(line, "I ") {
		t.Errorf("expected I event, got %q", line)
	}
}

func TestWindowFsEventWriteOnlyNoSub(t *testing.T) {
	_, _, win, _ := setupWindowTest(t)
	wfs := &windowFs{win: win}

	f, err := wfs.OpenFile("event", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open event write-only: %v", err)
	}
	defer f.Close()

	ef := f.(*winEventFile)
	if ef.sub != nil {
		t.Error("write-only open should not create a subscription")
	}
	if win.hasEventSubs() {
		t.Error("write-only open should not count as a subscriber")
	}
}

func TestWindowFsEventSuppression(t *testing.T) {
	e, col, win, _ := setupWindowTest(t)
	wfs := &windowFs{win: win}

	// Open event file (read) — this subscribes and suppresses x/l actions
	evF, err := wfs.OpenFile("event", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("open event: %v", err)
	}
	defer evF.Close()

	if !win.hasEventSubs() {
		t.Fatal("expected subscriber after opening event file")
	}

	executed := false
	win.onExec = func(_ *Column, _ *Window, _ string) bool {
		executed = true
		return true
	}

	// Simulate a middle-click execute via the window handler
	e.Call(func() {
		win.broadcastEvent('x', 0, 3, "Get")
		// When subscribers present, the editor should NOT call onExec itself
	})

	// Give any spurious call time to arrive
	time.Sleep(30 * time.Millisecond)

	if executed {
		t.Error("onExec was called despite active event subscriber (suppression failed)")
	}
	_ = col
}

func TestWindowFsEventBounceback(t *testing.T) {
	e, col, win, _ := setupWindowTest(t)
	wfs := &windowFs{win: win}

	evF, err := wfs.OpenFile("event", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open event rdwr: %v", err)
	}
	defer evF.Close()

	executed := false
	win.onExec = func(_ *Column, _ *Window, cmd string) bool {
		if cmd == "Get" {
			executed = true
		}
		return true
	}

	// Write an x event back — this re-dispatches it as if the tool decided to let the editor handle it
	evF.WriteString("x 0 3 Get\n")

	// editor.Call is synchronous — by the time it returns the dispatch has happened
	e.Call(func() {})

	if !executed {
		t.Error("onExec was not called after bounce-back write")
	}
	_ = col
}

// ---- global lifecycle events ----

func subscribeGlobal(e *Editor) *eventSub {
	return e.ninep.bus.subscribe()
}

func TestLifecycleEventsNewClose(t *testing.T) {
	e, s := setupTest(t, 120, 30)
	col := NewColumn(0, 1, e.width, e.height-1, e, e.Execute)
	e.columns = append(e.columns, col)
	e.Resize()
	_ = s

	sub := subscribeGlobal(e)
	defer e.ninep.bus.unsubscribe(sub)
	er := &eventReader{sub: sub}

	win := col.AddWindow(" /tmp/lifecycle.txt Get Put Del ", "")
	line, ok := er.ReadLine(2 * time.Second)
	if !ok {
		t.Fatal("timeout waiting for new event")
	}
	if !strings.HasPrefix(line, "new ") {
		t.Errorf("expected 'new' event, got %q", line)
	}
	if !strings.Contains(line, "/tmp/lifecycle.txt") {
		t.Errorf("new event missing filename: %q", line)
	}

	e.deleteWindow(win)
	line, ok = er.ReadLine(2 * time.Second)
	if !ok {
		t.Fatal("timeout waiting for close event")
	}
	if !strings.HasPrefix(line, "close ") {
		t.Errorf("expected 'close' event, got %q", line)
	}
	if !strings.Contains(line, "/tmp/lifecycle.txt") {
		t.Errorf("close event missing filename: %q", line)
	}
}

func TestLifecycleEventsFocus(t *testing.T) {
	e, s := setupTest(t, 120, 30)
	col := NewColumn(0, 1, e.width, e.height-1, e, e.Execute)
	e.columns = append(e.columns, col)
	e.Resize()
	_ = s

	win1 := col.AddWindow(" /tmp/a.txt Get Put Del ", "")
	win2 := col.AddWindow(" /tmp/b.txt Get Put Del ", "")

	sub := subscribeGlobal(e)
	defer e.ninep.bus.unsubscribe(sub)
	er := &eventReader{sub: sub}

	e.Call(func() {
		e.ActivateWindow(win1)
	})

	line, ok := er.ReadLine(2 * time.Second)
	if !ok {
		t.Fatal("timeout waiting for focus event")
	}
	if !strings.HasPrefix(line, "focus ") {
		t.Errorf("expected 'focus' event, got %q", line)
	}
	if !strings.Contains(line, "/tmp/a.txt") {
		t.Errorf("focus event wrong filename: %q", line)
	}
	_ = win2
}

func TestLifecycleEventsGetPut(t *testing.T) {
	// Write a temp file so Get can read it
	tmp, err := os.CreateTemp("", "peak-test-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	tmp.WriteString("file content\n")
	tmp.Close()
	defer os.Remove(tmp.Name())

	e, s := setupTest(t, 120, 30)
	col := NewColumn(0, 1, e.width, e.height-1, e, e.Execute)
	e.columns = append(e.columns, col)
	win := col.AddWindow(" "+tmp.Name()+" Get Put Del ", "")
	e.ActivateWindow(win)
	e.Resize()

	sub := subscribeGlobal(e)
	defer e.ninep.bus.unsubscribe(sub)
	er := &eventReader{sub: sub}

	// cmdGet runs async (PostEvent); drive the event loop until we see the 'get' event.
	e.Call(func() { e.cmdGet(win, "Get") })

	var getLine string
	waitFor(t, e, s, func() bool {
		l, ok := er.ReadLine(50 * time.Millisecond)
		if ok {
			getLine = l
		}
		return strings.HasPrefix(getLine, "get ")
	})
	if !strings.Contains(getLine, tmp.Name()) {
		t.Errorf("get event missing filename: %q", getLine)
	}

	e.Call(func() { e.cmdPut(win, "Put") })

	var putLine string
	waitFor(t, e, s, func() bool {
		l, ok := er.ReadLine(50 * time.Millisecond)
		if ok {
			putLine = l
		}
		return strings.HasPrefix(putLine, "put ")
	})
	if !strings.Contains(putLine, tmp.Name()) {
		t.Errorf("put event missing filename: %q", putLine)
	}
}

// ---- findOrCreateErrorWindow ----

func TestFindOrCreateErrorWindowReuse(t *testing.T) {
	e, col, win, _ := setupWindowTest(t)

	// First call creates the window
	var w1, w2 *Window
	e.Call(func() {
		w1 = e.findOrCreateErrorWindow(col, win, "")
	})
	if w1 == nil {
		t.Fatal("findOrCreateErrorWindow returned nil")
	}

	// Second call should return the same window
	e.Call(func() {
		w2 = e.findOrCreateErrorWindow(col, win, "")
	})
	if w1 != w2 {
		t.Error("findOrCreateErrorWindow created a duplicate instead of reusing")
	}
}

func TestFindOrCreateErrorWindowSkipsTerminal(t *testing.T) {
	e, col, _, _ := setupWindowTest(t)

	// Pre-create a terminal window named /tmp/+Errors
	termWin, err := col.AddTermWindow(" /tmp/+Errors Zerox Del ", "sh", "/tmp")
	if err != nil {
		t.Skipf("cannot create term window: %v", err)
	}
	e.Resize()

	// Create a source text window in /tmp
	srcWin := col.AddWindow(" /tmp/src.txt Get Put Del ", "")

	var errWin *Window
	e.Call(func() {
		errWin = e.findOrCreateErrorWindow(col, srcWin, "")
	})

	if errWin == nil {
		t.Fatal("expected a text error window, got nil")
	}
	if errWin == termWin {
		t.Error("findOrCreateErrorWindow returned terminal window instead of creating a text one")
	}
	if errWin.bodyTextView() == nil {
		t.Error("returned error window has no text view")
	}
}

// ---- addr parse correctness ----

func TestAddrParseRuneVsByte(t *testing.T) {
	e, _, win, _ := setupWindowTest(t)
	// "héllo" — 'é' is 2 bytes but 1 rune; rune offset 3 = 'l', byte offset 3 = second byte of 'é'
	e.Call(func() {
		win.body.GetBuffer().SetText("héllo world\n")
	})
	wfs := &windowFs{win: win}
	writeClose(t, wfs, "addr", "#0,#5")

	got := readAll(t, wfs, "data")
	if got != "héllo" {
		t.Errorf("data with non-ASCII: got %q, want %q", got, "héllo")
	}
}

// ---- event file directory listing ----

func TestWindowFsDirListing(t *testing.T) {
	_, _, win, _ := setupWindowTest(t)
	wfs := &windowFs{win: win}
	f, err := wfs.Open(".")
	if err != nil {
		t.Fatalf("open dir: %v", err)
	}
	defer f.Close()
	infos, err := f.Readdir(-1)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	names := make(map[string]bool)
	for _, fi := range infos {
		names[fi.Name()] = true
	}
	for _, want := range []string{"body", "tag", "ctl", "event", "addr", "data", "errors", "color"} {
		if !names[want] {
			t.Errorf("missing %q from window dir listing", want)
		}
	}
}

// ---- event scanner integration (like peak-lsp/peak-git use) ----

func TestEventScannerIntegration(t *testing.T) {
	e, s := setupTest(t, 120, 30)
	col := NewColumn(0, 1, e.width, e.height-1, e, e.Execute)
	e.columns = append(e.columns, col)
	e.Resize()
	_ = s

	sub := subscribeGlobal(e)
	defer e.ninep.bus.unsubscribe(sub)

	// Wrap sub in a pipe-like reader so bufio.Scanner works
	pr, pw := io.Pipe()
	go func() {
		buf := make([]byte, 4096)
		var off int64
		for {
			n, err := sub.readAt(buf, off)
			if n > 0 {
				pw.Write(buf[:n])
				off += int64(n)
			}
			if err != nil {
				pw.Close()
				return
			}
		}
	}()

	received := make(chan string, 10)
	go func() {
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			received <- sc.Text()
		}
	}()

	win := col.AddWindow(" /tmp/scanner.txt Get Put Del ", "")

	select {
	case line := <-received:
		parts := strings.Fields(line)
		if len(parts) < 2 || parts[0] != "new" {
			t.Errorf("scanner got %q, want 'new <id> ...'", line)
		}
		if len(parts) < 3 || !strings.Contains(parts[2], "scanner.txt") {
			t.Errorf("new event missing filename: %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for new event via scanner")
	}

	e.deleteWindow(win)
	select {
	case line := <-received:
		if !strings.HasPrefix(line, "close ") {
			t.Errorf("expected 'close' event, got %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for close event via scanner")
	}

	sub.close()
}
