package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aleksana/peak/internal/peakfs"
	"github.com/aleksana/peak/internal/quote"
	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
)

//go:embed doc
var docFS embed.FS

//go:embed theme
var themeFS embed.FS

// ns is the process's namespace: every path peak opens resolves in it, as
// in Plan 9, where a namespace belongs to the process.
var ns *vfs.CompositeFs

// NineP manages the virtual filesystem and 9P server for Peak.
type NineP struct {
	editor *Editor
	bus    *eventBus
	nsFs   *peakNamespaceFs
	nsBase string // VFS path where nsFs is mounted
}

// NewNineP builds the process namespace, ns, and peak's file server over it.
func NewNineP(e *Editor) *NineP {
	const nsBase = "/peak"
	ns = vfs.NewCompositeFs()
	p := &NineP{editor: e, bus: &eventBus{}, nsBase: nsBase}

	ns.Mount("/", afero.NewOsFs(), "")
	p.nsFs = newPeakNamespaceFs(e, p.bus)
	ns.Mount(nsBase, p.nsFs, "")

	docFs := afero.FromIOFS{FS: docFS}
	ns.Mount("/peak/doc", afero.NewBasePathFs(docFs, "doc"), "")

	themeLayer := afero.NewMemMapFs()
	themeLayer.Mkdir("/", 0755)
	themeBase := afero.NewBasePathFs(afero.FromIOFS{FS: themeFS}, "theme")
	ns.Mount("/peak/theme", afero.NewCopyOnWriteFs(themeBase, themeLayer), "")

	ns.Mount("/peak/mirage", afero.NewMemMapFs(), "")

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
	ns.Mount("/peak/"+strconv.Itoa(win.ID), newWindowFs(win), "")
	p.bus.broadcast(fmt.Appendf(nil, "new %d %s\n", win.ID, win.GetFilename()))
}

// UmountWindow removes a window's namespace.
func (p *NineP) UmountWindow(win *Window) {
	ns.Umount("/peak/" + strconv.Itoa(win.ID))
	p.bus.broadcast(fmt.Appendf(nil, "close %d %s\n", win.ID, win.GetFilename()))
}

func (p *NineP) BroadcastFocus(win *Window) {
	p.bus.broadcast(fmt.Appendf(nil, "focus %d %s\n", win.ID, win.GetFilename()))
}

func (p *NineP) BroadcastGet(win *Window) {
	p.bus.broadcast(fmt.Appendf(nil, "get %d %s\n", win.ID, win.GetFilename()))
}

func (p *NineP) BroadcastPut(win *Window) {
	p.bus.broadcast(fmt.Appendf(nil, "put %d %s\n", win.ID, win.GetFilename()))
}

// Mount attaches a 9P server to path in the VFS, listed in /mount: a
// service posted in /peak/srv, or else one listening on the Unix socket
// socket. Returns the resolved destination path.
func (p *NineP) Mount(socket, path string) (string, error) {
	var clientFs *vfs.NinePClientFs
	var err error
	if name, ok := strings.CutPrefix(normalizePath(socket, ""), p.nsBase+"/srv/"); ok {
		clientFs, err = p.nsFs.srvReg.fs(name)
	} else {
		clientFs, err = vfs.NewNinePClientFs("unix", normalizePath(socket, ""))
	}
	if err != nil {
		return "", err
	}
	path = normalizePath(path, "")
	ns.Mount(path, clientFs, socket)
	return path, nil
}

func (p *NineP) Umount(path string) {
	ns.Umount(normalizePath(path, ""))
}

// Bind overlays a source path onto dest in the VFS, listed in /bind. The
// source may be any path reachable through the composite VFS (internal or
// external).
func (p *NineP) Bind(src, dest string) error {
	src = normalizePath(src, "")
	ns.Mount(normalizePath(dest, ""), afero.NewBasePathFs(ns, src), src)
	return nil
}

// ListMounts returns the 9P servers mounted in the namespace as "src dst"
// lines.
func (p *NineP) ListMounts() string { return listNamespace(false) }

// ListBinds returns the paths bound in the namespace as "src dst" lines.
func (p *NineP) ListBinds() string { return listNamespace(true) }

// listNamespace lists the binds, or else the mounts, of the namespace as
// quoted "src dst" lines. A bind is the namespace itself shown at another
// path. A mount point is a directory, shown with a trailing slash, when what
// is attached there is: a server's root always is, a bound path when it is
// one.
func listNamespace(binds bool) string {
	var sb strings.Builder
	for _, m := range ns.Mounts() {
		_, bind := m.Fs.(*afero.BasePathFs)
		if m.Src == "" || bind != binds {
			continue
		}
		dst := m.Path
		if !bind || strings.HasSuffix(m.Src, "/") {
			dst += "/"
		}
		fmt.Fprintf(&sb, "%s %s\n", quote.Quote(m.Src), quote.Quote(dst))
	}
	return sb.String()
}
