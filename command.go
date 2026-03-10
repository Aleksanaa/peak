package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/atotto/clipboard"
	"github.com/gdamore/tcell/v2"
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
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '/' || r == '.' || r == '-' || r == '~'
}

// resolvePath returns an absolute path, expanding ~ and handling relative segments.
func resolvePath(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~") {
		home, _ := os.UserHomeDir()
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
			// Base directory is the part before +Errors
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

// Execute parses and runs internal or external commands.
func (e *Editor) Execute(col *Column, win *Window, cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}

	fields := strings.Fields(cmd)
	root := fields[0]

	switch root {
	case "Exit":
		return true
	case "Get":
		e.cmdGet(win)
	case "Put":
		e.cmdPut(win)
	case "Del":
		e.cmdDel(win)
	case "Delcol":
		e.cmdDelcol(col, win)
	case "NewCol":
		e.cmdNewCol()
	case "New":
		e.cmdNew(col, win)
	case "Zerox":
		e.cmdZerox(col, win)
	case "Snarf":
		e.cmdSnarf()
	case "Undo":
		e.cmdUndo(win)
	case "Redo":
		e.cmdRedo(win)
	case "Look":
		e.cmdLook(win, cmd)
	default:
		e.runExternal(col, win, cmd)
	}
	return false
}

func (e *Editor) listDir(path string) (string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		sb.WriteString(name + "\n")
	}
	return sb.String(), nil
}

func (e *Editor) getTargetColumn(col *Column, win *Window) *Column {
	if col != nil {
		return col
	}
	if win != nil {
		return win.parent
	}
	if e.active != nil {
		return e.active.parent
	}
	if len(e.columns) > 0 {
		return e.columns[0]
	}
	return nil
}

func (e *Editor) cmdGet(win *Window) {
	if win == nil {
		return
	}
	path := e.resolvePathWithContext(win, win.GetFilename())
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			if content, err := e.listDir(path); err == nil {
				win.body.buffer.SetText(content)
			}
		} else if data, err := os.ReadFile(path); err == nil {
			win.body.buffer.SetText(string(data))
		}
	}
}

func (e *Editor) cmdPut(win *Window) {
	if win == nil {
		return
	}
	path := e.resolvePathWithContext(win, win.GetFilename())
	if path != "" && !strings.HasSuffix(path, "/") {
		os.WriteFile(path, []byte(win.body.buffer.GetText()), 0644)
	}
}

func (e *Editor) cmdDel(win *Window) {
	if win == nil {
		return
	}
	col := win.parent
	for i, w := range col.windows {
		if w == win {
			col.windows = append(col.windows[:i], col.windows[i+1:]...)
			col.Resize(col.x, col.y, col.w, col.h)
			if e.active == win {
				if len(col.windows) > 0 {
					e.active = col.windows[0]
				} else {
					e.active = nil
				}
				if e.active != nil {
					e.focusedView = e.active.body
				} else {
					e.focusedView = col.tag
				}
			}
			return
		}
	}
}

func (e *Editor) cmdDelcol(col *Column, win *Window) {
	target := col
	if target == nil && win != nil {
		target = win.parent
	}
	if target != nil {
		e.RemoveColumn(target)
	}
}

func (e *Editor) cmdNewCol() {
	nc := NewColumn(e.width, 1, 0, e.height-1, e, e.Execute)
	e.columns = append(e.columns, nc)
	e.active = nc.AddWindow("", "")
	e.focusedView = e.active.body
	e.Resize()
}

func (e *Editor) cmdNew(col *Column, win *Window) {
	target := e.getTargetColumn(col, win)
	if target != nil {
		e.active = target.AddWindow("", "")
		e.focusedView = e.active.body
		target.Resize(target.x, target.y, target.w, target.h)
	}
}

