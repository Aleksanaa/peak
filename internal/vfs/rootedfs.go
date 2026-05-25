package vfs

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aleksana/peak/internal/vfs/afero"
)

// rootedFs exposes a subtree of a CompositeFs to the 9P server. It translates
// server-relative paths to composite-absolute paths without any file wrapping,
// so files returned from OpenFile are exactly what the composite (and its
// mounted filesystems) return.
type rootedFs struct {
	composite *CompositeFs
	base      string
}

// NewRootedFs returns an afero.Fs that exposes the composite subtree at base.
func NewRootedFs(composite *CompositeFs, base string) afero.Fs {
	return &rootedFs{composite: composite, base: filepath.Clean(base)}
}

func (f *rootedFs) abs(name string) string { return filepath.Join(f.base, name) }

func (f *rootedFs) Open(name string) (afero.File, error) {
	return f.composite.Open(f.abs(name))
}
func (f *rootedFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	return f.composite.OpenFile(f.abs(name), flag, perm)
}
func (f *rootedFs) Stat(name string) (os.FileInfo, error) {
	return f.composite.Stat(f.abs(name))
}
func (f *rootedFs) Create(name string) (afero.File, error) {
	return f.composite.Create(f.abs(name))
}
func (f *rootedFs) Mkdir(name string, perm os.FileMode) error {
	return f.composite.Mkdir(f.abs(name), perm)
}
func (f *rootedFs) MkdirAll(name string, perm os.FileMode) error {
	return f.composite.MkdirAll(f.abs(name), perm)
}
func (f *rootedFs) Remove(name string) error    { return f.composite.Remove(f.abs(name)) }
func (f *rootedFs) RemoveAll(name string) error { return f.composite.RemoveAll(f.abs(name)) }
func (f *rootedFs) Rename(o, n string) error    { return f.composite.Rename(f.abs(o), f.abs(n)) }
func (f *rootedFs) Chmod(name string, mode os.FileMode) error {
	return f.composite.Chmod(f.abs(name), mode)
}
func (f *rootedFs) Chown(name string, uid, gid int) error {
	return f.composite.Chown(f.abs(name), uid, gid)
}
func (f *rootedFs) Chtimes(name string, atime, mtime time.Time) error {
	return f.composite.Chtimes(f.abs(name), atime, mtime)
}
func (f *rootedFs) Name() string { return "rootedFs(" + f.base + ")" }

func (f *rootedFs) WalkRedirect(dir, name string) (string, os.FileInfo, bool) {
	absDir := f.abs(dir)
	rp, fi, ok := f.composite.WalkRedirect(absDir, name)
	if !ok {
		return "", nil, false
	}
	rel := strings.TrimPrefix(filepath.Clean(rp), f.base)
	if rel == "" {
		rel = "/"
	}
	return rel, fi, true
}
