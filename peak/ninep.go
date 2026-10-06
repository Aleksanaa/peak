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

	"github.com/aleksana/peak/internal/peakfs"
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

// ns is the process's namespace: every path peak opens resolves in it, as
// in Plan 9, where a namespace belongs to the process.
var ns *vfs.CompositeFs

// NineP manages the virtual filesystem and 9P server for Peak.
type NineP struct {
	editor  *Editor
	bus     *globalEventBus
	nsFs    *peakNamespaceFs
	nsBase  string // VFS path where nsFs is mounted
	mountMu sync.RWMutex
	mounts  []mountEntry // 9P mounts via Mount()
	binds   []mountEntry // local overlays via Bind()
}

// NewNineP builds the process namespace, ns, and peak's file server over it.
func NewNineP(e *Editor) *NineP {
	const nsBase = "/peak"
	ns = vfs.NewCompositeFs()
	p := &NineP{editor: e, bus: &globalEventBus{}, nsBase: nsBase}

	ns.Mount("/", afero.NewOsFs())
	p.nsFs = newPeakNamespaceFs(e, p.bus)
	ns.Mount(nsBase, p.nsFs)

	docFs := afero.FromIOFS{FS: docFS}
	ns.Mount("/peak/doc", afero.NewBasePathFs(docFs, "doc"))

	themeLayer := afero.NewMemMapFs()
	themeLayer.Mkdir("/", 0755)
	themeBase := afero.NewBasePathFs(afero.FromIOFS{FS: themeFS}, "theme")
	ns.Mount("/peak/theme", afero.NewCopyOnWriteFs(themeBase, themeLayer))

	ns.Mount("/peak/mirage", afero.NewMemMapFs())

	return p
}

func (p *NineP) Listen() {
	sockPath := peakfs.Socket()
	os.MkdirAll(filepath.Dir(sockPath), 0700)
	os.Remove(sockPath)

	srv := vfs.NewNinePSrv(vfs.NewRootedFs(ns, p.nsBase))
	go func() {
		if err := srv.Serve("unix", sockPath); err != nil {
			log.Printf("9P server error: %v", err)
		}
	}()
}

// MountWindow exposes a window's namespace at /peak/<id>/.
func (p *NineP) MountWindow(win *Window) {
	ns.Mount("/peak/"+strconv.Itoa(win.ID), newWindowFs(win))
	p.bus.broadcast(fmt.Sprintf("new %d %s\n", win.ID, win.GetFilename()))
}

// UmountWindow removes a window's namespace.
func (p *NineP) UmountWindow(win *Window) {
	ns.Umount("/peak/" + strconv.Itoa(win.ID))
	p.bus.broadcast(fmt.Sprintf("close %d %s\n", win.ID, win.GetFilename()))
}

func (p *NineP) BroadcastFocus(win *Window) {
	p.bus.broadcast(fmt.Sprintf("focus %d %s\n", win.ID, win.GetFilename()))
}

func (p *NineP) BroadcastGet(win *Window) {
	p.bus.broadcast(fmt.Sprintf("get %d %s\n", win.ID, win.GetFilename()))
}

func (p *NineP) BroadcastPut(win *Window) {
	p.bus.broadcast(fmt.Sprintf("put %d %s\n", win.ID, win.GetFilename()))
}

// Mount attaches a 9P server to path in the VFS and records it in /mount. If
// socket can be opened as a file in peak's own VFS it is treated as a virtual
// socket; otherwise it is dialled as a Unix socket. Returns the resolved
// destination path.
func (p *NineP) Mount(socket, path string) (string, error) {
	var clientFs afero.Fs
	if f, err := ns.OpenFile(socket, os.O_RDONLY, 0); err == nil {
		if clientFs, err = vfs.NewNinePClientFsFromConn(f); err != nil {
			f.Close()
			return "", err
		}
	} else if clientFs, err = vfs.NewNinePClientFs("unix", normalizePath(socket, "")); err != nil {
		return "", err
	}
	path = normalizePath(path, "")
	ns.Mount(path, clientFs)
	p.record(&p.mounts, socket, path)
	return path, nil
}

func (p *NineP) Umount(path string) {
	path = normalizePath(path, "")
	ns.Umount(path)
	p.mountMu.Lock()
	p.mounts = removeByDst(p.mounts, path)
	p.binds = removeByDst(p.binds, path)
	p.mountMu.Unlock()
}

// Bind overlays a source path onto dest in the VFS and records it in /bind.
// The source may be any path reachable through the composite VFS (internal
// or external).
func (p *NineP) Bind(src, dest string) error {
	src = normalizePath(src, "")
	dest = normalizePath(dest, "")
	ns.Mount(dest, afero.NewBasePathFs(ns, src))
	// Normalize again now that dest exists, so /bind lists it as a directory
	// (with a trailing slash), as it always has.
	p.record(&p.binds, normalizePath(src, ""), normalizePath(dest, ""))
	return nil
}

func (p *NineP) record(table *[]mountEntry, src, dst string) {
	p.mountMu.Lock()
	*table = append(*table, mountEntry{src, dst})
	p.mountMu.Unlock()
}

// removeByDst drops the entries mounted at dst. Paths are compared cleaned:
// normalizePath adds a trailing slash only once a path exists in the VFS, so
// the same destination is spelled differently before and after mounting.
func removeByDst(entries []mountEntry, dst string) []mountEntry {
	dst = filepath.Clean(dst)
	out := entries[:0]
	for _, e := range entries {
		if filepath.Clean(e.dst) != dst {
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
		sb.WriteString(e.src)
		sb.WriteByte(' ')
		sb.WriteString(e.dst)
		sb.WriteByte('\n')
	}
	return sb.String()
}
