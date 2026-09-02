package main

import (
	"os/exec"

	"github.com/aleksana/peak/internal/quote"

	
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

var (
	plumbRx  = regexp.MustCompile(`^([^\x00]*?)(?:([^:]):(\d+)(?::(\d+))?)?$`)
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

// Plumb attempts to handle a string (path or search).
func (e *Editor) Plumb(win *Window, word string) bool {
	word = strings.TrimSpace(word)
	if word == "" {
		return false
	}
	// Unwrap a single backtick-quoted token (e.g. `foo bar` → foo bar).
	if parts := quote.Fields(word); len(parts) == 1 {
		word = parts[0]
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
	col := -1
	if m[4] != "" {
		col, _ = strconv.Atoi(m[4])
	}
	base := ""
	if win != nil {
		base = win.GetDir()
	} else if e.active != nil {
		base = e.active.GetDir()
	}
	e.OpenLine(win, path, line-1, col, func() {
		OpenExternal(normalizePath(path, base))
	}, func() {
		e.Execute(nil, win, "Look "+word)
	})
	return false
}
