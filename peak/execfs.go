package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aleksana/peak/internal/vfs/afero"
	"github.com/gdamore/tcell/v2"
)

// peakSrvFs is the afero.Fs handed to the 9P server. It embeds the
// BasePathFs-over-composite that handles all real file operations, and
// additionally implements vfs.WalkRedirector by delegating directly to
// peakNamespaceFs — whose namespace root matches the 9P namespace root.
type peakSrvFs struct {
	afero.Fs
	nsFs *peakNamespaceFs
}

func (fs *peakSrvFs) WalkRedirect(dir, name string) (string, os.FileInfo, bool) {
	return fs.nsFs.WalkRedirect(dir, name)
}

// peakNamespaceFs wraps the 9P-served peak namespace (BasePathFs over the
// composite VFS) to add virtual control files. Without this wrapper the
// composite mount mechanism would create them as directories.
type peakNamespaceFs struct {
	inner  afero.Fs
	editor *Editor
	bus    *globalEventBus
	srvReg *srvRegistry
}

func newPeakNamespaceFs(inner afero.Fs, editor *Editor, bus *globalEventBus) *peakNamespaceFs {
	return &peakNamespaceFs{inner: inner, editor: editor, bus: bus, srvReg: newSrvRegistry()}
}

func (fs *peakNamespaceFs) Stat(name string) (os.FileInfo, error) {
	s := trimSlash(name)
	switch s {
	case "exec":
		return &simpleFileInfo{name: "exec", mode: 0600}, nil
	case "event":
		return &simpleFileInfo{name: "event", mode: 0444}, nil
	case "mount":
		return &simpleFileInfo{name: "mount", mode: 0600}, nil
	case "unmount":
		return &simpleFileInfo{name: "unmount", mode: 0200}, nil
	case "bind":
		return &simpleFileInfo{name: "bind", mode: 0600}, nil
	case "new":
		return &simpleFileInfo{name: "new", isDir: true, mode: 0555}, nil
	case "srv":
		return &simpleFileInfo{name: "srv", isDir: true, mode: 0555}, nil
	}
	if strings.HasPrefix(s, "srv/") {
		sname := s[4:]
		if sname != "" {
			return &simpleFileInfo{name: sname, mode: 0600}, nil
		}
	}
	return fs.inner.Stat(name)
}

// WalkRedirect implements vfs.WalkRedirector. Walking "new" from the root
// creates a fresh text window and redirects the fid to that window's directory,
// matching acme's /acme/new semantics.
func (fs *peakNamespaceFs) WalkRedirect(dir, name string) (string, os.FileInfo, bool) {
	if (dir == "" || dir == "/") && name == "new" {
		var win *Window
		fs.editor.Call(func() {
			col := fs.editor.getTargetColumn(nil, nil)
			if col == nil {
				return
			}
			win = col.AddWindow(" New ", "")
			fs.editor.ActivateWindow(win)
			col.Resize(col.x, col.y, col.w, col.h)
		})
		if win == nil {
			return "", nil, false
		}
		id := strconv.Itoa(win.ID)
		return "/" + id, &simpleFileInfo{name: id, isDir: true, mode: 0555}, true
	}
	return "", nil, false
}

func (fs *peakNamespaceFs) Open(name string) (afero.File, error) {
	return fs.OpenFile(name, os.O_RDONLY, 0)
}

