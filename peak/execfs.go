package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aleksana/peak/internal/vfs/afero"
	"github.com/gdamore/tcell/v2"
)

// peakNamespaceFs wraps the 9P-served peak namespace (BasePathFs over the
// composite VFS) to add /exec, /event, and /bind as virtual files. Without
// this wrapper the composite mount mechanism would create them as directories.
type peakNamespaceFs struct {
	inner  afero.Fs
	editor *Editor
	bus    *globalEventBus
}

func newPeakNamespaceFs(inner afero.Fs, editor *Editor, bus *globalEventBus) *peakNamespaceFs {
	return &peakNamespaceFs{inner: inner, editor: editor, bus: bus}
}

func (fs *peakNamespaceFs) Stat(name string) (os.FileInfo, error) {
	switch trimSlash(name) {
	case "exec":
		return &simpleFileInfo{name: "exec", mode: 0600}, nil
	case "event":
		return &simpleFileInfo{name: "event", mode: 0444}, nil
	case "bind":
		return &simpleFileInfo{name: "bind", mode: 0200}, nil
	}
	return fs.inner.Stat(name)
}

func (fs *peakNamespaceFs) Open(name string) (afero.File, error) {
	return fs.OpenFile(name, os.O_RDONLY, 0)
}

func (fs *peakNamespaceFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	switch trimSlash(name) {
	case "exec":
		return &execFile{editor: fs.editor}, nil
	case "event":
		sub := fs.bus.subscribe()
		return &globalEventFile{bus: fs.bus, sub: sub}, nil
	case "bind":
		return &bindFile{editor: fs.editor}, nil
	case "", ".":
		f, err := fs.inner.OpenFile(name, flag, perm)
		if err != nil {
			return nil, err
		}
		return &peakRootDirFile{File: f}, nil
	default:
		return fs.inner.OpenFile(name, flag, perm)
	}
}

func (fs *peakNamespaceFs) Create(n string) (afero.File, error)    { return fs.inner.Create(n) }
func (fs *peakNamespaceFs) Mkdir(n string, p os.FileMode) error    { return fs.inner.Mkdir(n, p) }
func (fs *peakNamespaceFs) MkdirAll(n string, p os.FileMode) error { return fs.inner.MkdirAll(n, p) }
func (fs *peakNamespaceFs) Remove(n string) error                  { return fs.inner.Remove(n) }
func (fs *peakNamespaceFs) RemoveAll(n string) error               { return fs.inner.RemoveAll(n) }
func (fs *peakNamespaceFs) Rename(o, n string) error               { return fs.inner.Rename(o, n) }
func (fs *peakNamespaceFs) Chmod(n string, m os.FileMode) error    { return fs.inner.Chmod(n, m) }
func (fs *peakNamespaceFs) Chown(n string, u, g int) error         { return fs.inner.Chown(n, u, g) }
func (fs *peakNamespaceFs) Chtimes(n string, a, m time.Time) error { return fs.inner.Chtimes(n, a, m) }
func (fs *peakNamespaceFs) Name() string                           { return "peakNamespaceFs" }

// peakRootDirFile replaces virtual file directory entries (created by Mount's
// MkdirAll) with regular file entries in directory listings.
type peakRootDirFile struct{ afero.File }

func (f *peakRootDirFile) Readdir(count int) ([]os.FileInfo, error) {
	entries, err := f.File.Readdir(count)
	if count > 0 {
		return entries, err
	}
	virtual := map[string]bool{"exec": true, "event": true, "bind": true}
	filtered := entries[:0]
	for _, e := range entries {
		if !virtual[e.Name()] {
			filtered = append(filtered, e)
		}
	}
	filtered = append(filtered,
		&simpleFileInfo{name: "exec", mode: 0600},
		&simpleFileInfo{name: "event", mode: 0444},
		&simpleFileInfo{name: "bind", mode: 0200},
	)
	return filtered, err
}

// ---- globalEventFile ----

// globalEventFile is a blocking-read stream of editor lifecycle events.
// Each open of /event creates an independent subscriber.
type globalEventFile struct {
	winStub
	bus *globalEventBus
	sub *eventSub
}

func (f *globalEventFile) Name() string { return "event" }
func (f *globalEventFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: "event", mode: 0444}, nil
}
func (f *globalEventFile) ReadAt(p []byte, off int64) (int, error) {
	return f.sub.readAt(p, off)
}
func (f *globalEventFile) Close() error {
	f.bus.unsubscribe(f.sub)
	f.sub.close()
	return nil
}

// ---- bindFile ----

// bindFile implements /bind: write "<socket-path> <mount-path>\n" to mount an
// external 9P server into peak's composite VFS at the given path.
type bindFile struct {
	winStub
	editor *Editor
}

func (f *bindFile) Name() string { return "bind" }
func (f *bindFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: "bind", mode: 0200}, nil
}

func (f *bindFile) WriteAt(p []byte, _ int64) (int, error) {
	parts := strings.Fields(strings.TrimSpace(string(p)))
	if len(parts) < 2 {
		return len(p), nil
	}
	if err := f.editor.ninep.Mount(parts[0], parts[1]); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (f *bindFile) Write(p []byte) (int, error)       { return f.WriteAt(p, 0) }
func (f *bindFile) WriteString(s string) (int, error) { return f.WriteAt([]byte(s), 0) }

// execFile implements /exec: write a window title to create an externally-driven
// terminal window; read back the numeric window ID followed by a newline.
type execFile struct {
	winStub
	editor  *Editor
	written bool
	resp    []byte
}

func (f *execFile) Name() string { return "exec" }
func (f *execFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: "exec", mode: 0600}, nil
}

func (f *execFile) WriteAt(p []byte, _ int64) (int, error) {
	if f.written {
		return 0, os.ErrPermission
	}
	title := strings.TrimSpace(string(p))

	reply := make(chan int, 1)
	f.editor.screen.PostEvent(tcell.NewEventInterrupt(func() {
		pty := newExternalPTY()
		col := f.editor.getTargetColumn(nil, nil)
		if col == nil {
			reply <- -1
			return
		}
		newWin, err := col.AddSessionTermWindow(title, pty)
		if err != nil {
			reply <- -1
			return
		}
		f.editor.ActivateWindow(newWin)
		col.Resize(col.x, col.y, col.w, col.h)
		reply <- newWin.ID
	}))

	id := <-reply
	if id < 0 {
		return 0, fmt.Errorf("exec: failed to create terminal window")
	}
	f.written = true
	f.resp = []byte(fmt.Sprintf("%d\n", id))
	return len(p), nil
}

func (f *execFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(f.resp)) {
		return 0, io.EOF
	}
	n := copy(p, f.resp[off:])
	if off+int64(n) >= int64(len(f.resp)) {
		return n, io.EOF
	}
	return n, nil
}

func (f *execFile) Write(p []byte) (int, error)       { return f.WriteAt(p, 0) }
func (f *execFile) WriteString(s string) (int, error) { return f.WriteAt([]byte(s), 0) }
