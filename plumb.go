package main

import (
	"strconv"
	"strings"
	"unicode"
)

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
	word = strings.TrimSpace(word)
	if word == "" {
		return false
	}

	pathPart, lineNum := word, -1
	if idx := strings.LastIndex(word, ":"); idx != -1 {
		lineStr := word[idx+1:]
		if n, err := strconv.Atoi(lineStr); err == nil && n > 0 {
			pathPart, lineNum = word[:idx], n-1
		}
	}

	// Always try to open as a path first, but asynchronously.
	// If it doesn't exist, fallback to search.
	e.OpenLine(win, pathPart, lineNum, func() {
		e.Execute(nil, win, "Look "+word)
	})

	return false
}
