package main

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/aleksana/peak/internal/quote"
	"github.com/aleksana/peak/internal/session"
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
		if err := e.Dump(e.argName(win, cmd)); err != nil {
			e.showError(nil, win, "Dump: "+err.Error())
		}
	case "Load":
		if !e.warnDirty(nil, win, e.allWindows()) {
			return false
		}
		if err := e.Load(e.argName(win, cmd)); err != nil {
			e.showError(nil, win, "Load: "+err.Error())
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
	args := e.argFields(win, cmd)
	if len(args) < 2 {
		e.showError(nil, win, "Usage: Mount socket path")
		return
	}
	socket, path := args[0], args[1]
	if _, err := e.ninep.Mount(socket, path); err != nil {
		e.showError(nil, win, "Mount failed: "+err.Error())
	}
}

func (e *Editor) cmdBind(win *Window, cmd string) {
	args := e.argFields(win, cmd)
	if len(args) < 2 {
		e.showError(nil, win, "Usage: Bind src dest")
		return
	}
	src, dest := args[0], args[1]
	err := e.ninep.Bind(src, dest)
	if err != nil {
		e.showError(nil, win, "Bind failed: "+err.Error())
	}
}

func (e *Editor) cmdUmount(win *Window, cmd string) {
	arg := e.argName(win, cmd)
	if arg == "" {
		return
	}
	e.ninep.Umount(arg)
}

// argText returns a command's argument: the text after the command word, or
// else the selection. Commands taking free text (Edit, Look, Win) use it as
// is; argName and argFields read it as quoted names.
func (e *Editor) argText(win *Window, cmd string) string {
	if _, rest := quote.Cut(cmd); strings.TrimSpace(rest) != "" {
		return strings.TrimSpace(rest)
	}
	sel := e.focusedView.GetBuffer().GetSelectedText()
	if sel == "" {
		if target := e.getTargetWindow(win); target != nil {
			sel = target.body.GetBuffer().GetSelectedText()
			if sel == "" {
				sel = target.tag.buffer.GetSelectedText()
			}
		}
	}
	return strings.TrimSpace(sel)
}

// argName returns the single name a command takes, such as Get's file.
func (e *Editor) argName(win *Window, cmd string) string {
	return quote.Unquote(e.argText(win, cmd))
}

// argFields returns the names a command takes, such as Mount's socket and path.
func (e *Editor) argFields(win *Window, cmd string) []string {
	return quote.Fields(e.argText(win, cmd))
}

// Open opens path in a window, or shows the one it is open in.
func (e *Editor) Open(win *Window, path string) {
	e.OpenLine(win, path, -1, 0, func(full string, err error) {
		e.showError(nil, win, full+": "+normalizeError(err))
	})
}

// OpenLine opens path as Open does and goes to column col of line, or
// selects the line for a negative col; a negative line goes nowhere. If
// path, made absolute as full, cannot be opened, it calls failed instead.
func (e *Editor) OpenLine(win *Window, path string, line, col int, failed func(full string, err error)) {
	full := normalizePath(path, e.dirOf(win))

	// /peak/new is a new window, as walking it over 9P makes.
	if full == "/peak/new" {
		e.newFileWindow(e.getTargetColumn(nil, win), win)
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

	// 2. Open a new one, unless there is no such file.
	if _, err := ns.Stat(full); err != nil {
		failed(full, err)
		return
	}
	w := e.createWindow(e.getTargetColumn(nil, win), full)
	e.get(w, full, func(err error) {
		if err != nil {
			e.RemoveWindow(w)
			failed(full, err)
		} else if line >= 0 {
			w.bodyTextView().GotoLineCol(line, col)
		}
	})
}

// dirOf returns the directory win is in, or else the active window, or else
// peak.
func (e *Editor) dirOf(win *Window) string {
	if win == nil {
		win = e.active
	}
	if win == nil {
		return getwd()
	}
	return win.GetDir()
}

// createWindow adds a window for the file name to col and shows it. It is
// empty, and not writable, until it is got.
func (e *Editor) createWindow(col *Column, name string) *Window {
	w := col.AddWindow(tagText(name, "Get Put Undo Redo Snarf Zerox Del"), "")
	e.showWindow(w)
	return w
}

// newFileWindow adds an empty window to col for a new file in the directory
// of win, which it is named after until the file is named; see fileName.
func (e *Editor) newFileWindow(col *Column, win *Window) *Window {
	w := e.createWindow(col, e.dirOf(win))
	w.loaded(false, true)
	return w
}

// get reads the file or directory path into win's body in the background
// and names the window after it. Then, on the main goroutine, it calls done
// with how it failed. It is the one way a file's text comes into a window.
func (e *Editor) get(win *Window, path string, done func(error)) {
	go func() {
		content, isDir, writable, err := readFileOrDir(path)
		e.callCh <- func() {
			if err == nil {
				win.SetName(path)
				win.bodyTextView().buffer.SetText(content)
				win.loaded(isDir, writable)
				e.ninep.BroadcastGet(win)
			}
			done(err)
		}
	}()
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
	nc := NewColumn(e.w, 1, 0, e.h-1, e)
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
		// With no window to get into, Get file opens it.
		if arg := e.argName(nil, cmd); arg != "" {
			e.Open(nil, arg)
		}
		return
	}
	if target.kind == WinTerm {
		return
	}
	arg := e.argName(target, cmd)
	if arg == "" {
		arg = target.GetFilename()
	}
	path := normalizePath(arg, target.GetDir())
	e.get(target, path, func(err error) {
		if err != nil {
			e.showError(target.parent, target, path+": "+normalizeError(err))
		}
	})
}

