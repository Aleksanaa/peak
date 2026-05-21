package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
)

//go:embed doc
var docFS embed.FS

// NineP manages the virtual filesystem and 9P server for Peak.
type NineP struct {
	editor *Editor
	vfs    *vfs.CompositeFs
}

func NewNineP(e *Editor) *NineP {
	fs := vfs.NewCompositeFs()
	p := &NineP{editor: e, vfs: fs}

	p.vfs.Mount("/", afero.NewOsFs())
	p.vfs.Mount("/peak", afero.NewMemMapFs())

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

	srv := vfs.NewNinePSrv(afero.NewBasePathFs(p.vfs, "/peak"))
	go func() {
		if err := srv.Serve("unix", sockPath); err != nil {
			log.Printf("9P server error: %v", err)
		}
	}()
}

// MountWindow exposes a window's namespace at /peak/<id>/.
func (p *NineP) MountWindow(win *Window) {
	p.vfs.Mount("/peak/"+strconv.Itoa(win.ID), &windowFs{win: win})
}

// UmountWindow removes a window's namespace.
func (p *NineP) UmountWindow(win *Window) {
	p.vfs.Umount("/peak/" + strconv.Itoa(win.ID))
}

func (p *NineP) Mount(socket, path string) error {
	socket = resolvePath(socket)
	path = resolvePath(path)
	clientFs, err := vfs.NewNinePClientFs("unix", socket)
	if err != nil {
		return err
	}
	p.vfs.Mount(path, clientFs)
	return nil
}

func (p *NineP) Umount(path string) {
	p.vfs.Umount(resolvePath(path))
}

func (p *NineP) Bind(src, dest string) error {
	src = resolvePath(src)
	dest = resolvePath(dest)
	p.vfs.Mount(dest, afero.NewBasePathFs(afero.NewOsFs(), src))
	return nil
}

func (p *NineP) RunInternal(path, cmd, input string, winid int) (string, error) {
	return "", fmt.Errorf("%s: virtual path cannot execute external command", path)
}