func (fs *peakNamespaceFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	s := trimSlash(name)
	switch s {
	case "exec":
		return &execFile{editor: fs.editor}, nil
	case "event":
		sub := fs.bus.subscribe()
		return &globalEventFile{bus: fs.bus, sub: sub}, nil
	case "mount":
		return &mountFile{editor: fs.editor, data: []byte(fs.editor.ninep.ListMounts())}, nil
	case "unmount":
		return &unmountFile{editor: fs.editor}, nil
	case "bind":
		return &bindFile{editor: fs.editor, data: []byte(fs.editor.ninep.ListBinds())}, nil
	case "srv", "srv/":
		return &srvDirFile{reg: fs.srvReg}, nil
	case "", ".":
		f, err := fs.inner.OpenFile(name, flag, perm)
		if err != nil {
			return nil, err
		}
		return &peakRootDirFile{File: f}, nil
	default:
		if strings.HasPrefix(s, "srv/") {
			sname := s[4:]
			if sname == "" {
				return &srvDirFile{reg: fs.srvReg}, nil
			}
			if flag&os.O_RDWR != 0 {
				sock, err := fs.srvReg.create(sname)
				if err != nil {
					return nil, err
				}
				return &srvServerFile{name: sname, conn: sock.server, reg: fs.srvReg}, nil
			}
			return nil, os.ErrPermission
		}
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
	virtual := map[string]bool{"exec": true, "event": true, "mount": true, "unmount": true, "bind": true, "new": true, "srv": true}
	filtered := entries[:0]
	for _, e := range entries {
		if !virtual[e.Name()] {
			filtered = append(filtered, e)
		}
	}
	filtered = append(filtered,
		&simpleFileInfo{name: "exec", mode: 0600},
		&simpleFileInfo{name: "event", mode: 0444},
		&simpleFileInfo{name: "mount", mode: 0600},
		&simpleFileInfo{name: "unmount", mode: 0200},
		&simpleFileInfo{name: "bind", mode: 0600},
		&simpleFileInfo{name: "new", isDir: true, mode: 0555},
		&simpleFileInfo{name: "srv", isDir: true, mode: 0555},
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

// ---- mountFile ----

// mountFile implements /mount: write "<socket-path> <mount-path>\n" to mount a
// 9P server (real Unix socket or virtual /srv entry) into peak's composite VFS.
// Reading returns the current mount table as "src dst\n" lines.
type mountFile struct {
	winStub
	editor *Editor
	data   []byte
	off    int64
}

func (f *mountFile) Name() string { return "mount" }
func (f *mountFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: "mount", mode: 0600}, nil
}

func (f *mountFile) WriteAt(p []byte, _ int64) (int, error) {
	parts := strings.Fields(strings.TrimSpace(string(p)))
	if len(parts) < 2 {
		return len(p), nil
	}
	resolvedSrc, err := f.editor.ninep.Mount(parts[0], parts[1])
	if err != nil {
		return 0, err
	}
	f.editor.ninep.record(&f.editor.ninep.mounts, resolvedSrc, resolvePath(parts[1]))
	return len(p), nil
}

func (f *mountFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[off:])
	if off+int64(n) >= int64(len(f.data)) {
		return n, io.EOF
	}
	return n, nil
}

func (f *mountFile) Read(p []byte) (int, error) {
	n, err := f.ReadAt(p, f.off)
	f.off += int64(n)
	return n, err
}

func (f *mountFile) Write(p []byte) (int, error)       { return f.WriteAt(p, 0) }
func (f *mountFile) WriteString(s string) (int, error) { return f.WriteAt([]byte(s), 0) }

// ---- unmountFile ----

// unmountFile implements /unmount: write a path to detach it from the VFS.
type unmountFile struct {
	winStub
	editor *Editor
}

func (f *unmountFile) Name() string { return "unmount" }
func (f *unmountFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: "unmount", mode: 0200}, nil
}

func (f *unmountFile) WriteAt(p []byte, _ int64) (int, error) {
	if path := strings.TrimSpace(string(p)); path != "" {
		f.editor.ninep.Umount(path)
	}
	return len(p), nil
}

func (f *unmountFile) Write(p []byte) (int, error)       { return f.WriteAt(p, 0) }
func (f *unmountFile) WriteString(s string) (int, error) { return f.WriteAt([]byte(s), 0) }

// ---- bindFile ----

// bindFile implements /bind: write "<src> <dst>\n" to overlay a local path onto
// another path in peak's VFS (Plan 9-style namespace bind).
// Reading returns the current mount table as "src dst\n" lines.
type bindFile struct {
	winStub
	editor *Editor
	data   []byte
	off    int64
}

func (f *bindFile) Name() string { return "bind" }
func (f *bindFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: "bind", mode: 0600}, nil
}

func (f *bindFile) WriteAt(p []byte, _ int64) (int, error) {
	parts := strings.Fields(strings.TrimSpace(string(p)))
	if len(parts) < 2 {
		return len(p), nil
	}
	if err := f.editor.ninep.Bind(parts[0], parts[1]); err != nil {
		return 0, err
	}
	f.editor.ninep.record(&f.editor.ninep.binds, resolvePath(parts[0]), resolvePath(parts[1]))
	return len(p), nil
}

func (f *bindFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[off:])
	if off+int64(n) >= int64(len(f.data)) {
		return n, io.EOF
	}
	return n, nil
}

