//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// ownGroup puts c in a process group of its own, as Plan 9 gives a command
// its own note group: killing it kills the whole command, pipelines and
// all, and signals meant for peak's group do not reach it.
func ownGroup(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
}
