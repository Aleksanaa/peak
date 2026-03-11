package main

import (
	"bytes"
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
		e.cmdGet(win, cmd)
	case "Put":
		e.cmdPut(win, cmd)
	case "Edit":
		e.cmdEdit(col, win, cmd)
	case "Del":
		e.cmdDel(win)
	case "Delcol":
		e.cmdDelcol(col, win)
	case "NewCol":
		e.cmdNewCol()
	case "New":
		e.cmdNew(col, win, cmd)
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

func (e *Editor) getArg(win *Window, cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) > 1 {
		return strings.Join(fields[1:], " ")
	}

	// Prefer selection in the current focused view
	if e.focusedView != nil {
		if sel := e.focusedView.buffer.GetSelectedText(); sel != "" {
			return sel
		}
	}

	target := e.getTargetWindow(win)
	if target != nil {
		if sel := target.body.buffer.GetSelectedText(); sel != "" {
			return sel
		}
		if sel := target.tag.buffer.GetSelectedText(); sel != "" {
			return sel
		}
	}
	return ""
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

func (e *Editor) getTargetWindow(win *Window) *Window {
	if win != nil {
		return win
	}
	return e.active
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

func (e *Editor) cmdGet(win *Window, cmd string) {
	target := e.getTargetWindow(win)
	if target == nil {
		return
	}
	arg := e.getArg(target, cmd)
	if arg == "" {
		arg = target.GetFilename()
	}
	path := e.resolvePathWithContext(target, arg)
	if content, err := e.readFileOrDir(path); err == nil {
		target.body.buffer.SetText(content)
	}
}

func (e *Editor) cmdPut(win *Window, cmd string) {
	target := e.getTargetWindow(win)
	if target == nil {
		return
	}
	arg := e.getArg(target, cmd)
	if arg == "" {
		arg = target.GetFilename()
	}
	path := e.resolvePathWithContext(target, arg)
	if path != "" && !strings.HasSuffix(path, "/") {
		os.WriteFile(path, []byte(target.body.buffer.GetText()), 0644)
	}
}

func (e *Editor) cmdDel(win *Window) {
	target := e.getTargetWindow(win)
	if target == nil {
		return
	}
	col := target.parent
	for i, w := range col.windows {
		if w == target {
			col.windows = append(col.windows[:i], col.windows[i+1:]...)
			col.Resize(col.x, col.y, col.w, col.h)
			if e.active == target {
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
	win := nc.AddWindow("", "")
	e.ActivateWindow(win)
	e.Resize()
}

func (e *Editor) cmdNew(col *Column, win *Window, cmd string) {
	targetCol := e.getTargetColumn(col, win)
	if targetCol == nil {
		return
	}

	arg := e.getArg(win, cmd)
	if arg != "" {
		path := e.resolvePathWithContext(win, arg)
		if content, err := e.readFileOrDir(path); err == nil {
			newWin := targetCol.AddWindow(path+" Get Put Del ", content)
			e.ActivateWindow(newWin)
			targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
			return
		}
	}

	newWin := targetCol.AddWindow("", "")
	e.ActivateWindow(newWin)
	targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
}

func (e *Editor) cmdZerox(col *Column, win *Window) {
	target := e.getTargetWindow(win)
	if target != nil {
		newWin := target.parent.AddWindow(target.tag.buffer.GetText(), target.body.buffer.GetText())
		newWin.body.scroll = target.body.scroll
		newWin.body.buffer.cursor = target.body.buffer.cursor
		e.ActivateWindow(newWin)
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
	target := e.getTargetWindow(win)
	if target != nil {
		target.body.buffer.Undo()
	}
}

func (e *Editor) cmdRedo(win *Window) {
	target := e.getTargetWindow(win)
	if target != nil {
		target.body.buffer.Redo()
	}
}

func (e *Editor) cmdLook(win *Window, cmd string) {
	target := e.getTargetWindow(win)
	if target == nil {
		return
	}

	arg := e.getArg(target, cmd)
	if arg == "" {
		return
	}

	foundLine := target.body.Search(arg)
	if foundLine != -1 {
		e.alignWindow(target, foundLine)
	}
}

func (e *Editor) cmdEdit(col *Column, win *Window, cmd string) {
	target := e.getTargetWindow(win)
	if target == nil {
		return
	}

	arg := e.getArg(target, cmd)
	if arg == "" {
		return
	}

	var pOut bytes.Buffer
	res, err := SregxCompile(arg, &pOut)
	if err != nil {
		e.showError(col, target, "", err.Error())
		return
	}

	buf := target.body.buffer
	dot := Range{buf.CursorToRuneOffset(buf.cursor), buf.CursorToRuneOffset(buf.cursor)}
	if buf.selectionStart != nil && buf.selectionEnd != nil {
		s, end := buf.orderedSelection()
		dot = Range{buf.CursorToRuneOffset(s), buf.CursorToRuneOffset(end)}
	}

	log := &Elog{}
	ctx := &Context{Editor: e, Column: col, Window: target, Buffer: buf, Out: &pOut, Log: log}
	newDot, ok := res.Cmd.Execute(ctx, dot)
	if !ok {
		return
	}

	log.Apply(buf)

	// Update selection/cursor from newDot
	start := buf.RuneOffsetToCursor(newDot.q0)
	end := buf.RuneOffsetToCursor(newDot.q1)
	buf.SetSelection(start, end)
	buf.cursor = end

	if res.Cmd.cmdc == '\n' {
		e.alignWindow(target, end.y)
	}

	if pOut.Len() > 0 {
		e.showError(col, target, "", pOut.String())
	}
}

func (e *Editor) alignWindow(target *Window, line int) {
	vrow := e.lastClickY - target.body.y
	if vrow < 0 {
		vrow = 0
	} else if vrow >= target.body.h {
		vrow = target.body.h / 2
	}
	target.body.ShowLineAt(line, vrow)
}

func (e *Editor) showError(col *Column, win *Window, dir, msg string) {
	if dir == "" {
		if win != nil {
			dir = win.GetDir()
		} else {
			dir, _ = os.Getwd()
		}
	}

	var reuse *Window
	if win != nil && strings.HasSuffix(win.GetFilename(), "+Errors") {
		reuse = win
	}
	if reuse == nil && e.active != nil && strings.HasSuffix(e.active.GetFilename(), "+Errors") {
		reuse = e.active
	}

	if reuse != nil {
		reuse.body.buffer.SetText(msg)
		e.focusedView = reuse.body
		return
	}

	targetCol := e.getTargetColumn(col, win)
	if targetCol != nil {
		newWin := targetCol.AddWindow(" "+filepath.Join(dir, "+Errors")+" Get Put Del ", msg)
		e.ActivateWindow(newWin)
		targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
	}
}

func (e *Editor) runExternal(col *Column, win *Window, cmd string) {
	dir := ""
	if win != nil {
		dir = win.GetDir()
	} else {
		dir, _ = os.Getwd()
	}

	e.runAsync(cmd, dir, func(out string) {
		e.showError(col, win, dir, out)
	})
}

func (e *Editor) runAsync(cmd, dir string, callback func(string)) {
	go func() {
		c := exec.Command("sh", "-c", cmd)
		c.Dir = dir
		out, _ := c.CombinedOutput()
		if len(out) > 0 {
			e.screen.PostEvent(tcell.NewEventInterrupt(func() {
				callback(string(out))
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
					e.ActivateWindow(e.columns[0].windows[0])
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
