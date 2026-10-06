package vfs

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/aleksana/peak/internal/vfs/afero"
)

// FileStub provides no-op implementations of the afero.File interface.
// Embed it in a concrete file type and override only the methods you need.
type FileStub struct{}

func (FileStub) Close() error                              { return nil }
func (FileStub) Read(p []byte) (int, error)                { return 0, io.EOF }
func (FileStub) ReadAt(p []byte, off int64) (int, error)   { return 0, io.EOF }
func (FileStub) Seek(off int64, whence int) (int64, error) { return 0, nil }
func (FileStub) Write(p []byte) (int, error)               { return 0, os.ErrPermission }
func (FileStub) WriteAt(p []byte, off int64) (int, error)  { return 0, os.ErrPermission }
func (FileStub) WriteString(s string) (int, error)         { return 0, os.ErrPermission }
func (FileStub) Name() string                              { return "" }
func (FileStub) Readdir(n int) ([]os.FileInfo, error)      { return nil, nil }
func (FileStub) Readdirnames(n int) ([]string, error)      { return nil, nil }
func (FileStub) Stat() (os.FileInfo, error)                { return nil, os.ErrNotExist }
func (FileStub) Sync() error                               { return nil }
func (FileStub) Truncate(size int64) error                 { return nil }

// Sizer is implemented by file types that can report their data size.
// NamespaceFs uses it to populate the Size field in Stat results.
type Sizer interface{ Size() int64 }

// ReadonlyFile is a FileStub backed by a static byte slice set at open time.
// ReadAt and Read serve from Data; all writes return ErrPermission.
type ReadonlyFile struct {
	FileStub
	Data []byte
	off  int64
}

func (f *ReadonlyFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(f.Data)) {
		return 0, io.EOF
	}
	n := copy(p, f.Data[off:])
	if off+int64(n) >= int64(len(f.Data)) {
		return n, io.EOF
	}
	return n, nil
}

func (f *ReadonlyFile) Read(p []byte) (int, error) {
	n, err := f.ReadAt(p, f.off)
	f.off += int64(n)
	return n, err
}

func (f *ReadonlyFile) Size() int64 { return int64(len(f.Data)) }

// WriteBuffer accumulates WriteAt chunks into a flat byte slice.
// A nil WriteBuffer means no writes have been made.
type WriteBuffer []byte

func (b *WriteBuffer) WriteAt(p []byte, off int64) (int, error) {
	end := int(off) + len(p)
	if end > len(*b) {
		*b = append(*b, make([]byte, end-len(*b))...)
	}
	copy((*b)[off:], p)
	return len(p), nil
}

// WriteOnlyFile is a FileStub that accumulates writes in Writes.
// All reads return EOF.
type WriteOnlyFile struct {
	FileStub
	Writes WriteBuffer
}

func (f *WriteOnlyFile) WriteAt(p []byte, off int64) (int, error) { return f.Writes.WriteAt(p, off) }

// ReadWriteFile combines ReadonlyFile with a write buffer.
// Reads serve from Data; writes accumulate in Writes for processing on Close.
type ReadWriteFile struct {
	ReadonlyFile
	Writes WriteBuffer
}

func (f *ReadWriteFile) WriteAt(p []byte, off int64) (int, error) { return f.Writes.WriteAt(p, off) }

// FileEntry describes one file or directory in a NamespaceFs.
type FileEntry struct {
	Name  string
	Mode  os.FileMode
	IsDir bool
	// Active is called before Stat, OpenFile, and Readdir for this entry.
	// If non-nil and returns false, the entry is treated as non-existent.
	Active func() bool
	// Open is called on each open of this entry. If nil, opening returns
	// os.ErrPermission (useful for directories handled via WalkRedirect).
	Open func(flag int) (afero.File, error)
	// ChildMode, if non-zero, makes "<Name>/<child>" stat as a regular file
	// with this mode for any child name. Used for directories whose children
	// are dynamic (e.g. /srv/<name>).
	ChildMode os.FileMode
	// OpenChild is called to open "<Name>/<child>" paths. Required when
	// ChildMode is set. Ignored if ChildMode is zero.
	OpenChild func(child string, flag int) (afero.File, error)
}

