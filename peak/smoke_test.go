package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// GetWordCoordinate finds the occurrence of a word starting from (startX, startY) and returns its (x, y) coordinate.
func GetWordCoordinate(s tcell.SimulationScreen, word string, startX, startY int) (int, int, bool) {
	width, height := s.Size()
	for y := startY; y < height; y++ {
		var line strings.Builder
		xMin := 0
		if y == startY {
			xMin = startX
		}
		for x := 0; x < width; x++ {
			mainc, _, _, _ := s.GetContent(x, y)
			line.WriteRune(mainc)
		}
		lineStr := line.String()
		if idx := strings.Index(lineStr[xMin:], word); idx != -1 {
			return xMin + idx, y, true
		}
	}
	return -1, -1, false
}

// GetColorCoordinate finds the first cell with the specified foreground or background color.
func GetColorCoordinate(s tcell.SimulationScreen, color tcell.Color) (int, int, bool) {
	width, height := s.Size()
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			_, _, style, _ := s.GetContent(x, y)
			fg, bg, _ := style.Decompose()
			if fg == color || bg == color {
				return x, y, true
			}
		}
	}
	return -1, -1, false
}

// VerifyNewColExists checks if the word "NewCol" is present anywhere on the screen.
func VerifyNewColExists(s tcell.SimulationScreen) bool {
	_, _, found := GetWordCoordinate(s, "NewCol", 0, 0)
	return found
}

func setupTest(t *testing.T, w, h int) (*Editor, tcell.SimulationScreen) {
	s := tcell.NewSimulationScreen("")
	if err := s.Init(); err != nil {
		t.Fatalf("failed to init simulation screen: %v", err)
	}
	s.SetSize(w, h)
	e := &Editor{
		screen:   s,
		theme:    defaultTheme,
		CmdChan:  make(chan func()),
		redrawCh: make(chan struct{}, 1),
		execCh:   make(chan execReq, 8),
	}
	appEditor = e
	e.ninep = NewNineP(e)
	e.width, e.height = s.Size()

	go func() {
		for fn := range e.CmdChan {
			fn()
		}
	}()

	go func() {
		for req := range e.execCh {
			switch req.kind {
			case 'x':
				if req.win != nil && req.win.onExec != nil {
					req.win.onExec(req.col, req.win, req.text)
				}
			case 'l':
				e.Plumb(req.win, req.text)
			case 'e':
				e.appendToErrorWindow(req.col, req.win, req.text)
			}
			// Wake up any waitFor loops so they can recheck their condition.
			e.screen.PostEvent(tcell.NewEventInterrupt(nil))
		}
	}()

	tagStyle := tcell.StyleDefault.Background(e.theme.GlobalTagBG).Foreground(e.theme.GlobalTagFG)
	e.tag = NewTextView(" NewCol Help Exit ", 0, 0, e.width, 1, tagStyle, true, false)
	e.tag.theme = &e.theme
	return e, s
}

