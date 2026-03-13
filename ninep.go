package main

import (
	"embed"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/halfwit/styx"
	"github.com/spf13/afero"
)

//go:embed doc
var docFS embed.FS

// NineP handles the 9P filesystem for Peak.
type NineP struct {
	editor *Editor
	vfs    *CompositeFs
	index  atomic.Value // holds string
}

func NewNineP(e *Editor) *NineP {
	vfs := NewCompositeFs(afero.NewOsFs())
	p := &NineP{editor: e, vfs: vfs}
	p.index.Store("")

	// Create /peak container
	vfs.Mount("/peak", &PeakDirectoryFs{Fs: afero.NewMemMapFs(), p: p})

	// Mount virtual filesystems
	docFs := afero.FromIOFS{FS: docFS}
	vfs.Mount("/peak/doc", afero.NewBasePathFs(docFs, "doc"))
	vfs.Mount("/peak/ssh", NewSftpMountFs())
	vfs.Mount("/peak/git", NewGitFs())

	return p
}

func (p *NineP) UpdateIndex() {
	if p.editor == nil {
		return
	}
	var sb strings.Builder
	for _, col := range p.editor.columns {
		for _, win := range col.windows {
			dirty := 0
			if win.hasVersion && win.body.buffer.version != win.savedVersion {
				dirty = 1
			}
			tagLen := win.tag.buffer.Len()
			bodyLen := win.body.buffer.Len()
			isdir := 0
			if win.isDir {
				isdir = 1
			}
			tagText := win.tag.buffer.GetText()
			if i := strings.Index(tagText, "\n"); i >= 0 {
				tagText = tagText[:i]
			}
			fmt.Fprintf(&sb, "%11d %11d %11d %11d %11d %s\n", win.ID, tagLen, bodyLen, isdir, dirty, tagText)
		}
	}
	p.index.Store(sb.String())
}

func (p *NineP) getIndex() string {
	return p.index.Load().(string)
}

func (p *NineP) Listen() {
	userDir, err := os.UserHomeDir()
	if err != nil {
		return
	}
	sockPath := filepath.Join(userDir, ".peak", "9p")
	os.MkdirAll(filepath.Dir(sockPath), 0700)
	os.Remove(sockPath)

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		log.Printf("failed to listen on %s: %v", sockPath, err)
		return
	}

	go func() {
		defer l.Close()
		handler := New9PHandler(afero.NewBasePathFs(p.vfs, "/peak"))
		srv := &styx.Server{Handler: handler}
		if err := srv.Serve(l); err != nil {
			log.Printf("9P server error: %v", err)
		}
	}()
}

func New9PHandler(vfs afero.Fs) styx.Handler {
	return styx.HandlerFunc(func(s *styx.Session) {
		for s.Next() {
			req := s.Request()
			path := req.Path()
			switch msg := req.(type) {
			case styx.Twalk:
				fi, err := vfs.Stat(path)
				msg.Rwalk(fi, err)
			case styx.Topen:
				flags := os.O_RDWR
				if msg.Flag&0x10 != 0 {
					flags |= os.O_TRUNC
				}
				f, err := vfs.OpenFile(path, flags, 0)
				msg.Ropen(f, err)
			case styx.Tstat:
				fi, err := vfs.Stat(path)
				msg.Rstat(fi, err)
			case styx.Tremove:
				msg.Rremove(vfs.Remove(path))
			case styx.Tsync:
				msg.Rsync(nil)
			case styx.Ttruncate:
				f, err := vfs.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
				if err == nil {
					f.Close()
				}
				msg.Rtruncate(err)
			case styx.Tcreate:
				// Use O_RDWR by default to ensure file creation works
				f, err := vfs.OpenFile(filepath.Join(path, msg.Name), os.O_CREATE|os.O_TRUNC|os.O_RDWR, msg.Mode)
				msg.Rcreate(f, err)
			case styx.Tchmod:
				msg.Rchmod(vfs.Chmod(path, msg.Mode))
			case styx.Tchown:
				msg.Rchown(nil)
			case styx.Tutimes:
				msg.Rutimes(vfs.Chtimes(path, msg.Atime, msg.Mtime))
			case styx.Trename:
				newPath := msg.NewPath
				if !filepath.IsAbs(newPath) && !strings.HasPrefix(newPath, "/") {
					newPath = filepath.Join(filepath.Dir(msg.OldPath), newPath)
				}
				msg.Rrename(vfs.Rename(msg.OldPath, newPath))
			}
		}
	})
}

