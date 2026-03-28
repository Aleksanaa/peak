package main

import (
	"bytes"
	"os"
	"sort"
	"strconv"
	"strings"

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
		var dirty []*Window
		for _, col := range e.columns {
			for _, w := range col.windows {
				if w.IsDirty() && !w.Warned() {
					dirty = append(dirty, w)
				}
			}
		}

		if len(dirty) > 0 {
			msg := ""
			for _, w := range dirty {
				w.Warn()
				msg += w.GetFilename() + " modified\n"
			}
			e.showError(nil, nil, "", msg)
			return false
		}
		return true
	case "Get":
		e.cmdGet(win, cmd)
	case "Put":
		e.cmdPut(win, cmd)
	case "Edit":
		e.cmdEdit(col, win, cmd)
	case "Del":
		e.cmdDel(win)
	case "Delete":
		e.cmdDelete(win)
	case "Delcol":
		e.cmdDelcol(col, win)
	case "NewCol":
		e.cmdNewCol()
	case "New":
		e.cmdNew(col, win, cmd)
	case "Win":
		e.cmdWin(col, win, cmd)
	case "Zerox":
		e.cmdZerox(col, win)
	case "Snarf":
		e.cmdSnarf()
	case "Cut":
		e.cmdCut()
	case "Paste":
		e.cmdPaste()
	case "Sort":
		e.cmdSort(col, win)
	case "Tab":
		e.cmdTab(col, win, cmd)
	case "Undo":
		e.cmdUndo(win)
	case "Redo":
		e.cmdRedo(win)
	case "Look":
		e.cmdLook(win, cmd)
	case "Mount":
		e.cmdMount(win, cmd)
	case "Bind":
		e.cmdBind(win, cmd)
	case "Umount":
		e.cmdUmount(win, cmd)
	case "Help":
		e.Open(win, "/peak/doc/README.md")
	default:
		e.runExternal(col, win, cmd)
	}
	return false
}

func (e *Editor) cmdMount(win *Window, cmd string) {
	arg := e.getArg(win, cmd)
	if arg == "" {
		return
	}
	parts := strings.SplitN(arg, ":", 2)
	if len(parts) != 2 {
		e.showError(nil, win, "", "Usage: Mount socket:path")
		return
	}
	socket, path := parts[0], parts[1]
	err := e.ninep.Mount(socket, path)
	if err != nil {
		e.showError(nil, win, "", "Mount failed: "+err.Error())
	}
}

func (e *Editor) cmdBind(win *Window, cmd string) {
	arg := e.getArg(win, cmd)
	if arg == "" {
		return
	}
	parts := strings.SplitN(arg, ":", 2)
	if len(parts) != 2 {
		e.showError(nil, win, "", "Usage: Bind src:dest")
		return
	}
	src, dest := parts[0], parts[1]
	err := e.ninep.Bind(src, dest)
	if err != nil {
		e.showError(nil, win, "", "Bind failed: "+err.Error())
	}
}

func (e *Editor) cmdUmount(win *Window, cmd string) {
	arg := e.getArg(win, cmd)
	if arg == "" {
		return
	}
	e.ninep.Umount(arg)
}

func (e *Editor) getArg(win *Window, cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) > 1 {
		return strings.Join(fields[1:], " ")
	}

	// Prefer selection in the current focused view
	if e.focusedView != nil {
		if sel := e.focusedView.GetSelectedText(); sel != "" {
			return sel
		}
	}

	target := win
	if target == nil {
		target = e.active
	}
	if target != nil {
		if target.body != nil {
			if sel := target.body.GetSelectedText(); sel != "" {
				return sel
			}
		}
		if sel := target.tag.GetSelectedText(); sel != "" {
			return sel
		}
	}
	return ""
}

// resolvePathWithContext is now in plumb.go

func (e *Editor) Open(win *Window, path string) {
	e.OpenLine(win, path, -1, 0, nil, nil)
}

func (e *Editor) OpenLine(win *Window, path string, line, col int, binaryFallback, fallback func()) {
	full := e.resolvePathWithContext(win, path)

	// 1. Try to find existing window
	for _, c := range e.columns {
		for _, w := range c.windows {
			if e.resolvePathWithContext(nil, w.GetFilename()) == full {
				e.ActivateWindow(w)
				if line >= 0 {
					if tv := w.bodyTextView(); tv != nil {
						tv.GotoLineCol(line, col)
					}
				}
				return
			}
		}
	}

	// 2. Try to open new window
	go func() {
		content, isDir, err := readFileOrDir(full)
		e.screen.PostEvent(tcell.NewEventInterrupt(func() {
			if err == nil {
				target := e.getTargetColumn(nil, win)
				if target != nil {
					e.createWindow(target, full, content, isDir, line, col)
				}
			} else {
				if binaryFallback != nil && err.Error() == "binary file" {
					binaryFallback()
				} else if fallback != nil && os.IsNotExist(err) {
					fallback()
				} else {
					e.showError(nil, win, "", full+": "+normalizeError(err))
				}
			}
		}))
	}()
}

