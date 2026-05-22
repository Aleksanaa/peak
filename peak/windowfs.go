package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aleksana/peak/internal/vfs/afero"
)

// windowFs implements afero.Fs for a single window's /peak/<id>/ directory.
// Files: body (rw), tag (rw), ctl (wo).
type windowFs struct{ win *Window }

func (fs *windowFs) Stat(name string) (os.FileInfo, error) {
	switch trimSlash(name) {
	case "", ".":
		return &simpleFileInfo{name: ".", isDir: true, mode: 0555}, nil
	case "body":
		var size int64
		if tv, ok := fs.win.body.(*TermView); ok {
			size = int64(len(tv.GetScrollback()))
		} else {
			fs.win.editor.Call(func() {
				if buf := fs.win.body.GetBuffer(); buf != nil {
					size = int64(len(buf.GetText()))
				}
			})
		}
		return &simpleFileInfo{name: "body", mode: 0644, size: size}, nil
	case "tag":
		var size int64
		fs.win.editor.Call(func() {
			size = int64(len(fs.win.tag.buffer.GetText()))
		})
		return &simpleFileInfo{name: "tag", mode: 0644, size: size}, nil
	case "ctl":
		return &simpleFileInfo{name: "ctl", mode: 0200}, nil
	case "event":
		return &simpleFileInfo{name: "event", mode: 0644}, nil
	case "addr":
		var snap []byte
		fs.win.editor.Call(func() {
			snap = []byte(fmt.Sprintf("#%d,#%d\n", fs.win.addrQ0, fs.win.addrQ1))
		})
		return &simpleFileInfo{name: "addr", mode: 0644, size: int64(len(snap))}, nil
	case "data":
		var size int64
		fs.win.editor.Call(func() {
			if buf := fs.win.body.GetBuffer(); buf != nil {
				runes := buf.RunesInRange(fs.win.addrQ0, fs.win.addrQ1)
				size = int64(len([]byte(string(runes))))
			}
		})
		return &simpleFileInfo{name: "data", mode: 0644, size: size}, nil
	case "color":
		return &simpleFileInfo{name: "color", mode: 0200}, nil
	case "io":
		if tv, ok := fs.win.body.(*TermView); ok {
			if tv.externalPTY() != nil {
				return &simpleFileInfo{name: "io", mode: 0600}, nil
			}
		}
	}
	return nil, os.ErrNotExist
}

func (fs *windowFs) Open(name string) (afero.File, error) {
	return fs.OpenFile(name, os.O_RDONLY, 0)
}

func (fs *windowFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	switch trimSlash(name) {
	case "", ".":
		return &winDirFile{win: fs.win}, nil
	case "body":
		f := &winBodyFile{win: fs.win}
		if flag&os.O_WRONLY == 0 {
			if tv, ok := fs.win.body.(*TermView); ok {
				f.snap = []byte(tv.GetScrollback())
			} else {
				fs.win.editor.Call(func() {
					if buf := fs.win.body.GetBuffer(); buf != nil {
						f.snap = []byte(buf.GetText())
					}
				})
			}
		}
		return f, nil
	case "tag":
		f := &winTagFile{win: fs.win}
		if flag&os.O_WRONLY == 0 {
			fs.win.editor.Call(func() {
				f.snap = []byte(fs.win.tag.buffer.GetText())
			})
		}
		return f, nil
	case "ctl":
		return &winCtlFile{win: fs.win}, nil
	case "event":
		sub := fs.win.subscribeEvent()
		return &winEventFile{win: fs.win, sub: sub}, nil
	case "addr":
		f := &winAddrFile{win: fs.win}
		if flag&os.O_WRONLY == 0 {
			fs.win.editor.Call(func() {
				f.snap = []byte(fmt.Sprintf("#%d,#%d\n", fs.win.addrQ0, fs.win.addrQ1))
			})
		}
		return f, nil
	case "data":
		f := &winDataFile{win: fs.win}
		if flag&os.O_WRONLY == 0 {
			fs.win.editor.Call(func() {
				if buf := fs.win.body.GetBuffer(); buf != nil {
					runes := buf.RunesInRange(fs.win.addrQ0, fs.win.addrQ1)
					f.snap = []byte(string(runes))
				}
			})
		}
		return f, nil
	case "color":
		return &winColorFile{win: fs.win}, nil
	case "io":
		if tv, ok := fs.win.body.(*TermView); ok {
			if pty := tv.externalPTY(); pty != nil {
				return &winIoFile{pty: pty}, nil
			}
		}
	}
	return nil, os.ErrNotExist
}

