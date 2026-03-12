package main

import (
	"embed"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/knusbaum/go9p"
	p9fs "github.com/knusbaum/go9p/fs"
	"github.com/knusbaum/go9p/proto"
)

//go:embed doc
var docFS embed.FS

// NineP handles the 9P filesystem for Peak.
type NineP struct {
	editor *Editor
	fs     *p9fs.FS
	root   *p9fs.StaticDir
	ssh    *SSHManager
}

func NewNineP(e *Editor) *NineP {
	// func NewFS(rootUser, rootGroup string, rootPerms uint32, opts ...Option) (*FS, *StaticDir)
	gfs, root := p9fs.NewFS("user", "user", 0755)
	p := &NineP{
		editor: e,
		fs:     gfs,
		root:   root,
		ssh:    NewSSHManager(),
	}

	// Create /index
	indexStat := p.fs.NewStat("index", "user", "user", 0444)
	index := &indexFile{
		BaseFile: *p9fs.NewBaseFile(indexStat),
		p:        p,
	}
	root.AddChild(index)

	// Create /doc
	docDir := p9fs.NewStaticDir(p.fs.NewStat("doc", "user", "user", 0755|proto.DMDIR))
	root.AddChild(docDir)

	// Populate /doc from docFS
	entries, _ := docFS.ReadDir("doc")
	for _, entry := range entries {
		if !entry.IsDir() {
			name := entry.Name()
			data, _ := docFS.ReadFile("doc/" + name)
			fileStat := p.fs.NewStat(name, "user", "user", 0444)
			docDir.AddChild(p9fs.NewStaticFile(fileStat, data))
		}
	}

	// Create /ssh
	root.AddChild(NewSSHDir(p.fs, p.ssh))

	return p
}

func (p *NineP) Listen() {
	user, err := os.UserHomeDir()
	if err != nil {
		return
	}
	sockDir := filepath.Join(user, ".peak")
	os.MkdirAll(sockDir, 0700)
	sockPath := filepath.Join(sockDir, "9p")
	os.Remove(sockPath)

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		log.Printf("failed to listen on %s: %v", sockPath, err)
		return
	}

	go func() {
		defer l.Close()
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go go9p.ServeReadWriter(conn, conn, &srvWrapper{
				srv: p.fs.Server(),
				ed:  p.editor,
			})
		}
	}()
}

type srvWrapper struct {
	srv go9p.Srv
	ed  *Editor
}

func (s *srvWrapper) NewConn() go9p.Conn {
	return s.srv.NewConn()
}

func (s *srvWrapper) Version(c go9p.Conn, t *proto.TRVersion) (proto.FCall, error) {
	if strings.HasPrefix(t.Version, "9P2000") {
		t.Version = "9P2000"
	}
	return s.srv.Version(c, t)
}

func (s *srvWrapper) Auth(c go9p.Conn, t *proto.TAuth) (proto.FCall, error) {
	var ret proto.FCall
	var err error
	s.ed.Call(func() {
		ret, err = s.srv.Auth(c, t)
	})
	return ret, err
}

func (s *srvWrapper) Attach(c go9p.Conn, t *proto.TAttach) (proto.FCall, error) {
	var ret proto.FCall
	var err error
	s.ed.Call(func() {
		ret, err = s.srv.Attach(c, t)
	})
	return ret, err
}

func (s *srvWrapper) Walk(c go9p.Conn, t *proto.TWalk) (proto.FCall, error) {
	var ret proto.FCall
	var err error
	s.ed.Call(func() {
		ret, err = s.srv.Walk(c, t)
	})
	return ret, err
}

func (s *srvWrapper) Open(c go9p.Conn, t *proto.TOpen) (proto.FCall, error) {
	var ret proto.FCall
	var err error
	s.ed.Call(func() {
		ret, err = s.srv.Open(c, t)
	})
	return ret, err
}

func (s *srvWrapper) Create(c go9p.Conn, t *proto.TCreate) (proto.FCall, error) {
	var ret proto.FCall
	var err error
	s.ed.Call(func() {
		ret, err = s.srv.Create(c, t)
	})
	return ret, err
}

func (s *srvWrapper) Read(c go9p.Conn, t *proto.TRead) (proto.FCall, error) {
	var ret proto.FCall
	var err error
	s.ed.Call(func() {
		ret, err = s.srv.Read(c, t)
	})
	return ret, err
}

func (s *srvWrapper) Write(c go9p.Conn, t *proto.TWrite) (proto.FCall, error) {
	var ret proto.FCall
	var err error
	s.ed.Call(func() {
		ret, err = s.srv.Write(c, t)
	})
	return ret, err
}

func (s *srvWrapper) Clunk(c go9p.Conn, t *proto.TClunk) (proto.FCall, error) {
	var ret proto.FCall
	var err error
	s.ed.Call(func() {
		ret, err = s.srv.Clunk(c, t)
	})
	return ret, err
}

func (s *srvWrapper) Remove(c go9p.Conn, t *proto.TRemove) (proto.FCall, error) {
	var ret proto.FCall
	var err error
	s.ed.Call(func() {
		ret, err = s.srv.Remove(c, t)
	})
	return ret, err
}

func (s *srvWrapper) Stat(c go9p.Conn, t *proto.TStat) (proto.FCall, error) {
	var ret proto.FCall
	var err error
	s.ed.Call(func() {
		ret, err = s.srv.Stat(c, t)
	})
	return ret, err
}