func (f *bindFile) Read(p []byte) (int, error) {
	n, err := f.ReadAt(p, f.off)
	f.off += int64(n)
	return n, err
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

// ---- /srv virtual socket registry ----

// pipeConn is one end of a virtual socketpair backed by two io.Pipes.
type pipeConn struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (c *pipeConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *pipeConn) Write(p []byte) (int, error) { return c.w.Write(p) }
func (c *pipeConn) Close() error {
	c.r.Close()
	c.w.Close()
	return nil
}

type srvSocket struct {
	server *pipeConn // peak-git reads requests here, writes responses here
	client *pipeConn // peak writes requests here, reads responses here; nil once taken
}

// srvRegistry tracks virtual sockets posted under /srv.
type srvRegistry struct {
	mu      sync.Mutex
	sockets map[string]*srvSocket
}

func newSrvRegistry() *srvRegistry {
	return &srvRegistry{sockets: make(map[string]*srvSocket)}
}

func (r *srvRegistry) create(name string) (*srvSocket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sockets[name]; ok {
		return nil, os.ErrExist
	}
	r1, w1 := io.Pipe() // peak → server
	r2, w2 := io.Pipe() // server → peak
	sock := &srvSocket{
		server: &pipeConn{r: r1, w: w2},
		client: &pipeConn{r: r2, w: w1},
	}
	r.sockets[name] = sock
	return sock, nil
}

// takeClient retrieves and removes the client end so peak can mount it.
func (r *srvRegistry) takeClient(name string) (*pipeConn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sock, ok := r.sockets[name]
	if !ok {
		return nil, fmt.Errorf("srv: %s not found", name)
	}
	if sock.client == nil {
		return nil, fmt.Errorf("srv: %s client already taken", name)
	}
	c := sock.client
	sock.client = nil
	return c, nil
}

func (r *srvRegistry) remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sockets, name)
}

// openSocket returns the client end of a virtual socket at path, or an error
// if path does not name a registered virtual socket. The caller is responsible
// for closing the returned conn on error from the subsequent mount.
func (fs *peakNamespaceFs) openSocket(path string) (io.ReadWriteCloser, error) {
	s := trimSlash(path)
	if strings.HasPrefix(s, "srv/") {
		name := s[4:]
		if name != "" {
			return fs.srvReg.takeClient(name)
		}
	}
	return nil, os.ErrInvalid
}

func (r *srvRegistry) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.sockets))
	for n := range r.sockets {
		names = append(names, n)
	}
	return names
}

// srvServerFile is the afero.File returned to the posting process (peak-git).
type srvServerFile struct {
	winStub
	name string
	conn *pipeConn
	reg  *srvRegistry
}

func (f *srvServerFile) Name() string { return f.name }
func (f *srvServerFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: f.name, mode: 0600}, nil
}
func (f *srvServerFile) Read(p []byte) (int, error)              { return f.conn.Read(p) }
func (f *srvServerFile) ReadAt(p []byte, _ int64) (int, error)   { return f.conn.Read(p) }
func (f *srvServerFile) Write(p []byte) (int, error)             { return f.conn.Write(p) }
func (f *srvServerFile) WriteAt(p []byte, _ int64) (int, error)  { return f.conn.Write(p) }
func (f *srvServerFile) WriteString(s string) (int, error)       { return f.conn.Write([]byte(s)) }
func (f *srvServerFile) Close() error {
	f.conn.Close()
	f.reg.remove(f.name)
	return nil
}

// srvDirFile serves the /srv directory listing.
type srvDirFile struct {
	winStub
	reg     *srvRegistry
	entries []os.FileInfo
	offset  int
}

func (f *srvDirFile) Name() string { return "srv" }
func (f *srvDirFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: "srv", isDir: true, mode: 0555}, nil
}
func (f *srvDirFile) Readdir(count int) ([]os.FileInfo, error) {
	if f.entries == nil {
		for _, name := range f.reg.list() {
			f.entries = append(f.entries, &simpleFileInfo{name: name, mode: 0600})
		}
	}
	if count <= 0 {
		return f.entries, nil
	}
	if f.offset >= len(f.entries) {
		return nil, io.EOF
	}
	end := f.offset + count
	if end > len(f.entries) {
		end = len(f.entries)
	}
	res := f.entries[f.offset:end]
	f.offset = end
	return res, nil
}
func (f *srvDirFile) Readdirnames(n int) ([]string, error) {
	infos, err := f.Readdir(n)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(infos))
	for i, info := range infos {
		names[i] = info.Name()
	}
	return names, nil
}
