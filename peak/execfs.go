package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/aleksana/peak/internal/quote"
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

func newPeakNamespaceFs(editor *Editor, bus *eventBus) *peakNamespaceFs {
	srvReg := newSrvRegistry()
	return &peakNamespaceFs{
		editor: editor,
		srvReg: srvReg,
		NamespaceFs: &vfs.NamespaceFs{
			RootName: "peak",
			Entries: []vfs.FileEntry{
				{Name: "event", Mode: 0444, Open: func(_ int) (afero.File, error) {
					return &globalEventFile{bus: bus, sub: bus.subscribe()}, nil
				}},
				{Name: "index", Mode: 0444, Open: func(_ int) (afero.File, error) {
					return &vfs.ReadonlyFile{Data: indexSnap(editor)}, nil
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
					Open:      func(_ int) (afero.File, error) { return srvReg.dir(), nil },
					ChildMode: 0600,
					// Opening an entry read-write posts a service under its name.
					OpenChild: func(child string, flag int) (afero.File, error) {
						if flag&os.O_RDWR == 0 {
							return nil, os.ErrPermission
						}
						return srvReg.post(child)
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
			win = fs.editor.getTargetColumn(nil, nil).AddWindow(" New ", "")
			fs.editor.showWindow(win)
		})
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
	bus *eventBus
	sub *eventSub
}

func (f *globalEventFile) ReadAt(p []byte, off int64) (int, error) {
	return f.sub.readAt(p, off)
}
func (f *globalEventFile) Close() error {
	f.bus.unsubscribe(f.sub)
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
	parts := quote.Fields(string(p))
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
	return len(p), nil
}

// ---- unmountFile ----

// unmountFile implements /unmount: write a path to detach it from the VFS.
type unmountFile struct {
	vfs.FileStub
	editor *Editor
}

func (f *unmountFile) WriteAt(p []byte, _ int64) (int, error) {
	if path, _ := quote.Cut(string(p)); path != "" {
		f.editor.ninep.Umount(path)
	}
	return len(p), nil
}

// ---- bindFile ----

// bindFile implements /bind: write "<src> <dst>\n" to overlay a local path onto
// another path in peak's VFS (Plan 9-style namespace bind).
// Reading returns the current bind table as "src dst\n" lines.
type bindFile struct {
	vfs.ReadonlyFile
	editor *Editor
}

func (f *bindFile) WriteAt(p []byte, _ int64) (int, error) {
	parts := quote.Fields(string(p))
	if len(parts) < 2 {
		return len(p), nil
	}
	if err := f.editor.ninep.Bind(parts[0], parts[1]); err != nil {
		return 0, err
	}
	return len(p), nil
}

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
			if i := strings.IndexByte(tag, '\n'); i >= 0 {
				tag = tag[:i]
			}
			fmt.Fprintf(&sb, "%11d %11d %11d %11d %11d %s\n",
				win.ID, tagLen, bodyLen, isDir, isDirty, tag)
		}
	})
	return []byte(sb.String())
}

// ---- /srv ----

// srvRegistry holds the 9P services posted under /srv. A service serves on
// one end of a pipe and peak holds the other, over which it has one 9P
// conversation with the service: every mount of the service shares it, as
// Plan 9's mount driver shares a posted channel.
type srvRegistry struct {
	mu       sync.Mutex
	services map[string]*service
}

type service struct {
	conn net.Conn // peak's end of the pipe
	once sync.Once
	fs   *vfs.NinePClientFs
	err  error
}

func newSrvRegistry() *srvRegistry {
	return &srvRegistry{services: make(map[string]*service)}
}

// post registers a service under name and returns the file it serves on.
func (r *srvRegistry) post(name string) (afero.File, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.services[name]; ok {
		return nil, os.ErrExist
	}
	ours, theirs := net.Pipe()
	s := &service{conn: ours}
	r.services[name] = s
	return &srvServerFile{name: name, conn: theirs, svc: s, reg: r}, nil
}

// fs returns peak's conversation with the service posted under name. The
// first call starts it: a service is mounted once it serves, so the
// handshake has someone to answer it.
func (r *srvRegistry) fs(name string) (*vfs.NinePClientFs, error) {
	r.mu.Lock()
	s, ok := r.services[name]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("srv: %s not found", name)
	}
	s.once.Do(func() { s.fs, s.err = vfs.NewNinePClientFsFromConn(s.conn) })
	return s.fs, s.err
}

// remove ends service s, posted under name, and with it peak's conversation.
// A service posted under the name since is another one, and stays.
func (r *srvRegistry) remove(name string, s *service) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.services[name] == s {
		delete(r.services, name)
	}
	s.conn.Close()
}

// srvServerFile is the file a service serves 9P on: its end of the pipe.
// Closing it ends the service; ServeConn closes it too, so it may be closed
// twice.
type srvServerFile struct {
	vfs.FileStub
	name string
	conn net.Conn
	svc  *service
	reg  *srvRegistry
}

func (f *srvServerFile) Read(p []byte) (int, error)             { return f.conn.Read(p) }
func (f *srvServerFile) ReadAt(p []byte, _ int64) (int, error)  { return f.conn.Read(p) }
func (f *srvServerFile) Write(p []byte) (int, error)            { return f.conn.Write(p) }
func (f *srvServerFile) WriteAt(p []byte, _ int64) (int, error) { return f.conn.Write(p) }

func (f *srvServerFile) Close() error {
	f.reg.remove(f.name, f.svc)
	return f.conn.Close()
}

// dir returns the /srv directory, listing the services posted now.
func (r *srvRegistry) dir() *vfs.DirFile {
	d := &vfs.DirFile{Info: vfs.NewFileInfo("srv", 0555, true)}
	r.mu.Lock()
	defer r.mu.Unlock()
	for name := range r.services {
		d.Entries = append(d.Entries, vfs.NewFileInfo(name, 0600, false))
	}
	return d
}
