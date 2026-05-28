package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
)

//go:embed doc
var docFS embed.FS

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
	p.vfs.Mount("/peak/"+strconv.Itoa(win.ID), &windowFs{win: win})
	p.bus.broadcast(fmt.Sprintf("new %d %s\n", win.ID, win.GetFilename()))
}

// UmountWindow removes a window's namespace.
func (p *NineP) UmountWindow(win *Window) {
	p.vfs.Umount("/peak/" + strconv.Itoa(win.ID))
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

// Mount attaches a 9P server to path in the VFS. The first return value is the
// resolved source path suitable for display; callers that want the mount to
// appear in /mount should record it themselves via record().
func (p *NineP) Mount(socket, path string) (string, error) {
	// Try virtual socket first: explicit positive check against the namespace.
	if conn, err := p.nsFs.openSocket(context.Background(), socket); err == nil {
		mountPath := resolvePath(path)
		clientFs, err := vfs.NewNinePClientFsFromConn(conn)
		if err != nil {
			conn.Close()
			return "", err
		}
		p.vfs.Mount(mountPath, clientFs)
		return p.nsBase + "/" + strings.TrimPrefix(socket, "/"), nil
	}
	// Not in the namespace — must be a real Unix socket.
	socket = resolvePath(socket)
	path = resolvePath(path)
	clientFs, err := vfs.NewNinePClientFs("unix", socket)
	if err != nil {
		return "", err
	}
	p.vfs.Mount(path, clientFs)
	return socket, nil
}

func (p *NineP) Umount(path string) {
	path = resolvePath(path)
	p.vfs.Umount(path)
	p.mountMu.Lock()
	p.mounts = removeByDst(p.mounts, path)
	p.binds = removeByDst(p.binds, path)
	p.mountMu.Unlock()
}

// Bind overlays a local path onto dest in the VFS. Callers that want the bind
// to appear in /bind should record it themselves via record().
func (p *NineP) Bind(src, dest string) error {
	src = resolvePath(src)
	dest = resolvePath(dest)
	p.vfs.Mount(dest, afero.NewBasePathFs(afero.NewOsFs(), src))
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
		sb.WriteString(e.src)
		sb.WriteByte(' ')
		sb.WriteString(e.dst)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func (p *NineP) RunInternal(path, cmd, input string, winid int) (string, error) {
	return "", fmt.Errorf("%s: virtual path cannot execute external command", path)
}

// FindMount returns the mount path and mounted Fs for the deepest non-root
// mount containing path. Returns ("", nil) if none found.
func (p *NineP) FindMount(path string) (string, afero.Fs) {
	return p.vfs.FindMount(path)
}
