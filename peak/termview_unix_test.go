//go:build linux || darwin || dragonfly || solaris || openbsd || netbsd || freebsd

package main

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
)

// Deleting a terminal window must end its child process, and the child must be
// reaped rather than left as a zombie.
func TestRemoveTermWindowEndsProcess(t *testing.T) {
	e, _, _ := setupMouseChordWindow(t)
	col := e.columns[0]

	pidFile := filepath.Join(t.TempDir(), "pid")
	termWin, err := col.AddTermWindow(" /tmp/-sh Zerox Del ", "echo $$ > "+pidFile+"; exec sleep 30", "/tmp")
	if err != nil {
		t.Skipf("cannot create term window: %v", err)
	}

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
	termWin, err := col.AddTermWindow(" /tmp/-sh Zerox Del ", "printf 'hello world\\nsecond'; exec sleep 30", "/tmp")
	if err != nil {
		t.Skipf("cannot create term window: %v", err)
	}
	// Reading rdsel below goes through e.Call, after which the harness draws on
	// its own goroutine; remove the window there too, not alongside that draw.
	defer e.Call(func() { e.RemoveWindow(termWin) })

	col.Resize(col.rect)
	tv := termWin.body.(*TermView)

	deadline := time.Now().Add(5 * time.Second)
	for {
		tv.Layout()
		if strings.Contains(tv.GetScrollback(), "second") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal output never arrived: %q", tv.GetScrollback())
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
