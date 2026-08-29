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

	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
)

// peakNamespaceFs is the virtual file server for the /peak control files.
// It embeds NamespaceFs so Stat/OpenFile/Readdir are derived from the entry
// table. The only additional behaviour is WalkRedirect (for /peak/new).
type peakNamespaceFs struct {
	*vfs.NamespaceFs
	editor *Editor
	srvReg *srvRegistry
}

func newPeakNamespaceFs(editor *Editor, bus *globalEventBus) *peakNamespaceFs {
	srvReg := newSrvRegistry()
	return &peakNamespaceFs{
		editor: editor,
		srvReg: srvReg,
		NamespaceFs: &vfs.NamespaceFs{
			RootName: "peak",
			Entries: []vfs.FileEntry{
				{Name: "event", Mode: 0444, Open: func(_ int) (afero.File, error) {
					sub := bus.subscribe()
					return &globalEventFile{bus: bus, sub: sub}, nil
				}},
				{Name: "index", Mode: 0444, Open: func(_ int) (afero.File, error) {
					return &indexFile{ReadonlyFile: vfs.ReadonlyFile{Data: indexSnap(editor)}}, nil
				}},
				{Name: "mount", Mode: 0600, Open: func(_ int) (afero.File, error) {
					return &mountFile{editor: editor, ReadonlyFile: vfs.ReadonlyFile{Data: []byte(editor.ninep.ListMounts())}}, nil
				}},
				{Name: "unmount", Mode: 0200, Open: func(_ int) (afero.File, error) {
					return &unmountFile{editor: editor}, nil
				}},
				{Name: "bind", Mode: 0600, Open: func(_ int) (afero.File, error) {
					return &bindFile{editor: editor, ReadonlyFile: vfs.ReadonlyFile{Data: []byte(editor.ninep.ListBinds())}}, nil
				}},
				// "new" is intercepted by WalkRedirect; direct open is not supported.
				{Name: "new", Mode: 0555, IsDir: true},
				{Name: "srv", Mode: 0555, IsDir: true,
					Open:      func(_ int) (afero.File, error) { return &srvDirFile{reg: srvReg}, nil },
					ChildMode: 0600,
					OpenChild: func(child string, flag int) (afero.File, error) {
						if flag&os.O_RDWR != 0 {
							sock, serverRight, err := srvReg.create(child)
							if err != nil {
								return nil, err
							}
							return &srvServerFile{name: child, sock: sock, serverRight: serverRight, reg: srvReg}, nil
						}
						// O_RDONLY: Plan 9-style client connect. Dial the service and
						// return the client end of a fresh independent connection.
						rwc, err := srvReg.dial(context.Background(), child)
						if err != nil {
							return nil, err
						}
						return &srvConnFile{rwc: rwc}, nil
					},
				},
			},
		},
	}
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
		return "/" + id, vfs.NewFileInfo(id, 0555, true), true
	}
	return "", nil, false
}

// ---- globalEventFile ----

// globalEventFile is a blocking-read stream of editor lifecycle events.
// Each open of /event creates an independent subscriber.
type globalEventFile struct {
	vfs.FileStub
	bus *globalEventBus
	sub *eventSub
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
	vfs.ReadonlyFile
	editor *Editor
	conn   vfs.ConnCleaner // set by 9P server on open; nil for in-process callers
}

func (f *mountFile) SetConn(c vfs.ConnCleaner) { f.conn = c }

func (f *mountFile) WriteAt(p []byte, _ int64) (int, error) {
	parts := strings.Fields(strings.TrimSpace(string(p)))
	if len(parts) < 2 {
		return len(p), nil
	}
	mountedPath, err := f.editor.ninep.Mount(parts[0], parts[1])
	if err != nil {
		return 0, err
	}
	if f.conn != nil {
		f.conn.RegisterCleanup(func() { f.editor.ninep.Umount(mountedPath) })
	}
	f.editor.ninep.record(&f.editor.ninep.mounts, parts[0], mountedPath)
	return len(p), nil
}

func (f *mountFile) Write(p []byte) (int, error)       { return f.WriteAt(p, 0) }
func (f *mountFile) WriteString(s string) (int, error) { return f.WriteAt([]byte(s), 0) }

// ---- unmountFile ----

