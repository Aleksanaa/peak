package vfs

import (
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"time"

	"github.com/knusbaum/go9p/client"
	"github.com/knusbaum/go9p/proto"
	"github.com/aleksana/peak/internal/vfs/afero"
)

// NinePClientFs implements afero.Fs by wrapping a 9P client.
type NinePClientFs struct {
	client *client.Client
}

// NewNinePClientFs creates a new 9P client and wraps it as an afero.Fs.
func NewNinePClientFs(network, address string) (*NinePClientFs, error) {
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}
	userStr := os.Getenv("USER")
	if userStr == "" {
		userStr = "guest"
	}
	c, err := client.NewClient(conn, userStr, "")
	if err != nil {
		return nil, err
	}
	return &NinePClientFs{client: c}, nil
}

// NewNinePClientFsFromConn creates a 9P client over an existing ReadWriteCloser.
func NewNinePClientFsFromConn(rwc io.ReadWriteCloser) (*NinePClientFs, error) {
	userStr := os.Getenv("USER")
	if userStr == "" {
		userStr = "guest"
	}
	c, err := client.NewClient(&rwcConn{rwc}, userStr, "")
	if err != nil {
		return nil, err
	}
	return &NinePClientFs{client: c}, nil
}

// rwcConn wraps an io.ReadWriteCloser as a net.Conn for go9p's client.
type rwcConn struct{ io.ReadWriteCloser }

func (c *rwcConn) LocalAddr() net.Addr                { return rwcAddr{} }
func (c *rwcConn) RemoteAddr() net.Addr               { return rwcAddr{} }
func (c *rwcConn) SetDeadline(t time.Time) error      { return nil }
func (c *rwcConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *rwcConn) SetWriteDeadline(t time.Time) error { return nil }

type rwcAddr struct{}

func (rwcAddr) Network() string { return "pipe" }
func (rwcAddr) String() string  { return "pipe" }

func (fs *NinePClientFs) Create(name string) (afero.File, error) {
	// go9p's client.Create leaves iounit=0, which breaks twrite. Close and
	// reopen so the client picks up iounit from the ROpen response.
	f, err := fs.client.Create(name, 0644)
	if err != nil {
		return nil, err
	}
	f.Close()
	f2, err := fs.client.Open(name, proto.Ordwr)
	if err != nil {
		return nil, err
	}
	return &NinePFile{f: f2, name: name, fs: fs}, nil
}

func (fs *NinePClientFs) Mkdir(name string, perm os.FileMode) error {
	f, err := fs.client.Create(name, perm|0x80000000)
	if err != nil {
		return err
	}
	f.Close()
	return nil
}

func (fs *NinePClientFs) MkdirAll(p string, perm os.FileMode) error {
	p = path.Clean(p)
	if p == "." || p == "/" {
		return nil
	}
	fi, err := fs.Stat(p)
	if err == nil {
		if fi.IsDir() {
			return nil
		}
		return fmt.Errorf("path exists and is not a directory")
	}

	parent := path.Dir(p)
	if parent != p {
		err = fs.MkdirAll(parent, perm)
		if err != nil {
			return err
		}
	}
	return fs.Mkdir(p, perm)
}

func (fs *NinePClientFs) Open(name string) (afero.File, error) {
	f, err := fs.client.Open(name, proto.Oread)
	if err != nil {
		return nil, err
	}
	return &NinePFile{f: f, name: name, fs: fs}, nil
}

func (fs *NinePClientFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if flag&os.O_CREATE != 0 {
		return fs.Create(name)
	}
	var mode proto.Mode
	if flag&os.O_RDWR != 0 {
		mode = proto.Ordwr
	} else if flag&os.O_WRONLY != 0 {
		mode = proto.Owrite
	} else {
		mode = proto.Oread
	}
	if flag&os.O_TRUNC != 0 {
		mode |= proto.Otrunc
	}
	f, err := fs.client.Open(name, mode)
	if err != nil {
		return nil, err
	}
	return &NinePFile{f: f, name: name, fs: fs}, nil
}

func (fs *NinePClientFs) Remove(name string) error {
	return fs.client.Remove(name)
}

func (fs *NinePClientFs) RemoveAll(p string) error {
	fi, err := fs.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !fi.IsDir() {
		return fs.Remove(p)
	}

	f, err := fs.Open(p)
	if err != nil {
		return err
	}
	infos, err := f.Readdir(-1)
	f.Close()
	if err != nil {
		return err
	}

	for _, info := range infos {
		err = fs.RemoveAll(path.Join(p, info.Name()))
		if err != nil {
			return err
		}
	}
	return fs.Remove(p)
}

