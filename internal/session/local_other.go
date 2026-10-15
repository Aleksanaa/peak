//go:build !linux && !darwin && !dragonfly && !solaris && !openbsd && !netbsd && !freebsd && !windows

package session

import (
	"errors"
	"os/exec"
)

func NewLocal(cmdStr, dir string) (Session, error) {
	return nil, errors.New("local sessions not supported on this platform")
}

// Command returns a command that runs cmdStr with the Runner.
func Command(cmdStr string) *exec.Cmd {
	return exec.Command(Runner(), "-c", cmdStr)
}
