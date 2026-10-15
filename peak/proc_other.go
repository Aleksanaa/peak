//go:build !unix

package main

import "os/exec"

// ownGroup leaves c as it is: killing it kills only the shell.
func ownGroup(c *exec.Cmd) {}