func waitFor(t *testing.T, e *Editor, s tcell.SimulationScreen, condition func() bool) {
	timeout := time.After(3 * time.Second)
	done := make(chan bool)
	go func() {
		for {
			if condition() {
				done <- true
				return
			}
			ev := s.PollEvent()
			if ev == nil {
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
		// Post an interrupt event to unblock PollEvent
		s.PostEvent(tcell.NewEventInterrupt(nil))
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
	cx, cy, cfound := GetColorCoordinate(s, e.theme.GlobalTagBG)
	if !cfound {
		t.Errorf("Expected to find GlobalTagBG color (%v), but it was not found", e.theme.GlobalTagBG)
	} else {
		t.Logf("GlobalTagBG color found at coordinate: (%d, %d)", cx, cy)
	}
}

func TestTextViewClickPlacesCursorOnClickedCharacter(t *testing.T) {
	tv := NewTextView("abc", 0, 0, 10, 1, tcell.StyleDefault, false, false)

	tv.HandleEvent(tcell.NewEventMouse(0, 0, tcell.Button1, 0))

	if got := tv.buffer.cursor; got != (Cursor{0, 0}) {
		t.Fatalf("cursor after clicking first character = %+v, want %+v", got, Cursor{0, 0})
	}
}

func TestNewColClick(t *testing.T) {
	e, s := setupTest(t, 100, 24)

	// Initialize with 2 columns as in main.go
	leftWidth := e.width / 2
	colLeft := NewColumn(0, 1, leftWidth, e.height-1, e, e.Execute)
	e.columns = append(e.columns, colLeft)

	colRight := NewColumn(leftWidth, 1, e.width-leftWidth, e.height-1, e, e.Execute)
	e.columns = append(e.columns, colRight)

	e.Resize()
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
	ev := tcell.NewEventMouse(x, y, tcell.Button3, 0)
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

	col := NewColumn(0, 1, e.width, e.height-1, e, e.Execute)
	e.columns = append(e.columns, col)

	e.Resize()
	e.Draw()
	s.Show()

	// Find "Help" coordinate
	x, y, found := GetWordCoordinate(s, "Help", 0, 0)
	if !found {
		t.Fatal("Could not find 'Help' on screen")
	}

	// Simulate Middle-click on "Help"
	ev := tcell.NewEventMouse(x, y, tcell.Button3, 0)
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
	colWidth := e.width / 3
	for i := 0; i < 3; i++ {
		col := NewColumn(i*colWidth, 1, colWidth, e.height-1, e, e.Execute)
		e.columns = append(e.columns, col)
	}

	e.Resize()
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
	ev := tcell.NewEventMouse(x, y, tcell.Button3, 0)
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

func TestZeroxClick(t *testing.T) {
	e, s := setupTest(t, 100, 24)

	col := NewColumn(0, 1, e.width, e.height-1, e, e.Execute)
	e.columns = append(e.columns, col)

	win := col.AddWindow(" test.txt Zerox ", "Hello Zerox")
	e.ActivateWindow(win)

	e.Resize()
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
	ev := tcell.NewEventMouse(x, y, tcell.Button3, 0)
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

	col := NewColumn(0, 1, e.width, e.height-1, e, e.Execute)
	e.columns = append(e.columns, col)

	// Create window with /peak/doc as the name
	win := col.AddWindow(" /peak/doc Get Put ", "")
	e.ActivateWindow(win)

	e.Resize()
	e.Draw()
	s.Show()

	// Find "Get" in the window tag (y=2)
	x, y, found := GetWordCoordinate(s, "Get", 0, 2)
	if !found {
		t.Fatal("Could not find 'Get' in the window tag")
	}

	// Simulate Middle-click on "Get"
	ev := tcell.NewEventMouse(x, y, tcell.Button3, 0)
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
	ev2 := tcell.NewEventMouse(rx, ry, tcell.Button2, 0)
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
	colWidth := e.width / 3
	for i := 0; i < 3; i++ {
		col := NewColumn(i*colWidth, 1, colWidth, e.height-1, e, e.Execute)
		e.columns = append(e.columns, col)
	}

	// Col 0: 1 window
	w1 := e.columns[0].AddWindow(" w1 ", "content 1")
	// Col 1: 2 windows
	w2 := e.columns[1].AddWindow(" w2 ", "content 2")
	w3 := e.columns[1].AddWindow(" w3 ", "content 3")
	// Col 2: 0 windows

	e.Resize()
	e.Draw()
	s.Show()

	// Initial check
	if len(e.columns[0].windows) != 1 || len(e.columns[1].windows) != 2 || len(e.columns[2].windows) != 0 {
		t.Fatalf("Initial state wrong: %d, %d, %d", len(e.columns[0].windows), len(e.columns[1].windows), len(e.columns[2].windows))
	}

	// 1. Drag W1 (Col 0) to Col 2
	// Handle for W1 is at (e.columns[0].x, w1.y) = (0, 2)
	e.HandleEvent(tcell.NewEventMouse(0, 2, tcell.Button1, 0)) // Start drag
	if e.dragWin != w1 {
		t.Fatal("Failed to start dragging w1")
	}
	// Move to Col 2 (x = 2*colWidth + 10, y = 10)
	e.HandleEvent(tcell.NewEventMouse(2*colWidth+10, 10, tcell.Button1, 0))
	// Release
	e.HandleEvent(tcell.NewEventMouse(2*colWidth+10, 10, tcell.ButtonNone, 0))
	if e.dragWin != nil {
		t.Fatal("Drag should have ended")
	}

	// 2. Drag W2 (Col 1) to Col 0
	// Handle for W2 is at (e.columns[1].x, w2.y) = (40, 2)
	e.HandleEvent(tcell.NewEventMouse(colWidth, 2, tcell.Button1, 0))
	if e.dragWin != w2 {
		t.Fatal("Failed to start dragging w2")
	}
	e.HandleEvent(tcell.NewEventMouse(colWidth/2, 10, tcell.Button1, 0))
	e.HandleEvent(tcell.NewEventMouse(colWidth/2, 10, tcell.ButtonNone, 0))

	// 3. Drag W3 (Col 1) to Col 2
	// W3 is now at the top of Col 1 since W2 moved.
	// Handle for W3 is at (colWidth, 2)
	e.HandleEvent(tcell.NewEventMouse(colWidth, 2, tcell.Button1, 0))
	if e.dragWin != w3 {
		t.Fatal("Failed to start dragging w3")
	}
	e.HandleEvent(tcell.NewEventMouse(2*colWidth+10, 20, tcell.Button1, 0))
	e.HandleEvent(tcell.NewEventMouse(2*colWidth+10, 20, tcell.ButtonNone, 0))

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

	col := NewColumn(0, 1, e.width, e.height-1, e, e.Execute)
	e.columns = append(e.columns, col)

	w1 := col.AddWindow(" w1 ", "c1")
	w2 := col.AddWindow(" w2 ", "c2")
	w3 := col.AddWindow(" w3 ", "c3")

	e.Resize()
	e.Draw()
	s.Show()

	if len(col.windows) != 3 || col.windows[0] != w1 || col.windows[1] != w2 || col.windows[2] != w3 {
		t.Fatalf("Initial order wrong")
	}

	// 1. Drag W1 (idx 0) below W2.
	// W1 handle at (0, w1.y). We drop it in W2's body area.
	_, w1y, _, _ := w1.GetRect()
	e.HandleEvent(tcell.NewEventMouse(0, w1y, tcell.Button1, 0))
	if e.dragWin != w1 {
		t.Fatal("drag w1 failed")
	}
	_, w2y, _, _ := w2.GetRect()
	// W2.y + 2 should be in W2's body
	e.HandleEvent(tcell.NewEventMouse(0, w2y+2, tcell.Button1, 0))
	e.HandleEvent(tcell.NewEventMouse(0, w2y+2, tcell.ButtonNone, 0))

	if col.windows[0] != w2 || col.windows[1] != w1 || col.windows[2] != w3 {
		t.Errorf("Order after first drag wrong: %d, %d, %d", col.windows[0].ID, col.windows[1].ID, col.windows[2].ID)
	}

	// 2. Drag W3 (idx 2) above W1 (idx 1).
	// Current: [w2, w1, w3]
	// We drop it in W1's tag area (w1.y)
	_, w3y, _, _ := w3.GetRect()
	e.HandleEvent(tcell.NewEventMouse(0, w3y, tcell.Button1, 0))
	if e.dragWin != w3 {
		t.Fatal("drag w3 failed")
	}
	_, w1y, _, _ = w1.GetRect()
	e.HandleEvent(tcell.NewEventMouse(0, w1y, tcell.Button1, 0))
	e.HandleEvent(tcell.NewEventMouse(0, w1y, tcell.ButtonNone, 0))

	// Verify final: w2, w3, w1
	if col.windows[0] != w2 || col.windows[1] != w3 || col.windows[2] != w1 {
		t.Errorf("Final order wrong: expected [w2, w3, w1], got [%d, %d, %d]", col.windows[0].ID, col.windows[1].ID, col.windows[2].ID)
	}
}

func TestSimpleEdit(t *testing.T) {
	e, s := setupTest(t, 100, 24)

	col := NewColumn(0, 1, e.width, e.height-1, e, e.Execute)
	e.columns = append(e.columns, col)

	e.Resize()
	e.Draw()
	s.Show()

	path := "/peak/mirage/1.txt"

	// Pre-create the directory and file in memory FS so Open succeeds
	getVFS().MkdirAll("/peak/mirage", 0755)
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
		e.HandleEvent(tcell.NewEventMouse(px, py, tcell.Button3, 0))

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
		e.HandleEvent(tcell.NewEventMouse(dx, dy, tcell.Button3, 0))

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
		e.HandleEvent(tcell.NewEventMouse(dx, dy, tcell.Button3, 0))
		t.Logf("Iteration %d finished", i)
	}
}

func TestExternalCommand(t *testing.T) {
	e, s := setupTest(t, 120, 30)

	col := NewColumn(0, 1, e.width, e.height-1, e, e.Execute)
	e.columns = append(e.columns, col)

	e.Resize()
	e.Draw()
	s.Show()

	// 1. Create window and add "uname -a" to tag
	win := col.AddWindow(" /tmp/test.txt Get Put uname -a Del ", "initial body")
	e.ActivateWindow(win)
	e.Resize()
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
	e.HandleEvent(tcell.NewEventMouse(tx, ty, tcell.Button3, 0))

	// 4. Wait for +Errors window and check content
	var errWin *Window
	waitFor(t, e, s, func() bool {
		for _, c := range e.columns {
			for _, w := range c.windows {
				if strings.HasSuffix(w.GetFilename(), "+Errors") {
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
	expected, _ := runLocalCommand("uname -a", "/tmp/", "", 0)
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
	_, errY, _, _ := errWin.body.GetPos()
	bx, by, bfound := GetWordCoordinate(s, "uname -a", 0, errY)
	if !bfound {
		t.Fatal("Could not find 'uname -a' in +Errors body")
	}
	e.HandleEvent(tcell.NewEventMouse(bx, by, tcell.Button3, 0))

	// 9. Observe result isn't changed (replaces buffer with same output)
	waitFor(t, e, s, func() bool {
		newOutput := strings.TrimSpace(errWin.body.GetBuffer().GetText())
		return newOutput == expected
	})
}

func TestSimplePlumb(t *testing.T) {
	e, s := setupTest(t, 100, 30)

	col := NewColumn(0, 1, e.width, e.height-1, e, e.Execute)
	e.columns = append(e.columns, col)

	e.Resize()
	e.Draw()
	s.Show()

	path := "/peak/mirage/2.txt"
	testString := "DirListing Test Content"

	getVFS().MkdirAll("/peak/mirage", 0755)
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
	e.HandleEvent(tcell.NewEventMouse(px, py, tcell.Button3, 0))

	// Wait for Put
	waitFor(t, e, s, func() bool {
		return win.savedVersion == win.body.GetBuffer().version
	})

	// Close window
	dx, dy, _ := GetWordCoordinate(s, "Del", 0, win.tag.y)
	e.HandleEvent(tcell.NewEventMouse(dx, dy, tcell.Button3, 0))

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
	_, bodyY, _, _ := dirWin.body.GetPos()
	fx, fy, ffound := GetWordCoordinate(s, "2.txt", 0, bodyY)
	if !ffound {
		t.Fatal("Could not find '2.txt' in directory listing")
	}

	// Simulate Button2 (Right-click) on "2.txt"
	e.HandleEvent(tcell.NewEventMouse(fx, fy, tcell.Button2, 0))

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
	tv := NewTextView(body, 0, 0, 40, 10, tcell.StyleDefault, false, true)
	tv.UpdateLayout()

	bx, by := tv.visualToBuffer(0, 9)
	tv.buffer.cursor = Cursor{bx, by}
	tv.HandleEvent(tcell.NewEventKey(tcell.KeyDown, 0, 0))

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
	tv := NewTextView(body, 0, 0, 40, 10, tcell.StyleDefault, false, true)
	tv.UpdateLayout()

	tv.scroll.Pos = 50
	tv.scroll.AutoScroll = false
	tv.HandleEvent(tcell.NewEventKey(tcell.KeyRune, 'x', 0))

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
	tv := NewTextView(body, 0, 0, 40, 10, tcell.StyleDefault, false, true)
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
	tv := NewTextView(body, 0, 0, 40, 10, tcell.StyleDefault, false, true)
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
	col := NewColumn(0, 1, 40, 19, e, e.Execute)
	e.columns = append(e.columns, col)

	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("L%02d", i))
	}
	body := strings.Join(lines, "\n")
	win := col.AddWindow(" /test ", body)
	e.Resize()
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