type PeakDirectoryFs struct {
	afero.Fs
	p *NineP
}

func (d *PeakDirectoryFs) Stat(name string) (os.FileInfo, error) {
	if strings.TrimPrefix(filepath.ToSlash(filepath.Clean(name)), "/") == "index" {
		return &SimpleFileInfo{name: "index", size: int64(len(d.p.getIndex())), mode: 0444}, nil
	}
	return d.Fs.Stat(name)
}

func (d *PeakDirectoryFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	rel := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(name)), "/")
	if rel == "index" {
		return &indexFile{p: d.p, name: "index"}, nil
	}
	f, err := d.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	if rel == "." || rel == "" {
		return &mergedDirFile{
			File: f,
			extra: []os.FileInfo{
				&SimpleFileInfo{name: "index", size: int64(len(d.p.getIndex())), mode: 0444},
			},
		}, nil
	}
	return f, nil
}

func (d *PeakDirectoryFs) Open(name string) (afero.File, error) {
	return d.OpenFile(name, os.O_RDONLY, 0)
}

type indexFile struct {
	p      *NineP
	name   string
	offset int64
}

func (f *indexFile) Close() error                           { return nil }
func (f *indexFile) Write(p []byte) (int, error)            { return 0, os.ErrPermission }
func (f *indexFile) WriteAt(p []byte, o int64) (int, error) { return 0, os.ErrPermission }
func (f *indexFile) WriteString(s string) (int, error)      { return 0, os.ErrPermission }
func (f *indexFile) Truncate(s int64) error                 { return os.ErrPermission }
func (f *indexFile) Sync() error                            { return nil }
func (f *indexFile) Name() string                           { return f.name }
func (f *indexFile) Readdir(n int) ([]os.FileInfo, error)   { return nil, nil }
func (f *indexFile) Readdirnames(n int) ([]string, error)   { return nil, nil }

func (f *indexFile) Read(p []byte) (n int, err error) {
	content := f.p.getIndex()
	if f.offset >= int64(len(content)) {
		return 0, io.EOF
	}
	n = copy(p, content[f.offset:])
	f.offset += int64(n)
	return n, nil
}

func (f *indexFile) ReadAt(p []byte, off int64) (n int, err error) {
	content := f.p.getIndex()
	if off >= int64(len(content)) {
		return 0, io.EOF
	}
	n = copy(p, content[off:])
	return n, nil
}

func (f *indexFile) Seek(offset int64, whence int) (int64, error) {
	content := f.p.getIndex()
	switch whence {
	case io.SeekStart:
		f.offset = offset
	case io.SeekCurrent:
		f.offset += offset
	case io.SeekEnd:
		f.offset = int64(len(content)) + offset
	}
	return f.offset, nil
}

func (f *indexFile) Stat() (os.FileInfo, error) {
	return &SimpleFileInfo{name: f.name, size: int64(len(f.p.getIndex())), mode: 0444}, nil
}

func (p *NineP) RunInternal(path, cmd, input string, winid int) (string, error) {
	fs, rel := p.vfs.getFs(path)
	if runner, ok := fs.(interface {
		Run(path, cmd, input string, winid int) (string, error)
	}); ok {
		return runner.Run(rel, cmd, input, winid)
	}
	return "", fmt.Errorf("%s: virtual path cannot execute external command", path)
}

type SimpleFileInfo struct {
	name    string
	isDir   bool
	size    int64
	modTime time.Time
	mode    os.FileMode
}

