//go:build linux || darwin || dragonfly || solaris || openbsd || netbsd || freebsd

package main

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

// Deleting a terminal window must end its child process, and the child must be
// reaped rather than left as a zombie.
func TestRemoveTermWindowEndsProcess(t *testing.T) {
	e, _, _ := setupMouseChordWindow(t)
	col := e.columns[0]

	pidFile := filepath.Join(t.TempDir(), "pid")
	termWin := addTerm(t, col, " /tmp/-sh Zerox Del ", "echo $$ > "+pidFile+"; exec sleep 30")

	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for pid == 0 {
		if time.Now().After(deadline) {
			t.Fatal("terminal child never wrote its pid")
		}
		if data, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		time.Sleep(10 * time.Millisecond)
	}

	e.RemoveWindow(termWin)
	// A second removal (the exit/Del race) must be a no-op.
	e.RemoveWindow(termWin)
	if len(col.windows) != 1 {
		t.Fatalf("column has %d windows after removal, want 1", len(col.windows))
	}

	// kill(pid, 0) keeps succeeding for a zombie, so ESRCH proves the child
	// both exited and was reaped.
	deadline = time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) != syscall.ESRCH {
		if time.Now().After(deadline) {
			t.Fatalf("terminal child %d still exists after its window was removed", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A mouse selection in a terminal is the one every path sees: the chord path,
// the Snarf/Cut commands (through the buffer) and /peak/<id>/rdsel. Selecting
// past the end of the text must stop at the end of that line.
func TestTermSelectionSeenByBufferPaths(t *testing.T) {
	e, _, _ := setupMouseChordWindow(t)
	col := e.columns[0]
	termWin := addTerm(t, col, " /tmp/-sh Zerox Del ", "printf 'hello world\\nsecond'; exec sleep 30")
	// Reading rdsel below goes through e.Call, after which the harness draws on
	// its own goroutine; remove the window there too, not alongside that draw.
	defer e.Call(func() { e.RemoveWindow(termWin) })

	col.Resize(col.rect)
	tv := termWin.body.(*TermView)

	deadline := time.Now().Add(5 * time.Second)
	for {
		tv.Layout()
		if strings.Contains(tv.GetBuffer().GetText(), "second") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal output never arrived: %q", tv.GetBuffer().GetText())
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Drag across the first line and well past its end.
	tv.HandleEvent(tcell.NewEventMouse(0, 0, tcell.ButtonPrimary, 0))
	tv.HandleEvent(tcell.NewEventMouse(20, 0, tcell.ButtonPrimary, 0))
	tv.HandleEvent(tcell.NewEventMouse(20, 0, tcell.ButtonNone, 0))

	const want = "hello world"
	if got := tv.GetBuffer().GetSelectedText(); got != want {
		t.Errorf("buffer selection (Snarf/Cut and chords) = %q, want %q", got, want)
	}
	f, err := newWindowFs(termWin).OpenFile("rdsel", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("open rdsel: %v", err)
	}
	defer f.Close()
	if data, _ := io.ReadAll(f); string(data) != want {
		t.Errorf("rdsel = %q, want %q", data, want)
	}
}

// runTerm runs cmd in a terminal w wide and h high, and waits for its text to
// contain want.
func runTerm(t *testing.T, cmd string, w, h int, want string) *TermView {
	t.Helper()
	e, col := newTestEditorWithColumn(t)
	win := addTerm(t, col, " /tmp/-sh Del ", cmd)
	t.Cleanup(func() { e.Call(func() { e.RemoveWindow(win) }) })
	tv := win.body.(*TermView)
	tv.Resize(w, h)
	waitText(t, tv, want)
	return tv
}

func waitText(t *testing.T, tv *TermView, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		tv.Layout()
		if strings.Contains(tv.GetBuffer().GetText(), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal never showed %q: %q", want, tv.GetBuffer().GetText())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Output longer than the screen scrolls into history, in order, and the
// view follows the screen. The program's screen is the view's size, so the
// cursor never leaves it.
func TestTermHistory(t *testing.T) {
	tv := runTerm(t, "seq 1 50; printf end; exec sleep 30", 20, 10, "end")
	lines := strings.Split(tv.GetBuffer().GetText(), "\n")
	for i := 1; i <= 50; i++ {
		if lines[i-1] != strconv.Itoa(i) {
			t.Fatalf("line %d = %q, want %d", i, lines[i-1], i)
		}
	}
	tv.state.Lock()
	_, cy := tv.state.Cursor()
	tv.state.Unlock()
	if cy >= 10 {
		t.Errorf("cursor row %d on a screen of 10 rows", cy)
	}
	if top := tv.lines()[tv.top()].BufferLine; top != tv.screenTop.y {
		t.Errorf("view starts at line %d, want the screen's first, %d", top, tv.screenTop.y)
	}
}

// Clearing the screen leaves history alone.
func TestTermClearKeepsHistory(t *testing.T) {
	tv := runTerm(t, `seq 1 30; printf '\033[H\033[2Jcleared'; exec sleep 30`, 20, 10, "cleared")
	if text := tv.GetBuffer().GetText(); !strings.HasPrefix(text, "1\n2\n") {
		t.Errorf("history lost on clear: %q", text)
	}
}

// Colors stay with the text into history, and a wide character is one rune.
func TestTermColorsAndWideCharacters(t *testing.T) {
	tv := runTerm(t, `printf '\033[31mred\033[0m 你好\n'; seq 1 20; exec sleep 30`, 20, 5, "20")
	if line := string(tv.buffer.lines[0]); line != "red 你好" {
		t.Fatalf("first line = %q, want %q", line, "red 你好")
	}
	if len(tv.hist) == 0 {
		t.Fatal("the colored line should have scrolled into history")
	}
	if fg := tv.styles[0][0].GetForeground(); fg != color.PaletteColor(1) {
		t.Errorf("history keeps red as %v", fg)
	}
}

// Shrinking the screen pushes its top rows into history instead of losing
// them.
func TestTermShrinkKeepsRows(t *testing.T) {
	tv := runTerm(t, "seq 1 10; printf end; exec sleep 30", 20, 12, "end")
	tv.Resize(20, 4)
	tv.changed.Store(true)
	tv.Layout()
	if text := tv.GetBuffer().GetText(); !strings.HasPrefix(text, "1\n2\n3\n") {
		t.Errorf("rows lost on shrinking: %q", text)
	}
}

// Look finds text in history and the view stays on it while output goes on.
func TestTermLookStaysOnMatch(t *testing.T) {
	tv := runTerm(t, "echo needle; seq 1 50; printf end; exec sleep 30", 20, 10, "end")
	if line := tv.Search("needle"); line != 0 {
		t.Fatalf("Search found line %d, want 0", line)
	}
	tv.ShowLineAt(0)
	tv.changed.Store(true)
	tv.Layout()
	if top := tv.lines()[tv.top()].BufferLine; top > 0 {
		t.Errorf("view went back to line %d; want it to stay on the match", top)
	}
}

// A line longer than the screen is one line of text, on the screen and in
// history, spaces at its wraps included, and it wraps again to the view.
func TestTermWrappedLineIsOneLine(t *testing.T) {
	long := strings.Repeat("a", 19) + " " + strings.Repeat("b", 25) // the space is a row's last cell
	tv := runTerm(t, "printf '"+long+"\\n'; printf end; exec sleep 30", 20, 10, "end")
	if line := string(tv.buffer.lines[0]); line != long {
		t.Fatalf("on the screen: %q, want %q", line, long)
	}
	tv = runTerm(t, "printf '"+long+"\\n'; seq 1 20; exec sleep 30", 20, 5, "20")
	if line := string(tv.buffer.lines[0]); line != long {
		t.Errorf("in history: %q, want %q", line, long)
	}
	tv.Resize(40, 5)
	if n := len(slices.DeleteFunc(slices.Clone(tv.lines()), func(vl VisualLine) bool { return vl.BufferLine != 0 })); n != 2 {
		t.Errorf("at width 40 the line takes %d rows, want 2", n)
	}
}