func (fs *NinePClientFs) Rename(oldname, newname string) error {
	if path.Dir(oldname) != path.Dir(newname) {
		return fmt.Errorf("cross-directory rename not supported by 9P Wstat")
	}
	s, err := fs.client.Stat(oldname)
	if err != nil {
		return err
	}
	s.Name = path.Base(newname)
	return fs.client.WStat(oldname, s)
}

func (fs *NinePClientFs) Stat(name string) (os.FileInfo, error) {
	s, err := fs.client.Stat(name)
	if err != nil {
		return nil, err
	}
	return &NinePFileInfo{stat: s}, nil
}

func (fs *NinePClientFs) Name() string { return "NinePClientFs" }

func (fs *NinePClientFs) Chmod(name string, mode os.FileMode) error {
	s, err := fs.client.Stat(name)
	if err != nil {
		return err
	}
	s.Mode = (s.Mode & 0xFF000000) | uint32(mode&0777)
	return fs.client.WStat(name, s)
}

func (fs *NinePClientFs) Chown(name string, uid, gid int) error {
	return fmt.Errorf("chown not implemented")
}

func (fs *NinePClientFs) Chtimes(name string, atime time.Time, mtime time.Time) error {
	s, err := fs.client.Stat(name)
	if err != nil {
		return err
	}
	s.Atime = uint32(atime.Unix())
	s.Mtime = uint32(mtime.Unix())
	return fs.client.WStat(name, s)
}

type NinePFile struct {
	f      *client.File
	name   string
	fs     *NinePClientFs
	offset int64
}

func (f *NinePFile) Close() error { return f.f.Close() }
func (f *NinePFile) Read(p []byte) (n int, err error) {
	n, err = f.f.ReadAt(p, f.offset)
	f.offset += int64(n)
	return n, err
}
func (f *NinePFile) ReadAt(p []byte, off int64) (n int, err error) { return f.f.ReadAt(p, off) }

func (f *NinePFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		f.offset = offset
	case io.SeekCurrent:
		f.offset += offset
	case io.SeekEnd:
		fi, err := f.Stat()
		if err != nil {
			return 0, err
		}
		f.offset = fi.Size() + offset
	}
	return f.offset, nil
}

func (f *NinePFile) Write(p []byte) (n int, err error) {
	n, err = f.f.WriteAt(p, f.offset)
	f.offset += int64(n)
	return n, err
}
func (f *NinePFile) WriteAt(p []byte, off int64) (n int, err error) { return f.f.WriteAt(p, off) }
func (f *NinePFile) Name() string                                   { return f.name }
func (f *NinePFile) Readdir(count int) ([]os.FileInfo, error) {
	stats, err := f.fs.client.Readdir(f.name)
	if err != nil {
		return nil, err
	}
	var infos []os.FileInfo
	for i := range stats {
		if count > 0 && i >= count {
			break
		}
		infos = append(infos, &NinePFileInfo{stat: &stats[i]})
	}
	return infos, nil
}
func (f *NinePFile) Readdirnames(n int) ([]string, error) {
	infos, err := f.Readdir(n)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, info := range infos {
		names = append(names, info.Name())
	}
	return names, nil
}
func (f *NinePFile) Stat() (os.FileInfo, error) { return f.fs.Stat(f.name) }
func (f *NinePFile) Sync() error                { return nil }
func (f *NinePFile) Truncate(size int64) error {
	s, err := f.fs.client.Stat(f.name)
	if err != nil {
		return err
	}
	s.Length = uint64(size)
	return f.fs.client.WStat(f.name, s)
}
func (f *NinePFile) WriteString(s string) (ret int, err error) { return f.Write([]byte(s)) }

type NinePFileInfo struct {
	stat *proto.Stat
}

func (fi *NinePFileInfo) Name() string       { return fi.stat.Name }
func (fi *NinePFileInfo) Size() int64        { return int64(fi.stat.Length) }
func (fi *NinePFileInfo) Mode() os.FileMode  { return os.FileMode(fi.stat.Mode) }
func (fi *NinePFileInfo) ModTime() time.Time { return time.Unix(int64(fi.stat.Mtime), 0) }
func (fi *NinePFileInfo) IsDir() bool        { return fi.stat.Mode&0x80000000 != 0 }
func (fi *NinePFileInfo) Sys() interface{}   { return fi.stat }