// NamespaceFs is an afero.Fs backed by a fixed list of FileEntry values.
// Stat, Readdir, and OpenFile are derived from the entry list automatically.
// All mutation operations return os.ErrPermission.
type NamespaceFs struct {
	// RootName is the Name field returned when stat-ing the root.
	// Defaults to ".".
	RootName string
	// Entries is the fixed list of files and directories in this namespace.
	Entries []FileEntry
}

func (fs *NamespaceFs) rootName() string {
	if fs.RootName != "" {
		return fs.RootName
	}
	return "."
}

func (fs *NamespaceFs) find(name string) *FileEntry {
	for i := range fs.Entries {
		e := &fs.Entries[i]
		if e.Name == name && (e.Active == nil || e.Active()) {
			return e
		}
	}
	return nil
}

func (fs *NamespaceFs) Stat(name string) (os.FileInfo, error) {
	s := strings.Trim(name, "/")
	if s == "" || s == "." {
		return &nsFileInfo{name: fs.rootName(), isDir: true, mode: 0555}, nil
	}
	if e := fs.find(s); e != nil {
		return &nsFileInfo{name: e.Name, mode: e.Mode, isDir: e.IsDir}, nil
	}
	if parent, child, ok := splitChild(s); ok {
		for i := range fs.Entries {
			e := &fs.Entries[i]
			if e.ChildMode != 0 && e.Name == parent && (e.Active == nil || e.Active()) {
				return &nsFileInfo{name: child, mode: e.ChildMode}, nil
			}
		}
	}
	return nil, os.ErrNotExist
}

func (fs *NamespaceFs) Open(name string) (afero.File, error) {
	return fs.OpenFile(name, os.O_RDONLY, 0)
}

func (fs *NamespaceFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	s := strings.Trim(name, "/")
	if s == "" || s == "." {
		return &nsDir{rootName: fs.rootName(), entries: fs.Entries}, nil
	}
	if e := fs.find(s); e != nil {
		if e.Open == nil {
			return nil, os.ErrPermission
		}
		f, err := e.Open(flag)
		if err != nil {
			return nil, err
		}
		return &namedFile{File: f, name: e.Name, mode: e.Mode, isDir: e.IsDir}, nil
	}
	if parent, child, ok := splitChild(s); ok {
		for i := range fs.Entries {
			e := &fs.Entries[i]
			if e.OpenChild != nil && e.Name == parent && (e.Active == nil || e.Active()) {
				f, err := e.OpenChild(child, flag)
				if err != nil {
					return nil, err
				}
				return &namedFile{File: f, name: child, mode: e.ChildMode}, nil
			}
		}
	}
	return nil, os.ErrNotExist
}

// namedFile wraps an afero.File with the name, mode, and isDir from a
// FileEntry. NamespaceFs.OpenFile wraps every opened file with namedFile so
// concrete file types need not repeat the entry's name and mode in their own
// Name() and Stat() methods. Size is delegated to the inner file via Sizer.
type namedFile struct {
	afero.File
	name  string
	mode  os.FileMode
	isDir bool
}

func (f *namedFile) Name() string { return f.name }

// Namespace files implement writing once, as WriteAt; a plain Write lands at
// offset 0, which is what every file served here expects.
func (f *namedFile) Write(p []byte) (int, error)       { return f.File.WriteAt(p, 0) }
func (f *namedFile) WriteString(s string) (int, error) { return f.File.WriteAt([]byte(s), 0) }

func (f *namedFile) Unwrap() afero.File { return f.File }

// SetConn forwards the connAwareFile hook to the inner file if it implements it.
func (f *namedFile) SetConn(c ConnCleaner) {
	if s, ok := f.File.(interface{ SetConn(ConnCleaner) }); ok {
		s.SetConn(c)
	}
}
func (f *namedFile) Stat() (os.FileInfo, error) {
	var size int64
	if s, ok := f.File.(Sizer); ok {
		size = s.Size()
	}
	return &nsFileInfo{name: f.name, mode: f.mode, isDir: f.isDir, size: size}, nil
}