func (e *Editor) createWindow(target *Column, full string, content string, isDir bool, line, col int) *Window {
	if isDir {
		full = toDir(full)
	}
	tagPath := e.formatPathForTag(nil, full)
	newWin := target.AddWindow(" "+tagPath+" Get Put Undo Redo Snarf Zerox Del ", content)
	e.ActivateWindow(newWin)
	newWin.isDir = isDir
	newWin.hasVersion = hasVersion(full)
	if tv := newWin.bodyTextView(); tv != nil {
		newWin.savedVersion = tv.buffer.version
		if line >= 0 {
			tv.GotoLineCol(line, col)
		}
	}
	target.Resize(target.x, target.y, target.w, target.h)
	return newWin
}

func (e *Editor) formatPathForTag(contextWin *Window, fullPath string) string {
	if contextWin == nil {
		return formatPath(fullPath, "")
	}
	return formatPath(fullPath, contextWin.GetFilename())
}

func (e *Editor) getTargetWindow(win *Window) *Window {
	if win != nil {
		return win
	}
	return e.active
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

	// create a column if none is present
	nc := NewColumn(e.width, 1, 0, e.height-1, e, e.Execute)
	e.columns = append(e.columns, nc)
	e.resize()
	return nc
}

func normalizeError(err error) string {
	if err == nil {
		return ""
	}
	if os.IsNotExist(err) {
		return "No such file or directory"
	}
	return err.Error()
}

func (e *Editor) cmdGet(win *Window, cmd string) {
	target := e.getTargetWindow(win)
	if target == nil {
		col := e.getTargetColumn(nil, win)
		target = e.createWindow(col, "./untitled.txt", "", false, -1, 0)
	}
	arg := e.getArg(target, cmd)
	if arg == "" {
		arg = target.GetFilename()
	}
	path := e.resolvePathWithContext(target, arg)
	go func() {
		content, isDir, err := readFileOrDir(path)
		e.screen.PostEvent(tcell.NewEventInterrupt(func() {
			if err == nil {
				if isDir {
					path = toDir(path)
					target.SetName(path)
				}
				if tv := target.bodyTextView(); tv != nil {
					tv.buffer.SetText(content)
					target.isDir = isDir
					target.hasVersion = hasVersion(path)
					target.savedVersion = tv.buffer.version
					target.warnedVersion = target.savedVersion
				}
			} else {
				e.showError(target.parent, target, "", path+": "+normalizeError(err))
			}
		}))
	}()
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
	if path != "" {
		tv := target.bodyTextView()
		if tv == nil {
			return
		}
		text := tv.buffer.GetText()
		version := tv.buffer.version
		go func() {
			// In cmdPut, we don't know if it's a dir yet, but writeFile handles it.
			err := writeFile(path, []byte(text))
			e.screen.PostEvent(tcell.NewEventInterrupt(func() {
				if err != nil {
					e.showError(target.parent, target, "", normalizeError(err))
				} else {
					target.hasVersion = hasVersion(path)
					target.savedVersion = version
					target.warnedVersion = version
				}
			}))
		}()
	}
}

func (e *Editor) cmdDel(win *Window) {
	target := e.getTargetWindow(win)
	if target == nil {
		return
	}

	if target.IsDirty() && !target.Warned() {
		target.Warn()
		e.showError(target.parent, target, "", target.GetFilename()+" modified\n")
		return
	}

	e.deleteWindow(target)
}

func (e *Editor) cmdDelete(win *Window) {
	target := e.getTargetWindow(win)
	if target != nil {
		e.deleteWindow(target)
	}
}

func (e *Editor) deleteWindow(target *Window) {
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
	if target == nil {
		return
	}

	var dirty []*Window
	for _, w := range target.windows {
		if w.IsDirty() && !w.Warned() {
			dirty = append(dirty, w)
		}
	}

	if len(dirty) > 0 {
		msg := ""
		for _, w := range dirty {
			w.Warn()
			msg += w.GetFilename() + " modified\n"
		}
		e.showError(target, nil, "", msg)
		return
	}

	e.RemoveColumn(target)
}

func (e *Editor) cmdNewCol() {
	nc := NewColumn(e.width, 1, 0, e.height-1, e, e.Execute)
	e.columns = append(e.columns, nc)
	e.createWindow(nc, "./untitled.txt", "", false, -1, 0)
	e.Resize()
}

func (e *Editor) cmdNew(col *Column, win *Window, cmd string) {
	arg := e.getArg(win, cmd)
	if arg != "" {
		e.Open(win, arg)
		return
	}

	targetCol := e.getTargetColumn(col, win)
	if targetCol != nil {
		e.createWindow(targetCol, "./untitled.txt", "", false, -1, 0)
	}
}

func (e *Editor) cmdWin(col *Column, win *Window, cmd string) {
	arg := e.getArg(win, cmd)
	targetCol := e.getTargetColumn(col, win)
	if targetCol == nil {
		return
	}

	dir := ""
	if win != nil {
		dir = win.GetDir()
	} else {
		dir = getwd()
	}

	newWin, err := targetCol.AddTermWindow("", arg, dir)
	if err != nil {
		e.showError(targetCol, win, "", err.Error())
		return
	}

	e.ActivateWindow(newWin)
	targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
}

