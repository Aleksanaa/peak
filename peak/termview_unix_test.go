//go:build linux || darwin || dragonfly || solaris || openbsd || netbsd || freebsd

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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
