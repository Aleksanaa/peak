package vfs

import (
	"fmt"
	"os"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/aleksana/peak/internal/vfs/afero"
)

// uncFs serves Windows network shares as /server/share/...: the root lists
// the servers this user is connected to and those peak has opened a share
// of, and a server lists its shares. A server need not be listed to be
// opened.
type uncFs struct {
	LocalFs
	mu     sync.Mutex
	opened []string // servers peak has opened a share of
}

func newUNCFs() *uncFs { return &uncFs{LocalFs: LocalFs{`\\`}} }

// split returns the elements of name: none for the root, the server, then
// the share and the path in it.
func (*uncFs) split(name string) []string {
	if name = strings.Trim(path.Clean("/"+name), "/"); name == "" {
		return nil
	}
	return strings.Split(name, "/")
}

// dir returns a directory named name holding the directories names.
func (*uncFs) dir(name string, names []string) afero.Fs {
	m := afero.NewMemMapFs()
	m.MkdirAll(name, 0555)
	for _, n := range names {
		m.Mkdir(path.Join(name, n), 0555)
	}
	return m
}

// list returns the directories of the root or of a server.
func (u *uncFs) list(elems []string) ([]string, error) {
	if len(elems) > 0 {
		return serverShares(elems[0])
	}
	servers, err := connectedServers()
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, s := range u.opened {
		if !containsFold(servers, s) {
			servers = append(servers, s)
		}
	}
	return servers, nil
}

// reached notes that peak opened a share of server.
func (u *uncFs) reached(server string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !containsFold(u.opened, server) {
		u.opened = append(u.opened, server)
	}
}

func containsFold(list []string, s string) bool {
	return slices.ContainsFunc(list, func(l string) bool { return strings.EqualFold(l, s) })
}

func (u *uncFs) Stat(name string) (os.FileInfo, error) {
	if elems := u.split(name); len(elems) < 2 {
		// Opening a server is what reaches it; naming one doesn't.
		dir := "/" + path.Join(elems...)
		return u.dir(dir, nil).Stat(dir)
	}
	return u.LocalFs.Stat(name)
}

func (u *uncFs) Open(name string) (afero.File, error) {
	return u.OpenFile(name, os.O_RDONLY, 0)
}

func (u *uncFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	elems := u.split(name)
	if len(elems) >= 2 {
		f, err := u.LocalFs.OpenFile(name, flag, perm)
		if err == nil {
			u.reached(elems[0])
		}
		return f, err
	}
	names, err := u.list(elems)
	if err != nil {
		// Not as the error itself: a server that can't be listed may
		// still be opened by share, so it is not "no such file".
		return nil, fmt.Errorf("can't list: %v", err)
	}
	dir := "/" + path.Join(elems...)
	return u.dir(dir, names).Open(dir)
}

// RealPath returns the OS path of name, and whether it is one: a server and
// the root are not.
func (u *uncFs) RealPath(name string) (string, bool) {
	return u.osPath(name), len(u.split(name)) >= 2
}