func (s *srvWrapper) Wstat(c go9p.Conn, t *proto.TWstat) (proto.FCall, error) {
	var ret proto.FCall
	var err error
	s.ed.Call(func() {
		ret, err = s.srv.Wstat(c, t)
	})
	return ret, err
}

func (p *NineP) getIndex() string {
	var sb strings.Builder
	if p.editor != nil {
		// ASSUME LOCKED by caller (either srvWrapper or TUI thread)
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

				fmt.Fprintf(&sb, "%11d %11d %11d %11d %11d ", win.ID, tagLen, bodyLen, isdir, dirty)

				if i := strings.Index(tagText, "\n"); i >= 0 {
					tagText = tagText[:i]
				}
				sb.WriteString(tagText)
				sb.WriteString("\n")
			}
		}
	}
	return sb.String()
}

type indexFile struct {
	p9fs.BaseFile
	p *NineP
}

func (f *indexFile) Stat() proto.Stat {
	content := f.p.getIndex()
	s := f.BaseFile.Stat()
	s.Length = uint64(len(content))
	return s
}

func (f *indexFile) Open(fid uint64, omode proto.Mode) error {
	return nil
}

func (f *indexFile) Read(fid uint64, offset uint64, count uint64) ([]byte, error) {
	content := f.p.getIndex()
	if offset >= uint64(len(content)) {
		return []byte{}, nil
	}
	end := offset + count
	if end > uint64(len(content)) {
		end = uint64(len(content))
	}
	return []byte(content[offset:end]), nil
}

func (f *indexFile) Write(fid uint64, offset uint64, data []byte) (uint32, error) {
	return 0, fmt.Errorf("read-only")
}

func (f *indexFile) Close(fid uint64) error {
	return nil
}

// Internal VFS access for path.go

func (p *NineP) findNode(path string) (p9fs.FSNode, error) {
	path = strings.TrimPrefix(path, "/peak")
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return p.root, nil
	}
	parts := strings.Split(path, "/")
	var current p9fs.FSNode = p.root
	for _, part := range parts {
		if part == "" {
			continue
		}
		// Check for Children() first (StaticDir)
		if dir, ok := current.(p9fs.Dir); ok {
			if children := dir.Children(); children != nil {
				if next, ok := children[part]; ok {
					current = next
					continue
				}
			}
		}
		// Fallback to Walk() (dynamic nodes like SSHDir)
		if walker, ok := current.(interface {
			Walk(string) (p9fs.FSNode, error)
		}); ok {
			next, err := walker.Walk(part)
			if err == nil {
				current = next
				continue
			}
		}
		return nil, os.ErrNotExist
	}
	return current, nil
}

func (p *NineP) ReadInternal(path string) ([]byte, error) {
	node, err := p.findNode(path)
	if err != nil {
		return nil, err
	}
	file, ok := node.(p9fs.File)
	if !ok {
		return nil, os.ErrInvalid
	}

	// Use a dummy fid for internal reading
	const dummyFid = 0
	if err := file.Open(dummyFid, proto.Oread); err != nil {
		return nil, err
	}
	defer file.Close(dummyFid)

	var result []byte
	var offset uint64
	for {
		chunk, err := file.Read(dummyFid, offset, 8192)
		if err != nil {
			return nil, err
		}
		if len(chunk) == 0 {
			break
		}
		result = append(result, chunk...)
		offset += uint64(len(chunk))
	}
	return result, nil
}

func (p *NineP) WriteInternal(path string, data []byte) error {
	node, err := p.findNode(path)
	if err != nil {
		return err
	}
	file, ok := node.(p9fs.File)
	if !ok {
		return os.ErrInvalid
	}

	const dummyFid = 0
	if err := file.Open(dummyFid, proto.Owrite|proto.Otrunc); err != nil {
		return err
	}
	defer file.Close(dummyFid)

	var offset uint64
	for len(data) > 0 {
		n, err := file.Write(dummyFid, offset, data)
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("zero write")
		}
		data = data[n:]
		offset += uint64(n)
	}
	return nil
}

func (p *NineP) IsDirInternal(path string) bool {
	node, err := p.findNode(path)
	if err != nil {
		return false
	}
	return (node.Stat().Mode & proto.DMDIR) != 0
}

func (p *NineP) IsFileInternal(path string) bool {
	node, err := p.findNode(path)
	if err != nil {
		return false
	}
	return (node.Stat().Mode & proto.DMDIR) == 0
}

func (p *NineP) ListDirInternal(path string) (string, error) {
	node, err := p.findNode(path)
	if err != nil {
		return "", err
	}
	dir, ok := node.(p9fs.Dir)
	if !ok {
		return "", os.ErrInvalid
	}

	children := dir.Children()
	names := make([]string, 0, len(children))
	for name, child := range children {
		if (child.Stat().Mode & proto.DMDIR) != 0 {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)

	var sb strings.Builder
	for _, name := range names {
		sb.WriteString(name + "\n")
	}
	return sb.String(), nil
}

func (p *NineP) RunInternal(path, cmd, input string, winid int) (string, error) {
	node, err := p.findNode(path)
	if err != nil {
		return "", err
	}
	if runner, ok := node.(interface {
		Run(cmd, input string, winid int) (string, error)
	}); ok {
		return runner.Run(cmd, input, winid)
	}
	return "", fmt.Errorf("%s: does not support command execution", path)
}
