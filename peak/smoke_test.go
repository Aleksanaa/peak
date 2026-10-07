package main

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/vt"
)

// GetWordCoordinate finds the occurrence of a word starting from (startX, startY) and returns its (x, y) coordinate.
func GetWordCoordinate(s tcell.Screen, word string, startX, startY int) (int, int, bool) {
	width, height := s.Size()
	for y := startY; y < height; y++ {
		var line strings.Builder
		xMin := 0
		if y == startY {
			xMin = startX
		}
		for x := 0; x < width; x++ {
			str, _, _ := s.Get(x, y)
			r := ' '
			if len(str) > 0 {
				r = []rune(str)[0]
			}
			line.WriteRune(r)
		}
		lineStr := line.String()
		if idx := strings.Index(lineStr[xMin:], word); idx != -1 {
			return xMin + idx, y, true
		}
	}
	return -1, -1, false
}

// GetColorCoordinate finds the first cell with the specified foreground or background color.
func GetColorCoordinate(s tcell.Screen, color tcell.Color) (int, int, bool) {
	width, height := s.Size()
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			_, style, _ := s.Get(x, y)
			if style.GetForeground() == color || style.GetBackground() == color {
				return x, y, true
			}
		}
	}
	return -1, -1, false
}

// Views hold pointers into the editor's theme, so switching themes recolors
// everything already on screen.
func TestThemeSwitchRecolorsViews(t *testing.T) {
	e, col := newTestEditorWithColumn(t)
	col.AddWindow(" /tmp/theme.txt Del ", "some text")
	col.Resize(col.rect)

	if err := e.ApplyTheme("acme"); err != nil {
		t.Fatalf("ApplyTheme: %v", err)
	}
	e.Draw()

	for row, want := range []tcell.Color{e.theme.GlobalTag.BG, e.theme.ColTag.BG, e.theme.Tag.BG, e.theme.Body.BG} {
		if _, style, _ := e.screen.Get(10, row); style.GetBackground() != want {
			t.Errorf("row %d background = %v, want %v", row, style.GetBackground(), want)
		}
	}
}

// VerifyNewColExists checks if the word "NewCol" is present anywhere on the screen.
func VerifyNewColExists(s tcell.Screen) bool {
	_, _, found := GetWordCoordinate(s, "NewCol", 0, 0)
	return found
}

func setupTest(t *testing.T, w, h int) (*Editor, tcell.Screen) {
	mt := vt.NewMockTerm(vt.MockOptSize{X: vt.Col(w), Y: vt.Row(h)})
	s, err := tcell.NewTerminfoScreenFromTty(mt)
	if err != nil {
		t.Fatalf("failed to create screen: %v", err)
	}
	if err := s.Init(); err != nil {
		t.Fatalf("failed to init screen: %v", err)
	}
	e := &Editor{}
	e.setup(s)

	// This goroutine stands in for the main loop. It draws only when asked,
	// inside a call: a draw after a call returns would race with the caller
	// reading the screen.
	go func() {
		for fn := range e.callCh {
			fn()
			// Wake up any waitFor loops so they can recheck their condition.
			select {
			case e.screen.EventQ() <- tcell.NewEventInterrupt(nil):
			default:
			}
		}
	}()
	return e, s
}

func waitFor(t *testing.T, e *Editor, s tcell.Screen, condition func() bool) {
	timeout := time.After(3 * time.Second)
	done := make(chan bool)
	go func() {
		for {
			ok := condition()
			// Draw on the editor's goroutine, whole frames only: the screen
			// shows what the condition saw, and a condition on the screen is
			// checked again against this frame. The draw is itself a call, so
			// it wakes this loop.
			e.Call(e.Draw)
			if ok {
				done <- true
				return
			}
			if _, ok := <-s.EventQ(); !ok {
				return
			}
		}
	}()

	select {
	case <-done:
		return
	case <-timeout:
		select {
		case s.EventQ() <- tcell.NewEventInterrupt(nil):
		default:
		}
		t.Fatal("timeout waiting for condition")
	}
}

func TestTcellView(t *testing.T) {
	e, s := setupTest(t, 80, 24)

	e.tag.Draw(canvas{s, 0, 0, e.w, 1})
	s.Show()

	if !VerifyNewColExists(s) {
		t.Error("Expected 'NewCol' to exist on the screen, but it was not found")
	}

	x, y, found := GetWordCoordinate(s, "NewCol", 0, 0)
	if !found {
		t.Error("Could not find coordinate for 'NewCol'")
	} else {
		t.Logf("'NewCol' found at coordinate: (%d, %d)", x, y)
		if x != 1 || y != 0 {
			t.Errorf("Expected 'NewCol' at (1, 0), got (%d, %d)", x, y)
		}
	}

	// Test color search for the GlobalTagBG
	cx, cy, cfound := GetColorCoordinate(s, e.theme.GlobalTag.BG)
	if !cfound {
		t.Errorf("Expected to find GlobalTagBG color (%v), but it was not found", e.theme.GlobalTag.BG)
	} else {
		t.Logf("GlobalTagBG color found at coordinate: (%d, %d)", cx, cy)
	}
}

func TestTextViewClickPlacesCursorOnClickedCharacter(t *testing.T) {
	tv := NewTextView("abc", 10, 1, nil, nil, false, false)

	tv.HandleEvent(tcell.NewEventMouse(0, 0, tcell.ButtonPrimary, 0))

	if b := tv.buffer; b.q0 != 0 || b.q1 != 0 {
		t.Fatalf("dot after clicking the first character = [%d, %d), want the cursor at 0", b.q0, b.q1)
	}
}

func TestNewColClick(t *testing.T) {
	e, s := setupTest(t, 100, 24)

	// Initialize with 2 columns as in main.go
	leftWidth := e.w / 2
	colLeft := NewColumn(0, 1, leftWidth, e.h-1, e)
	e.columns = append(e.columns, colLeft)

	colRight := NewColumn(leftWidth, 1, e.w-leftWidth, e.h-1, e)
	e.columns = append(e.columns, colRight)

	e.resize()
	e.Draw()
	s.Show()

	if len(e.columns) != 2 {
		t.Errorf("Expected 2 columns initially, got %d", len(e.columns))
	}

	// Find "NewCol" coordinate
	x, y, found := GetWordCoordinate(s, "NewCol", 0, 0)
	if !found {
		t.Fatal("Could not find 'NewCol' on screen")
	}

	// Simulate Button3 (Middle-click) on "NewCol"
	// Editor.HandleEvent handles clicks on y=0
	ev := tcell.NewEventMouse(x, y, tcell.ButtonMiddle, 0)
	quit, redraw := e.HandleEvent(ev)
	if quit {
		t.Error("HandleEvent returned quit=true unexpectedly")
	}
	if !redraw {
		t.Error("HandleEvent returned redraw=false, expected true after command execution")
	}

	if len(e.columns) != 3 {
		t.Errorf("Expected 3 columns after clicking NewCol, got %d", len(e.columns))
	}

	// Verify the last column is new and has a window
	lastCol := e.columns[2]
	if len(lastCol.windows) != 1 {
		t.Errorf("Expected new column to have 1 window, got %d", len(lastCol.windows))
	}
}

func TestHelpClick(t *testing.T) {
	e, s := setupTest(t, 100, 24)

	col := NewColumn(0, 1, e.w, e.h-1, e)
	e.columns = append(e.columns, col)

	e.resize()
	e.Draw()
	s.Show()

	// Find "Help" coordinate
	x, y, found := GetWordCoordinate(s, "Help", 0, 0)
	if !found {
		t.Fatal("Could not find 'Help' on screen")
	}

	// Simulate Middle-click on "Help"
	ev := tcell.NewEventMouse(x, y, tcell.ButtonMiddle, 0)
	e.HandleEvent(ev)

	// Use a channel to signal when we find the doc
	waitFor(t, e, s, func() bool {
		_, _, found := GetWordCoordinate(s, "Peak Documentation", 0, 0)
		return found
	})

	// Verify windows
	totalWindows := 0
	for _, c := range e.columns {
		totalWindows += len(c.windows)
	}
	if totalWindows == 0 {
		t.Error("Expected at least one window to be open")
	}
}

func TestDelColClick(t *testing.T) {
	e, s := setupTest(t, 120, 24)

	// Start with 3 columns
	colWidth := e.w / 3
	for i := 0; i < 3; i++ {
		col := NewColumn(i*colWidth, 1, colWidth, e.h-1, e)
		e.columns = append(e.columns, col)
	}

	e.resize()
	e.Draw()
	s.Show()

	if len(e.columns) != 3 {
		t.Errorf("Expected 3 columns initially, got %d", len(e.columns))
	}

	// Find the "Delcol" in the 3rd column's tag (y=1)
	// Column tags are at y=1.
	// 3rd column starts at x = 2*colWidth
	x, y, found := GetWordCoordinate(s, "Delcol", 2*colWidth+1, 1)
	if !found {
		t.Fatal("Could not find 'Delcol' in the 3rd column")
	}

	// Simulate Middle-click on "Delcol"
	ev := tcell.NewEventMouse(x, y, tcell.ButtonMiddle, 0)
	quit, redraw := e.HandleEvent(ev)
	if quit {
		t.Error("HandleEvent returned quit=true unexpectedly")
	}
	if !redraw {
		t.Error("HandleEvent returned redraw=false, expected true")
	}

	if len(e.columns) != 2 {
		t.Errorf("Expected 2 columns after clicking Delcol, got %d", len(e.columns))
	}
}

// A Zerox copy has its own buffer and version numbers; it must be dirty
// exactly when the original is.
func TestZeroxKeepsDirtyState(t *testing.T) {
	for _, saved := range []bool{true, false} {
		e, col := newTestEditorWithColumn(t)
		path := writeTempFile(t, "hello\n")
		win := e.createWindow(col, path, "hello\n", false, true, -1, 0)
		win.bodyTextView().buffer.Insert("x")
		if saved {
			win.markSaved(win.body.GetBuffer().version) // as Put does
		}

		e.cmdZerox(col, win)

		if e.active == win {
			t.Fatal("Zerox did not create a window")
		}
		if got := e.active.IsDirty(); got != !saved {
			t.Errorf("saved=%v: copy dirty = %v, want %v", saved, got, !saved)
		}
	}
}