// trimSlash strips leading slashes for name lookup.
func trimSlash(name string) string {
	return strings.TrimLeft(name, "/")
}

// Unsupported mutations.
func (fs *windowFs) Create(name string) (afero.File, error)       { return nil, os.ErrPermission }
func (fs *windowFs) Mkdir(name string, perm os.FileMode) error    { return os.ErrPermission }
func (fs *windowFs) MkdirAll(path string, perm os.FileMode) error { return os.ErrPermission }
func (fs *windowFs) Remove(name string) error                     { return os.ErrPermission }
func (fs *windowFs) RemoveAll(path string) error                  { return os.ErrPermission }
func (fs *windowFs) Rename(oldname, newname string) error         { return os.ErrPermission }
func (fs *windowFs) Chmod(name string, mode os.FileMode) error    { return os.ErrPermission }
func (fs *windowFs) Chown(name string, uid, gid int) error        { return os.ErrPermission }
func (fs *windowFs) Chtimes(name string, a, m time.Time) error    { return os.ErrPermission }
func (fs *windowFs) Name() string                                 { return "windowFs" }

// ---- stub base ----

// winStub provides no-op implementations of the afero.File interface.
// Concrete types embed it and override only what they need.
type winStub struct{}

func (winStub) Close() error                              { return nil }
func (winStub) Read(p []byte) (int, error)                { return 0, io.EOF }
func (winStub) ReadAt(p []byte, off int64) (int, error)   { return 0, io.EOF }
func (winStub) Seek(off int64, whence int) (int64, error) { return 0, nil }
func (winStub) Write(p []byte) (int, error)               { return 0, os.ErrPermission }
func (winStub) WriteAt(p []byte, off int64) (int, error)  { return 0, os.ErrPermission }
func (winStub) WriteString(s string) (int, error)         { return 0, os.ErrPermission }
func (winStub) Name() string                              { return "" }
func (winStub) Readdir(n int) ([]os.FileInfo, error)      { return nil, nil }
func (winStub) Readdirnames(n int) ([]string, error)      { return nil, nil }
func (winStub) Stat() (os.FileInfo, error)                { return nil, os.ErrNotExist }
func (winStub) Sync() error                               { return nil }
func (winStub) Truncate(size int64) error                 { return nil }

// ---- directory ----

type winDirFile struct {
	winStub
	win *Window
}

func (f *winDirFile) Name() string { return "." }

func (f *winDirFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: ".", isDir: true, mode: 0555}, nil
}

func (f *winDirFile) Readdir(count int) ([]os.FileInfo, error) {
	all := []os.FileInfo{
		&simpleFileInfo{name: "body", mode: 0644},
		&simpleFileInfo{name: "tag", mode: 0644},
		&simpleFileInfo{name: "ctl", mode: 0200},
		&simpleFileInfo{name: "event", mode: 0644},
		&simpleFileInfo{name: "addr", mode: 0644},
		&simpleFileInfo{name: "data", mode: 0644},
		&simpleFileInfo{name: "color", mode: 0200},
	}
	if tv, ok := f.win.body.(*TermView); ok {
		if tv.externalPTY() != nil {
			all = append(all, &simpleFileInfo{name: "io", mode: 0600})
		}
	}
	if count > 0 && count < len(all) {
		return all[:count], nil
	}
	return all, nil
}

// ---- io file (ExternalPTY windows only) ----

type winIoFile struct {
	winStub
	pty *ExternalPTY
}

func (f *winIoFile) Name() string { return "io" }
func (f *winIoFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: "io", mode: 0600}, nil
}
func (f *winIoFile) ReadAt(p []byte, off int64) (int, error) {
	return f.pty.ReadInput(p, off)
}
func (f *winIoFile) WriteAt(p []byte, _ int64) (int, error) {
	return f.pty.WriteOutput(p)
}
func (f *winIoFile) Write(p []byte) (int, error)       { return f.WriteAt(p, 0) }
func (f *winIoFile) WriteString(s string) (int, error) { return f.WriteAt([]byte(s), 0) }
func (f *winIoFile) Close() error {
	f.pty.Close()
	return nil
}

// ---- body ----

