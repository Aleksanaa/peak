package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/aleksana/peak/internal/quote"
	"github.com/aleksana/peak/internal/session"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

// Execute parses and runs internal or external commands.
func (e *Editor) Execute(col *Column, win *Window, cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}

	fields := quote.Fields(cmd)
	root := fields[0]

	switch root {
	case "Exit":
		if !e.warnDirty(nil, nil, e.allWindows()) {
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
	case "Dump":
		if err := e.Dump(e.getArg(win, cmd)); err != nil {
			e.showError(nil, win, "", "Dump: "+err.Error())
		}
	case "Load":
		if !e.warnDirty(nil, win, e.allWindows()) {
			return false
		}
		if err := e.Load(e.getArg(win, cmd)); err != nil {
			e.showError(nil, win, "", "Load: "+err.Error())
		}
	case "Help":
		e.Open(win, "/peak/doc/README.md")
	case "Theme":
		e.cmdTheme(win, cmd)
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
	mountedPath, err := e.ninep.Mount(socket, path)
	if err != nil {
		e.showError(nil, win, "", "Mount failed: "+err.Error())
		return
	}
	e.ninep.record(&e.ninep.mounts, socket, mountedPath)
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
	fields := quote.Fields(cmd)
	if len(fields) > 1 {
		return fields[1:]
	}

	// Fallback to selection if no arguments provided in the command line
	sel := e.focusedView.GetSelectedText()
	if sel == "" {
		target := win
		if target == nil {
			target = e.active
		}
		if target != nil {
			sel = target.body.GetSelectedText()
			if sel == "" {
				sel = target.tag.GetSelectedText()
			}
		}
	}

	if sel != "" {
		return quote.Fields(sel)
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

func (e *Editor) Open(win *Window, path string) {
	e.OpenLine(win, path, -1, 0, nil, nil)
}

func (e *Editor) OpenLine(win *Window, path string, line, col int, binaryFallback, fallback func()) {
	base := ""
	if win != nil {
		base = win.GetDir()
	} else if e.active != nil {
		base = e.active.GetDir()
	}
	full := normalizePath(path, base)

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
	for _, w := range e.allWindows() {
		if w.GetFilename() == full {
			e.ActivateWindow(w)
			if line >= 0 {
				if tv := w.bodyTextView(); tv != nil {
					tv.GotoLineCol(line, col)
				}
			}
			return
		}
	}

	// 2. Try to open new window
	go func() {
		content, isDir, writable, err := readFileOrDir(full)
		e.callCh <- func() {
			if err == nil {
				e.createWindow(e.getTargetColumn(nil, win), full, content, isDir, writable, line, col)
			} else {
				if binaryFallback != nil && err.Error() == "binary file" {
					binaryFallback()
				} else if fallback != nil && os.IsNotExist(err) {
					fallback()
				} else {
					e.showError(nil, win, "", full+": "+normalizeError(err))
				}
			}
		}
	}()
}

func (e *Editor) createWindow(target *Column, full string, content string, isDir bool, writable bool, line, col int) *Window {
	newWin := target.AddWindow(" "+quote.Quote(full)+" Get Put Undo Redo Snarf Zerox Del ", content)
	e.ActivateWindow(newWin)
	if isDir {
		newWin.kind = WinDir
	} else {
		newWin.kind = WinFile
		newWin.writable = writable
	}
	newWin.savedVersion = newWin.bodyTextView().buffer.version
	target.Resize(target.x, target.y, target.w, target.h)
	if line >= 0 {
		newWin.bodyTextView().GotoLineCol(line, col)
	}
	return newWin
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
	if os.IsNotExist(err) {
		return "No such file or directory"
	}
	return err.Error()
}

func (e *Editor) cmdGet(win *Window, cmd string) {
	target := e.getTargetWindow(win)
	if target == nil {
		col := e.getTargetColumn(nil, win)
		target = e.createWindow(col, "./untitled.txt", "", false, true, -1, 0)
	}
	arg := e.getArg(target, cmd)
	if arg == "" {
		arg = target.GetFilename()
	}
	path := normalizePath(arg, target.GetDir())
	go func() {
		content, isDir, writable, err := readFileOrDir(path)
		e.callCh <- func() {
			if err == nil {
				target.SetName(path)
				if tv := target.bodyTextView(); tv != nil {
					tv.buffer.SetText(content)
					if isDir {
						target.kind = WinDir
					} else {
						target.kind = WinFile
						target.writable = writable
					}
					target.savedVersion = tv.buffer.version
					target.warnedVersion = target.savedVersion
					e.ninep.BroadcastGet(target)
				}
			} else {
				e.showError(target.parent, target, "", path+": "+normalizeError(err))
			}
		}
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
	path := normalizePath(arg, target.GetDir())
	if path != "" {
		tv := target.bodyTextView()
		if tv == nil {
			return
		}
		text := tv.buffer.GetText()
		version := tv.buffer.version
		go func() {
			err := writeFile(path, []byte(text))
			e.callCh <- func() {
				if err != nil {
					e.showError(target.parent, target, "", normalizeError(err))
				} else {
					target.writable = true
					target.savedVersion = version
					target.warnedVersion = version
					e.ninep.BroadcastPut(target)
				}
			}
		}()
	}
}

func (e *Editor) cmdDel(win *Window) {
	target := e.getTargetWindow(win)
	if target == nil {
		return
	}
	if !e.warnDirty(target.parent, target, []*Window{target}) {
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
			if col.maximized == target {
				col.maximized = nil
			}
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

	if !e.warnDirty(target, nil, target.windows) {
		return
	}
	e.RemoveColumn(target)
}

func (e *Editor) cmdNewCol() {
	nc := NewColumn(e.w, 1, 0, e.h-1, e, e.Execute)
	e.columns = append(e.columns, nc)
	e.createWindow(nc, "./untitled.txt", "", false, true, -1, 0)
	e.resize()
}

func (e *Editor) cmdNew(col *Column, win *Window, cmd string) {
	arg := e.getArg(win, cmd)
	if arg != "" {
		e.Open(win, arg)
		return
	}

	e.createWindow(e.getTargetColumn(col, win), "./untitled.txt", "", false, true, -1, 0)
}

func (e *Editor) cmdWin(col *Column, win *Window, cmd string) {
	arg := e.getArg(win, cmd)
	win = e.getTargetWindow(win)
	targetCol := e.getTargetColumn(col, win)
	if win != nil {
		winPath := win.GetFilename()
		if mountPath, mountFs := e.ninep.FindMount(winPath); mountPath != "" {
			dir := getPathDir(winPath)
			relPath, _ := filepath.Rel(mountPath, dir)
			if newF, err := mountFs.OpenFile("new", os.O_RDWR, 0); err == nil {
				go func() {
					defer newF.Close()
					payload := toDir(relPath)
					if arg != "" {
						payload += "\n" + arg
					}
					if _, werr := newF.WriteAt([]byte(payload), 0); werr != nil {
						e.callCh <- func() {
							e.showError(targetCol, win, "", "remote session: "+werr.Error())
						}
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
	}

	dir := ""
	if win != nil {
		dir = win.GetDir()
	} else {
		dir = getwd()
	}
	if localDir, ok := e.ninep.ResolveLocalPath(dir); ok {
		dir = localDir
	} else {
		e.showError(targetCol, win, "", dir+": don't know how to open terminal window")
		return
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
		e.callCh <- func() {
			e.showError(targetCol, win, "", "remote io: "+err.Error())
		}
		return
	}
	ioWrite, err := vfsRoot.OpenFile(ioPath, os.O_WRONLY, 0)
	if err != nil {
		ioRead.Close()
		e.callCh <- func() {
			e.showError(targetCol, win, "", "remote io: "+err.Error())
		}
		return
	}
	ctlF, err := vfsRoot.OpenFile(ctlPath, os.O_WRONLY, 0)
	if err != nil {
		ioRead.Close()
		ioWrite.Close()
		e.callCh <- func() {
			e.showError(targetCol, win, "", "remote ctl: "+err.Error())
		}
		return
	}

	sess := session.NewRemote(ioRead, ioWrite, ctlF)
	title := filepath.Join(dir, "-"+filepath.Base(mountPath))

	e.Call(func() {
		newWin, err := targetCol.AddSessionTermWindow(title, sess)
		if err != nil {
			sess.Close()
			e.showError(targetCol, win, "", err.Error())
			return
		}
		e.ActivateWindow(newWin)
		targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
	})
}

func (e *Editor) cmdZerox(col *Column, win *Window) {
	target := e.getTargetWindow(win)
	if target == nil {
		return
	}

	if tv := target.bodyTextView(); tv != nil {
		newWin := target.parent.AddWindow(target.tag.buffer.GetText(), tv.buffer.GetText())
		newTv := newWin.bodyTextView()
		newTv.scroll.Pos = tv.scroll.Pos
		newTv.buffer.cursor = tv.buffer.cursor
		newWin.kind = target.kind
		newWin.writable = target.writable
		newWin.savedVersion = target.savedVersion
		newWin.warnedVersion = target.warnedVersion
		e.ActivateWindow(newWin)
		target.parent.Resize(target.parent.x, target.parent.y, target.parent.w, target.parent.h)
	} else if target.kind == WinTerm {
		e.cmdWin(col, target, "Win")
	}
}

func (e *Editor) cmdSnarf() { e.focusedView.GetBuffer().Snarf() }
func (e *Editor) cmdCut()   { e.focusedView.GetBuffer().Cut() }
func (e *Editor) cmdPaste() { e.focusedView.GetBuffer().Paste() }

func (e *Editor) cmdSort(col *Column, win *Window) {
	targetCol := e.getTargetColumn(col, win)
	if len(targetCol.windows) <= 1 {
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

	foundLine := target.body.Search(arg)
	if foundLine != -1 {
		target.body.ShowLineAt(foundLine)
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

	if target.kind == WinTerm && len(log.ops) > 0 {
		e.showError(col, target, "", "Edit: text modifications not allowed on terminal windows")
		return
	}
	log.Apply(buf)
	start := buf.RuneOffsetToCursor(newDot.q0)
	end := buf.RuneOffsetToCursor(newDot.q1)
	buf.SetSelection(start, end)
	if res.Cmd.cmdc == '\n' {
		buf.cursor = start
		target.body.ShowLineAt(start.y)
		if target.kind == WinTerm {
			target.body.(*TermView).scroll.AutoScroll = false
		}
	} else {
		buf.cursor = end
	}

	if target.kind == WinTerm {
		tv := target.body.(*TermView)
		tv.selection = buf.selection
	}

	if pOut.Len() > 0 {
		e.showError(col, target, "", pOut.String())
	}
}

func (e *Editor) findOrCreateErrorWindow(col *Column, win *Window, dir string) *Window {
	if dir == "" {
		if win != nil {
			dir = win.GetDir()
		} else {
			dir = getwd()
		}
	}
	errName := filepath.Join(dir, "+Errors")

	for _, w := range e.allWindows() {
		if w.kind == WinOut && w.GetFilename() == errName {
			return w
		}
	}

	targetCol := e.getTargetColumn(col, win)
	newWin := targetCol.AddWindow(" "+quote.Quote(errName)+" Get Del ", "")
	newWin.kind = WinOut
	e.ActivateWindow(newWin)
	targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
	return newWin
}

func (e *Editor) allWindows() []*Window {
	var ws []*Window
	for _, col := range e.columns {
		ws = append(ws, col.windows...)
	}
	return ws
}

// warnDirty checks windows for unsaved changes. If any are dirty and unwarned
// it warns them, shows an error, and returns false. Returns true if safe to proceed.
func (e *Editor) warnDirty(col *Column, win *Window, windows []*Window) bool {
	var dirty []*Window
	for _, w := range windows {
		if w.IsDirty() && !w.Warned() {
			dirty = append(dirty, w)
		}
	}
	if len(dirty) == 0 {
		return true
	}
	var msg strings.Builder
	for _, w := range dirty {
		w.Warn()
		msg.WriteString(w.GetFilename() + " modified\n")
	}
	e.showError(col, win, "", msg.String())
	return false
}

func (e *Editor) appendToErrorWindow(col *Column, win *Window, msg string) {
	tv := e.findOrCreateErrorWindow(col, win, "").bodyTextView()
	existing := tv.buffer.GetText()
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}
	tv.buffer.SetText(existing + msg)
	e.focusedView = tv
}

func (e *Editor) showError(col *Column, win *Window, dir, msg string) {
	tv := e.findOrCreateErrorWindow(col, win, dir).bodyTextView()
	tv.buffer.SetText(msg)
	e.focusedView = tv
}

func (e *Editor) runExternal(col *Column, win *Window, cmd string) {
	if win != nil && win.kind == WinTerm {
		tv := win.body.(*TermView)
		tv.scroll.AutoScroll = true
		tv.session.Write([]byte(cmd + "\r"))
		return
	}

	pipechar := byte(0)
	if len(cmd) > 0 && (cmd[0] == '<' || cmd[0] == '>' || cmd[0] == '|') {
		pipechar = cmd[0]
		cmd = strings.TrimSpace(cmd[1:])
	}

	filename := ""
	winid := 0
	if win != nil {
		filename = win.GetFilename()
		winid = win.ID
	} else {
		filename = getwd()
	}

	var input string
	var selStart, selEnd Cursor
	if win != nil {
		buf := win.body.GetBuffer()
		if buf.selection.Active {
			selStart, selEnd = buf.selection.Ordered()
		} else {
			selStart, selEnd = buf.cursor, buf.cursor
		}
		if pipechar == '>' || pipechar == '|' {
			input = buf.GetSelectedText()
		}
	}

	go func() {
		out, err := runCommand(cmd, filename, input, winid)
		e.callCh <- func() {
			if (pipechar == '<' || pipechar == '|') && win != nil {
				buf := win.body.GetBuffer()
				newCursor := buf.SetTextInRange(selStart, selEnd, out)
				buf.cursor = newCursor
				buf.ClearSelection()
				if err != nil {
					e.showError(col, win, getPathDir(filename), err.Error())
				}
				return
			}
			if err != nil || len(out) > 0 {
				msg := out
				if msg == "" && err != nil {
					msg = err.Error()
				}
				e.showError(col, win, getPathDir(filename), msg)
			}
		}
	}()
}

func (e *Editor) RemoveColumn(c *Column) {
	for len(c.windows) > 0 {
		e.RemoveWindow(c.windows[0])
	}
	i := slices.Index(e.columns, c)
	e.columns = slices.Delete(e.columns, i, i+1)
	e.resize()
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

func (e *Editor) cmdTheme(win *Window, cmd string) {
	name := e.getArg(win, cmd)
	if name == "" {
		e.Open(win, "/peak/theme")
		return
	}
	if err := e.ApplyTheme(name); err != nil {
		e.showError(nil, win, "", "Theme: "+err.Error())
		return
	}
	e.Redraw()
}

func (e *Editor) ApplyTheme(name string) error {
	data, err := readFile("/peak/theme/" + name)
	if err != nil {
		return err
	}
	return applyThemeFromData(&e.theme, data)
}

func applyThemeFromData(t *Theme, data []byte) error {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		hex, err := strconv.ParseUint(parts[1], 0, 32)
		if err != nil {
			continue
		}
		setThemeField(t, key, color.NewHexColor(int32(hex)))
	}
	return nil
}

func setThemeField(t *Theme, key string, c tcell.Color) {
	switch key {
	case "GlobalTagBG":
		t.GlobalTagBG = c
	case "GlobalTagFG":
		t.GlobalTagFG = c
	case "ColTagBG":
		t.ColTagBG = c
	case "ColTagFG":
		t.ColTagFG = c
	case "TagBG":
		t.TagBG = c
	case "TagFG":
		t.TagFG = c
	case "BodyBG":
		t.BodyBG = c
	case "BodyFG":
		t.BodyFG = c
	case "Handle":
		t.Handle = c
	case "ScrollThumb":
		t.ScrollThumb = c
	case "ScrollGutter":
		t.ScrollGutter = c
	case "HandleDirty":
		t.HandleDirty = c
	case "HandleError":
		t.HandleError = c
	case "HandleWritable":
		t.HandleWritable = c
	case "HandleUnwritable":
		t.HandleUnwritable = c
	case "SelectionBG":
		t.SelectionBG = c
	case "SelectionFG":
		t.SelectionFG = c
	case "HandleColumn":
		t.HandleColumn = c
	case "SynKeyword":
		t.SynKeyword = c
	case "SynType":
		t.SynType = c
	case "SynComment":
		t.SynComment = c
	case "SynString":
		t.SynString = c
	case "SynNumber":
		t.SynNumber = c
	case "SynFunction":
		t.SynFunction = c
	case "SynOperator":
		t.SynOperator = c
	case "SynVariable":
		t.SynVariable = c
	case "SynConstant":
		t.SynConstant = c
	case "SynError":
		t.SynError = c
	}
}