// A |cmd's output may arrive after the window was edited; it must land at the
// (clamped) selection instead of indexing lines that no longer exist.
func TestPipeOutputAfterEdit(t *testing.T) {
	e, col := newTestEditorWithColumn(t)
	win := col.AddWindow(" /tmp/pipe.txt Get Put Del ", "a\nb\nc\nd\nselected")
	buf := win.body.GetBuffer()
	buf.SetDot(8, 16)

	e.runExternal(col, win, "|sleep 0.2; echo out")
	e.Call(func() { buf.SetText("x") }) // the user replaces the text meanwhile

	deadline := time.Now().Add(5 * time.Second)
	for {
		var text string
		e.Call(func() { text = buf.GetText() })
		if text == "xout\n" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("body = %q, want %q", text, "xout\n")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestZeroxClick(t *testing.T) {
	e, s := setupTest(t, 100, 24)

	col := NewColumn(0, 1, e.w, e.h-1, e)
	e.columns = append(e.columns, col)

	win := col.AddWindow(" test.txt Zerox ", "Hello Zerox")
	e.ActivateWindow(win)

	e.resize()
	e.Draw()
	s.Show()

	if len(col.windows) != 1 {
		t.Errorf("Expected 1 window initially, got %d", len(col.windows))
	}

	// Find "Zerox" in the window tag.
	// Global tag y=0, Column tag y=1, Window tag y=2.
	x, y, found := GetWordCoordinate(s, "Zerox", 0, 2)
	if !found {
		t.Fatal("Could not find 'Zerox' in the window tag")
	}

	// Simulate Middle-click on "Zerox"
	ev := tcell.NewEventMouse(x, y, tcell.ButtonMiddle, 0)
	e.HandleEvent(ev)

	if len(col.windows) != 2 {
		t.Errorf("Expected 2 windows after clicking Zerox, got %d", len(col.windows))
	}

	if col.windows[1].body.GetBuffer().GetText() != "Hello Zerox" {
		t.Errorf("Expected second window to have same text, got %q", col.windows[1].body.GetBuffer().GetText())
	}
}

func TestGetDirClick(t *testing.T) {
	e, s := setupTest(t, 100, 100)

	col := NewColumn(0, 1, e.w, e.h-1, e)
	e.columns = append(e.columns, col)

	// Create window with /peak/doc as the name
	win := col.AddWindow(" /peak/doc Get Put ", "")
	e.ActivateWindow(win)

	e.resize()
	e.Draw()
	s.Show()

	// Find "Get" in the window tag (y=2)
	x, y, found := GetWordCoordinate(s, "Get", 0, 2)
	if !found {
		t.Fatal("Could not find 'Get' in the window tag")
	}

	// Simulate Middle-click on "Get"
	ev := tcell.NewEventMouse(x, y, tcell.ButtonMiddle, 0)
	e.HandleEvent(ev)

	// Wait for async directory listing
	waitFor(t, e, s, func() bool {
		_, _, found := GetWordCoordinate(s, "README.md", 0, 3)
		return found
	})

	// Verify that the window name updated to /peak/doc/ (with trailing slash)
	if !strings.Contains(win.tag.buffer.GetText(), "/peak/doc/") {
		t.Errorf("Expected window tag to contain '/peak/doc/', got %q", win.tag.buffer.GetText())
	}

	// Now right-click on README.md in the window body
	rx, ry, found := GetWordCoordinate(s, "README.md", 0, 3)
	if !found {
		t.Fatal("Could not find 'README.md' in window body")
	}

	// Simulate Button2 (Right-click) on "README.md"
	ev2 := tcell.NewEventMouse(rx, ry, tcell.ButtonSecondary, 0)
	e.HandleEvent(ev2)

	// Wait for README.md to open
	waitFor(t, e, s, func() bool {
		_, _, found := GetWordCoordinate(s, "Peak Documentation", 0, 0)
		return found
	})

	// Check that we have a window with README.md in the title
	foundTitle := false
	for _, c := range e.columns {
		for _, w := range c.windows {
			if strings.Contains(w.tag.buffer.GetText(), "README.md") {
				foundTitle = true
				break
			}
		}
	}
	if !foundTitle {
		t.Error("Expected a window with 'README.md' in the tag")
	}

	// Verify the text "Peak is a TUI text editor" is on screen
	tx, ty, foundText := GetWordCoordinate(s, "Peak is a TUI text editor", 0, 0)
	if !foundText {
		t.Fatal("Could not find 'Peak is a TUI text editor' on screen")
	}
	t.Logf("Initial text position: (%d, %d)", tx, ty)

	// Simulate 6 WheelDown events on the README.md window body (tx, ty)
	for i := 0; i < 6; i++ {
		// WheelDown on the text coordinate
		ev := tcell.NewEventMouse(tx, ty, tcell.WheelDown, 0)
		e.HandleEvent(ev)
	}

	// Redraw and Show
	e.Draw()
	s.Show()

	// Verify the text has moved up by 6 lines
	ntx, nty, nfoundText := GetWordCoordinate(s, "Peak is a TUI text editor", 0, 0)
	if !nfoundText {
		t.Error("Text 'Peak is a TUI text editor' disappeared after scrolling")
	} else {
		t.Logf("New text position: (%d, %d)", ntx, nty)
		if nty != ty-6 {
			t.Errorf("Expected text to move up 6 lines to y=%d, got y=%d", ty-6, nty)
		}
	}
}

func TestDragWindow(t *testing.T) {
	e, s := setupTest(t, 120, 40)

	// Create 3 columns
	colWidth := e.w / 3
	for i := 0; i < 3; i++ {
		col := NewColumn(i*colWidth, 1, colWidth, e.h-1, e)
		e.columns = append(e.columns, col)
	}

	// Col 0: 1 window
	w1 := e.columns[0].AddWindow(" w1 ", "content 1")
	// Col 1: 2 windows
	w2 := e.columns[1].AddWindow(" w2 ", "content 2")
	w3 := e.columns[1].AddWindow(" w3 ", "content 3")
	// Col 2: 0 windows

	e.resize()
	e.Draw()
	s.Show()

	// Initial check
	if len(e.columns[0].windows) != 1 || len(e.columns[1].windows) != 2 || len(e.columns[2].windows) != 0 {
		t.Fatalf("Initial state wrong: %d, %d, %d", len(e.columns[0].windows), len(e.columns[1].windows), len(e.columns[2].windows))
	}

	// 1. Drag W1 (Col 0) to Col 2: cross one boundary at a time.
	e.HandleEvent(tcell.NewEventMouse(0, 2, tcell.ButtonPrimary, 0))
	if e.capture == nil {
		t.Fatal("Failed to start dragging w1")
	}
	// Step into Col 1 (past col0's right edge at x=colWidth)
	e.HandleEvent(tcell.NewEventMouse(colWidth+5, 10, tcell.ButtonPrimary, 0))
	// Step into Col 2 (past col1's right edge at x=2*colWidth)
	e.HandleEvent(tcell.NewEventMouse(2*colWidth+5, 10, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(2*colWidth+5, 10, tcell.ButtonNone, 0))

	// 2. Drag W2 (Col 1) to Col 0
	// W2's handle is at (40, 2) on screen
	e.HandleEvent(tcell.NewEventMouse(colWidth, 2, tcell.ButtonPrimary, 0))
	if e.capture == nil {
		t.Fatal("Failed to start dragging w2")
	}
	// Left threshold: mx < col0.x+col0.w-col0.w/4 = 40-10 = 30; use x=5.
	e.HandleEvent(tcell.NewEventMouse(5, 10, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(5, 10, tcell.ButtonNone, 0))

	// 3. Drag W3 (Col 1, now alone at top) to Col 2
	e.HandleEvent(tcell.NewEventMouse(colWidth, 2, tcell.ButtonPrimary, 0))
	if e.capture == nil {
		t.Fatal("Failed to start dragging w3")
	}
	e.HandleEvent(tcell.NewEventMouse(2*colWidth+5, 20, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(2*colWidth+5, 20, tcell.ButtonNone, 0))

	// Final check: Col 0 (W2), Col 1 (Empty), Col 2 (W1, W3)
	if len(e.columns[0].windows) != 1 || e.columns[0].windows[0] != w2 {
		t.Errorf("Col 0 should have w2, got %d windows", len(e.columns[0].windows))
	}
	if len(e.columns[1].windows) != 0 {
		t.Errorf("Col 1 should be empty, got %d windows", len(e.columns[1].windows))
	}
	// The order depends on where exactly we dropped them.
	// In my simulation, w3 ended up before w1.
	if len(e.columns[2].windows) != 2 {
		t.Errorf("Col 2 should have 2 windows, got %d", len(e.columns[2].windows))
	}
	hasW1, hasW3 := false, false
	for _, w := range e.columns[2].windows {
		if w == w1 {
			hasW1 = true
		}
		if w == w3 {
			hasW3 = true
		}
	}
	if !hasW1 || !hasW3 {
		t.Errorf("Col 2 missing windows: w1=%v, w3=%v", hasW1, hasW3)
	}
}

func TestDragWindowInternal(t *testing.T) {
	e, s := setupTest(t, 120, 60)

	col := NewColumn(0, 1, e.w, e.h-1, e)
	e.columns = append(e.columns, col)

	w1 := col.AddWindow(" w1 ", "c1")
	w2 := col.AddWindow(" w2 ", "c2")
	w3 := col.AddWindow(" w3 ", "c3")

	e.resize()
	e.Draw()
	s.Show()

	if len(col.windows) != 3 || col.windows[0] != w1 || col.windows[1] != w2 || col.windows[2] != w3 {
		t.Fatalf("Initial order wrong")
	}

	// 1. Drag W1 (idx 0) below W2.
	// Drag W1 by its handle and drop it in W2's body area.
	e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w1).Y, tcell.ButtonPrimary, 0))
	if e.capture == nil {
		t.Fatal("drag w1 failed")
	}
	// Drag past W2's midpoint to trigger swap-right
	e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w2).Y+w2.h/2+1, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w2).Y+w2.h/2+1, tcell.ButtonNone, 0))

	if col.windows[0] != w2 || col.windows[1] != w1 || col.windows[2] != w3 {
		t.Errorf("Order after first drag wrong: %d, %d, %d", col.windows[0].ID, col.windows[1].ID, col.windows[2].ID)
	}

	// 2. Drag W3 (idx 2) above W1 (idx 1).
	// Current: [w2, w1, w3]
	// We drop it in W1's tag area
	e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w3).Y, tcell.ButtonPrimary, 0))
	if e.capture == nil {
		t.Fatal("drag w3 failed")
	}
	e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w1).Y, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w1).Y, tcell.ButtonNone, 0))

	// Verify final: w2, w3, w1
	if col.windows[0] != w2 || col.windows[1] != w3 || col.windows[2] != w1 {
		t.Errorf("Final order wrong: expected [w2, w3, w1], got [%d, %d, %d]", col.windows[0].ID, col.windows[1].ID, col.windows[2].ID)
	}
}

