package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
	"github.com/gdamore/tcell/v2"
)

// peakNamespaceFs is a pure virtual file server for the /peak control files.
// It serves only its own files; the composite VFS routes all other paths (window
// subdirectories, mounted services) to the appropriate file server before they
// reach nsFs. The 9P server exposes the complete /peak namespace via rootedFs,
// which serves nsFs control files through the composite's union-mount routing.
type peakNamespaceFs struct {
	editor *Editor
	bus    *globalEventBus
	srvReg *srvRegistry
}

func newPeakNamespaceFs(editor *Editor, bus *globalEventBus) *peakNamespaceFs {
	return &peakNamespaceFs{editor: editor, bus: bus, srvReg: newSrvRegistry()}
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
	case "", ".":
		return &simpleFileInfo{name: "peak", isDir: true, mode: 0555}, nil
	}
	if strings.HasPrefix(s, "srv/") {
		sname := s[4:]
		if sname != "" {
			return &simpleFileInfo{name: sname, mode: 0600}, nil
		}
	}
	return nil, os.ErrNotExist
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
			col.Reflow()
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
		return &peakRootDirFile{}, nil
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
				return &srvServerFile{name: sname, sock: sock, serverRight: sock.serverRight, reg: fs.srvReg}, nil
			}
			// O_RDONLY: Plan 9-style client connect. Dial the service and
			// return the client end of a fresh independent connection.
			rwc, err := fs.srvReg.dial(context.Background(), sname)
			if err != nil {
				return nil, err
			}
			return &srvConnFile{rwc: rwc, name: sname}, nil
		}
		return nil, os.ErrNotExist
	}
}

func (fs *peakNamespaceFs) Create(n string) (afero.File, error)    { return nil, os.ErrPermission }
func (fs *peakNamespaceFs) Mkdir(n string, p os.FileMode) error    { return os.ErrPermission }
func (fs *peakNamespaceFs) MkdirAll(n string, p os.FileMode) error { return os.ErrPermission }
func (fs *peakNamespaceFs) Remove(n string) error                  { return os.ErrPermission }
func (fs *peakNamespaceFs) RemoveAll(n string) error               { return os.ErrPermission }
func (fs *peakNamespaceFs) Rename(o, n string) error               { return os.ErrPermission }
func (fs *peakNamespaceFs) Chmod(n string, m os.FileMode) error    { return os.ErrPermission }
func (fs *peakNamespaceFs) Chown(n string, u, g int) error         { return os.ErrPermission }
func (fs *peakNamespaceFs) Chtimes(n string, a, m time.Time) error { return os.ErrPermission }
func (fs *peakNamespaceFs) Name() string                           { return "peakNamespaceFs" }

// peakRootDirFile lists the virtual control files served directly by nsFs.
// The composite's CompositeFile.Readdir merges these entries with window stubs
// (created in the composite's root MemMapFs by MkdirAll on each window mount),
// producing the complete /peak directory listing.
type peakRootDirFile struct {
	winStub
	offset int
}

var peakVirtualEntries = []os.FileInfo{
	&simpleFileInfo{name: "exec", mode: 0600},
	&simpleFileInfo{name: "event", mode: 0444},
	&simpleFileInfo{name: "mount", mode: 0600},
	&simpleFileInfo{name: "unmount", mode: 0200},
	&simpleFileInfo{name: "bind", mode: 0600},
	&simpleFileInfo{name: "new", isDir: true, mode: 0555},
	&simpleFileInfo{name: "srv", isDir: true, mode: 0555},
}

func (f *peakRootDirFile) Name() string { return "/" }
func (f *peakRootDirFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: "peak", isDir: true, mode: 0555}, nil
}
func (f *peakRootDirFile) Readdir(count int) ([]os.FileInfo, error) {
	if count <= 0 {
		return peakVirtualEntries, nil
	}
	if f.offset >= len(peakVirtualEntries) {
		return nil, io.EOF
	}
	end := f.offset + count
	if end > len(peakVirtualEntries) {
		end = len(peakVirtualEntries)
	}
	res := peakVirtualEntries[f.offset:end]
	f.offset = end
	return res, nil
}
func (f *peakRootDirFile) Readdirnames(n int) ([]string, error) {
	infos, err := f.Readdir(n)
	names := make([]string, len(infos))
	for i, fi := range infos {
		names[i] = fi.Name()
	}
	return names, err
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
	conn   vfs.ConnCleaner // set by 9P server on open; nil for in-process callers
}

