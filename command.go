package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/gdamore/tcell/v2"
)

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
	if content, err := e.readFileOrDir(path); err == nil {
		win.body.buffer.SetText(content)
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
	word := strings.TrimSpace(strings.TrimPrefix(cmd, "Look"))
	if word == "" {
		return
	}
	target := win
	if target == nil {
		target = e.active
	}
	if target != nil {
		target.body.Search(word)
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
