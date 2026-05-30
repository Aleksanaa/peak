package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/aleksana/peak/internal/session"
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
	args := e.getArgs(win, cmd)
	if len(args) < 2 {
		e.showError(nil, win, "", "Usage: Mount socket path")
		return
	}
	socket, path := args[0], args[1]
	resolvedSrc, err := e.ninep.Mount(socket, path)
	if err != nil {
		e.showError(nil, win, "", "Mount failed: "+err.Error())
		return
	}
	e.ninep.record(&e.ninep.mounts, resolvedSrc, resolvePath(path))
}

func (e *Editor) cmdBind(win *Window, cmd string) {
	args := e.getArgs(win, cmd)
	if len(args) < 2 {
		e.showError(nil, win, "", "Usage: Bind src dest")
		return
	}
	src, dest := args[0], args[1]
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

func (e *Editor) getArgs(win *Window, cmd string) []string {
	fields := strings.Fields(cmd)
	if len(fields) > 1 {
		return fields[1:]
	}

	// Fallback to selection if no arguments provided in the command line
	var sel string
	if e.focusedView != nil {
		sel = e.focusedView.GetSelectedText()
	}

	if sel == "" {
		target := win
		if target == nil {
			target = e.active
		}
		if target != nil {
			if target.body != nil {
				sel = target.body.GetSelectedText()
			}
			if sel == "" && target.tag != nil {
				sel = target.tag.GetSelectedText()
			}
		}
	}

	if sel != "" {
		return strings.Fields(sel)
	}
	return nil
}

func (e *Editor) getArg(win *Window, cmd string) string {
	args := e.getArgs(win, cmd)
	if len(args) > 0 {
		return strings.Join(args, " ")
	}
	return ""
}

// resolvePathWithContext is now in plumb.go

func (e *Editor) Open(win *Window, path string) {
	e.OpenLine(win, path, -1, 0, nil, nil)
}

func (e *Editor) OpenLine(win *Window, path string, line, col int, binaryFallback, fallback func()) {
	full := e.resolvePathWithContext(win, path)

	// /peak/new creates a fresh text window, same semantics as walking the 9P /new path.
	if full == "/peak/new" {
		target := e.getTargetColumn(nil, win)
		if target != nil {
			newWin := target.AddWindow(" New ", "")
			e.ActivateWindow(newWin)
			target.Resize(target.x, target.y, target.w, target.h)
		}
		return
	}

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
	nc := NewColumn(e.w, 1, 0, e.h-1, e, e.Execute)
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
					e.ninep.BroadcastGet(target)
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
					e.ninep.BroadcastPut(target)
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

	e.RemoveWindow(target)
}

func (e *Editor) cmdDelete(win *Window) {
	target := e.getTargetWindow(win)
	if target != nil {
		e.RemoveWindow(target)
	}
}

func (e *Editor) RemoveWindow(target *Window) {
	e.ninep.UmountWindow(target)
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
	nc := NewColumn(e.w, 1, 0, e.h-1, e, e.Execute)
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
	win = e.getTargetWindow(win)
	targetCol := e.getTargetColumn(col, win)
	if targetCol == nil {
		return
	}

	// If the window's path is under an external mount, delegate session
	// creation to that mount.  Do not fall through to a local terminal
	// because the working directory belongs to the remote filesystem.
	if e.ninep != nil && win != nil {
		winPath := win.GetFilename()
		if mountPath, mountFs := e.ninep.FindMount(winPath); mountPath != "" {
			dir := getPathDir(winPath)
			relPath, _ := filepath.Rel(mountPath, dir)
			relPath += "/"
			newF, err := mountFs.OpenFile("new", os.O_RDWR, 0)
			if err != nil {
				e.showError(targetCol, win, "", winPath+": virtual path cannot open pty window")
				return
			}
			go func() {
				defer newF.Close()
				if _, werr := newF.WriteAt([]byte(relPath), 0); werr != nil {
					e.screen.PostEvent(tcell.NewEventInterrupt(func() {
						e.showError(targetCol, win, "", "remote session: "+werr.Error())
					}))
					return
				}
				buf := make([]byte, 256)
				n, _ := newF.ReadAt(buf, 0)
				sessRel := strings.TrimSpace(string(buf[:n]))
				if sessRel != "" {
					e.openRemoteTermWindow(targetCol, win, mountPath, sessRel, dir)
				}
			}()
			return
		}
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

func (e *Editor) openRemoteTermWindow(targetCol *Column, win *Window, mountPath, sessRel, dir string) {
	vfsRoot := getVFS()
	ioPath := filepath.Join(mountPath, sessRel, "io")
	ctlPath := filepath.Join(mountPath, sessRel, "ctl")

	ioRead, err := vfsRoot.OpenFile(ioPath, os.O_RDONLY, 0)
	if err != nil {
		e.screen.PostEvent(tcell.NewEventInterrupt(func() {
			e.showError(targetCol, win, "", "remote io: "+err.Error())
		}))
		return
	}
	ioWrite, err := vfsRoot.OpenFile(ioPath, os.O_WRONLY, 0)
	if err != nil {
		ioRead.Close()
		e.screen.PostEvent(tcell.NewEventInterrupt(func() {
			e.showError(targetCol, win, "", "remote io: "+err.Error())
		}))
		return
	}
	ctlF, err := vfsRoot.OpenFile(ctlPath, os.O_WRONLY, 0)
	if err != nil {
		ioRead.Close()
		ioWrite.Close()
		e.screen.PostEvent(tcell.NewEventInterrupt(func() {
			e.showError(targetCol, win, "", "remote ctl: "+err.Error())
		}))
		return
	}

	sess := session.NewRemote(ioRead, ioWrite, ctlF)
	title := join(dir, "+Errors")

	reply := make(chan error, 1)
	e.screen.PostEvent(tcell.NewEventInterrupt(func() {
		newWin, err := targetCol.AddSessionTermWindow(title, sess)
		if err != nil {
			sess.Close()
			e.showError(targetCol, win, "", err.Error())
			reply <- err
			return
		}
		e.ActivateWindow(newWin)
		targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
		reply <- nil
	}))
	<-reply
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

	buf := target.body.GetBuffer()
	if buf == nil {
		return
	}

	dot := Range{buf.CursorToRuneOffset(buf.cursor), buf.CursorToRuneOffset(buf.cursor)}
	if buf.selection.Active {
		s, end := buf.selection.Ordered()
		dot = Range{buf.CursorToRuneOffset(s), buf.CursorToRuneOffset(end)}
	}

	log := &Elog{}
	ctx := &Context{Editor: e, Column: col, Window: target, Buffer: buf, Out: &pOut, Log: log}
	newDot, ok := res.Cmd.Execute(ctx, dot)
	if !ok {
		return
	}

	_, isTerm := target.body.(*TermView)
	if isTerm && len(log.ops) > 0 {
		e.showError(col, target, "", "Edit: text modifications not allowed on terminal windows")
		return
	}
	log.Apply(buf)
	start := buf.RuneOffsetToCursor(newDot.q0)
	end := buf.RuneOffsetToCursor(newDot.q1)
	buf.SetSelection(start, end)
	buf.cursor = end

	if res.Cmd.cmdc == '\n' {
		e.alignWindow(target, end.y)
		if isTerm {
			target.body.(*TermView).scroll.AutoScroll = false
		}
	}

	if isTerm {
		tv := target.body.(*TermView)
		tv.selection = buf.selection
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

func (e *Editor) findOrCreateErrorWindow(col *Column, win *Window, dir string) *Window {
	if dir == "" {
		if win != nil {
			dir = win.GetDir()
		} else {
			dir = getwd()
		}
	}
	errName := join(dir, "+Errors")

	for _, c := range e.columns {
		for _, w := range c.windows {
			if w.GetFilename() == errName && w.bodyTextView() != nil {
				return w
			}
		}
	}

	targetCol := e.getTargetColumn(col, win)
	if targetCol == nil {
		return nil
	}
	newWin := targetCol.AddWindow(" "+errName+" Get Put Del ", "")
	e.ActivateWindow(newWin)
	targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
	return newWin
}

func (e *Editor) appendToErrorWindow(col *Column, win *Window, msg string) {
	errWin := e.findOrCreateErrorWindow(col, win, "")
	if errWin == nil {
		return
	}
	if tv := errWin.bodyTextView(); tv != nil {
		existing := tv.buffer.GetText()
		if existing != "" && !strings.HasSuffix(existing, "\n") {
			existing += "\n"
		}
		tv.buffer.SetText(existing + msg)
		e.focusedView = tv
	}
}

func (e *Editor) showError(col *Column, win *Window, dir, msg string) {
	errWin := e.findOrCreateErrorWindow(col, win, dir)
	if errWin == nil {
		return
	}
	if tv := errWin.bodyTextView(); tv != nil {
		tv.buffer.SetText(msg)
		e.focusedView = tv
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
	for len(c.windows) > 0 {
		e.RemoveWindow(c.windows[0])
	}
	i := slices.Index(e.columns, c)
	if i < 0 {
		return
	}
	e.columns = slices.Delete(e.columns, i, i+1)
	e.Resize()
	if len(e.columns) == 0 {
		e.active, e.focusedView = nil, e.tag
		return
	}
	first := e.columns[0]
	if len(first.windows) > 0 {
		e.ActivateWindow(first.windows[0])
	} else {
		e.active, e.focusedView = nil, first.tag
	}
}
