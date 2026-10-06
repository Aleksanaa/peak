package main

import (
	"fmt"
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
	col.Resize(col.x, col.y, col.w, col.h)

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

	go func() {
		for fn := range e.callCh {
			fn()
			e.Draw()
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
			if condition() {
				done <- true
				return
			}
			ev, ok := <-s.EventQ()
			if !ok {
				return
			}
			if intr, ok := ev.(*tcell.EventInterrupt); ok {
				if f, ok := intr.Data().(func()); ok {
					e.Call(f)
					e.Draw()
					s.Show()
				}
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

	e.tag.Draw(s)
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
	tv := NewTextView("abc", 0, 0, 10, 1, nil, nil, false, false)

	tv.HandleEvent(tcell.NewEventMouse(0, 0, tcell.ButtonPrimary, 0))

	if got := tv.buffer.cursor; got != (Cursor{0, 0}) {
		t.Fatalf("cursor after clicking first character = %+v, want %+v", got, Cursor{0, 0})
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
		win.bodyTextView().buffer.Insert('x')
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
	buf.SetSelection(Cursor{0, 4}, Cursor{8, 4})

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
	if e.dragWin != w1 {
		t.Fatal("Failed to start dragging w1")
	}
	// Step into Col 1 (past col0's right edge at x=colWidth)
	e.HandleEvent(tcell.NewEventMouse(colWidth+5, 10, tcell.ButtonPrimary, 0))
	// Step into Col 2 (past col1's right edge at x=2*colWidth)
	e.HandleEvent(tcell.NewEventMouse(2*colWidth+5, 10, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(2*colWidth+5, 10, tcell.ButtonNone, 0))

	// 2. Drag W2 (Col 1) to Col 0
	// Handle for W2 is at (e.columns[1].x, w2.y) = (40, 2)
	e.HandleEvent(tcell.NewEventMouse(colWidth, 2, tcell.ButtonPrimary, 0))
	if e.dragWin != w2 {
		t.Fatal("Failed to start dragging w2")
	}
	// Left threshold: mx < col0.x+col0.w-col0.w/4 = 40-10 = 30; use x=5.
	e.HandleEvent(tcell.NewEventMouse(5, 10, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(5, 10, tcell.ButtonNone, 0))

	// 3. Drag W3 (Col 1, now alone at top) to Col 2
	e.HandleEvent(tcell.NewEventMouse(colWidth, 2, tcell.ButtonPrimary, 0))
	if e.dragWin != w3 {
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
	// W1 handle at (0, w1.y). We drop it in W2's body area.
	e.HandleEvent(tcell.NewEventMouse(0, w1.y, tcell.ButtonPrimary, 0))
	if e.dragWin != w1 {
		t.Fatal("drag w1 failed")
	}
	// Drag past W2's midpoint to trigger swap-right
	e.HandleEvent(tcell.NewEventMouse(0, w2.y+w2.h/2+1, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(0, w2.y+w2.h/2+1, tcell.ButtonNone, 0))

	if col.windows[0] != w2 || col.windows[1] != w1 || col.windows[2] != w3 {
		t.Errorf("Order after first drag wrong: %d, %d, %d", col.windows[0].ID, col.windows[1].ID, col.windows[2].ID)
	}

	// 2. Drag W3 (idx 2) above W1 (idx 1).
	// Current: [w2, w1, w3]
	// We drop it in W1's tag area (w1.y)
	e.HandleEvent(tcell.NewEventMouse(0, w3.y, tcell.ButtonPrimary, 0))
	if e.dragWin != w3 {
		t.Fatal("drag w3 failed")
	}
	e.HandleEvent(tcell.NewEventMouse(0, w1.y, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(0, w1.y, tcell.ButtonNone, 0))

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
		e.HandleEvent(tcell.NewEventMouse(0, w2.y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, w3.y+w3.h/2+1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, w3.y+w3.h/2+1, tcell.ButtonNone, 0))
		checkOrder(t, col, w1, w3, w2)
		checkHeights(t, w1, w2, w3, h1, h2, h3)
	})

	// swap-left: drag w3 into w2's tag area → [w1, w3, w2]
	t.Run("3to2", func(t *testing.T) {
		e, col, w1, w2, w3 := newSetup(t)
		h1, h2, h3 := w1.h, w2.h, w3.h
		e.HandleEvent(tcell.NewEventMouse(0, w3.y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, w2.y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, w2.y, tcell.ButtonNone, 0))
		checkOrder(t, col, w1, w3, w2)
		checkHeights(t, w1, w2, w3, h1, h2, h3)
	})

	// swap-right from first: drag w1 past w2's midpoint → [w2, w1, w3]
	t.Run("1to2", func(t *testing.T) {
		e, col, w1, w2, w3 := newSetup(t)
		h1, h2, h3 := w1.h, w2.h, w3.h
		e.HandleEvent(tcell.NewEventMouse(0, w1.y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, w2.y+w2.h/2+1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, w2.y+w2.h/2+1, tcell.ButtonNone, 0))
		checkOrder(t, col, w2, w1, w3)
		checkHeights(t, w1, w2, w3, h1, h2, h3)
	})

	// swap-left to first: drag w2 into w1's tag area → [w2, w1, w3]
	t.Run("2to1", func(t *testing.T) {
		e, col, w1, w2, w3 := newSetup(t)
		h1, h2, h3 := w1.h, w2.h, w3.h
		e.HandleEvent(tcell.NewEventMouse(0, w2.y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, w1.y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, w1.y, tcell.ButtonNone, 0))
		checkOrder(t, col, w2, w1, w3)
		checkHeights(t, w1, w2, w3, h1, h2, h3)
	})

	// two consecutive swap-rights: drag w1 past w2, then past w3 → [w2, w3, w1]
	t.Run("1to3", func(t *testing.T) {
		e, col, w1, w2, w3 := newSetup(t)
		h1, h2, h3 := w1.h, w2.h, w3.h
		e.HandleEvent(tcell.NewEventMouse(0, w1.y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, w2.y+w2.h/2+1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, w3.y+w3.h/2+1, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, w3.y+w3.h/2+1, tcell.ButtonNone, 0))
		checkOrder(t, col, w2, w3, w1)
		checkHeights(t, w1, w2, w3, h1, h2, h3)
	})

	// two consecutive swap-lefts: drag w3 into w2's tag, then into w1's tag → [w3, w1, w2]
	t.Run("3to1", func(t *testing.T) {
		e, col, w1, w2, w3 := newSetup(t)
		h1, h2, h3 := w1.h, w2.h, w3.h
		e.HandleEvent(tcell.NewEventMouse(0, w3.y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, w2.y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, w1.y, tcell.ButtonPrimary, 0))
		e.HandleEvent(tcell.NewEventMouse(0, w1.y, tcell.ButtonNone, 0))
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
		px, py, pfound := GetWordCoordinate(s, "Put", 0, win.tag.y)
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
		dx, dy, dfound := GetWordCoordinate(s, "Del", 0, win.tag.y)
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
		dx, dy, _ = GetWordCoordinate(s, "Del", 0, win.tag.y)
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
	start := win.tag.buffer.RuneOffsetToCursor(idx)
	end := win.tag.buffer.RuneOffsetToCursor(idx + len("uname -a"))
	win.tag.buffer.SetSelection(start, end)

	// 3. Middle click on the selection in the tag
	tx, ty, tfound := GetWordCoordinate(s, "uname -a", 0, win.tag.y)
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
	expected, _ := runLocalCommand("uname -a", "/tmp/", "/tmp/", "", 0)
	expected = strings.TrimSpace(expected)
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
	bstart := errWin.body.GetBuffer().RuneOffsetToCursor(bidx)
	bend := errWin.body.GetBuffer().RuneOffsetToCursor(bidx + len("uname -a"))
	errWin.body.GetBuffer().SetSelection(bstart, bend)

	// 8. Run it (middle click)
	errY := errWin.bodyTextView().y
	bx, by, bfound := GetWordCoordinate(s, "uname -a", 0, errY)
	if !bfound {
		t.Fatal("Could not find 'uname -a' in +Errors body")
	}
	e.HandleEvent(tcell.NewEventMouse(bx, by, tcell.ButtonMiddle, 0))

	// 9. Observe result isn't changed (replaces buffer with same output)
	waitFor(t, e, s, func() bool {
		newOutput := strings.TrimSpace(errWin.body.GetBuffer().GetText())
		return newOutput == expected
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
	px, py, _ := GetWordCoordinate(s, "Put", 0, win.tag.y)
	e.HandleEvent(tcell.NewEventMouse(px, py, tcell.ButtonMiddle, 0))

	// Wait for Put
	waitFor(t, e, s, func() bool {
		return win.savedVersion == win.body.GetBuffer().version
	})

	// Close window
	dx, dy, _ := GetWordCoordinate(s, "Del", 0, win.tag.y)
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
	bodyY := dirWin.bodyTextView().y
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
		if tv.buffer.cursor.y != 1 {
			t.Errorf("cursor line: got %d, want 1", tv.buffer.cursor.y)
		}
		if tv.buffer.cursor.x != 5 {
			t.Errorf("cursor col: got %d, want 5", tv.buffer.cursor.x)
		}
		if tv.buffer.selection.Active {
			t.Error("expected no selection for line:col plumb")
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
		if tv.buffer.cursor.y != 2 {
			t.Errorf("cursor line: got %d, want 2", tv.buffer.cursor.y)
		}
		if !tv.buffer.selection.Active {
			t.Error("expected selection for line-only plumb")
		}
		start, end := tv.buffer.selection.Ordered()
		if start.y != 2 || start.x != 0 {
			t.Errorf("selection start: got (%d,%d), want (0,2)", start.x, start.y)
		}
		if end.y != 2 {
			t.Errorf("selection end line: got %d, want 2", end.y)
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

func TestScrollStateClampDoesNotFreezeShortContent(t *testing.T) {
	// Clamp must not force limit=0 when total <= visible.
	var s ScrollState
	s.Pos = 7
	s.Clamp(10, 30)
	if s.Pos != 7 {
		t.Fatalf("Clamp(10,30) with Pos=7: got %d, want 7", s.Pos)
	}
	s.Pos = 15
	s.Clamp(10, 30)
	if s.Pos != 10 {
		t.Fatalf("Clamp(10,30) with Pos=15: got %d, want 10 (clamp upper bound to total)", s.Pos)
	}
}

func TestScrollStateScrollUpDisablesAutoScroll(t *testing.T) {
	var s ScrollState
	s.AutoScroll = true
	s.Scroll(-1, 100, 30)
	if s.AutoScroll {
		t.Fatal("wheel-up must disable AutoScroll")
	}
}

func TestScrollStateScrollDownAtBottomEnablesAutoScroll(t *testing.T) {
	var s ScrollState
	s.Pos = 69 // total=100, visible=30, bottom starts at 70
	s.AutoScroll = false
	s.Scroll(1, 100, 30)
	if !s.AutoScroll {
		t.Fatal("wheel-down at bottom must enable AutoScroll")
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
	tv := NewTextView(body, 0, 0, 40, 10, nil, nil, false, true)
	tv.UpdateLayout()

	bx, by := tv.visualToBuffer(0, 9)
	tv.buffer.cursor = Cursor{bx, by}
	tv.HandleEvent(tcell.NewEventKey(tcell.KeyDown, "", 0))

	if tv.scroll.Pos != 1 {
		t.Fatalf("after KeyDown past visible, scroll.Pos=%d, want 1", tv.scroll.Pos)
	}
	if !tv.scroll.AutoScroll {
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
	tv := NewTextView(body, 0, 0, 40, 10, nil, nil, false, true)
	tv.UpdateLayout()

	tv.scroll.Pos = 50
	tv.scroll.AutoScroll = false
	tv.HandleEvent(tcell.NewEventKey(tcell.KeyRune, "x", 0))

	if tv.scroll.Pos != 0 {
		t.Fatalf("after typing while scrolled away, scroll.Pos=%d, want 0", tv.scroll.Pos)
	}
	if !tv.scroll.AutoScroll {
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
	tv := NewTextView(body, 0, 0, 40, 10, nil, nil, false, true)
	tv.UpdateLayout()

	tv.scroll.Pos = 50
	tv.scroll.AutoScroll = true
	tv.SyncScroll()

	if tv.scroll.Pos != 50 {
		t.Fatalf("SyncScroll snapped upward: scroll.Pos=%d, want 50", tv.scroll.Pos)
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
	tv := NewTextView(body, 0, 0, 40, 10, nil, nil, false, true)
	tv.UpdateLayout()

	tv.buffer.cursor = Cursor{0, 95}
	tv.UpdateLayout()
	tv.scroll.Pos = 80
	tv.scroll.AutoScroll = true
	tv.SyncScroll()

	if tv.scroll.Pos <= 80 {
		t.Fatalf("SyncScroll did not follow downward: scroll.Pos=%d, want > 80", tv.scroll.Pos)
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
	if e.dragWin != w2 {
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
	w2HandleY := w2.y
	e.HandleEvent(tcell.NewEventMouse(0, w2HandleY, tcell.ButtonPrimary, 0))
	if e.dragWin != w2 {
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
	if e.dragCol != col1 {
		t.Fatal("failed to start column drag")
	}
	e.HandleEvent(tcell.NewEventMouse(7, 1, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(7, 1, tcell.ButtonNone, 0))

	// Drag col1's handle back to 60.
	e.HandleEvent(tcell.NewEventMouse(7, 1, tcell.ButtonPrimary, 0))
	if e.dragCol != col1 {
		t.Fatalf("expected dragCol=col1 after second start, got %v", e.dragCol)
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
	if w.handle.h != 1 {
		t.Errorf("handle height = %d, want 1", w.handle.h)
	}

	// The row immediately below the window tag must have body background,
	// not tag background — no stale blank tag row.
	bodyRow := w.y + w.tagHeight()
	_, style, _ := s.Get(w.x+1, bodyRow)
	if style.GetBackground() == e.theme.Tag.BG {
		t.Errorf("row %d below window tag has TagBG background — stale extra tag row", bodyRow)
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
	bodyX, bodyY, bodyH := tv.x, tv.y, tv.h

	// Press in the middle of the body to start a drag.
	e.HandleEvent(tcell.NewEventMouse(bodyX, bodyY+bodyH/2, tcell.ButtonPrimary, 0))
	// Move to the bottom edge.
	e.HandleEvent(tcell.NewEventMouse(bodyX, bodyY+bodyH-1, tcell.ButtonPrimary, 0))

	if e.scrollWin != win {
		t.Fatalf("at bottom edge: scrollWin = %v, want win", e.scrollWin)
	}
	if e.scrollDir != 1 {
		t.Errorf("at bottom edge: scrollDir = %d, want 1", e.scrollDir)
	}

	// Move back into the middle — scrollWin must be cleared.
	e.HandleEvent(tcell.NewEventMouse(bodyX, bodyY+bodyH/2, tcell.ButtonPrimary, 0))
	if e.scrollWin != nil {
		t.Errorf("after moving to middle: scrollWin should be nil")
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
	bodyX, bodyY, bodyH := tv.x, tv.y, tv.h

	// Start drag and drag to bottom edge (sets scrollWin + dir=1).
	e.HandleEvent(tcell.NewEventMouse(bodyX, bodyY, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(bodyX, bodyY+bodyH-1, tcell.ButtonPrimary, 0))

	if e.scrollWin != win {
		t.Fatal("scrollWin not set after dragging to bottom edge")
	}

	wantScrollPos := tv.scroll.Pos + 1
	wantEndY := tv.buffer.selection.End.y + 1

	// Simulate one timer tick: scroll then advance the drag cursor.
	win.body.Scroll(e.scrollDir * e.scrollAmount)
	win.body.(dragCursor).AdvanceDragCursor(e.scrollDir)

	if tv.scroll.Pos != wantScrollPos {
		t.Errorf("after tick: scroll.Pos = %d, want %d", tv.scroll.Pos, wantScrollPos)
	}
	if tv.buffer.selection.End.y != wantEndY {
		t.Errorf("after tick: selection.End.y = %d, want %d", tv.buffer.selection.End.y, wantEndY)
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

	// Click-drag across the tag (y = win.tag.y).
	tagY := win.tag.y
	e.HandleEvent(tcell.NewEventMouse(win.x+1, tagY, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(win.x+5, tagY, tcell.ButtonPrimary, 0))

	if e.scrollWin != nil {
		t.Error("dragging in tag must not set scrollWin")
	}

	_ = s
}

func TestEscToggleSelection(t *testing.T) {
	text := strings.Repeat("line\n", 20)
	tv := NewTextView(text, 0, 0, 40, 5, nil, nil, false, true)
	tv.UpdateLayout()

	selStart := Cursor{0, 3}
	selEnd := Cursor{2, 5}
	tv.typingStart = &selStart
	tv.buffer.cursor = selEnd

	esc := func() { tv.HandleEvent(tcell.NewEventKey(tcell.KeyEsc, "", 0)) }

	// ESC1: typing → select [selStart, selEnd]
	esc()
	if !tv.buffer.selection.Active {
		t.Fatal("ESC1: selection should be active")
	}
	if tv.typingEnd == nil {
		t.Fatal("ESC1: typingEnd should be set")
	}
	s, e := tv.buffer.selection.Ordered()
	if s != selStart || e != selEnd {
		t.Errorf("ESC1: selection %v–%v, want %v–%v", s, e, selStart, selEnd)
	}

	// ESC2: deselect, cursor moves to start of selection
	esc()
	if tv.buffer.selection.Active {
		t.Fatal("ESC2: selection should be cleared")
	}
	if tv.buffer.cursor != selStart {
		t.Errorf("ESC2: cursor %v, want %v", tv.buffer.cursor, selStart)
	}

	// ESC3: re-select same range
	esc()
	if !tv.buffer.selection.Active {
		t.Fatal("ESC3: selection should be active again")
	}
	s2, e2 := tv.buffer.selection.Ordered()
	if s2 != selStart || e2 != selEnd {
		t.Errorf("ESC3: selection %v–%v, want %v–%v", s2, e2, selStart, selEnd)
	}

	// ESC4: deselect again
	esc()
	if tv.buffer.selection.Active {
		t.Fatal("ESC4: selection should be cleared")
	}
	if tv.buffer.cursor != selStart {
		t.Errorf("ESC4: cursor %v, want %v", tv.buffer.cursor, selStart)
	}

	// Typing must break the toggle cycle (typingEnd cleared)
	tv.HandleEvent(tcell.NewEventKey(tcell.KeyRune, "x", 0))
	if tv.typingEnd != nil {
		t.Error("after typing: typingEnd should be nil (cycle broken)")
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
	bodyX, bodyY, bodyH := tv.x, tv.y, tv.h

	// Drag to the bottom edge to arm the scroll timer.
	e.HandleEvent(tcell.NewEventMouse(bodyX, bodyY, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(bodyX, bodyY+bodyH-1, tcell.ButtonPrimary, 0))

	scrollBefore := tv.scroll.Pos
	endYBefore := tv.buffer.selection.End.y

	// Simulate a tick: boundary guard must prevent scroll and selection extension.
	scroll, total, visible := win.body.GetScroll()
	if !(e.scrollDir > 0 && scroll+visible >= total) {
		win.body.Scroll(e.scrollDir * e.scrollAmount)
		win.body.(dragCursor).AdvanceDragCursor(e.scrollDir)
	}

	if tv.scroll.Pos != scrollBefore {
		t.Errorf("scroll.Pos changed from %d to %d; should stay at boundary", scrollBefore, tv.scroll.Pos)
	}
	if tv.buffer.selection.End.y != endYBefore {
		t.Errorf("selection.End.y changed from %d to %d; should stay at boundary", endYBefore, tv.buffer.selection.End.y)
	}

	_ = s
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
	e.HandleEvent(tcell.NewEventMouse(w2.x, w2.y, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(w2.x, w2.y, tcell.ButtonNone, 0))

	if w2.h <= before {
		t.Errorf("Button1 grow: w2.h = %d, want > %d", w2.h, before)
	}
	if col.maximized != nil {
		t.Error("Button1 grow must not set col.maximized")
	}

	// All windows must still be on-screen.
	colBottom := col.y + col.h
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
	e.HandleEvent(tcell.NewEventMouse(w3.x, w3.y, tcell.ButtonSecondary, 0))
	e.HandleEvent(tcell.NewEventMouse(w3.x, w3.y, tcell.ButtonNone, 0))

	if col.maximized != w3 {
		t.Fatalf("col.maximized = %v, want w3", col.maximized)
	}
	if col.windows[0] != w3 {
		t.Errorf("maximized window should be at index 0, got ID=%d", col.windows[0].ID)
	}
	if w3.h != col.h-1 {
		t.Errorf("maximized window h = %d, want %d (col.h-1)", w3.h, col.h-1)
	}
	if w3.y != col.y+1 {
		t.Errorf("maximized window y = %d, want %d (top of column)", w3.y, col.y+1)
	}

	// All other windows must be pushed completely off-screen.
	colBottom := col.y + col.h
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
	e.HandleEvent(tcell.NewEventMouse(w2.x, w2.y, tcell.ButtonSecondary, 0))
	e.HandleEvent(tcell.NewEventMouse(w2.x, w2.y, tcell.ButtonNone, 0))
	if col.maximized != w2 {
		t.Fatal("setup: expected w2 to be maximized")
	}

	// w2 is now at the top of the column (y = col.y+1). Static Button3 click
	// on its handle exits maximize and grows w2 while keeping all visible.
	e.HandleEvent(tcell.NewEventMouse(w2.x, w2.y, tcell.ButtonMiddle, 0))
	e.HandleEvent(tcell.NewEventMouse(w2.x, w2.y, tcell.ButtonNone, 0))

	if col.maximized != nil {
		t.Fatalf("col.maximized should be nil after Button3 grow, got %v", col.maximized)
	}

	colBottom := col.y + col.h
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
	e.HandleEvent(tcell.NewEventMouse(w2.x, w2.y, tcell.ButtonSecondary, 0))
	if e.dragWin != w2 {
		t.Fatal("Button2 on handle should start a window drag")
	}
	if e.dragWinButton != tcell.ButtonSecondary {
		t.Fatalf("dragWinButton = %v, want Button2", e.dragWinButton)
	}

	// Drag past w3's midpoint to trigger a swap, then release at a different Y.
	dragY := w3.y + w3.h/2 + 1
	e.HandleEvent(tcell.NewEventMouse(w2.x, dragY, tcell.ButtonSecondary, 0))
	e.HandleEvent(tcell.NewEventMouse(w2.x, dragY, tcell.ButtonNone, 0))

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

	e.HandleEvent(tcell.NewEventMouse(w2.x, w2.y, tcell.ButtonMiddle, 0))
	if e.dragWin != w2 {
		t.Fatal("Button3 on handle should start a window drag")
	}

	dragY := w3.y + w3.h/2 + 1
	e.HandleEvent(tcell.NewEventMouse(w2.x, dragY, tcell.ButtonMiddle, 0))
	e.HandleEvent(tcell.NewEventMouse(w2.x, dragY, tcell.ButtonNone, 0))

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
	e.HandleEvent(tcell.NewEventMouse(w1.x, w1.y, tcell.ButtonSecondary, 0))
	e.HandleEvent(tcell.NewEventMouse(w1.x, w1.y, tcell.ButtonNone, 0))
	if col.maximized != w1 {
		t.Fatal("setup: w1 should be maximized")
	}

	e.RemoveWindow(w1)

	if col.maximized != nil {
		t.Errorf("col.maximized should be nil after removing the maximized window, got %v", col.maximized)
	}
	// w2 must now be laid out on-screen.
	colBottom := col.y + col.h
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
	e.HandleEvent(tcell.NewEventMouse(w1.x, w1.y, tcell.ButtonSecondary, 0))
	e.HandleEvent(tcell.NewEventMouse(w1.x, w1.y, tcell.ButtonNone, 0))
	if col0.maximized != w1 {
		t.Fatal("setup: w1 should be maximized in col0")
	}

	// Drag w1 into col1 using Button1 (regular drag, past col0's right edge).
	e.HandleEvent(tcell.NewEventMouse(w1.x, w1.y, tcell.ButtonPrimary, 0))
	if e.dragWin != w1 {
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
	colBottom := col0.y + col0.h
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
			if e.dragCol != col1 {
				t.Fatalf("%s on column gutter: dragCol = %v, want col1", name, e.dragCol)
			}

			// Drag left — dragCol must persist while button held.
			e.HandleEvent(tcell.NewEventMouse(30, col1.y, btn, 0))
			if e.dragCol == nil {
				t.Fatalf("%s: dragCol cleared during drag", name)
			}

			// Release — dragCol must clear.
			e.HandleEvent(tcell.NewEventMouse(30, col1.y, tcell.ButtonNone, 0))
			if e.dragCol != nil {
				t.Fatalf("%s: dragCol should be nil after release", name)
			}
		})
	}
}
