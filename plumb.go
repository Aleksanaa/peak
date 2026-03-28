package main

import (
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

var (
	plumbRx  = regexp.MustCompile(`^([^\x00]*?)(?:([^:]):(\d+):(\d+))?$`)
	httpRx   = regexp.MustCompile(`https?://[a-zA-Z0-9][-a-zA-Z0-9.]*(?::\d+)?(?:/[^\s"'>]*)?`)
	mailtoRx = regexp.MustCompile(`mailto:[^\s"'>]+@[^\s"'>]+(\?[^\s"'>]*)?`)
	magnetRx = regexp.MustCompile(`magnet:\?xt=urn:[a-z0-9]+:[a-z0-9]{32,128}[^"'\s<>]*`)
)

// OpenExternal opens a path using the system's default application
func OpenExternal(path string) error {
	cmdName := "xdg-open"
	if runtime.GOOS == "darwin" {
		cmdName = "open"
	}
	return exec.Command(cmdName, path).Start()
}

// GetWordAt returns the word under the given x, y buffer coordinates.
func (b *Buffer) GetWordAt(x, y int) string {
	if y < 0 || y >= len(b.lines) {
		return ""
	}
	line := b.lines[y]
	start, end := GetWordBoundaries(x, len(line), func(i int) rune { return line[i] })
	return string(line[start:end])
}

func (e *Editor) resolvePathWithContext(win *Window, path string) string {
	dir := ""
	if win != nil {
		dir = win.GetDir()
	} else if e.active != nil {
		dir = e.active.GetDir()
	} else {
		dir = getwd()
	}
	return resolveWithContext(path, dir)
}

// Plumb attempts to handle a string (path or search).
func (e *Editor) Plumb(win *Window, word string) bool {
	word = strings.TrimSpace(word)
	if word == "" {
		return false
	}

	// Try protocol handlers first
	if httpRx.MatchString(word) || mailtoRx.MatchString(word) || magnetRx.MatchString(word) {
		if err := OpenExternal(word); err == nil {
			return false
		}
	}

	m := plumbRx.FindStringSubmatch(word)
	if m == nil {
		return false
	}
	path := m[1] + m[2]
	line, _ := strconv.Atoi(m[3])
	col, _ := strconv.Atoi(m[4])
	e.OpenLine(win, path, line-1, col, func() {
		full := e.resolvePathWithContext(win, path)
		if isPeakPath(full) {
			e.showError(nil, win, "", full+": cannot open binary file from virtual filesystem")
			return
		}
		OpenExternal(full)
	}, func() {
		e.Execute(nil, win, "Look "+word)
	})
	return false
}
