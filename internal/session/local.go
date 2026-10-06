//go:build linux || darwin || dragonfly || solaris || openbsd || netbsd || freebsd

package session

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

type localSession struct {
	pty    *os.File
	pid    int
	exited chan struct{} // closed once the child has been reaped
}

// NewLocal starts cmdStr (or $SHELL if empty) in dir under a new PTY.
func NewLocal(cmdStr, dir string) (Session, error) {
	var cmd *exec.Cmd
	if cmdStr == "" {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		cmd = exec.Command(shell)
	} else {
		cmd = exec.Command("/bin/sh", "-c", cmdStr)
	}
	cmd.Dir = dir

	ptyFile, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	s := &localSession{pty: ptyFile, pid: cmd.Process.Pid, exited: make(chan struct{})}
	// Reap the child whenever it exits, on its own or after Close hangs it up;
	// otherwise every finished shell lingers as a zombie.
	go func() {
		cmd.Wait()
		close(s.exited)
	}()
	return s, nil
}

func (s *localSession) Read(p []byte) (int, error)  { return s.pty.Read(p) }
func (s *localSession) Write(p []byte) (int, error) { return s.pty.Write(p) }

// Close hangs up the child's process group before closing the pty. Closing the
// pty alone is not enough: while the parse goroutine is blocked in Read, the
// fd stays open until that Read returns, and an idle shell never writes.
//
// This is not a full hangup: a child that ignores SIGHUP is not shown EOF on
// the tty, so it and the pty live on until it exits by itself. A real hangup
// needs an interruptible Read (a non-blocking, poller-managed fd), which Go
// cannot provide for ttys on macOS and the BSDs.
func (s *localSession) Close() error {
	select {
	case <-s.exited:
		// Already reaped; its pid may have been reused, so don't signal it.
	default:
		// pty.Start runs the child with Setsid, so its pid is also its pgid.
		syscall.Kill(-s.pid, syscall.SIGHUP)
	}
	return s.pty.Close()
}

func (s *localSession) Resize(rows, cols int) error {
	return pty.Setsize(s.pty, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
}
