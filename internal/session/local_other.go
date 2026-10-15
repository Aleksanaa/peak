//go:build !linux && !darwin && !dragonfly && !solaris && !openbsd && !netbsd && !freebsd && !windows

package session

import (
	"errors"
	"os/exec"
)

func NewLocal(cmdStr, dir string) (Session, error) {
	return nil, errors.New("local sessions not supported on this platform")
}

// Command returns a command that runs cmdStr with sh -c.
func Command(cmdStr string) *exec.Cmd {
	return exec.Command("sh", "-c", cmdStr)
}