func (s *SimpleFileInfo) Name() string       { return s.name }
func (s *SimpleFileInfo) Size() int64        { return s.size }
func (s *SimpleFileInfo) IsDir() bool        { return s.isDir }
func (s *SimpleFileInfo) ModTime() time.Time { return s.modTime }
func (s *SimpleFileInfo) Sys() interface{}   { return nil }
func (s *SimpleFileInfo) Mode() os.FileMode {
	// Just some simple modes to make 9pfuse happy
	mode := s.mode.Perm()
	if mode == 0 {
		if s.isDir {
			mode = 0755
		} else {
			mode = 0644
		}
	}
	if s.isDir {
		return os.ModeDir | mode
	}
	return mode
}

func NewFileInfo(fi os.FileInfo) *SimpleFileInfo {
	return &SimpleFileInfo{
		name:    fi.Name(),
		isDir:   fi.IsDir(),
		size:    fi.Size(),
		modTime: fi.ModTime(),
		mode:    fi.Mode(),
	}
}

func sliceReaddir(offset *int, entries []os.FileInfo, count int) ([]os.FileInfo, error) {
	if count <= 0 {
		return entries, nil
	}
	if *offset >= len(entries) {
		return nil, io.EOF
	}
	end := *offset + count
	if end > len(entries) {
		end = len(entries)
	}
	res := entries[*offset:end]
	*offset = end
	return res, nil
}

func convertEntries(raw []os.FileInfo) []os.FileInfo {
	res := make([]os.FileInfo, 0, len(raw))
	for _, fi := range raw {
		if name := fi.Name(); name != "." && name != ".." {
			res = append(res, NewFileInfo(fi))
		}
	}
	return res
}

type CompositeFs struct {
	base   afero.Fs
	mounts sync.Map // string -> afero.Fs
}

func NewCompositeFs(base afero.Fs) *CompositeFs {
	return &CompositeFs{base: base}
}

func (c *CompositeFs) Mount(path string, fs afero.Fs) {
	path = filepath.ToSlash(filepath.Clean(path))
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	c.mounts.Store(path, fs)
	_ = c.base.MkdirAll(path, 0755)
}

func (c *CompositeFs) getFs(name string) (afero.Fs, string) {
	name = filepath.ToSlash(filepath.Clean(name))
	if !strings.HasPrefix(name, "/") {
		return c.base, name
	}

	var bestPath string
	var bestFs afero.Fs
	c.mounts.Range(func(key, value interface{}) bool {
		mpath := key.(string)
		if name == mpath || strings.HasPrefix(name, mpath+"/") {
			if len(mpath) > len(bestPath) {
				bestPath, bestFs = mpath, value.(afero.Fs)
			}
		}
		return true
	})

	if bestFs != nil {
		rel := strings.TrimPrefix(name, bestPath)
		if rel == "" || rel == "/" {
			return bestFs, "."
		}
		return bestFs, strings.TrimPrefix(rel, "/")
	}
	return c.base, name
}

func (c *CompositeFs) Open(name string) (afero.File, error) {
	return c.OpenFile(name, os.O_RDONLY, 0)
}

func (c *CompositeFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	name = filepath.ToSlash(filepath.Clean(name))
	fs, rel := c.getFs(name)
	f, err := fs.OpenFile(rel, flag, perm)
	if err != nil {
		return nil, err
	}

	fi, err := f.Stat()
	if err == nil && fi.IsDir() {
		var extra []os.FileInfo
		prefix := name
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		c.mounts.Range(func(key, value interface{}) bool {
			mpath := key.(string)
			if mpath != name && strings.HasPrefix(mpath, prefix) {
				if relM := strings.TrimPrefix(mpath, prefix); !strings.Contains(relM, "/") && relM != "" {
					extra = append(extra, &SimpleFileInfo{name: relM, isDir: true})
				}
			}
			return true
		})
		if len(extra) > 0 {
			return &mergedDirFile{File: f, extra: extra}, nil
		}
	}
	return f, nil
}

