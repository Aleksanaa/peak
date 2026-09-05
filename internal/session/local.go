//go:build linux || darwin || dragonfly || solaris || openbsd || netbsd || freebsd

package session

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type localSession struct {
	pty *os.File
}

// NewLocal starts cmdStr (or $SHELL if empty) in dir under a new PTY.
// A nil env inherits peak's environment unchanged.
func NewLocal(cmdStr, dir string, env []string) (Session, error) {
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
	cmd.Env = env

	ptyFile, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	return &localSession{pty: ptyFile}, nil
}

func (s *localSession) Read(p []byte) (int, error)  { return s.pty.Read(p) }
func (s *localSession) Write(p []byte) (int, error) { return s.pty.Write(p) }
func (s *localSession) Close() error                { return s.pty.Close() }
func (s *localSession) Resize(rows, cols int) error {
	return pty.Setsize(s.pty, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
}
