package vfs

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/aleksana/peak/internal/vfs/afero"
)

// The namespace names files with slash-separated paths, as Plan 9 does, on
// every OS. On Windows, which has no single root, drive C: is the directory
// /c, as in Git Bash, and the share \\server\share is /unc/server/share.

// FromOS converts the OS path p to the namespace's form. It also takes a
// drive path written with slashes after a slash, as file URIs give it
// (/C:/Users). On Unix it returns p unchanged.
func FromOS(p string) string {
	p = filepath.ToSlash(p)
	// A device path (\\?\C:\x, \\?\UNC\server\share) names what follows.
	if filepath.VolumeName(p) != "" && (strings.HasPrefix(p, "//?/") || strings.HasPrefix(p, "//./")) {
		p = p[4:]
		if len(p) >= 4 && strings.EqualFold(p[:4], "UNC/") {
			p = "//" + p[4:]
		}
	}
	rest := strings.TrimPrefix(p, "/")
	if v := filepath.VolumeName(rest); len(v) == 2 && v[1] == ':' {
		return "/" + strings.ToLower(v[:1]) + "/" + strings.TrimPrefix(rest[2:], "/")
	}
	if strings.HasPrefix(p, "//") && filepath.VolumeName(p) != "" {
		return "/unc" + p[1:]
	}
	return p
}

// MountLocal mounts the OS's files: its root at / on Unix; on Windows, each
// drive at its letter and network shares at /unc.
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
	fs.Mount("/unc", newUNCFs(), "")
}

// LocalFs serves the OS directory Root, translating the namespace's names to
// OS paths.
type LocalFs struct{ Root string }

// RealPath returns the OS path of name, and whether it is one: here always.
func (fs LocalFs) RealPath(name string) (string, bool) { return fs.osPath(name), true }

// osPath returns the OS path of name. Root ends in a separator.
func (fs LocalFs) osPath(name string) string {
	return fs.Root + filepath.FromSlash(strings.TrimPrefix(path.Clean("/"+name), "/"))
}

var osFs afero.OsFs

func (fs LocalFs) Name() string { return "LocalFs" }

func (fs LocalFs) Create(name string) (afero.File, error) { return osFs.Create(fs.osPath(name)) }
func (fs LocalFs) Open(name string) (afero.File, error)   { return osFs.Open(fs.osPath(name)) }
func (fs LocalFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	return osFs.OpenFile(fs.osPath(name), flag, perm)
}
func (fs LocalFs) Mkdir(name string, perm os.FileMode) error {
	return osFs.Mkdir(fs.osPath(name), perm)
}
func (fs LocalFs) MkdirAll(name string, perm os.FileMode) error {
	return osFs.MkdirAll(fs.osPath(name), perm)
}
func (fs LocalFs) Remove(name string) error    { return osFs.Remove(fs.osPath(name)) }
func (fs LocalFs) RemoveAll(name string) error { return osFs.RemoveAll(fs.osPath(name)) }
func (fs LocalFs) Rename(oldname, newname string) error {
	return osFs.Rename(fs.osPath(oldname), fs.osPath(newname))
}
func (fs LocalFs) Stat(name string) (os.FileInfo, error) { return osFs.Stat(fs.osPath(name)) }
func (fs LocalFs) Chmod(name string, mode os.FileMode) error {
	return osFs.Chmod(fs.osPath(name), mode)
}
func (fs LocalFs) Chown(name string, uid, gid int) error {
	return osFs.Chown(fs.osPath(name), uid, gid)
}
func (fs LocalFs) Chtimes(name string, atime, mtime time.Time) error {
	return osFs.Chtimes(fs.osPath(name), atime, mtime)
}