func (e *Editor) cmdPut(win *Window, cmd string) {
	target := e.getTargetWindow(win)
	if target == nil || target.kind == WinTerm {
		return
	}
	arg := e.argName(target, cmd)
	if arg == "" {
		arg = target.fileName()
	}
	if arg == "" {
		e.showError(target.parent, target, "no file name")
		return
	}
	path := normalizePath(arg, target.GetDir())
	if target.fileName() == "" {
		target.SetName(path) // naming a new window's file
	}
	own := path == normalizePath(target.fileName(), target.GetDir())
	b := target.bodyTextView().buffer
	text, version := b.GetText(), b.version
	go func() {
		err := writeFile(path, []byte(text))
		e.callCh <- func() {
			if err != nil {
				e.showError(target.parent, target, normalizeError(err))
			} else if own {
				e.saved(target, version)
			}
		}
	}()
}

// saved records that win's own file holds its text as of version: the
// window is clean, and its file writable. Writing another file, or part of
// the text, saves nothing, as in acme.
func (e *Editor) saved(win *Window, version int) {
	win.writable = true
	win.markSaved(version)
	e.ninep.BroadcastPut(win)
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

// RemoveWindow detaches target from the editor and releases its resources.
// It is a no-op for a window that was already removed: a terminal whose
// process exits races with Del, and both paths end up here.
func (e *Editor) RemoveWindow(target *Window) {
	col := target.parent
	if !slices.Contains(col.windows, target) {
		return
	}
	e.ninep.UmountWindow(target)
	target.Close()
	col.remove(target)
	if e.active == target {
		e.active, e.focusedView = nil, col.tag
		if len(col.windows) > 0 {
			e.active = col.windows[0]
			e.focusedView = e.active.body
		}
	}
}

func (e *Editor) cmdDelcol(col *Column, win *Window) {
	if col == nil && win != nil {
		col = win.parent
	}
	if col != nil && e.warnDirty(col, nil, col.windows) {
		e.RemoveColumn(col)
	}
}

func (e *Editor) cmdNewCol() {
	nc := NewColumn(e.w, 1, 0, e.h-1, e)
	e.columns = append(e.columns, nc)
	e.newFileWindow(nc, nil)
	e.resize()
}

func (e *Editor) cmdNew(col *Column, win *Window, cmd string) {
	arg := e.argName(win, cmd)
	if arg != "" {
		e.Open(win, arg)
		return
	}

	e.newFileWindow(e.getTargetColumn(col, win), win)
}

func (e *Editor) cmdWin(col *Column, win *Window, cmd string) {
	arg := e.argText(win, cmd)
	win = e.getTargetWindow(win)
	targetCol := e.getTargetColumn(col, win)
	dir := e.dirOf(win)
	if mountPath, mountFs := ns.FindMount(dir); mountPath != "" {
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
						e.showError(targetCol, win, "remote session: "+werr.Error())
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

	if localDir, ok := ns.ResolveLocalPath(dir); ok {
		dir = localDir
	} else {
		e.showError(targetCol, win, dir+": don't know how to open terminal window")
		return
	}
	var name string
	if arg == "" {
		if name, _ = os.Hostname(); name == "" {
			name = "term"
		}
	} else {
		name = filepath.Base(strings.Fields(arg)[0])
	}
	sess, err := session.NewLocal(arg, dir)
	var newWin *Window
	if err == nil {
		newWin, err = targetCol.AddTermWindow(tagText(filepath.Join(dir, "-"+name), "Zerox Del"), arg, sess)
	}
	if err != nil {
		e.showError(targetCol, win, err.Error())
		return
	}
	e.showWindow(newWin)
}

func (e *Editor) openRemoteTermWindow(targetCol *Column, win *Window, mountPath, sessRel, dir string) {
	ioPath := filepath.Join(mountPath, sessRel, "io")
	ctlPath := filepath.Join(mountPath, sessRel, "ctl")

	ioRead, err := ns.OpenFile(ioPath, os.O_RDONLY, 0)
	if err != nil {
		e.callCh <- func() {
			e.showError(targetCol, win, "remote io: "+err.Error())
		}
		return
	}
	ioWrite, err := ns.OpenFile(ioPath, os.O_WRONLY, 0)
	if err != nil {
		ioRead.Close()
		e.callCh <- func() {
			e.showError(targetCol, win, "remote io: "+err.Error())
		}
		return
	}
	ctlF, err := ns.OpenFile(ctlPath, os.O_WRONLY, 0)
	if err != nil {
		ioRead.Close()
		ioWrite.Close()
		e.callCh <- func() {
			e.showError(targetCol, win, "remote ctl: "+err.Error())
		}
		return
	}

	sess := session.NewRemote(ioRead, ioWrite, ctlF)
	title := filepath.Join(dir, "-"+filepath.Base(mountPath))

	e.Call(func() {
		newWin, err := targetCol.AddTermWindow(tagText(title, "Zerox Del"), "", sess)
		if err != nil {
			e.showError(targetCol, win, err.Error())
			return
		}
		e.showWindow(newWin)
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
		newTv.org = tv.org
		newTv.buffer.SetDot(tv.buffer.q0, tv.buffer.q1)
		newWin.kind, newWin.writable = target.kind, target.writable
		// Versions are per buffer: the copy matches disk iff the original does.
		if target.IsDirty() {
			newWin.markSaved(-1)
		} else {
			newWin.markSaved(newTv.buffer.version)
		}
		e.showWindow(newWin)
	} else {
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

	targetCol.Resize(targetCol.rect)
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
			e.showError(col, target, msg)
		}
		return
	}

	// Set new tab width
	newTab, err := strconv.Atoi(fields[1])
	if err == nil && newTab > 0 {
		if tv := target.bodyTextView(); tv != nil {
			tv.tabWidth = newTab
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

	arg := e.argText(target, cmd)
	if arg == "" {
		return
	}

	if b := target.body.GetBuffer(); b.Search(arg) {
		target.body.Show(b.q0)
	}
}

func (e *Editor) cmdEdit(col *Column, win *Window, cmd string) {
	win = e.getTargetWindow(win)
	e.Edit(col, win, e.argText(win, cmd))
}

// errorWindow returns the +Errors window of win's directory, or of peak's
// for no window. If there is none, it adds one to col, or else to the
// column where win or the active window is.
func (e *Editor) errorWindow(col *Column, win *Window) *Window {
	dir := getwd()
	if win != nil {
		dir = win.GetDir()
	}
	name := filepath.Join(dir, "+Errors")
	for _, w := range e.allWindows() {
		if w.kind == WinOut && w.GetFilename() == name {
			return w
		}
	}
	w := e.getTargetColumn(col, win).AddWindow(tagText(name, "Get Del"), "")
	w.kind = WinOut
	e.showWindow(w)
	return w
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
	e.showError(col, win, msg.String())
	return false
}

// showError adds msg to the end of win's +Errors window, on a line of its
// own, shows it there and gives the window the focus, as acme's warnings do.
func (e *Editor) showError(col *Column, win *Window, msg string) {
	tv := e.errorWindow(col, win).bodyTextView()
	b := tv.buffer
	n := b.Len()
	if n > 0 && b.RunesInRange(n-1, n)[0] != '\n' {
		msg = "\n" + msg
	}
	b.ReplaceRangeRunes(n, n, []rune(msg))
	tv.Show(n)
	e.focusedView = tv
}

func (e *Editor) runExternal(col *Column, win *Window, cmd string) {
	if win != nil && win.kind == WinTerm {
		tv := win.body.(*TermView)
		tv.autoScroll = true
		tv.session.Write([]byte(cmd + "\r"))
		return
	}

	pipe := byte(0)
	if strings.ContainsRune("<>|", rune(cmd[0])) {
		pipe, cmd = cmd[0], strings.TrimSpace(cmd[1:])
	}

	// The output lands after the user may have edited the window; dot, kept
	// as rune offsets, clamps to the buffer then, as acme's wrsel does.
	var input string
	var q0, q1 int
	if win != nil {
		buf := win.body.GetBuffer()
		q0, q1 = buf.q0, buf.q1
		if pipe == '>' || pipe == '|' {
			input = buf.GetSelectedText()
		}
	}

	e.run(win, cmd, input, func(out string, err error) {
		if win != nil && (pipe == '<' || pipe == '|') {
			win.body.GetBuffer().ReplaceRangeRunes(q0, q1, []rune(out))
			out = ""
		}
		e.showOutput(col, win, out, err)
	})
}

// run runs cmd for win, or for no window in peak's directory, in the
// background, with input on its standard input. Then, on the main
// goroutine, it calls done with what cmd printed and how it failed.
func (e *Editor) run(win *Window, cmd, input string, done func(out string, err error)) {
	path, winid := getwd(), 0
	if win != nil {
		path, winid = win.GetFilename(), win.ID
	}
	go func() {
		out, err := runCommand(cmd, path, input, winid)
		e.callCh <- func() { done(out, err) }
	}()
}

// showOutput shows what a command run for win printed, or else how it
// failed, in the window's +Errors.
func (e *Editor) showOutput(col *Column, win *Window, out string, err error) {
	if out == "" && err != nil {
		out = err.Error()
	}
	if out != "" {
		e.showError(col, win, out)
	}
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
	name := e.argName(win, cmd)
	if name == "" {
		e.Open(win, "/peak/theme")
		return
	}
	if err := e.ApplyTheme(name); err != nil {
		e.showError(nil, win, "Theme: "+err.Error())
		return
	}
	e.Redraw()
}