func TestWindowSwapAllDirections(t *testing.T) {
	newSetup := func(t *testing.T) (*Editor, *Column, *Window, *Window, *Window) {
		t.Helper()
		e, _ := setupTest(t, 120, 60)
		col := NewColumn(0, 1, e.w, e.h-1, e)
		e.columns = append(e.columns, col)
		w1 := col.AddWindow(" w1 ", "c1")
		w2 := col.AddWindow(" w2 ", "c2")
		w3 := col.AddWindow(" w3 ", "c3")
		e.resize()
		e.Draw()
		return e, col, w1, w2, w3
	}

	checkOrder := func(t *testing.T, col *Column, a, b, c *Window) {
		t.Helper()
		if col.windows[0] != a || col.windows[1] != b || col.windows[2] != c {
			t.Fatalf("order wrong: want [%d,%d,%d], got [%d,%d,%d]",
				a.ID, b.ID, c.ID,
				col.windows[0].ID, col.windows[1].ID, col.windows[2].ID)
		}
	}
	checkHeights := func(t *testing.T, w1, w2, w3 *Window, h1, h2, h3 int) {
		t.Helper()
		if w1.h != h1 || w2.h != h2 || w3.h != h3 {
			t.Errorf("heights changed: w1 %d→%d, w2 %d→%d, w3 %d→%d",
				h1, w1.h, h2, w2.h, h3, w3.h)
		}
	}

	// swap-right: drag w2 past w3's midpoint → [w1, w3, w2]
	t.Run("2to3", func(t *testing.T) {
		e, col, w1, w2, w3 := newSetup(t)
		h1, h2, h3 := w1.h, w2.h, w3.h
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w2).Y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w3).Y+w3.h/2+1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w3).Y+w3.h/2+1, tcell.ButtonNone, 0))
		checkOrder(t, col, w1, w3, w2)
		checkHeights(t, w1, w2, w3, h1, h2, h3)
	})

	// swap-left: drag w3 into w2's tag area → [w1, w3, w2]
	t.Run("3to2", func(t *testing.T) {
		e, col, w1, w2, w3 := newSetup(t)
		h1, h2, h3 := w1.h, w2.h, w3.h
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w3).Y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w2).Y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w2).Y, tcell.ButtonNone, 0))
		checkOrder(t, col, w1, w3, w2)
		checkHeights(t, w1, w2, w3, h1, h2, h3)
	})

	// swap-right from first: drag w1 past w2's midpoint → [w2, w1, w3]
	t.Run("1to2", func(t *testing.T) {
		e, col, w1, w2, w3 := newSetup(t)
		h1, h2, h3 := w1.h, w2.h, w3.h
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w1).Y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w2).Y+w2.h/2+1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w2).Y+w2.h/2+1, tcell.ButtonNone, 0))
		checkOrder(t, col, w2, w1, w3)
		checkHeights(t, w1, w2, w3, h1, h2, h3)
	})

	// swap-left to first: drag w2 into w1's tag area → [w2, w1, w3]
	t.Run("2to1", func(t *testing.T) {
		e, col, w1, w2, w3 := newSetup(t)
		h1, h2, h3 := w1.h, w2.h, w3.h
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w2).Y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w1).Y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w1).Y, tcell.ButtonNone, 0))
		checkOrder(t, col, w2, w1, w3)
		checkHeights(t, w1, w2, w3, h1, h2, h3)
	})

	// two consecutive swap-rights: drag w1 past w2, then past w3 → [w2, w3, w1]
	t.Run("1to3", func(t *testing.T) {
		e, col, w1, w2, w3 := newSetup(t)
		h1, h2, h3 := w1.h, w2.h, w3.h
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w1).Y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w2).Y+w2.h/2+1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w3).Y+w3.h/2+1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w3).Y+w3.h/2+1, tcell.ButtonNone, 0))
		checkOrder(t, col, w2, w3, w1)
		checkHeights(t, w1, w2, w3, h1, h2, h3)
	})

	// two consecutive swap-lefts: drag w3 into w2's tag, then into w1's tag → [w3, w1, w2]
	t.Run("3to1", func(t *testing.T) {
		e, col, w1, w2, w3 := newSetup(t)
		h1, h2, h3 := w1.h, w2.h, w3.h
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w3).Y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w2).Y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w1).Y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, screenAt(e, w1).Y, tcell.ButtonNone, 0))
		checkOrder(t, col, w3, w1, w2)
		checkHeights(t, w1, w2, w3, h1, h2, h3)
	})
}