func (f *mountFile) SetConn(c vfs.ConnCleaner) { f.conn = c }

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
	mountedPath := resolvePath(parts[1])
	if f.conn != nil {
		f.conn.RegisterCleanup(func() { f.editor.ninep.Umount(mountedPath) })
	}
	f.editor.ninep.record(&f.editor.ninep.mounts, resolvedSrc, mountedPath)
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
		col.Reflow()
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

// srvSocket is the server side of a virtual /srv entry. The mux and server
// pipe are created eagerly when the service posts (srvRegistry.create), so the
// service can start serving immediately and clients can dial at any time.
// done is closed by srvServerFile.Close, which also tears down the mux.
type srvSocket struct {
	mux         *vfs.NinePMux
	serverRight net.Conn
	done        chan struct{}
	closeOnce   sync.Once
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
	serverLeft, serverRight := net.Pipe()
	m := vfs.NewNinePMux(serverLeft)
	go m.Serve()
	sock := &srvSocket{
		mux:         m,
		serverRight: serverRight,
		done:        make(chan struct{}),
	}
	r.sockets[name] = sock
	return sock, nil
}

// dial returns a client connection to the service posted under name.
// All callers share the single server conversation via the mux.
func (r *srvRegistry) dial(ctx context.Context, name string) (io.ReadWriteCloser, error) {
	r.mu.Lock()
	sock, ok := r.sockets[name]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("srv: %s not found", name)
	}
	return sock.mux.Dial(ctx)
}

func (r *srvRegistry) remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sockets, name)
}

// openSocket dials the virtual socket at path and returns the client end of a
// fresh independent connection. Multiple calls each yield a separate session.
func (fs *peakNamespaceFs) openSocket(ctx context.Context, path string) (io.ReadWriteCloser, error) {
	s := trimSlash(path)
	if strings.HasPrefix(s, "srv/") {
		name := s[4:]
		if name != "" {
			return fs.srvReg.dial(ctx, name)
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

// srvServerFile is the afero.File returned to the posting service. It wraps
// the server end of the mux pipe and is used directly as an io.ReadWriteCloser
// by ServeConn.
type srvServerFile struct {
	winStub
	name        string
	sock        *srvSocket
	serverRight net.Conn
	reg         *srvRegistry
}

func (f *srvServerFile) Name() string { return f.name }
func (f *srvServerFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: f.name, mode: 0600}, nil
}
func (f *srvServerFile) Read(p []byte) (int, error)             { return f.serverRight.Read(p) }
func (f *srvServerFile) ReadAt(p []byte, _ int64) (int, error)  { return f.serverRight.Read(p) }
func (f *srvServerFile) Write(p []byte) (int, error)            { return f.serverRight.Write(p) }
func (f *srvServerFile) WriteAt(p []byte, _ int64) (int, error) { return f.serverRight.Write(p) }

func (f *srvServerFile) Close() error {
	f.sock.closeOnce.Do(func() {
		close(f.sock.done)
		f.sock.mux.Close()
		f.serverRight.Close()
	})
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

// srvConnFile wraps a client connection returned by O_RDONLY open of an /srv entry.
type srvConnFile struct {
	winStub
	rwc  io.ReadWriteCloser
	name string
}

func (f *srvConnFile) Name() string { return f.name }
func (f *srvConnFile) Stat() (os.FileInfo, error) {
	return &simpleFileInfo{name: f.name, mode: 0600}, nil
}
func (f *srvConnFile) Read(p []byte) (int, error)             { return f.rwc.Read(p) }
func (f *srvConnFile) ReadAt(p []byte, _ int64) (int, error)  { return f.rwc.Read(p) }
func (f *srvConnFile) Write(p []byte) (int, error)            { return f.rwc.Write(p) }
func (f *srvConnFile) WriteAt(p []byte, _ int64) (int, error) { return f.rwc.Write(p) }
func (f *srvConnFile) WriteString(s string) (int, error)      { return f.rwc.Write([]byte(s)) }
func (f *srvConnFile) Close() error                           { return f.rwc.Close() }
