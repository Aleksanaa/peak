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

// resolvePathWithContext resolves a path relative to a window's directory or CWD.
func (e *Editor) resolvePathWithContext(win *Window, path string) string {
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) || strings.HasPrefix(path, "~") {
		return resolvePath(path)
	}

	dir, _ := os.Getwd()
	target := win
	if target == nil {
		target = e.active
	}
	if target != nil {
		fn := target.GetFilename()
		if strings.HasSuffix(fn, "+Errors") {
			dir = filepath.Dir(fn)
		} else {
			absFn := resolvePath(fn)
			if info, err := os.Stat(absFn); err == nil && info.IsDir() {
				dir = absFn
			} else {
				dir = filepath.Dir(absFn)
			}
		}
	}
	return filepath.Join(dir, path)
}

// readFileOrDir returns the content of a file or a listing if it's a directory.
func (e *Editor) readFileOrDir(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return e.listDir(path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
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

	full := e.resolvePathWithContext(win, pathPart)
	// 1. Try to find existing window
	for _, c := range e.columns {
		for _, w := range c.windows {
			if e.resolvePathWithContext(nil, w.GetFilename()) == full {
				e.active, e.focusedView = w, w.body
				if lineNum >= 0 {
					w.body.GotoLine(lineNum)
				}
				return false
			}
		}
	}

	// 2. Try to open new window if path exists
	if content, err := e.readFileOrDir(full); err == nil {
		target := e.getTargetColumn(nil, win)
		if target != nil {
			tagPath := full
			if win != nil {
				// Attempt to maintain relative/home-relative path style in tag
				parentFn := win.GetFilename()
				if strings.HasPrefix(parentFn, "~") {
					if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(full, home) {
						tagPath = "~" + full[len(home):]
					}
				} else if !filepath.IsAbs(parentFn) {
					cwd, _ := os.Getwd()
					if rel, err := filepath.Rel(cwd, full); err == nil {
						if !strings.HasPrefix(rel, ".") && !strings.HasPrefix(rel, "/") {
							tagPath = "./" + rel
						} else {
							tagPath = rel
						}
					}
				}
			}
			newWin := target.AddWindow(" "+tagPath+" Get Put Undo Redo Snarf Zerox Del ", content)
			e.active, e.focusedView = newWin, newWin.body
			if lineNum >= 0 {
				newWin.body.GotoLine(lineNum)
			}
			target.Resize(target.x, target.y, target.w, target.h)
			return false
		}
	}

	// 3. Fallback: Search
	return e.Execute(nil, win, "Look "+word)
}
