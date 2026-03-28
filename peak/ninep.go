package main

import (
	"embed"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aleksana/peak/internal/vfs"
	"github.com/spf13/afero"
)

//go:embed doc
var docFS embed.FS

// NineP handles the 9P filesystem for Peak.
type NineP struct {
	editor *Editor
	vfs    *vfs.CompositeFs
	index  atomic.Value // holds string
}

func NewNineP(e *Editor) *NineP {
	fs := vfs.NewCompositeFs()
	p := &NineP{editor: e, vfs: fs}
	p.index.Store("")

	// Mount the real OS filesystem as the root
	p.vfs.Mount("/", afero.NewOsFs())

	// Create /peak container
	p.vfs.Mount("/peak", &PeakDirectoryFs{Fs: afero.NewMemMapFs(), p: p})

	// Mount virtual filesystems
	docFs := afero.FromIOFS{FS: docFS}
	p.vfs.Mount("/peak/doc", afero.NewBasePathFs(docFs, "doc"))
	// p.vfs.Mount("/peak/ssh", NewSftpMountFs())
	// p.vfs.Mount("/peak/git", NewGitFs())
	p.vfs.Mount("/peak/mirage", afero.NewMemMapFs())

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
			tagBuf := win.tag.buffer
			bodyBuf := win.body.GetBuffer()

			if win.hasVersion && bodyBuf != nil && bodyBuf.version != win.savedVersion {
				dirty = 1
			}
			tagLen := tagBuf.Len()
			bodyLen := 0
			if bodyBuf != nil {
				bodyLen = bodyBuf.Len()
			}
			isdir := 0
			if win.isDir {
				isdir = 1
			}
			tagText := tagBuf.GetText()
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

	srv := vfs.NewNinePSrv(p.vfs)
	go func() {
		if err := srv.Serve("unix", sockPath); err != nil {
			log.Printf("9P server error: %v", err)
		}
	}()
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
func (f *indexFile) Stat() (os.FileInfo, error) {
	return &SimpleFileInfo{name: f.name, size: int64(len(f.p.getIndex())), mode: 0444}, nil
}

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

func (p *NineP) Mount(socket, path string) error {
	clientFs, err := vfs.NewNinePClientFs("unix", socket)
	if err != nil {
		return err
	}
	p.vfs.Mount(path, clientFs)
	return nil
}

func (p *NineP) Umount(path string) {
	p.vfs.Umount(path)
}

func (p *NineP) Bind(src, dest string) error {
	// For local bind, we use NewBasePathFs on top of OsFs
	p.vfs.Mount(dest, afero.NewBasePathFs(afero.NewOsFs(), src))
	return nil
}

func (p *NineP) RunInternal(path, cmd, input string, winid int) (string, error) {
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
