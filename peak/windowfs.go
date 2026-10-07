package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
)

// windowFs implements afero.Fs for a single window's /peak/<id>/ directory.
// Its files are served on 9P goroutines, so they reach window state only
// through editor.Call, on the main goroutine that owns it.
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
		win.editor.Call(func() { f.Data = []byte(win.body.GetBuffer().GetText()) })
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
	if tv, ok := f.win.body.(*TermView); ok {
		tv.session.Write(f.Writes)
		return nil
	}
	f.win.editor.Call(func() { f.win.body.GetBuffer().SetText(string(f.Writes)) })
	return nil
}

// ---- tag ----

func newWinTagFile(win *Window, flag int) *winTagFile {
	f := &winTagFile{win: win}
	if flag&os.O_WRONLY == 0 {
		win.editor.Call(func() { f.Data = []byte(win.tag.buffer.GetText()) })
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
	f.win.editor.Call(func() { f.win.tag.buffer.SetText(string(f.Writes)) })
	return nil
}

// ---- ctl ----

// ctlSnap returns the structured read payload for /<id>/ctl:
// "<id> <taglen> <bodylen> <isdir> <isdirty> <width> terminal <maxtab>\n"
// All lengths are rune counts; width is terminal columns.
func ctlSnap(win *Window) (snap []byte) {
	win.editor.Call(func() {
		isDir, isDirty, maxtab := 0, 0, 4
		if win.kind == WinDir {
			isDir = 1
		}
		if win.IsDirty() {
			isDirty = 1
		}
		if tv, ok := win.body.(*TextView); ok {
			maxtab = tv.tabWidth
		}
		snap = fmt.Appendf(nil, "%d %d %d %d %d %d terminal %d\n",
			win.ID, win.tag.buffer.Len(), win.body.GetBuffer().Len(), isDir, isDirty, win.w-1, maxtab)
	})
	return snap
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
	win.editor.callCh <- func() { win.editor.Execute(col, win, cmd) }
	return len(p), nil
}

// ---- rdsel ----

func newWinRdselFile(win *Window) *winRdselFile {
	f := &winRdselFile{}
	win.editor.Call(func() {
		f.Data = []byte(win.body.GetBuffer().GetSelectedText())
	})
	return f
}

// winRdselFile is a read-only snapshot of the window's current selection at open time.
type winRdselFile struct {
	vfs.ReadonlyFile
}

// ---- wrsel ----

func newWinWrselFile(win *Window) *winWrselFile {
	f := &winWrselFile{win: win}
	win.editor.Call(func() {
		buf := win.body.GetBuffer()
		f.q0, f.q1 = buf.q0, buf.q1
	})
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
	if _, ok := f.win.body.(*TermView); ok {
		return nil
	}
	runes := []rune(string(f.Writes))
	f.win.editor.Call(func() { f.win.body.GetBuffer().ReplaceRangeRunes(f.q0, f.q1, runes) })
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
	win.editor.callCh <- func() { win.editor.showError(col, win, text) }
	return nil
}