func (e *Editor) cmdZerox(col *Column, win *Window) {
	target := win
	if target == nil {
		target = e.active
	}
	if target != nil {
		newWin := target.parent.AddWindow(target.tag.buffer.GetText(), target.body.buffer.GetText())
		newWin.body.scroll = target.body.scroll
		newWin.body.buffer.cursor = target.body.buffer.cursor
		e.active, e.focusedView = newWin, newWin.body
		target.parent.Resize(target.parent.x, target.parent.y, target.parent.w, target.parent.h)
	}
}

func (e *Editor) cmdSnarf() {
	if e.focusedView != nil {
		if text := e.focusedView.buffer.GetSelectedText(); text != "" {
			clipboard.WriteAll(text)
		}
	}
}

func (e *Editor) cmdUndo(win *Window) {
	target := win
	if target == nil {
		target = e.active
	}
	if target != nil {
		target.body.buffer.Undo()
	}
}

func (e *Editor) cmdRedo(win *Window) {
	target := win
	if target == nil {
		target = e.active
	}
	if target != nil {
		target.body.buffer.Redo()
	}
}

func (e *Editor) cmdLook(win *Window, cmd string) {
	path := strings.TrimSpace(strings.TrimPrefix(cmd, "Look"))
	full := e.resolvePathWithContext(win, path)

	for _, c := range e.columns {
		for _, w := range c.windows {
			if e.resolvePathWithContext(nil, w.GetFilename()) == full {
				e.active, e.focusedView = w, w.body
				return
			}
		}
	}

	info, err := os.Stat(full)
	if err != nil {
		return
	}

	var content string
	if info.IsDir() {
		if c, err := e.listDir(full); err == nil {
			content = c
		}
	} else {
		if data, err := os.ReadFile(full); err == nil {
			content = string(data)
		}
	}

	target := e.getTargetColumn(nil, win)
	if target != nil {
		tagPath := full // Default abspath
		if win != nil {
			parentFn := win.GetFilename()
			if strings.HasPrefix(parentFn, "~") {
				if home, _ := os.UserHomeDir(); strings.HasPrefix(full, home) {
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
		newWin := target.AddWindow(" "+tagPath+" Get Put Snarf Zerox Del ", content)
		e.active, e.focusedView = newWin, newWin.body
		target.Resize(target.x, target.y, target.w, target.h)
	}
}

func (e *Editor) runExternal(col *Column, win *Window, cmd string) {
	dir, _ := os.Getwd()
	if win != nil {
		if f := e.resolvePathWithContext(win, win.GetFilename()); f != "" {
			if info, err := os.Stat(f); err == nil {
				if info.IsDir() {
					dir = f
				} else {
					dir = filepath.Dir(f)
				}
			}
		}
	}

	go func() {
		out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
		if err == nil && len(out) > 0 {
			e.screen.PostEvent(tcell.NewEventInterrupt(func() {
				var reuse *Window
				if win != nil && strings.HasSuffix(win.GetFilename(), "+Errors") {
					reuse = win
				}
				if reuse == nil && e.active != nil && strings.HasSuffix(e.active.GetFilename(), "+Errors") {
					reuse = e.active
				}

				if reuse != nil {
					reuse.body.buffer.SetText(string(out))
					e.focusedView = reuse.body
					return
				}

				target := e.getTargetColumn(col, win)
				if target != nil {
					newWin := target.AddWindow(" "+filepath.Join(dir, "+Errors")+" Get Put Del ", string(out))
					e.active, e.focusedView = newWin, newWin.body
					target.Resize(target.x, target.y, target.w, target.h)
				}
			}))
		}
	}()
}

func (e *Editor) RemoveColumn(c *Column) {
	for i, col := range e.columns {
		if col == c {
			e.columns = append(e.columns[:i], e.columns[i+1:]...)
			e.Resize()
			if len(e.columns) > 0 {
				if len(e.columns[0].windows) > 0 {
					e.active, e.focusedView = e.columns[0].windows[0], e.columns[0].windows[0].body
				} else {
					e.active, e.focusedView = nil, e.columns[0].tag
				}
			} else {
				e.active, e.focusedView = nil, e.tag
			}
			break
		}
	}
}
