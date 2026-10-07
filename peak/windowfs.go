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
	file := func(read func() string, write func(string)) func(int) (afero.File, error) {
		return func(flag int) (afero.File, error) { return newWinFile(win, flag, read, write), nil }
	}
	body := func() *Buffer { return win.body.GetBuffer() }
	_, term := win.body.(*TermView)
	return &windowFs{&vfs.NamespaceFs{
		Entries: []vfs.FileEntry{
			{Name: "body", Mode: 0644, Open: file(func() string { return body().GetText() }, func(s string) {
				if term {
					win.body.(*TermView).session.Write([]byte(s))
				} else {
					body().SetText(s)
				}
			})},
			{Name: "tag", Mode: 0644, Open: file(win.tag.buffer.GetText, win.tag.buffer.SetText)},
			{Name: "ctl", Mode: 0600, Open: func(flag int) (afero.File, error) {
				return &winCtlFile{newWinFile(win, flag, func() string { return ctlText(win) }, nil)}, nil
			}},
			{Name: "event", Mode: 0644, Open: func(flag int) (afero.File, error) { return newWinEventFile(win, flag), nil }},
			{Name: "addr", Mode: 0644, Open: file(func() string {
				return fmt.Sprintf("#%d,#%d\n", win.addrQ0, win.addrQ1)
			}, func(s string) {
				b := body()
				if q0, q1, err := parseAddr(strings.TrimSpace(s), b); err == nil {
					win.addrQ0, win.addrQ1 = clampAddr(q0, b), clampAddr(q1, b)
				}
			})},
			{Name: "data", Mode: 0644, Open: file(func() string {
				return string(body().RunesInRange(win.addrQ0, win.addrQ1))
			}, func(s string) {
				if !term {
					r := []rune(s)
					body().ReplaceRangeRunes(win.addrQ0, win.addrQ1, r)
					win.addrQ1 = win.addrQ0 + len(r)
				}
			})},
			{Name: "rdsel", Mode: 0444, Open: file(func() string { return body().GetSelectedText() }, nil)},
			// wrsel replaces what was selected when it was opened, as acme's does.
			{Name: "wrsel", Mode: 0200, Open: func(flag int) (afero.File, error) {
				var q0, q1 int
				win.editor.Call(func() { q0, q1 = body().q0, body().q1 })
				return newWinFile(win, flag, nil, func(s string) {
					if !term {
						body().ReplaceRangeRunes(q0, q1, []rune(s))
					}
				}), nil
			}},
			{Name: "errors", Mode: 0200, Open: file(nil, func(s string) { win.editor.showError(win.parent, win, s) })},
			{Name: "color", Mode: 0200, Open: func(_ int) (afero.File, error) { return &winColorFile{win: win}, nil }},
		},
	}}
}

// A winFile reads as the window was when it was opened, and what is written
// to it is applied when it is closed, if anything was. Both happen on the
// main goroutine. A file without read reads empty; one without write
// refuses writes.
type winFile struct {
	vfs.ReadWriteFile
	win   *Window
	write func(string)
}

func newWinFile(win *Window, flag int, read func() string, write func(string)) *winFile {
	f := &winFile{win: win, write: write}
	if read != nil && flag&os.O_WRONLY == 0 {
		win.editor.Call(func() { f.Data = []byte(read()) })
	}
	return f
}

func (f *winFile) WriteAt(p []byte, off int64) (int, error) {
	if f.write == nil {
		return 0, os.ErrPermission
	}
	return f.ReadWriteFile.WriteAt(p, off)
}

func (f *winFile) Close() error {
	if f.Writes != nil {
		s := string(f.Writes)
		f.win.editor.Call(func() { f.write(s) })
	}
	return nil
}

// ctlText is what /<id>/ctl reads:
// "<id> <taglen> <bodylen> <isdir> <isdirty> <width> terminal <maxtab>\n"
// All lengths are rune counts; width is terminal columns.
func ctlText(win *Window) string {
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
	return fmt.Sprintf("%d %d %d %d %d %d terminal %d\n",
		win.ID, win.tag.buffer.Len(), win.body.GetBuffer().Len(), isDir, isDirty, win.w-1, maxtab)
}

// winCtlFile runs each write as an editor command, as it comes.
type winCtlFile struct{ *winFile }

func (f *winCtlFile) WriteAt(p []byte, _ int64) (int, error) {
	cmd := strings.TrimSpace(string(p))
	if cmd == "" {
		return len(p), nil
	}
	win, col := f.win, f.win.parent
	win.editor.callCh <- func() { win.editor.Execute(col, win, cmd) }
	return len(p), nil
}
