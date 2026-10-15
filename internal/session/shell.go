package session

import (
	"os"
	"os/exec"
	"runtime"
)

// Shell returns the program a terminal window runs when given no command:
// $PEAKSHELL, else $SHELL, else the system's shell.
func Shell() string {
	return firstFound(os.Getenv("PEAKSHELL"), os.Getenv("SHELL"), systemShell())
}

// Runner returns the program that runs commands, given the command with -c
// (/C for cmd): $PEAKRUNNER, else sh, else the system's shell.
func Runner() string {
	return firstFound(os.Getenv("PEAKRUNNER"), "sh", systemShell())
}

// systemShell is %ComSpec% on Windows, /bin/sh elsewhere.
func systemShell() string {
	if runtime.GOOS == "windows" {
		return os.Getenv("ComSpec")
	}
	return "/bin/sh"
}

// firstFound returns the first of progs that is a program, or else the last.
func firstFound(progs ...string) string {
	for _, p := range progs {
		if p == "" {
			continue
		}
		if path, err := exec.LookPath(p); err == nil {
			return path
		}
	}
	return progs[len(progs)-1]
}