// unmountFile implements /unmount: write a path to detach it from the VFS.
type unmountFile struct {
	vfs.FileStub
	editor *Editor
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
// Reading returns the current bind table as "src dst\n" lines.
type bindFile struct {
	vfs.ReadonlyFile
	editor *Editor
}

func (f *bindFile) WriteAt(p []byte, _ int64) (int, error) {
	parts := strings.Fields(strings.TrimSpace(string(p)))
	if len(parts) < 2 {
		return len(p), nil
	}
	if err := f.editor.ninep.Bind(parts[0], parts[1]); err != nil {
		return 0, err
	}
	f.editor.ninep.record(&f.editor.ninep.binds, normalizePath(parts[0], ""), normalizePath(parts[1], ""))
	return len(p), nil
}

func (f *bindFile) Write(p []byte) (int, error)       { return f.WriteAt(p, 0) }
func (f *bindFile) WriteString(s string) (int, error) { return f.WriteAt([]byte(s), 0) }

// indexSnap builds the /peak/index payload. Each open window produces one line:
//
//	%11d %11d %11d %11d %11d <tag>\n
//	id    taglen bodylen isdir isdirty
//
// This matches acme's /acme/index format, making existing acme scripts portable.
func indexSnap(editor *Editor) []byte {
	var sb strings.Builder
	editor.Call(func() {
		for _, win := range editor.allWindows() {
			win.lk.Lock()
			tagLen := win.tag.buffer.Len()
			bodyLen := win.body.GetBuffer().Len()
			isDir, isDirty := 0, 0
			if win.kind == WinDir {
				isDir = 1
			}
			if win.IsDirty() {
				isDirty = 1
			}
			tag := win.tag.buffer.GetText()
			win.lk.Unlock()
			if i := strings.IndexByte(tag, '\n'); i >= 0 {
				tag = tag[:i]
			}
			fmt.Fprintf(&sb, "%11d %11d %11d %11d %11d %s\n",
				win.ID, tagLen, bodyLen, isDir, isDirty, tag)
		}
	})
	return []byte(sb.String())
}

// indexFile is the read-only /peak/index snapshot.
type indexFile struct {
	vfs.ReadonlyFile
}

// ---- /srv virtual socket registry ----

// srvSocket is the server side of a virtual /srv entry. The mux is created
// eagerly so clients can dial immediately. serverRight is owned by the
// srvServerFile, not the registry.
type srvSocket struct {
	mux       *vfs.NinePMux
	closeOnce sync.Once
}

// srvRegistry tracks virtual sockets posted under /srv.
type srvRegistry struct {
	mu      sync.Mutex
	sockets map[string]*srvSocket
}

func newSrvRegistry() *srvRegistry {
	return &srvRegistry{sockets: make(map[string]*srvSocket)}
}

func (r *srvRegistry) create(name string) (*srvSocket, net.Conn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sockets[name]; ok {
		return nil, nil, os.ErrExist
	}
	serverLeft, serverRight := net.Pipe()
	m := vfs.NewNinePMux(serverLeft)
	go m.Serve()
	sock := &srvSocket{mux: m}
	r.sockets[name] = sock
	return sock, serverRight, nil
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
	vfs.FileStub
	name        string
	sock        *srvSocket
	serverRight net.Conn
	reg         *srvRegistry
}

func (f *srvServerFile) Read(p []byte) (int, error)             { return f.serverRight.Read(p) }
func (f *srvServerFile) ReadAt(p []byte, _ int64) (int, error)  { return f.serverRight.Read(p) }
func (f *srvServerFile) Write(p []byte) (int, error)            { return f.serverRight.Write(p) }
func (f *srvServerFile) WriteAt(p []byte, _ int64) (int, error) { return f.serverRight.Write(p) }

func (f *srvServerFile) Close() error {
	f.sock.closeOnce.Do(func() {
		f.sock.mux.Close()
		f.serverRight.Close()
	})
	f.reg.remove(f.name)
	return nil
}

// srvDirFile serves the /srv directory listing.
type srvDirFile struct {
	vfs.FileStub
	reg     *srvRegistry
	entries []os.FileInfo
	offset  int
}

func (f *srvDirFile) Readdir(count int) ([]os.FileInfo, error) {
	if f.entries == nil {
		for _, name := range f.reg.list() {
			f.entries = append(f.entries, vfs.NewFileInfo(name, 0600, false))
		}
	}
	if count <= 0 {
		return f.entries, nil
	}
	if f.offset >= len(f.entries) {
		return nil, io.EOF
	}
	end := min(f.offset+count, len(f.entries))
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
	vfs.FileStub
	rwc io.ReadWriteCloser
}

func (f *srvConnFile) Read(p []byte) (int, error)             { return f.rwc.Read(p) }
func (f *srvConnFile) ReadAt(p []byte, _ int64) (int, error)  { return f.rwc.Read(p) }
func (f *srvConnFile) Write(p []byte) (int, error)            { return f.rwc.Write(p) }
func (f *srvConnFile) WriteAt(p []byte, _ int64) (int, error) { return f.rwc.Write(p) }
func (f *srvConnFile) WriteString(s string) (int, error)      { return f.rwc.Write([]byte(s)) }
func (f *srvConnFile) Close() error                           { return f.rwc.Close() }