func TestColumnSwapAllDirections(t *testing.T) {
	newSetup := func(t *testing.T) (*Editor, *Column, *Column, *Column) {
		t.Helper()
		e, _ := setupTest(t, 120, 30)
		colW := e.w / 3
		c1 := NewColumn(0, 1, colW, e.h-1, e)
		c1.explicitWidth = colW
		c2 := NewColumn(colW, 1, colW, e.h-1, e)
		c2.explicitWidth = colW
		c3 := NewColumn(2*colW, 1, e.w-2*colW, e.h-1, e)
		c3.explicitWidth = e.w - 2*colW
		e.columns = append(e.columns, c1, c2, c3)
		e.resize()
		e.Draw()
		return e, c1, c2, c3
	}

	checkOrder := func(t *testing.T, e *Editor, a, b, c *Column) {
		t.Helper()
		if e.columns[0] != a || e.columns[1] != b || e.columns[2] != c {
			t.Fatalf("column order wrong: want [%p,%p,%p], got [%p,%p,%p]",
				a, b, c, e.columns[0], e.columns[1], e.columns[2])
		}
	}
	checkWidths := func(t *testing.T, c1, c2, c3 *Column, w1, w2, w3 int) {
		t.Helper()
		if c1.w != w1 || c2.w != w2 || c3.w != w3 {
			t.Errorf("widths changed: c1 %d→%d, c2 %d→%d, c3 %d→%d",
				w1, c1.w, w2, c2.w, w3, c3.w)
		}
	}

	// swap-right: drag c2 past c3's midpoint → [c1, c3, c2]
	t.Run("2to3", func(t *testing.T) {
		e, c1, c2, c3 := newSetup(t)
		w1, w2, w3 := c1.w, c2.w, c3.w
		e.HandleEvent(tcell.NewEventMouse(c2.x, 1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(c3.x+c3.w/2+1, 1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(c3.x+c3.w/2+1, 1, tcell.ButtonNone, 0))
		checkOrder(t, e, c1, c3, c2)
		checkWidths(t, c1, c2, c3, w1, w2, w3)
	})

	// swap-left: drag c3 into c2's left edge → [c1, c3, c2]
	t.Run("3to2", func(t *testing.T) {
		e, c1, c2, c3 := newSetup(t)
		w1, w2, w3 := c1.w, c2.w, c3.w
		e.HandleEvent(tcell.NewEventMouse(c3.x, 1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(c2.x, 1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(c2.x, 1, tcell.ButtonNone, 0))
		checkOrder(t, e, c1, c3, c2)
		checkWidths(t, c1, c2, c3, w1, w2, w3)
	})

	// swap-right from first: drag c1 past c2's midpoint → [c2, c1, c3]
	t.Run("1to2", func(t *testing.T) {
		e, c1, c2, c3 := newSetup(t)
		w1, w2, w3 := c1.w, c2.w, c3.w
		e.HandleEvent(tcell.NewEventMouse(c1.x, 1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(c2.x+c2.w/2+1, 1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(c2.x+c2.w/2+1, 1, tcell.ButtonNone, 0))
		checkOrder(t, e, c2, c1, c3)
		checkWidths(t, c1, c2, c3, w1, w2, w3)
	})

	// swap-left to first: drag c2 near c1's left edge → [c2, c1, c3]
	t.Run("2to1", func(t *testing.T) {
		e, c1, c2, c3 := newSetup(t)
		w1, w2, w3 := c1.w, c2.w, c3.w
		e.HandleEvent(tcell.NewEventMouse(c2.x, 1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(c1.x+1, 1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(c1.x+1, 1, tcell.ButtonNone, 0))
		checkOrder(t, e, c2, c1, c3)
		checkWidths(t, c1, c2, c3, w1, w2, w3)
	})

	// two consecutive swap-rights: drag c1 past c2, then past c3 → [c2, c3, c1]
	t.Run("1to3", func(t *testing.T) {
		e, c1, c2, c3 := newSetup(t)
		w1, w2, w3 := c1.w, c2.w, c3.w
		e.HandleEvent(tcell.NewEventMouse(c1.x, 1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(c2.x+c2.w/2+1, 1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(c3.x+c3.w/2+1, 1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(c3.x+c3.w/2+1, 1, tcell.ButtonNone, 0))
		checkOrder(t, e, c2, c3, c1)
		checkWidths(t, c1, c2, c3, w1, w2, w3)
	})

	// two consecutive swap-lefts: drag c3 to c2's left edge, then to c1's left edge → [c3, c1, c2]
	t.Run("3to1", func(t *testing.T) {
		e, c1, c2, c3 := newSetup(t)
		w1, w2, w3 := c1.w, c2.w, c3.w
		e.HandleEvent(tcell.NewEventMouse(c3.x, 1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(c2.x, 1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(c1.x+1, 1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(c1.x+1, 1, tcell.ButtonNone, 0))
		checkOrder(t, e, c3, c1, c2)
		checkWidths(t, c1, c2, c3, w1, w2, w3)
	})
}

func TestSimpleEdit(t *testing.T) {
	e, s := setupTest(t, 100, 24)

	col := NewColumn(0, 1, e.w, e.h-1, e)
	e.columns = append(e.columns, col)

	e.resize()
	e.Draw()
	s.Show()

	path := "/peak/mirage/1.txt"

	// Pre-create the directory and file in memory FS so Open succeeds
	ns.MkdirAll("/peak/mirage", 0755)
	writeFile(path, []byte(""))

	for i := 1; i <= 2; i++ {
		testString := "Iteration " + strings.Repeat("X", i)
		t.Logf("Starting iteration %d", i)

		// 1. Open the file
		e.Open(nil, path)
		t.Logf("Called Open for %s", path)

		var win *Window
		waitFor(t, e, s, func() bool {
			for _, c := range e.columns {
				for _, w := range c.windows {
					if strings.Contains(w.tag.buffer.GetText(), path) {
						win = w
						return true
					}
				}
			}
			return false
		})

		// 2. Write string to buffer
		win.body.GetBuffer().SetText(testString)
		t.Logf("Set text to %q", testString)

		// 3. Put
		px, py, pfound := GetWordCoordinate(s, "Put", 0, screenAt(e, win.tag).Y)
		if !pfound {
			t.Fatalf("Iteration %d: Could not find 'Put' in window tag", i)
		}
		t.Logf("Found 'Put' at (%d, %d), clicking", px, py)
		// Simulate middle click
		e.HandleEvent(tcell.NewEventMouse(px, py, tcell.ButtonMiddle, 0))

		// Wait for Put to complete (savedVersion matches current version)
		waitFor(t, e, s, func() bool {
			return win.savedVersion == win.body.GetBuffer().version
		})

		// 4. Close window
		dx, dy, dfound := GetWordCoordinate(s, "Del", 0, screenAt(e, win.tag).Y)
		if !dfound {
			t.Fatalf("Iteration %d: Could not find 'Del' in window tag", i)
		}
		t.Logf("Found 'Del' at (%d, %d), clicking", dx, dy)
		e.HandleEvent(tcell.NewEventMouse(dx, dy, tcell.ButtonMiddle, 0))

		// Verify closed
		closed := false
		for _, c := range e.columns {
			if len(c.windows) == 0 {
				closed = true
				break
			}
			foundWin := false
			for _, w := range c.windows {
				if w == win {
					foundWin = true
					break
				}
			}
			if !foundWin {
				closed = true
				break
			}
		}
		if !closed {
			t.Fatalf("Iteration %d: Window did not close", i)
		}
		t.Logf("Window closed successfully")

		// 5. Reopen and verify
		e.Open(nil, path)
		t.Logf("Reopening %s", path)

		waitFor(t, e, s, func() bool {
			for _, c := range e.columns {
				for _, w := range c.windows {
					if strings.Contains(w.tag.buffer.GetText(), path) {
						if w.body.GetBuffer().GetText() == testString {
							win = w // for next step Del
							return true
						}
					}
				}
			}
			return false
		})

		// Close it again for next iteration or finish
		dx, dy, _ = GetWordCoordinate(s, "Del", 0, screenAt(e, win.tag).Y)
		e.HandleEvent(tcell.NewEventMouse(dx, dy, tcell.ButtonMiddle, 0))
		t.Logf("Iteration %d finished", i)
	}
}

func TestExternalCommand(t *testing.T) {
	e, s := setupTest(t, 120, 30)

	col := NewColumn(0, 1, e.w, e.h-1, e)
	e.columns = append(e.columns, col)

	e.resize()
	e.Draw()
	s.Show()

	// 1. Create window and add "uname -a" to tag
	win := col.AddWindow(" /tmp/test.txt Get Put uname -a Del ", "initial body")
	e.ActivateWindow(win)
	e.resize()
	e.Draw()
	s.Show()

	// 2. Select "uname -a" in tag
	// "uname -a" starts at index 23 in " /tmp/test.txt Get Put uname -a Del "
	// But let's find it programmatically
	tagText := win.tag.buffer.GetText()
	idx := strings.Index(tagText, "uname -a")
	if idx == -1 {
		t.Fatal("Could not find 'uname -a' in tag text")
	}
	win.tag.buffer.SetDot(idx, idx+len("uname -a"))

	// 3. Middle click on the selection in the tag
	tx, ty, tfound := GetWordCoordinate(s, "uname -a", 0, screenAt(e, win.tag).Y)
	if !tfound {
		t.Fatal("Could not find 'uname -a' coordinates on screen")
	}
	e.HandleEvent(tcell.NewEventMouse(tx, ty, tcell.ButtonMiddle, 0))

	// 4. Wait for error output window and check content
	var errWin *Window
	waitFor(t, e, s, func() bool {
		for _, c := range e.columns {
			for _, w := range c.windows {
				if w.kind == WinOut {
					errWin = w
					return true
				}
			}
		}
		return false
	})

	output := strings.TrimSpace(errWin.body.GetBuffer().GetText())
	t.Logf("Output from uname -a: %q", output)

	// 5. Compare with Go's uname -a
	uname, _ := exec.Command("uname", "-a").Output()
	expected := strings.TrimSpace(string(uname))
	if output != expected {
		t.Errorf("Expected output %q, got %q", expected, output)
	}

	// 6. Add "uname -a" to +Errors window's buffer
	errWin.body.GetBuffer().SetText(output + "\n\nuname -a")
	e.Draw()
	s.Show()

	// 7. Select "uname -a" in buffer
	bodyText := errWin.body.GetBuffer().GetText()
	bidx := strings.Index(bodyText, "uname -a")
	errWin.body.GetBuffer().SetDot(bidx, bidx+len("uname -a"))

	// 8. Run it (middle click)
	errY := screenAt(e, errWin.body).Y
	bx, by, bfound := GetWordCoordinate(s, "uname -a", 0, errY)
	if !bfound {
		t.Fatal("Could not find 'uname -a' in +Errors body")
	}
	e.HandleEvent(tcell.NewEventMouse(bx, by, tcell.ButtonMiddle, 0))

	// 9. The output is added on a line of its own after what is there
	want := expected + "\n\nuname -a\n" + expected
	waitFor(t, e, s, func() bool {
		return strings.TrimSpace(errWin.body.GetBuffer().GetText()) == want
	})
}

func TestSimplePlumb(t *testing.T) {
	e, s := setupTest(t, 100, 30)

	col := NewColumn(0, 1, e.w, e.h-1, e)
	e.columns = append(e.columns, col)

	e.resize()
	e.Draw()
	s.Show()

	path := "/peak/mirage/2.txt"
	testString := "DirListing Test Content"

	ns.MkdirAll("/peak/mirage", 0755)
	writeFile(path, []byte(""))

	// 1. Open and Write
	e.Open(nil, path)

	var win *Window
	waitFor(t, e, s, func() bool {
		for _, c := range e.columns {
			for _, w := range c.windows {
				if strings.Contains(w.tag.buffer.GetText(), path) {
					win = w
					return true
				}
			}
		}
		return false
	})

	win.body.GetBuffer().SetText(testString)
	px, py, _ := GetWordCoordinate(s, "Put", 0, screenAt(e, win.tag).Y)
	e.HandleEvent(tcell.NewEventMouse(px, py, tcell.ButtonMiddle, 0))

	// Wait for Put
	waitFor(t, e, s, func() bool {
		return win.savedVersion == win.body.GetBuffer().version
	})

	// Close window
	dx, dy, _ := GetWordCoordinate(s, "Del", 0, screenAt(e, win.tag).Y)
	e.HandleEvent(tcell.NewEventMouse(dx, dy, tcell.ButtonMiddle, 0))

	// 2. Open directory /peak/mirage/
	e.Open(nil, "/peak/mirage/")

	var dirWin *Window
	waitFor(t, e, s, func() bool {
		for _, c := range e.columns {
			for _, w := range c.windows {
				if strings.Contains(w.tag.buffer.GetText(), "/peak/mirage/") {
					dirWin = w
					return true
				}
			}
		}
		return false
	})

	// 3. Find 2.txt in the body and right-click it
	e.Draw()
	s.Show()
	bodyY := screenAt(e, dirWin.body).Y
	fx, fy, ffound := GetWordCoordinate(s, "2.txt", 0, bodyY)
	if !ffound {
		t.Fatal("Could not find '2.txt' in directory listing")
	}

	// Simulate Button2 (Right-click) on "2.txt"
	e.HandleEvent(tcell.NewEventMouse(fx, fy, tcell.ButtonSecondary, 0))

	// 4. Verify new window opened with correct content
	waitFor(t, e, s, func() bool {
		for _, c := range e.columns {
			for _, w := range c.windows {
				// We want the window that IS NOT the dirWin
				if w != dirWin && strings.Contains(w.tag.buffer.GetText(), "2.txt") {
					if w.body.GetBuffer().GetText() == testString {
						return true
					}
				}
			}
		}
		return false
	})
}

func TestPlumbLineCol(t *testing.T) {
	e, s := setupTest(t, 100, 30)
	col := NewColumn(0, 1, e.w, e.h-1, e)
	e.columns = append(e.columns, col)
	e.resize()
	e.Draw()
	s.Show()

	path := "/peak/mirage/linecol.txt"
	ns.MkdirAll("/peak/mirage", 0755)
	writeFile(path, []byte("line one\nline two\nline three\n"))

	findWin := func() *Window {
		for _, c := range e.columns {
			for _, w := range c.windows {
				if strings.Contains(w.tag.buffer.GetText(), "linecol.txt") {
					return w
				}
			}
		}
		return nil
	}

	t.Run("LineCol", func(t *testing.T) {
		e.Plumb(nil, path+":2:5")
		waitFor(t, e, s, func() bool { return findWin() != nil })
		win := findWin()
		tv := win.bodyTextView()
		if tv == nil {
			t.Fatal("no text view")
		}
		b := tv.buffer
		if line, col := b.Pos(b.q0); line != 1 || col != 5 || b.q1 != b.q0 {
			t.Errorf("dot = [%d, %d) at (%d, %d), want the cursor at line 1, column 5", b.q0, b.q1, line, col)
		}
		// Close window
		e.Execute(nil, win, "Del")
	})

	t.Run("LineOnly", func(t *testing.T) {
		e.Plumb(nil, path+":3")
		waitFor(t, e, s, func() bool { return findWin() != nil })
		win := findWin()
		tv := win.bodyTextView()
		if tv == nil {
			t.Fatal("no text view")
		}
		b := tv.buffer
		if q0, q1 := b.Offset(2, 0), b.Offset(2, len(b.lines[2])); b.q0 != q0 || b.q1 != q1 {
			t.Errorf("dot = [%d, %d), want line 2, [%d, %d)", b.q0, b.q1, q0, q1)
		}
	})
}

func TestAutoCreationCommands(t *testing.T) {
	// Test Get
	t.Run("Get", func(t *testing.T) {
		e, _ := setupTest(t, 80, 24)
		if len(e.columns) != 0 {
			t.Fatalf("Expected 0 columns initially, got %d", len(e.columns))
		}
		e.Execute(nil, nil, "Get /nonexistent")
		if len(e.columns) == 0 {
			t.Error("Expected Get to create a column when none exist")
		} else if len(e.columns[0].windows) == 0 {
			t.Error("Expected Get to create a window when none exist")
		}
	})

	// Test Help
	t.Run("Help", func(t *testing.T) {
		e, s := setupTest(t, 80, 24)
		if len(e.columns) != 0 {
			t.Fatalf("Expected 0 columns initially, got %d", len(e.columns))
		}
		e.Execute(nil, nil, "Help")
		waitFor(t, e, s, func() bool {
			return len(e.columns) > 0 && len(e.columns[0].windows) > 0
		})
	})

	// Test New
	t.Run("New", func(t *testing.T) {
		e, _ := setupTest(t, 80, 24)
		if len(e.columns) != 0 {
			t.Fatalf("Expected 0 columns initially, got %d", len(e.columns))
		}
		e.Execute(nil, nil, "New")
		if len(e.columns) == 0 {
			t.Error("Expected New to create a column when none exist")
		} else if len(e.columns[0].windows) == 0 {
			t.Error("Expected New to create a window when none exist")
		}
	})
}

func TestPassiveCommands(t *testing.T) {
	// Test Look
	t.Run("Look", func(t *testing.T) {
		e, _ := setupTest(t, 80, 24)
		if len(e.columns) != 0 {
			t.Fatalf("Expected 0 columns initially, got %d", len(e.columns))
		}
		e.Execute(nil, nil, "Look something")
		if len(e.columns) != 0 {
			t.Errorf("Expected Look to NOT create anything when no windows exist, but got %d columns", len(e.columns))
		}
	})

	// Test Plumb
	t.Run("Plumb", func(t *testing.T) {
		e, _ := setupTest(t, 80, 24)
		if len(e.columns) != 0 {
			t.Fatalf("Expected 0 columns initially, got %d", len(e.columns))
		}
		// Plumb a non-existent file falls back to Look
		e.Plumb(nil, "nonexistent_file_xyz")

		// OpenLine is async, so we wait a short bit.
		time.Sleep(100 * time.Millisecond)

		if len(e.columns) != 0 {
			t.Errorf("Expected Plumb(nonexistent) to NOT create anything when no windows exist, but got %d columns", len(e.columns))
		}
	})
}

// Scrolling up stops the view following the cursor; scrolling down to the
// bottom starts it again.
func TestScrollTogglesFollowing(t *testing.T) {
	tv := NewTextView(strings.Repeat("x\n", 99), 40, 30, nil, nil, false, true)
	tv.setTop(70) // 100 lines, 30 shown: 70 is the bottom
	tv.autoScroll = true
	tv.Scroll(-1)
	if tv.autoScroll {
		t.Fatal("scrolling up must stop following")
	}
	tv.Scroll(1)
	if !tv.autoScroll {
		t.Fatal("scrolling down to the bottom must follow again")
	}
}

func TestTextViewTypingRevealsCursorBelowVisible(t *testing.T) {
	// Cursor is on the last visible row. Pressing Down must scroll
	// the viewport so the cursor stays visible.
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = "line"
	}
	body := strings.Join(lines, "\n")
	tv := NewTextView(body, 40, 10, nil, nil, false, true)

	tv.buffer.moveTo(tv.PosAt(0, 9))
	tv.HandleEvent(tcell.NewEventKey(tcell.KeyDown, "", 0))

	if tv.top() != 1 {
		t.Fatalf("after KeyDown past visible, scroll.Pos=%d, want 1", tv.top())
	}
	if !tv.autoScroll {
		t.Fatal("after key event, AutoScroll must be true")
	}
}

func TestTextViewScrollAwayThenTypeSnapsToCursor(t *testing.T) {
	// Scroll far from the cursor, then type: HandleEvent must snap
	// scroll back so the cursor is visible.
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "text"
	}
	body := strings.Join(lines, "\n")
	tv := NewTextView(body, 40, 10, nil, nil, false, true)

	tv.setTop(50)
	tv.autoScroll = false
	tv.HandleEvent(tcell.NewEventKey(tcell.KeyRune, "x", 0))

	if tv.top() != 0 {
		t.Fatalf("after typing while scrolled away, scroll.Pos=%d, want 0", tv.top())
	}
	if !tv.autoScroll {
		t.Fatal("after key event, AutoScroll must be true")
	}
}

func TestSyncScrollOnlyFollowsDownward(t *testing.T) {
	// SyncScroll must never snap upward when the cursor is above
	// the viewport. That is HandleEvent's domain.
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "text"
	}
	body := strings.Join(lines, "\n")
	tv := NewTextView(body, 40, 10, nil, nil, false, true)

	tv.setTop(50)
	tv.autoScroll = true
	tv.SyncScroll()

	if tv.top() != 50 {
		t.Fatalf("SyncScroll snapped upward: scroll.Pos=%d, want 50", tv.top())
	}
}

func TestSyncScrollFollowsCursorDownward(t *testing.T) {
	// With AutoScroll=true and cursor below the viewport, SyncScroll
	// must follow downward.
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "text"
	}
	body := strings.Join(lines, "\n")
	tv := NewTextView(body, 40, 10, nil, nil, false, true)

	tv.buffer.moveTo(tv.buffer.Offset(95, 0))
	tv.setTop(80)
	tv.autoScroll = true
	tv.SyncScroll()

	if tv.top() <= 80 {
		t.Fatalf("SyncScroll did not follow downward: scroll.Pos=%d, want > 80", tv.top())
	}
}

func TestTextViewWheelScrollPreservedAcrossDraws(t *testing.T) {
	// Manual wheel-scroll must not be reset by SyncScroll during the
	// next Draw cycle.
	e, s := setupTest(t, 40, 20)
	col := NewColumn(0, 1, 40, 19, e)
	e.columns = append(e.columns, col)

	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("L%02d", i))
	}
	body := strings.Join(lines, "\n")
	win := col.AddWindow(" /test ", body)
	e.resize()
	e.Draw()
	s.Show()

	tx, ty, ok := GetWordCoordinate(s, "L05", 1, 2)
	if !ok {
		t.Fatal("could not find 'L05' on screen")
	}

	for i := 0; i < 3; i++ {
		e.HandleEvent(tcell.NewEventMouse(tx, ty, tcell.WheelDown, 0))
	}
	e.Draw()
	s.Show()

	_, nty, nok := GetWordCoordinate(s, "L05", 1, 2)
	if !nok {
		t.Fatal("'L05' disappeared after wheel-scrolling")
	}
	if nty != ty-3 {
		t.Errorf("expected 'L05' to move up 3 lines to y=%d, got y=%d", ty-3, nty)
	}

	_ = win
}

func TestDragWindowBetweenColumns(t *testing.T) {
	e, s := setupTest(t, 120, 40)

	col0 := NewColumn(0, 1, 60, e.h-1, e)
	col1 := NewColumn(60, 1, 60, e.h-1, e)
	e.columns = append(e.columns, col0, col1)

	w1 := col0.AddWindow(" w1 ", "left")
	w2 := col1.AddWindow(" w2 ", "right")
	_ = w1

	e.resize()
	e.Draw()
	s.Show()

	// Drag w2 from col1 to col0, below w1.
	e.HandleEvent(tcell.NewEventMouse(60, 2, tcell.ButtonPrimary, 0))
	if e.capture == nil {
		t.Fatal("failed to start dragging w2")
	}
	e.HandleEvent(tcell.NewEventMouse(10, 15, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(10, 15, tcell.ButtonNone, 0))

	if len(col0.windows) != 2 || len(col1.windows) != 0 {
		t.Fatalf("after first drag: col0=%d col1=%d, want 2 and 0",
			len(col0.windows), len(col1.windows))
	}
	e.Draw()

	// Drag w2 back to col1.
	w2HandleY := screenAt(e, w2).Y
	e.HandleEvent(tcell.NewEventMouse(0, w2HandleY, tcell.ButtonPrimary, 0))
	if e.capture == nil {
		t.Fatal("failed to start dragging w2 back")
	}
	e.HandleEvent(tcell.NewEventMouse(70, 10, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(70, 10, tcell.ButtonNone, 0))

	if len(col0.windows) != 1 || len(col1.windows) != 1 {
		t.Fatalf("after second drag: col0=%d col1=%d, want 1 and 1",
			len(col0.windows), len(col1.windows))
	}
	e.Draw()

	// w1 must fill col0: y=2, h = col0.h - col0.tag.h = (e.h-1) - 1 = e.h-2
	expectedH := e.h - 2
	if w1.h != expectedH {
		t.Errorf("w1.h=%d, want %d (should fill col0)", w1.h, expectedH)
	}
}

func TestColumnDragPreservesBackground(t *testing.T) {
	e, s := setupTest(t, 120, 24)

	col0 := NewColumn(0, 1, 60, e.h-1, e)
	col1 := NewColumn(60, 1, 60, e.h-1, e)
	e.columns = append(e.columns, col0, col1)

	col1.AddWindow(" win ", "hello")

	e.resize()
	e.Draw()
	s.Show()

	// Drag col1's column tag handle at (60, 1) left by 7 cells.
	e.HandleEvent(tcell.NewEventMouse(60, 1, tcell.ButtonPrimary, 0))
	if e.capture == nil {
		t.Fatal("failed to start column drag")
	}
	e.HandleEvent(tcell.NewEventMouse(7, 1, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(7, 1, tcell.ButtonNone, 0))

	// Drag col1's handle back to 60.
	e.HandleEvent(tcell.NewEventMouse(7, 1, tcell.ButtonPrimary, 0))
	if e.capture == nil {
		t.Fatal("failed to start the second column drag")
	}
	e.HandleEvent(tcell.NewEventMouse(60, 1, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(60, 1, tcell.ButtonNone, 0))

	e.Draw()

	for y := 2; y < e.h; y++ {
		for x := 0; x < col0.w; x++ {
			str, _, _ := s.Get(x, y)
			r := ' '
			if len(str) > 0 {
				r = []rune(str)[0]
			}
			if r != ' ' {
				t.Errorf("non-blank cell at (%d,%d) in left column after drag: mainc=%q", x, y, r)
				return
			}
		}
	}
}

func TestDelcolLeavesBlank(t *testing.T) {
	e, s := setupTest(t, 100, 24)

	col := NewColumn(0, 1, e.w, e.h-1, e)
	e.columns = append(e.columns, col)
	col.AddWindow(" win ", "content")

	e.resize()
	e.Draw()
	s.Show()

	if len(e.columns) != 1 {
		t.Fatal("expected 1 column")
	}

	x, y, found := GetWordCoordinate(s, "Delcol", 0, 1)
	if !found {
		t.Fatal("could not find 'Delcol'")
	}

	ev := tcell.NewEventMouse(x, y, tcell.ButtonMiddle, 0)
	e.HandleEvent(ev)

	if len(e.columns) != 0 {
		t.Fatalf("expected 0 columns after Delcol, got %d", len(e.columns))
	}

	e.Draw()

	// Everything below global tag (y >= 1) must be blank.
	for y := 1; y < e.h; y++ {
		for x := 0; x < e.w; x++ {
			str, _, _ := s.Get(x, y)
			r := ' '
			if len(str) > 0 {
				r = []rune(str)[0]
			}
			if r != ' ' {
				t.Errorf("non-blank cell at (%d,%d) after Delcol: mainc=%q", x, y, r)
				return
			}
		}
	}
}

func TestDelcolNarrowNoExtraTagRow(t *testing.T) {
	// Narrow terminal (width < height). Two columns make each half-width,
	// which wraps the window tag to multiple lines. After deleting one
	// column the survivor gets full width, its tag un-wraps to 1 line,
	// and there must be no stale tag-background rows.
	e, s := setupTest(t, 80, 100)

	col0 := NewColumn(0, 1, e.w/2, e.h-1, e)
	col1 := NewColumn(e.w/2, 1, e.w-e.w/2, e.h-1, e)
	e.columns = append(e.columns, col0, col1)
	w := col1.AddWindow("", "")
	e.ActivateWindow(w)

	e.resize()
	e.Draw()
	s.Show()

	if len(e.columns) != 2 {
		t.Fatal("expected 2 columns")
	}

	// Programmatically delete col0 to avoid searching for truncated tag text.
	e.RemoveColumn(col0)

	if len(e.columns) != 1 {
		t.Fatalf("expected 1 column after RemoveColumn, got %d", len(e.columns))
	}

	e.resize()
	e.Draw()

	// Handle must be exactly 1 pixel high (tag un-wrapped to one line).
	if w.tag.h != 1 {
		t.Errorf("handle height = %d, want 1", w.tag.h)
	}

	// The row immediately below the window tag must have body background,
	// not tag background — no stale blank tag row.
	body := screenAt(e, w.body)
	_, style, _ := s.Get(body.X, body.Y)
	if style.GetBackground() == e.theme.Tag.BG {
		t.Errorf("row %d below window tag has TagBG background — stale extra tag row", body.Y)

	}
}

// --- drag-select scroll tests ---

func TestDragSelectAtBottomEdgeSetsScrollWin(t *testing.T) {
	e, s := setupTest(t, 80, 20)
	col := NewColumn(0, 1, 80, 19, e)
	e.columns = append(e.columns, col)

	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("L%02d", i)
	}
	win := col.AddWindow(" test ", strings.Join(lines, "\n"))
	e.ActivateWindow(win)
	e.resize()
	e.Draw()
	s.Show()

	tv := win.bodyTextView()
	bodyX, bodyY, bodyH := screenAt(e, tv).X, screenAt(e, tv).Y, tv.h

	// Press in the middle of the body to start a drag.
	e.HandleEvent(tcell.NewEventMouse(bodyX, bodyY+bodyH/2, tcell.ButtonPrimary, 0))
	// Move to the bottom edge.
	e.HandleEvent(tcell.NewEventMouse(bodyX, bodyY+bodyH-1, tcell.ButtonPrimary, 0))

	if e.repeat == nil {
		t.Fatal("at bottom edge: no auto-scroll")
	}
	before := tv.top()
	e.repeat()
	if tv.top() != before+1 {
		t.Errorf("at bottom edge: a tick scrolled from %d to %d, want down by 1", before, tv.top())
	}

	// Move back into the middle: the auto-scroll stops.
	e.HandleEvent(tcell.NewEventMouse(bodyX, bodyY+bodyH/2, tcell.ButtonPrimary, 0))
	if e.repeat != nil {
		t.Errorf("after moving to middle: auto-scroll should stop")
	}

	_ = s
}

func TestDragSelectTickExtendsSelection(t *testing.T) {
	e, s := setupTest(t, 80, 20)
	col := NewColumn(0, 1, 80, 19, e)
	e.columns = append(e.columns, col)

	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("L%02d", i)
	}
	win := col.AddWindow(" test ", strings.Join(lines, "\n"))
	e.ActivateWindow(win)
	e.resize()
	e.Draw()
	s.Show()

	tv := win.bodyTextView()
	bodyX, bodyY, bodyH := screenAt(e, tv).X, screenAt(e, tv).Y, tv.h

	// Start drag and drag to bottom edge, which arms the auto-scroll.
	e.HandleEvent(tcell.NewEventMouse(bodyX, bodyY, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(bodyX, bodyY+bodyH-1, tcell.ButtonPrimary, 0))

	if e.repeat == nil {
		t.Fatal("no auto-scroll after dragging to bottom edge")
	}

	wantScrollPos := tv.top() + 1
	endLine := func() int { line, _ := tv.buffer.Pos(tv.buffer.q1); return line }
	wantEndLine := endLine() + 1

	// One timer tick scrolls and advances the drag cursor.
	e.repeat()

	if tv.top() != wantScrollPos {
		t.Errorf("after tick: scroll.Pos = %d, want %d", tv.top(), wantScrollPos)
	}
	if endLine() != wantEndLine {
		t.Errorf("after tick: selection ends on line %d, want %d", endLine(), wantEndLine)
	}

	_ = s
	_ = time.Now // keep import used
}

func TestDragSelectInTagDoesNotScrollBody(t *testing.T) {
	e, s := setupTest(t, 80, 20)
	col := NewColumn(0, 1, 80, 19, e)
	e.columns = append(e.columns, col)

	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("L%02d", i)
	}
	win := col.AddWindow(" test Put Del ", strings.Join(lines, "\n"))
	e.ActivateWindow(win)
	e.resize()
	e.Draw()
	s.Show()

	// Click-drag across the tag.
	tag := screenAt(e, win.tag)
	e.HandleEvent(tcell.NewEventMouse(tag.X, tag.Y, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(tag.X+4, tag.Y, tcell.ButtonPrimary, 0))

	if e.repeat != nil {
		t.Error("dragging in tag must not auto-scroll")
	}

	_ = s
}

func TestEscToggleSelection(t *testing.T) {
	text := strings.Repeat("line\n", 20)
	tv := NewTextView(text, 40, 5, nil, nil, false, true)

	from, to := 15, 27 // line 3, column 0 to line 5, column 2
	tv.typedFrom = from
	tv.buffer.moveTo(to)

	esc := func() { tv.HandleEvent(tcell.NewEventKey(tcell.KeyEsc, "", 0)) }
	dot := func(step string, q0, q1 int) {
		t.Helper()
		if b := tv.buffer; b.q0 != q0 || b.q1 != q1 {
			t.Fatalf("%s: dot [%d, %d), want [%d, %d)", step, b.q0, b.q1, q0, q1)
		}
	}

	esc()
	dot("ESC1 selects what was typed", from, to)
	if tv.typedTo != to {
		t.Fatalf("ESC1: typedTo = %d, want %d", tv.typedTo, to)
	}
	esc()
	dot("ESC2 deselects to its start", from, from)
	esc()
	dot("ESC3 selects it again", from, to)
	esc()
	dot("ESC4 deselects again", from, from)

	// Typing breaks the cycle.
	tv.HandleEvent(tcell.NewEventKey(tcell.KeyRune, "x", 0))
	if tv.typedTo != -1 {
		t.Error("after typing: typedTo should be -1 (cycle broken)")
	}
}

func TestDragSelectStopsAtLastLine(t *testing.T) {
	e, s := setupTest(t, 80, 20)
	col := NewColumn(0, 1, 80, 19, e)
	e.columns = append(e.columns, col)

	// 3 lines fit exactly in a small body — leave room for tag row.
	win := col.AddWindow(" test ", "a\nb\nc")
	e.ActivateWindow(win)
	e.resize()
	e.Draw()
	s.Show()

	tv := win.bodyTextView()
	bodyX, bodyY, bodyH := screenAt(e, tv).X, screenAt(e, tv).Y, tv.h

	// Drag to the bottom edge to arm the scroll timer.
	e.HandleEvent(tcell.NewEventMouse(bodyX, bodyY, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(bodyX, bodyY+bodyH-1, tcell.ButtonPrimary, 0))

	scrollBefore := tv.top()
	q1Before := tv.buffer.q1

	// A tick at the boundary must neither scroll nor extend the selection.
	e.repeat()

	if tv.top() != scrollBefore {
		t.Errorf("scroll.Pos changed from %d to %d; should stay at boundary", scrollBefore, tv.top())
	}
	if tv.buffer.q1 != q1Before {
		t.Errorf("selection end moved from %d to %d; should stay at boundary", q1Before, tv.buffer.q1)
	}

	_ = s
}

// The scroll bar holds the mouse until release: Button3 scrolls down by the
// rows down to the pointer and repeats while held, and moving the held pointer
// onto the body acts there no more than a drag elsewhere does.
func TestScrollBarHoldsMouse(t *testing.T) {
	e, _ := setupTest(t, 80, 20)
	col := NewColumn(0, 1, 80, 19, e)
	e.columns = append(e.columns, col)
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("L%02d", i)
	}
	win := col.AddWindow(" test ", strings.Join(lines, "\n"))
	e.resize()
	e.Draw()

	tv := win.bodyTextView()
	bar, body := screenAt(e, win).X, screenAt(e, win.body)
	e.HandleEvent(tcell.NewEventMouse(bar, body.Y+2, tcell.ButtonSecondary, 0))
	if tv.top() != 3 {
		t.Fatalf("Button3 on the bar's third row scrolled to %d, want 3", tv.top())
	}
	if e.repeat == nil {
		t.Fatal("holding Button3 on the bar should repeat")
	}

	e.HandleEvent(tcell.NewEventMouse(body.X+1, body.Y+4, tcell.ButtonSecondary, 0))
	if tv.top() != 3 || tv.buffer.q1 != 0 {
		t.Errorf("moving the held pointer onto the body acted there: scroll %d, dot end %d", tv.top(), tv.buffer.q1)
	}

	e.HandleEvent(tcell.NewEventMouse(body.X+1, body.Y+4, tcell.ButtonNone, 0))
	if e.capture != nil || e.repeat != nil {
		t.Error("release should end the scroll")
	}
}

// --- window handle button 2/3 tests ---

// verifyNoWindowOverlap asserts no two windows in a column share any row.
func verifyNoWindowOverlap(t *testing.T, col *Column) {
	t.Helper()
	for i, a := range col.windows {
		for j, b := range col.windows {
			if i >= j {
				continue
			}
			if a.y < b.y+b.h && b.y < a.y+a.h {
				t.Errorf("windows [%d] (y=%d,h=%d) and [%d] (y=%d,h=%d) overlap",
					i, a.y, a.h, j, b.y, b.h)
			}
		}
	}
}

// TestHandleButton1GrowsModerate verifies that a static Button1 click on a
// window handle grows the window by stealing rows from nearest neighbours,
// without collapsing anyone and without triggering maximize.
func TestHandleButton1GrowsModerate(t *testing.T) {
	e, _ := setupTest(t, 120, 40)
	col := NewColumn(0, 1, e.w, e.h-1, e)
	e.columns = append(e.columns, col)

	col.AddWindow(" w1 ", "c1")
	w2 := col.AddWindow(" w2 ", "c2")
	col.AddWindow(" w3 ", "c3")

	e.resize()
	e.Draw()

	before := w2.h

	// Static Button1 click on w2's handle.
	e.HandleEvent(tcell.NewEventMouse(screenAt(e, w2).X, screenAt(e, w2).Y, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(screenAt(e, w2).X, screenAt(e, w2).Y, tcell.ButtonNone, 0))

	if w2.h <= before {
		t.Errorf("Button1 grow: w2.h = %d, want > %d", w2.h, before)
	}
	if col.maximized != nil {
		t.Error("Button1 grow must not set col.maximized")
	}

	// All windows must still be on-screen.
	colBottom := col.h // in the column
	for _, w := range col.windows {
		if w.y+w.h > colBottom {
			t.Errorf("window ID=%d (y=%d,h=%d) pushed off-screen after moderate grow", w.ID, w.y, w.h)
		}
		if w.h < w.MinSize() {
			t.Errorf("window ID=%d (h=%d) below MinSize=%d after moderate grow", w.ID, w.h, w.MinSize())
		}
	}
	verifyNoWindowOverlap(t, col)
}

// TestHandleButton2Maximizes verifies that a static Button2 click on a window
// handle maximizes that window: it moves to position 0, fills the column, and
// all other windows are pushed entirely off-screen below the column bottom.
func TestHandleButton2Maximizes(t *testing.T) {
	e, _ := setupTest(t, 120, 40)
	col := NewColumn(0, 1, e.w, e.h-1, e)
	e.columns = append(e.columns, col)

	col.AddWindow(" w1 ", "c1")
	col.AddWindow(" w2 ", "c2")
	w3 := col.AddWindow(" w3 ", "c3")

	e.resize()
	e.Draw()

	// Static Button2 click on w3's handle (last window, below others).
	e.HandleEvent(tcell.NewEventMouse(screenAt(e, w3).X, screenAt(e, w3).Y, tcell.ButtonSecondary, 0))
	e.HandleEvent(tcell.NewEventMouse(screenAt(e, w3).X, screenAt(e, w3).Y, tcell.ButtonNone, 0))

	if col.maximized != w3 {
		t.Fatalf("col.maximized = %v, want w3", col.maximized)
	}
	if col.windows[0] != w3 {
		t.Errorf("maximized window should be at index 0, got ID=%d", col.windows[0].ID)
	}
	if w3.h != col.h-1 {
		t.Errorf("maximized window h = %d, want %d (col.h-1)", w3.h, col.h-1)
	}
	if w3.y != 1 {
		t.Errorf("maximized window y = %d, want 1 (top of column)", w3.y)
	}

	// All other windows must be pushed completely off-screen.
	colBottom := col.h // in the column
	for _, w := range col.windows {
		if w != w3 && w.y < colBottom {
			t.Errorf("non-maximized window ID=%d should be off-screen (y=%d, colBottom=%d)",
				w.ID, w.y, colBottom)
		}
	}

	verifyNoWindowOverlap(t, col)
}

// TestHandleButton3GrowsExitsMaximize verifies that a static Button3 click on
// a maximized window's handle exits maximize mode and makes all windows visible,
// with the clicked window taking the largest share.
func TestHandleButton3GrowsExitsMaximize(t *testing.T) {
	e, _ := setupTest(t, 120, 40)
	col := NewColumn(0, 1, e.w, e.h-1, e)
	e.columns = append(e.columns, col)

	col.AddWindow(" w1 ", "c1")
	w2 := col.AddWindow(" w2 ", "c2")
	col.AddWindow(" w3 ", "c3")

	e.resize()
	e.Draw()

	// Maximize w2 first.
	e.HandleEvent(tcell.NewEventMouse(screenAt(e, w2).X, screenAt(e, w2).Y, tcell.ButtonSecondary, 0))
	e.HandleEvent(tcell.NewEventMouse(screenAt(e, w2).X, screenAt(e, w2).Y, tcell.ButtonNone, 0))
	if col.maximized != w2 {
		t.Fatal("setup: expected w2 to be maximized")
	}

	// w2 is now at the top of the column. Static Button3 click
	// on its handle exits maximize and grows w2 while keeping all visible.
	e.HandleEvent(tcell.NewEventMouse(screenAt(e, w2).X, screenAt(e, w2).Y, tcell.ButtonMiddle, 0))
	e.HandleEvent(tcell.NewEventMouse(screenAt(e, w2).X, screenAt(e, w2).Y, tcell.ButtonNone, 0))

	if col.maximized != nil {
		t.Fatalf("col.maximized should be nil after Button3 grow, got %v", col.maximized)
	}

	colBottom := col.h // in the column
	for _, w := range col.windows {
		if w.y+w.h > colBottom {
			t.Errorf("window ID=%d (y=%d,h=%d) extends below column bottom (%d) after grow",
				w.ID, w.y, w.h, colBottom)
		}
	}

	// All other windows must be at their minimum (tag-only) height.
	for _, w := range col.windows {
		if w != w2 && w.h > w.MinSize() {
			t.Errorf("window ID=%d (h=%d) exceeds minSize=%d after grow; should be collapsed",
				w.ID, w.h, w.MinSize())
		}
	}

	verifyNoWindowOverlap(t, col)
}

// TestHandleButton2DragMovesWindow verifies that dragging with Button2 on the
// handle moves/swaps the window without triggering maximize.
func TestHandleButton2DragMovesWindow(t *testing.T) {
	e, _ := setupTest(t, 120, 60)
	col := NewColumn(0, 1, e.w, e.h-1, e)
	e.columns = append(e.columns, col)

	w1 := col.AddWindow(" w1 ", "c1")
	w2 := col.AddWindow(" w2 ", "c2")
	w3 := col.AddWindow(" w3 ", "c3")

	e.resize()
	e.Draw()

	// Button2 press on w2's handle.
	e.HandleEvent(tcell.NewEventMouse(screenAt(e, w2).X, screenAt(e, w2).Y, tcell.ButtonSecondary, 0))
	if e.capture == nil {
		t.Fatal("Button2 on handle should start a window drag")
	}

	// Drag past w3's midpoint to trigger a swap, then release at a different Y.
	dragY := screenAt(e, w3).Y + w3.h/2 + 1
	e.HandleEvent(tcell.NewEventMouse(screenAt(e, w2).X, dragY, tcell.ButtonSecondary, 0))
	e.HandleEvent(tcell.NewEventMouse(screenAt(e, w2).X, dragY, tcell.ButtonNone, 0))

	// Released at a different row → no maximize.
	if col.maximized != nil {
		t.Error("dragging with Button2 should not trigger maximize")
	}
	// Swap happened: [w1, w3, w2].
	if col.windows[0] != w1 || col.windows[1] != w3 || col.windows[2] != w2 {
		t.Errorf("expected order [w1,w3,w2] after drag, got [%d,%d,%d]",
			col.windows[0].ID, col.windows[1].ID, col.windows[2].ID)
	}
	verifyNoWindowOverlap(t, col)
}

// TestHandleButton3DragMovesWindow verifies that dragging with Button3 on the
// handle moves/swaps the window without triggering grow.
func TestHandleButton3DragMovesWindow(t *testing.T) {
	e, _ := setupTest(t, 120, 60)
	col := NewColumn(0, 1, e.w, e.h-1, e)
	e.columns = append(e.columns, col)

	w1 := col.AddWindow(" w1 ", "c1")
	w2 := col.AddWindow(" w2 ", "c2")
	w3 := col.AddWindow(" w3 ", "c3")

	e.resize()
	e.Draw()

	e.HandleEvent(tcell.NewEventMouse(screenAt(e, w2).X, screenAt(e, w2).Y, tcell.ButtonMiddle, 0))
	if e.capture == nil {
		t.Fatal("Button3 on handle should start a window drag")
	}

	dragY := screenAt(e, w3).Y + w3.h/2 + 1
	e.HandleEvent(tcell.NewEventMouse(screenAt(e, w2).X, dragY, tcell.ButtonMiddle, 0))
	e.HandleEvent(tcell.NewEventMouse(screenAt(e, w2).X, dragY, tcell.ButtonNone, 0))

	// Released at a different row → no grow triggered.
	if col.maximized != nil {
		t.Error("dragging with Button3 should not trigger grow/un-maximize")
	}
	if col.windows[0] != w1 || col.windows[1] != w3 || col.windows[2] != w2 {
		t.Errorf("expected order [w1,w3,w2] after drag, got [%d,%d,%d]",
			col.windows[0].ID, col.windows[1].ID, col.windows[2].ID)
	}
	verifyNoWindowOverlap(t, col)
}

// TestRemoveMaximizedWindowClearsFlag verifies that removing the maximized
// window clears col.maximized and restores normal layout for remaining windows.
func TestRemoveMaximizedWindowClearsFlag(t *testing.T) {
	e, _ := setupTest(t, 120, 40)
	col := NewColumn(0, 1, e.w, e.h-1, e)
	e.columns = append(e.columns, col)

	w1 := col.AddWindow(" w1 ", "c1")
	w2 := col.AddWindow(" w2 ", "c2")

	e.resize()
	e.Draw()

	// Maximize w1.
	e.HandleEvent(tcell.NewEventMouse(screenAt(e, w1).X, screenAt(e, w1).Y, tcell.ButtonSecondary, 0))
	e.HandleEvent(tcell.NewEventMouse(screenAt(e, w1).X, screenAt(e, w1).Y, tcell.ButtonNone, 0))
	if col.maximized != w1 {
		t.Fatal("setup: w1 should be maximized")
	}

	e.RemoveWindow(w1)

	if col.maximized != nil {
		t.Errorf("col.maximized should be nil after removing the maximized window, got %v", col.maximized)
	}
	// w2 must now be laid out on-screen.
	colBottom := col.h // in the column
	if w2.y+w2.h > colBottom {
		t.Errorf("w2 should be on-screen after the maximized window was removed (y=%d,h=%d,colBottom=%d)",
			w2.y, w2.h, colBottom)
	}
}

// TestMoveMaximizedWindowClearsSourceFlag verifies that dragging the maximized
// window to another column clears col.maximized on the source column and
// restores normal on-screen layout for windows left behind.
func TestMoveMaximizedWindowClearsSourceFlag(t *testing.T) {
	e, _ := setupTest(t, 120, 40)
	col0 := NewColumn(0, 1, 60, e.h-1, e)
	col0.explicitWidth = 60
	col1 := NewColumn(60, 1, 60, e.h-1, e)
	col1.explicitWidth = 60
	e.columns = append(e.columns, col0, col1)

	w1 := col0.AddWindow(" w1 ", "c1")
	w2 := col0.AddWindow(" w2 ", "c2")

	e.resize()
	e.Draw()

	// Maximize w1 in col0.
	e.HandleEvent(tcell.NewEventMouse(screenAt(e, w1).X, screenAt(e, w1).Y, tcell.ButtonSecondary, 0))
	e.HandleEvent(tcell.NewEventMouse(screenAt(e, w1).X, screenAt(e, w1).Y, tcell.ButtonNone, 0))
	if col0.maximized != w1 {
		t.Fatal("setup: w1 should be maximized in col0")
	}

	// Drag w1 into col1 using Button1 (regular drag, past col0's right edge).
	e.HandleEvent(tcell.NewEventMouse(screenAt(e, w1).X, screenAt(e, w1).Y, tcell.ButtonPrimary, 0))
	if e.capture == nil {
		t.Fatal("failed to start dragging w1")
	}
	e.HandleEvent(tcell.NewEventMouse(col1.x+5, 10, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(col1.x+5, 10, tcell.ButtonNone, 0))

	if col0.maximized != nil {
		t.Errorf("col0.maximized should be nil after w1 moved out, got %v", col0.maximized)
	}
	inCol1 := false
	for _, w := range col1.windows {
		if w == w1 {
			inCol1 = true
		}
	}
	if !inCol1 {
		t.Error("w1 should have moved to col1")
	}

	// w2 should be on-screen in col0.
	colBottom := col0.h // in the column
	if w2.y+w2.h > colBottom {
		t.Errorf("w2 should be on-screen in col0 after w1 moved out (y=%d,h=%d,colBottom=%d)",
			w2.y, w2.h, colBottom)
	}
}

// TestColumnGutterAllButtonsStartDrag verifies that Button1, Button2, and
// Button3 all start a column drag when clicking the column gutter.
func TestColumnGutterAllButtonsStartDrag(t *testing.T) {
	for _, btn := range []tcell.ButtonMask{tcell.ButtonPrimary, tcell.ButtonSecondary, tcell.ButtonMiddle} {
		name := map[tcell.ButtonMask]string{
			tcell.ButtonPrimary:   "Button1",
			tcell.ButtonSecondary: "Button2",
			tcell.ButtonMiddle:    "Button3",
		}[btn]
		t.Run(name, func(t *testing.T) {
			e, _ := setupTest(t, 120, 30)
			col0 := NewColumn(0, 1, 60, e.h-1, e)
			col0.explicitWidth = 60
			col1 := NewColumn(60, 1, 60, e.h-1, e)
			col1.explicitWidth = 60
			e.columns = append(e.columns, col0, col1)
			e.resize()
			e.Draw()

			// Click col1's gutter at (col1.x, col1.y).
			e.HandleEvent(tcell.NewEventMouse(col1.x, col1.y, btn, 0))
			if e.capture == nil {
				t.Fatalf("%s on column gutter should start a column drag", name)
			}

			// Drag left: the drag lasts while the button is held.
			e.HandleEvent(tcell.NewEventMouse(30, col1.y, btn, 0))
			if e.capture == nil {
				t.Fatalf("%s: drag ended while the button was held", name)
			}
			if col1.x != 30 {
				t.Errorf("%s: col1.x = %d after dragging to 30", name, col1.x)
			}

			// Release ends the drag.
			e.HandleEvent(tcell.NewEventMouse(30, col1.y, tcell.ButtonNone, 0))
			if e.capture != nil {
				t.Fatalf("%s: drag should end on release", name)
			}
		})
	}
}

// A tag whose text no longer fits on one line grows, and the body moves down
// to make room.
func TestTagGrowsWithItsText(t *testing.T) {
	e, _ := setupTest(t, 30, 20)
	col := NewColumn(0, 1, 30, 19, e)
	e.columns = append(e.columns, col)
	win := col.AddWindow(" /tmp/a Del ", "body")
	e.resize()
	if win.tag.h != 1 {
		t.Fatalf("tag height = %d, want 1", win.tag.h)
	}

	win.tag.buffer.SetText(" /tmp/a Del Get Put Undo Redo Snarf Zerox ")
	if win.tag.h != 2 {
		t.Fatalf("tag height after it outgrew one line = %d, want 2", win.tag.h)
	}
	if _, _, visible := win.body.GetScroll(); visible != win.h-2 {
		t.Errorf("body height = %d, want %d", visible, win.h-2)
	}
}

// The view stays on the text it shows when lines are added or removed above
// it, and an undo puts it back.
func TestScrollStaysOnItsText(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("L%02d", i)
	}
	win := col.AddWindow(" /tmp/s Del ", strings.Join(lines, "\n"))
	col.Resize(col.rect)
	tv := win.bodyTextView()
	shown := func() string { return string(tv.buffer.lines[tv.lines()[tv.top()].BufferLine]) }
	tv.setTop(50)

	tv.buffer.saveState()
	tv.buffer.replace(0, 0, "new\nlines\n")
	if got := shown(); got != "L50" {
		t.Errorf("after inserting above, the view shows %q first, want L50", got)
	}
	tv.buffer.Undo()
	if got := shown(); got != "L50" {
		t.Errorf("after undo, the view shows %q first, want L50", got)
	}
}

// Changing the tab width wraps the text again at once, not at the next edit.
func TestTabRewraps(t *testing.T) {
	e, col := newTestEditorWithColumn(t)
	win := col.AddWindow(" /tmp/t Del ", "\tx")
	col.Resize(col.rect)
	tv := win.bodyTextView()
	tv.w = 6 // "\tx" takes 5 columns at tab width 4, 9 at 8
	if n := len(tv.lines()); n != 1 {
		t.Fatalf("%d visual lines at tab width 4, want 1", n)
	}
	e.Execute(col, win, "Tab 8")
	if n := len(tv.lines()); n != 2 {
		t.Errorf("%d visual lines at tab width 8, want 2", n)
	}
}