// UnwrapFile returns the concrete afero.File wrapped inside a namedFile, or f
// itself if f is not a namedFile. Use this when you need a type assertion on a
// file returned by NamespaceFs.OpenFile.
func UnwrapFile(f afero.File) afero.File {
	if w, ok := f.(interface{ Unwrap() afero.File }); ok {
		return w.Unwrap()
	}
	return f
}

// splitChild splits "parent/child" into its two parts.
// It only matches a single level: "a/b" → ("a","b",true); "a/b/c" → ("","",false).
func splitChild(s string) (parent, child string, ok bool) {
	i := strings.IndexByte(s, '/')
	if i < 0 || strings.IndexByte(s[i+1:], '/') >= 0 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

func (fs *NamespaceFs) Name() string                           { return "NamespaceFs" }
func (fs *NamespaceFs) Create(n string) (afero.File, error)    { return nil, os.ErrPermission }
func (fs *NamespaceFs) Mkdir(n string, p os.FileMode) error    { return os.ErrPermission }
func (fs *NamespaceFs) MkdirAll(n string, p os.FileMode) error { return os.ErrPermission }
func (fs *NamespaceFs) Remove(n string) error                  { return os.ErrPermission }
func (fs *NamespaceFs) RemoveAll(n string) error               { return os.ErrPermission }
func (fs *NamespaceFs) Rename(o, n string) error               { return os.ErrPermission }
func (fs *NamespaceFs) Chmod(n string, m os.FileMode) error    { return os.ErrPermission }
func (fs *NamespaceFs) Chown(n string, u, g int) error         { return os.ErrPermission }
func (fs *NamespaceFs) Chtimes(n string, a, m time.Time) error { return os.ErrPermission }

// NewFileInfo creates an os.FileInfo for virtual files and directories.
func NewFileInfo(name string, mode os.FileMode, isDir bool) os.FileInfo {
	return &nsFileInfo{name: name, mode: mode, isDir: isDir}
}

// NewFileInfoSize is like NewFileInfo but reports a non-zero file size.
func NewFileInfoSize(name string, mode os.FileMode, size int64) os.FileInfo {
	return &nsFileInfo{name: name, mode: mode, size: size}
}

// nsFileInfo is the os.FileInfo implementation for namespace entries.
type nsFileInfo struct {
	name  string
	mode  os.FileMode
	isDir bool
	size  int64
}

func (fi *nsFileInfo) Name() string { return fi.name }
func (fi *nsFileInfo) Size() int64  { return fi.size }
func (fi *nsFileInfo) Mode() os.FileMode {
	if fi.isDir {
		return os.ModeDir | fi.mode
	}
	return fi.mode
}
func (fi *nsFileInfo) ModTime() time.Time { return time.Time{} }
func (fi *nsFileInfo) IsDir() bool        { return fi.isDir }
func (fi *nsFileInfo) Sys() any           { return nil }

// nsDir is the afero.File returned when opening the namespace root directory.
type nsDir struct {
	FileStub
	rootName string
	entries  []FileEntry
	offset   int
}

func (d *nsDir) Name() string { return "/" }
func (d *nsDir) Stat() (os.FileInfo, error) {
	return &nsFileInfo{name: d.rootName, isDir: true, mode: 0555}, nil
}

func (d *nsDir) Readdir(count int) ([]os.FileInfo, error) {
	all := d.entries
	if count <= 0 {
		return nsEntryInfos(all), nil
	}
	if d.offset >= len(all) {
		return nil, io.EOF
	}
	end := min(d.offset+count, len(all))
	infos := nsEntryInfos(all[d.offset:end])
	d.offset = end
	return infos, nil
}

func (d *nsDir) Readdirnames(n int) ([]string, error) {
	infos, err := d.Readdir(n)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(infos))
	for i, fi := range infos {
		names[i] = fi.Name()
	}
	return names, err
}

func nsEntryInfos(entries []FileEntry) []os.FileInfo {
	infos := make([]os.FileInfo, 0, len(entries))
	for i := range entries {
		e := &entries[i]
		if e.Active != nil && !e.Active() {
			continue
		}
		infos = append(infos, &nsFileInfo{name: e.Name, mode: e.Mode, isDir: e.IsDir})
	}
	return infos
}
