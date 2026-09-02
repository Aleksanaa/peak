package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	
	"github.com/aleksana/peak/internal/quote"
	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
)

//go:embed doc
var docFS embed.FS

//go:embed theme
var themeFS embed.FS

type mountEntry struct {
	src, dst string
}

// NineP manages the virtual filesystem and 9P server for Peak.
type NineP struct {
	editor  *Editor
	vfs     *vfs.CompositeFs
	bus     *globalEventBus
	nsFs    *peakNamespaceFs
	nsBase  string // VFS path where nsFs is mounted
	mountMu sync.RWMutex
	mounts  []mountEntry // 9P mounts via Mount()
	binds   []mountEntry // local overlays via Bind()
}

func NewNineP(e *Editor) *NineP {
	const nsBase = "/peak"
	fs := vfs.NewCompositeFs()
	p := &NineP{editor: e, vfs: fs, bus: &globalEventBus{}, nsBase: nsBase}

	p.vfs.Mount("/", afero.NewOsFs())
	p.nsFs = newPeakNamespaceFs(e, p.bus)
	p.vfs.Mount(nsBase, p.nsFs)

	docFs := afero.FromIOFS{FS: docFS}
	p.vfs.Mount("/peak/doc", afero.NewBasePathFs(docFs, "doc"))

	themeLayer := afero.NewMemMapFs()
	themeLayer.Mkdir("/", 0755)
	themeBase := afero.NewBasePathFs(afero.FromIOFS{FS: themeFS}, "theme")
	p.vfs.Mount("/peak/theme", afero.NewCopyOnWriteFs(themeBase, themeLayer))

	p.vfs.Mount("/peak/mirage", afero.NewMemMapFs())

	return p
}

func (p *NineP) Listen() {
	userDir, err := os.UserHomeDir()
	if err != nil {
		return
	}
	sockPath := filepath.Join(userDir, ".peak", "9p")
	os.MkdirAll(filepath.Dir(sockPath), 0700)
	os.Remove(sockPath)

	srv := vfs.NewNinePSrv(vfs.NewRootedFs(p.vfs, p.nsBase))
	go func() {
		if err := srv.Serve("unix", sockPath); err != nil {
			log.Printf("9P server error: %v", err)
		}
	}()
}

// MountWindow exposes a window's namespace at /peak/<id>/.
func (p *NineP) MountWindow(win *Window) {
	p.vfs.Mount("/peak/"+strconv.Itoa(win.ID), newWindowFs(win))
	p.bus.broadcast(fmt.Sprintf("new %d %s\n", win.ID, quote.Quote(win.GetFilename())))
}

// UmountWindow removes a window's namespace.
func (p *NineP) UmountWindow(win *Window) {
	p.vfs.Umount("/peak/" + strconv.Itoa(win.ID))
	p.bus.broadcast(fmt.Sprintf("close %d %s\n", win.ID, quote.Quote(win.GetFilename())))
}

func (p *NineP) BroadcastFocus(win *Window) {
	p.bus.broadcast(fmt.Sprintf("focus %d %s\n", win.ID, quote.Quote(win.GetFilename())))
}

func (p *NineP) BroadcastGet(win *Window) {
	p.bus.broadcast(fmt.Sprintf("get %d %s\n", win.ID, quote.Quote(win.GetFilename())))
}

func (p *NineP) BroadcastPut(win *Window) {
	p.bus.broadcast(fmt.Sprintf("put %d %s\n", win.ID, quote.Quote(win.GetFilename())))
}

// Mount attaches a 9P server to path in the VFS. Returns the resolved source
// Mount attaches a 9P server to path in the VFS. If socket can be opened as a
// file in peak's own VFS it is treated as a virtual socket; otherwise it is
// dialled as a Unix socket. Returns the resolved destination path. Callers
// that want the mount to appear in /mount should record it themselves via
// record().
func (p *NineP) Mount(socket, path string) (string, error) {
	if f, err := p.vfs.OpenFile(socket, os.O_RDONLY, 0); err == nil {
		mountPath := normalizePath(path, "")
		clientFs, err := vfs.NewNinePClientFsFromConn(f)
		if err != nil {
			f.Close()
			return "", err
		}
		p.vfs.Mount(mountPath, clientFs)
		return mountPath, nil
	}
	// Not in the VFS — dial as a real Unix socket.
	socket = normalizePath(socket, "")
	path = normalizePath(path, "")
	clientFs, err := vfs.NewNinePClientFs("unix", socket)
	if err != nil {
		return "", err
	}
	p.vfs.Mount(path, clientFs)
	return path, nil
}

func (p *NineP) Umount(path string) {
	path = normalizePath(path, "")
	p.vfs.Umount(path)
	p.mountMu.Lock()
	p.mounts = removeByDst(p.mounts, path)
	p.binds = removeByDst(p.binds, path)
	p.mountMu.Unlock()
}

// Bind overlays a source path onto dest in the VFS. The source may be any
// path reachable through the composite VFS (internal or external). Callers
// that want the bind to appear in /bind should record it via record().
func (p *NineP) Bind(src, dest string) error {
	src = normalizePath(src, "")
	dest = normalizePath(dest, "")
	p.vfs.Mount(dest, afero.NewBasePathFs(p.vfs, src))
	return nil
}

func (p *NineP) record(table *[]mountEntry, src, dst string) {
	p.mountMu.Lock()
	*table = append(*table, mountEntry{src, dst})
	p.mountMu.Unlock()
}

func removeByDst(entries []mountEntry, dst string) []mountEntry {
	out := entries[:0]
	for _, e := range entries {
		if e.dst != dst {
			out = append(out, e)
		}
	}
	return out
}

// ListMounts returns current 9P mounts as "src dst\n" lines.
func (p *NineP) ListMounts() string {
	p.mountMu.RLock()
	defer p.mountMu.RUnlock()
	return formatEntries(p.mounts)
}

// ListBinds returns current local binds as "src dst\n" lines.
func (p *NineP) ListBinds() string {
	p.mountMu.RLock()
	defer p.mountMu.RUnlock()
	return formatEntries(p.binds)
}

func formatEntries(entries []mountEntry) string {
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(quote.Quote(e.src))
		sb.WriteByte(' ')
		sb.WriteString(quote.Quote(e.dst))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// ResolveLocalPath resolves path to a real OS path through any bind mounts.
// Returns ("", false) for remote or purely virtual paths.
func (p *NineP) ResolveLocalPath(path string) (string, bool) {
	return p.vfs.ResolveLocalPath(path)
}

// FindMount returns the mount path and mounted Fs for the deepest non-root
// mount containing path. Returns ("", nil) if none found.
func (p *NineP) FindMount(path string) (string, afero.Fs) {
	return p.vfs.FindMount(path)
}