func (e *Editor) cmdZerox(col *Column, win *Window) {
	target := e.getTargetWindow(win)
	if target == nil {
		return
	}

	if tv := target.bodyTextView(); tv != nil {
		newWin := target.parent.AddWindow(target.tag.buffer.GetText(), tv.buffer.GetText())
		newTv := newWin.bodyTextView()
		if newTv != nil {
			newTv.scroll.Pos = tv.scroll.Pos
			newTv.buffer.cursor = tv.buffer.cursor
		}
		newWin.hasVersion = target.hasVersion
		newWin.isDir = target.isDir
		newWin.savedVersion = target.savedVersion
		newWin.warnedVersion = target.warnedVersion
		e.ActivateWindow(newWin)
		target.parent.Resize(target.parent.x, target.parent.y, target.parent.w, target.parent.h)
	} else if _, ok := target.body.(*TermView); ok {
		e.cmdWin(col, target, "Win")
	}
}

func (e *Editor) cmdSnarf() {
	if e.focusedView != nil {
		if buf := e.focusedView.GetBuffer(); buf != nil {
			buf.Snarf()
		}
	}
}

func (e *Editor) cmdCut() {
	if e.focusedView != nil {
		if buf := e.focusedView.GetBuffer(); buf != nil {
			buf.Cut()
		}
	}
}

func (e *Editor) cmdPaste() {
	if e.focusedView != nil {
		if buf := e.focusedView.GetBuffer(); buf != nil {
			buf.Paste()
		}
	}
}

func (e *Editor) cmdSort(col *Column, win *Window) {
	targetCol := e.getTargetColumn(col, win)
	if targetCol == nil || len(targetCol.windows) <= 1 {
		return
	}

	sort.Slice(targetCol.windows, func(i, j int) bool {
		return targetCol.windows[i].GetFilename() < targetCol.windows[j].GetFilename()
	})

	targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
}

func (e *Editor) cmdTab(col *Column, win *Window, cmd string) {
	target := e.getTargetWindow(win)
	if target == nil {
		return
	}

	fields := strings.Fields(cmd)
	if len(fields) == 1 {
		// Show current tab width
		tv := target.bodyTextView()
		if tv != nil {
			msg := target.GetFilename() + ": Tab " + strconv.Itoa(tv.tabWidth) + "\n"
			e.showError(col, target, "", msg)
		}
		return
	}

	// Set new tab width
	newTab, err := strconv.Atoi(fields[1])
	if err == nil && newTab > 0 {
		if tv := target.bodyTextView(); tv != nil {
			tv.tabWidth = newTab
			tv.UpdateLayout()
		}
	}
}

func (e *Editor) cmdUndo(win *Window) {
	target := e.getTargetWindow(win)
	if target != nil {
		if tv := target.bodyTextView(); tv != nil {
			tv.buffer.Undo()
		}
	}
}

func (e *Editor) cmdRedo(win *Window) {
	target := e.getTargetWindow(win)
	if target != nil {
		if tv := target.bodyTextView(); tv != nil {
			tv.buffer.Redo()
		}
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

	if target.body != nil {
		foundLine := target.body.Search(arg)
		if foundLine != -1 {
			e.alignWindow(target, foundLine)
		}
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

	tv := target.bodyTextView()
	if tv == nil {
		return
	}
	buf := tv.buffer
	dot := Range{buf.CursorToRuneOffset(buf.cursor), buf.CursorToRuneOffset(buf.cursor)}
	if buf.selection.Active {
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
	if target.body == nil {
		return
	}
	_, ty, _, th := target.body.GetPos()
	vrow := e.lastClickY - ty
	if vrow < 0 {
		vrow = 0
	} else if vrow >= th {
		vrow = th / 2
	}
	target.body.ShowLineAt(line, vrow)
}

func (e *Editor) showError(col *Column, win *Window, dir, msg string) {
	if dir == "" {
		if win != nil {
			dir = win.GetDir()
		} else {
			dir = getwd()
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
		if tv := reuse.bodyTextView(); tv != nil {
			tv.buffer.SetText(msg)
			e.focusedView = tv
		}
		return
	}

	targetCol := e.getTargetColumn(col, win)
	if targetCol != nil {
		newWin := targetCol.AddWindow(" "+join(dir, "+Errors")+" Get Put Del ", msg)
		e.ActivateWindow(newWin)
		targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
	}
}

func (e *Editor) runExternal(col *Column, win *Window, cmd string) {
	filename := ""
	winid := 0
	if win != nil {
		filename = win.GetFilename()
		winid = win.ID
	} else {
		filename = getwd()
	}

	go func() {
		out, err := runCommand(cmd, filename, "", winid)
		if err != nil || len(out) > 0 {
			msg := out
			if msg == "" && err != nil {
				msg = err.Error()
			}
			e.screen.PostEvent(tcell.NewEventInterrupt(func() {
				// Use getPathDir to show error in correct directory context
				e.showError(col, win, getPathDir(filename), msg)
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