func (c *CompositeFs) Stat(name string) (os.FileInfo, error) {
	fs, rel := c.getFs(name)
	return fs.Stat(rel)
}

func (c *CompositeFs) Remove(n string) error               { f, r := c.getFs(n); return f.Remove(r) }
func (c *CompositeFs) RemoveAll(n string) error            { f, r := c.getFs(n); return f.RemoveAll(r) }
func (c *CompositeFs) Create(n string) (afero.File, error) { f, r := c.getFs(n); return f.Create(r) }
func (c *CompositeFs) Mkdir(n string, p os.FileMode) error { f, r := c.getFs(n); return f.Mkdir(r, p) }
func (c *CompositeFs) MkdirAll(n string, p os.FileMode) error {
	f, r := c.getFs(n)
	return f.MkdirAll(r, p)
}
func (c *CompositeFs) Rename(o, n string) error {
	f1, r1 := c.getFs(o)
	f2, r2 := c.getFs(n)
	if f1 != f2 {
		return fmt.Errorf("cross-fs rename not supported")
	}
	return f1.Rename(r1, r2)
}
func (c *CompositeFs) Chmod(n string, m os.FileMode) error { f, r := c.getFs(n); return f.Chmod(r, m) }
func (c *CompositeFs) Chown(n string, u, g int) error      { f, r := c.getFs(n); return f.Chown(r, u, g) }
func (c *CompositeFs) Chtimes(n string, a, m time.Time) error {
	f, r := c.getFs(n)
	return f.Chtimes(r, a, m)
}
func (c *CompositeFs) Name() string { return "CompositeFs" }

type mergedDirFile struct {
	afero.File
	extra       []os.FileInfo
	extraOffset int
	baseDone    bool
	seen        map[string]bool
}

func (m *mergedDirFile) Readdir(count int) ([]os.FileInfo, error) {
	if m.seen == nil {
		m.seen = make(map[string]bool)
	}
	var res []os.FileInfo
	if !m.baseDone {
		entries, err := m.File.Readdir(count)
		for _, e := range entries {
			if !m.seen[e.Name()] {
				res = append(res, e)
				m.seen[e.Name()] = true
			}
		}
		if err == io.EOF || (count > 0 && len(entries) < count) {
			m.baseDone = true
		} else if err != nil {
			return nil, err
		}
	}

	for m.extraOffset < len(m.extra) && (count <= 0 || len(res) < count) {
		ex := m.extra[m.extraOffset]
		m.extraOffset++
		if !m.seen[ex.Name()] {
			res = append(res, ex)
			m.seen[ex.Name()] = true
		}
	}
	if len(res) == 0 && count > 0 {
		return nil, io.EOF
	}
	return res, nil
}

type memDirFile struct {
	name    string
	entries []os.FileInfo
	offset  int
}

func (v *memDirFile) Close() error                                   { return nil }
func (v *memDirFile) Read(p []byte) (n int, err error)               { return 0, io.EOF }
func (v *memDirFile) ReadAt(p []byte, off int64) (n int, err error)  { return 0, io.EOF }
func (v *memDirFile) Seek(offset int64, whence int) (int64, error)   { return 0, nil }
func (v *memDirFile) Write(p []byte) (n int, err error)              { return 0, os.ErrPermission }
func (v *memDirFile) WriteAt(p []byte, off int64) (n int, err error) { return 0, os.ErrPermission }
func (v *memDirFile) Name() string                                   { return v.name }
func (v *memDirFile) Readdir(count int) ([]os.FileInfo, error) {
	return sliceReaddir(&v.offset, v.entries, count)
}
func (v *memDirFile) Readdirnames(n int) ([]string, error) { return nil, nil }
func (v *memDirFile) Stat() (os.FileInfo, error) {
	return &SimpleFileInfo{name: v.name, isDir: true}, nil
}
func (v *memDirFile) Sync() error                               { return nil }
func (v *memDirFile) Truncate(size int64) error                 { return os.ErrPermission }
func (v *memDirFile) WriteString(s string) (ret int, err error) { return 0, os.ErrPermission }