type winBodyFile struct {
	winStub
	win    *Window
	snap   []byte // snapshot at open time
	writes []byte // accumulated write data (nil = no writes)
}

func (f *winBodyFile) Name() string { return "body" }

func (f *winBodyFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: "body", mode: 0644, size: int64(len(f.snap))}, nil
}

func (f *winBodyFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(f.snap)) {
		return 0, io.EOF
	}
	n := copy(p, f.snap[off:])
	if off+int64(n) >= int64(len(f.snap)) {
		return n, io.EOF
	}
	return n, nil
}

func (f *winBodyFile) WriteAt(p []byte, off int64) (int, error) {
	end := int(off) + len(p)
	if end > len(f.writes) {
		f.writes = append(f.writes, make([]byte, end-len(f.writes))...)
	}
	copy(f.writes[off:], p)
	return len(p), nil
}

func (f *winBodyFile) Write(p []byte) (int, error)       { return f.WriteAt(p, 0) }
func (f *winBodyFile) WriteString(s string) (int, error) { return f.WriteAt([]byte(s), 0) }

func (f *winBodyFile) Close() error {
	if f.writes == nil {
		return nil
	}
	if tv, ok := f.win.body.(*TermView); ok {
		tv.session.Write(f.writes)
		return nil
	}
	text := string(f.writes)
	f.win.editor.Call(func() {
		if buf := f.win.body.GetBuffer(); buf != nil {
			buf.SetText(text)
		}
	})
	return nil
}

// ---- tag ----

type winTagFile struct {
	winStub
	win    *Window
	snap   []byte
	writes []byte
}

func (f *winTagFile) Name() string { return "tag" }

func (f *winTagFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: "tag", mode: 0644, size: int64(len(f.snap))}, nil
}

func (f *winTagFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(f.snap)) {
		return 0, io.EOF
	}
	n := copy(p, f.snap[off:])
	if off+int64(n) >= int64(len(f.snap)) {
		return n, io.EOF
	}
	return n, nil
}

func (f *winTagFile) WriteAt(p []byte, off int64) (int, error) {
	end := int(off) + len(p)
	if end > len(f.writes) {
		f.writes = append(f.writes, make([]byte, end-len(f.writes))...)
	}
	copy(f.writes[off:], p)
	return len(p), nil
}

func (f *winTagFile) Write(p []byte) (int, error)       { return f.WriteAt(p, 0) }
func (f *winTagFile) WriteString(s string) (int, error) { return f.WriteAt([]byte(s), 0) }

func (f *winTagFile) Close() error {
	if f.writes == nil {
		return nil
	}
	text := string(f.writes)
	f.win.editor.Call(func() {
		f.win.tag.buffer.SetText(text)
	})
	return nil
}

// ---- ctl ----

type winCtlFile struct {
	winStub
	win *Window
}

func (f *winCtlFile) Name() string { return "ctl" }

func (f *winCtlFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: "ctl", mode: 0200}, nil
}

// WriteAt executes the trimmed string as an editor command.
func (f *winCtlFile) WriteAt(p []byte, off int64) (int, error) {
	cmd := strings.TrimSpace(string(p))
	if cmd == "" {
		return len(p), nil
	}
	f.win.editor.Call(func() {
		f.win.editor.Execute(f.win.parent, f.win, cmd)
	})
	return len(p), nil
}

func (f *winCtlFile) Write(p []byte) (int, error)       { return f.WriteAt(p, 0) }
func (f *winCtlFile) WriteString(s string) (int, error) { return f.WriteAt([]byte(s), 0) }

// ---- simpleFileInfo ----

type simpleFileInfo struct {
	name    string
	isDir   bool
	size    int64
	mode    os.FileMode
	modTime time.Time
}

func (s *simpleFileInfo) Name() string       { return s.name }
func (s *simpleFileInfo) Size() int64        { return s.size }
func (s *simpleFileInfo) IsDir() bool        { return s.isDir }
func (s *simpleFileInfo) ModTime() time.Time { return s.modTime }
func (s *simpleFileInfo) Sys() interface{}   { return nil }
func (s *simpleFileInfo) Mode() os.FileMode {
	mode := s.mode.Perm()
	if mode == 0 {
		if s.isDir {
			mode = 0755
		} else {
			mode = 0644
		}
	}
	if s.isDir {
		return os.ModeDir | mode
	}
	return mode
}
