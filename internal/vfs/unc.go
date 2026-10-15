package vfs

import (
	"os"
	"path"
	"strings"

	"github.com/aleksana/peak/internal/vfs/afero"
)

// uncFs serves Windows network shares as /server/share/...: the root lists
// the servers this user is connected to, and a server lists its shares. A
// server need not be listed to be opened.
type uncFs struct{ LocalFs }

func newUNCFs() uncFs { return uncFs{LocalFs{`\\`}} }

// split returns the elements of name: none for the root, the server, then
// the share and the path in it.
func (uncFs) split(name string) []string {
	if name = strings.Trim(path.Clean("/"+name), "/"); name == "" {
		return nil
	}
	return strings.Split(name, "/")
}

// dir returns a directory named name holding the directories names.
func (uncFs) dir(name string, names []string) afero.Fs {
	m := afero.NewMemMapFs()
	m.MkdirAll(name, 0555)
	for _, n := range names {
		m.Mkdir(path.Join(name, n), 0555)
	}
	return m
}

// list returns the directories of the root or of a server.
func (u uncFs) list(elems []string) ([]string, error) {
	if len(elems) == 0 {
		return connectedServers()
	}
	return serverShares(elems[0])
}

func (u uncFs) Stat(name string) (os.FileInfo, error) {
	if elems := u.split(name); len(elems) < 2 {
		// Opening a server is what reaches it; naming one doesn't.
		return u.dir("/"+path.Join(elems...), nil).Stat("/" + path.Join(elems...))
	}
	return u.LocalFs.Stat(name)
}

func (u uncFs) Open(name string) (afero.File, error) {
	elems := u.split(name)
	if len(elems) >= 2 {
		return u.LocalFs.Open(name)
	}
	names, err := u.list(elems)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	dir := "/" + path.Join(elems...)
	return u.dir(dir, names).Open(dir)
}

func (u uncFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if len(u.split(name)) < 2 {
		return u.Open(name)
	}
	return u.LocalFs.OpenFile(name, flag, perm)
}

// RealPath returns the OS path of name, and whether it is one: a server and
// the root are not.
func (u uncFs) RealPath(name string) (string, bool) {
	return u.osPath(name), len(u.split(name)) >= 2
}
