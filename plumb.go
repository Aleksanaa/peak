package main

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var plumbRx = regexp.MustCompile(`^(.*?)(?:([^:]):(\d+)(?::(\d+))?)?$`)

// GetWordAt returns the word under the given x, y buffer coordinates.
func (b *Buffer) GetWordAt(x, y int) string {
	if y < 0 || y >= len(b.lines) {
		return ""
	}
	line := b.lines[y]
	if x < 0 || x >= len(line) {
		return ""
	}

	start, end := x, x
	for start > 0 && isWordChar(line[start-1]) {
		start--
	}
	for end < len(line) && isWordChar(line[end]) {
		end++
	}
	return string(line[start:end])
}

func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '/' || r == '.' || r == '-' || r == '~' || r == ':'
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
	if word = strings.TrimSpace(word); word == "" {
		return false
	}
	m := plumbRx.FindStringSubmatch(word)
	if m == nil {
		return false
	}
	path := m[1] + m[2]
	line, _ := strconv.Atoi(m[3])
	col, _ := strconv.Atoi(m[4])
	e.OpenLine(win, path, line-1, col, func() {
		e.Execute(nil, win, "Look "+word)
	})
	return false
}
