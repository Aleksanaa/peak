package vfs

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/aleksana/peak/internal/vfs/afero"
)

// The namespace names files with slash-separated paths, as Plan 9 does, on
// every OS. On Windows, which has no single root, drive C: is the directory
// /c, as in Git Bash.

// FromOS converts the OS path p to the namespace's form. It also takes a
// drive path written with slashes after a slash, as file URIs give it
// (/C:/Users). On Unix it returns p unchanged.
func FromOS(p string) string {
	p = filepath.ToSlash(p)
	rest := strings.TrimPrefix(p, "/")
	if v := filepath.VolumeName(rest); len(v) == 2 && v[1] == ':' {
		return "/" + strings.ToLower(v[:1]) + "/" + strings.TrimPrefix(rest[2:], "/")
	}
	return p
}

// MountLocal mounts the OS's files: its root at / on Unix, each drive at its
// letter on Windows.
func (fs *CompositeFs) MountLocal() {
	if runtime.GOOS != "windows" {
		fs.Mount("/", LocalFs{"/"}, "")
		return
	}
	for d := 'A'; d <= 'Z'; d++ {
		root := string(d) + `:\`
		if _, err := os.Stat(root); err == nil {
			fs.Mount(FromOS(root), LocalFs{root}, "")
		}
	}
}

// LocalFs serves the OS directory Root, translating the namespace's names to
// OS paths.
type LocalFs struct{ Root string }

// RealPath returns the OS path of name.
func (fs LocalFs) RealPath(name string) string {
	return filepath.Join(fs.Root, filepath.FromSlash(name))
}

var osFs afero.OsFs

func (fs LocalFs) Name() string { return "LocalFs" }

func (fs LocalFs) Create(name string) (afero.File, error) { return osFs.Create(fs.RealPath(name)) }
func (fs LocalFs) Open(name string) (afero.File, error)   { return osFs.Open(fs.RealPath(name)) }
func (fs LocalFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	return osFs.OpenFile(fs.RealPath(name), flag, perm)
}
func (fs LocalFs) Mkdir(name string, perm os.FileMode) error {
	return osFs.Mkdir(fs.RealPath(name), perm)
}
func (fs LocalFs) MkdirAll(name string, perm os.FileMode) error {
	return osFs.MkdirAll(fs.RealPath(name), perm)
}
func (fs LocalFs) Remove(name string) error    { return osFs.Remove(fs.RealPath(name)) }
func (fs LocalFs) RemoveAll(name string) error { return osFs.RemoveAll(fs.RealPath(name)) }
func (fs LocalFs) Rename(oldname, newname string) error {
	return osFs.Rename(fs.RealPath(oldname), fs.RealPath(newname))
}
func (fs LocalFs) Stat(name string) (os.FileInfo, error) { return osFs.Stat(fs.RealPath(name)) }
func (fs LocalFs) Chmod(name string, mode os.FileMode) error {
	return osFs.Chmod(fs.RealPath(name), mode)
}
func (fs LocalFs) Chown(name string, uid, gid int) error {
	return osFs.Chown(fs.RealPath(name), uid, gid)
}
func (fs LocalFs) Chtimes(name string, atime, mtime time.Time) error {
	return osFs.Chtimes(fs.RealPath(name), atime, mtime)
}
