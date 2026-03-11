package main

import (
	"os"
	"path/filepath"
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

// resolvePath returns an absolute path, expanding ~ and handling relative segments.
func resolvePath(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		if path == "~" {
			return home
		}
		return filepath.Join(home, path[1:])
	}
	abs, _ := filepath.Abs(path)
	return abs
}

func (e *Editor) resolvePathWithContext(win *Window, path string) string {
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) || strings.HasPrefix(path, "~") {
		return resolvePath(path)
	}

	dir := ""
	if win != nil {
		dir = win.GetDir()
	} else if e.active != nil {
		dir = e.active.GetDir()
	} else {
		dir, _ = os.Getwd()
	}
	return filepath.Join(dir, path)
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

	if target := e.Open(win, pathPart); target != nil {
		if lineNum >= 0 {
			target.body.GotoLine(lineNum)
		}
		return false
	}

	// 3. Fallback: Search
	return e.Execute(nil, win, "Look "+word)
}
