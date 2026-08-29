package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
)

// windowFs implements afero.Fs for a single window's /peak/<id>/ directory.
type windowFs struct{ *vfs.NamespaceFs }

func newWindowFs(win *Window) *windowFs {
	return &windowFs{&vfs.NamespaceFs{
		Entries: []vfs.FileEntry{
			{Name: "body", Mode: 0644, Open: func(flag int) (afero.File, error) { return newWinBodyFile(win, flag), nil }},
			{Name: "tag", Mode: 0644, Open: func(flag int) (afero.File, error) { return newWinTagFile(win, flag), nil }},
			{Name: "ctl", Mode: 0600, Open: func(flag int) (afero.File, error) { return newWinCtlFile(win, flag), nil }},
			{Name: "event", Mode: 0644, Open: func(flag int) (afero.File, error) { return newWinEventFile(win, flag), nil }},
			{Name: "addr", Mode: 0644, Open: func(flag int) (afero.File, error) { return newWinAddrFile(win, flag), nil }},
			{Name: "data", Mode: 0644, Open: func(flag int) (afero.File, error) { return newWinDataFile(win, flag), nil }},
			{Name: "rdsel", Mode: 0444, Open: func(_ int) (afero.File, error) { return newWinRdselFile(win), nil }},
			{Name: "wrsel", Mode: 0200, Open: func(_ int) (afero.File, error) { return newWinWrselFile(win), nil }},
			{Name: "errors", Mode: 0200, Open: func(_ int) (afero.File, error) { return &winErrorsFile{win: win}, nil }},
			{Name: "color", Mode: 0200, Open: func(_ int) (afero.File, error) { return &winColorFile{win: win}, nil }},
		},
	}}
}

// ---- body ----

func newWinBodyFile(win *Window, flag int) *winBodyFile {
	f := &winBodyFile{win: win}
	if flag&os.O_WRONLY == 0 {
		win.lk.Lock()
		f.Data = []byte(win.body.GetBuffer().GetText())
		win.bodySnapSeq = win.mutSeq
		win.lk.Unlock()
	}
	return f
}

type winBodyFile struct {
	vfs.ReadWriteFile
	win *Window
}

func (f *winBodyFile) Close() error {
	if f.Writes == nil {
		return nil
	}
	if f.win.kind == WinTerm {
		f.win.body.(*TermView).session.Write(f.Writes)
		return nil
	}
	f.win.lk.Lock()
	f.win.body.GetBuffer().SetText(string(f.Writes))
	f.win.lk.Unlock()
	f.win.editor.Redraw()
	return nil
}

// ---- tag ----

func newWinTagFile(win *Window, flag int) *winTagFile {
	f := &winTagFile{win: win}
	if flag&os.O_WRONLY == 0 {
		win.lk.Lock()
		f.Data = []byte(win.tag.buffer.GetText())
		win.lk.Unlock()
	}
	return f
}

type winTagFile struct {
	vfs.ReadWriteFile
	win *Window
}

func (f *winTagFile) Close() error {
	if f.Writes == nil {
		return nil
	}
	f.win.lk.Lock()
	f.win.tag.buffer.SetText(string(f.Writes))
	f.win.lk.Unlock()
	f.win.editor.Redraw()
	return nil
}

// ---- ctl ----

// ctlSnap returns the structured read payload for /<id>/ctl:
// "<id> <taglen> <bodylen> <isdir> <isdirty> <width> terminal <maxtab>\n"
// All lengths are rune counts; width is terminal columns.
func ctlSnap(win *Window) []byte {
	var tagLen, bodyLen, width, maxtab int
	isDir, isDirty := 0, 0
	win.lk.Lock()
	tagLen = win.tag.buffer.Len()
	bodyLen = win.body.GetBuffer().Len()
	if win.kind == WinDir {
		isDir = 1
	}
	if win.IsDirty() {
		isDirty = 1
	}
	width = win.w - 1
	maxtab = 4
	if tv, ok := win.body.(*TextView); ok {
		maxtab = tv.tabWidth
	}
	win.lk.Unlock()
	return fmt.Appendf(nil, "%d %d %d %d %d %d terminal %d\n",
		win.ID, tagLen, bodyLen, isDir, isDirty, width, maxtab)
}

func newWinCtlFile(win *Window, flag int) *winCtlFile {
	f := &winCtlFile{win: win}
	if flag&os.O_WRONLY == 0 {
		f.Data = ctlSnap(win)
	}
	return f
}

type winCtlFile struct {
	vfs.ReadonlyFile
	win *Window
}

// WriteAt executes the trimmed string as an editor command.
func (f *winCtlFile) WriteAt(p []byte, _ int64) (int, error) {
	cmd := strings.TrimSpace(string(p))
	if cmd == "" {
		return len(p), nil
	}
	win, col := f.win, f.win.parent
	win.editor.callCh <- func() { win.onExec(col, win, cmd) }
	return len(p), nil
}

func (f *winCtlFile) Write(p []byte) (int, error)       { return f.WriteAt(p, 0) }
func (f *winCtlFile) WriteString(s string) (int, error) { return f.WriteAt([]byte(s), 0) }

// ---- rdsel ----

func newWinRdselFile(win *Window) *winRdselFile {
	f := &winRdselFile{}
	win.lk.Lock()
	buf := win.body.GetBuffer()
	if buf.selection.Active {
		start, end := buf.selection.Ordered()
		q0 := buf.RuneOffsetOfPos(start.y, start.x)
		q1 := buf.RuneOffsetOfPos(end.y, end.x)
		f.Data = []byte(string(buf.RunesInRange(q0, q1)))
	}
	win.lk.Unlock()
	return f
}

// winRdselFile is a read-only snapshot of the window's current selection at open time.
type winRdselFile struct {
	vfs.ReadonlyFile
}

// ---- wrsel ----

func newWinWrselFile(win *Window) *winWrselFile {
	f := &winWrselFile{win: win}
	win.lk.Lock()
	buf := win.body.GetBuffer()
	if buf.selection.Active {
		start, end := buf.selection.Ordered()
		f.q0 = buf.RuneOffsetOfPos(start.y, start.x)
		f.q1 = buf.RuneOffsetOfPos(end.y, end.x)
	}
	win.lk.Unlock()
	return f
}

// winWrselFile is a write-only file; on Close it replaces the selection captured at
// open time with the written bytes.
type winWrselFile struct {
	vfs.WriteOnlyFile
	win    *Window
	q0, q1 int
}

func (f *winWrselFile) Close() error {
	if f.Writes == nil {
		return nil
	}
	if f.win.kind == WinTerm {
		return nil
	}
	runes := []rune(string(f.Writes))
	f.win.lk.Lock()
	f.win.body.GetBuffer().ReplaceRangeRunes(f.q0, f.q1, runes)
	f.win.lk.Unlock()
	f.win.editor.Redraw()
	return nil
}

// ---- errors ----

type winErrorsFile struct {
	vfs.WriteOnlyFile
	win *Window
}

func (f *winErrorsFile) Close() error {
	if len(f.Writes) == 0 {
		return nil
	}
	win, col, text := f.win, f.win.parent, string(f.Writes)
	win.editor.callCh <- func() { win.editor.appendToErrorWindow(col, win, text) }
	return nil
}
