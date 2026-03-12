package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"

	p9fs "github.com/knusbaum/go9p/fs"
	"github.com/knusbaum/go9p/proto"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type SSHClient struct {
	conn   *ssh.Client
	sftp   *sftp.Client
	target string
	reqs   chan func()
	files  map[uint64]*sftp.File
}

func (c *SSHClient) loop() {
	for f := range c.reqs {
		f()
	}
}

func (c *SSHClient) call(f func()) {
	done := make(chan struct{})
	c.reqs <- func() {
		f()
		close(done)
	}
	<-done
}

type SSHManager struct {
	clients sync.Map // string -> *SSHClient
}

func NewSSHManager() *SSHManager {
	return &SSHManager{}
}

func (m *SSHManager) connect(target string) (*SSHClient, error) {
	log.Printf("SSH: Connecting to %s", target)
	var username string
	host := target
	if idx := strings.Index(target, "@"); idx != -1 {
		username = target[:idx]
		host = target[idx+1:]
	} else {
		u, err := user.Current()
		if err != nil {
			return nil, err
		}
		username = u.Username
	}

	auths := []ssh.AuthMethod{}
	if aconn, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK")); err == nil {
		auths = append(auths, ssh.PublicKeysCallback(agent.NewClient(aconn).Signers))
	}

	config := &ssh.ClientConfig{
		User:            username,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	addr := host
	if !filepath.IsAbs(host) && !strings.HasPrefix(host, "[") && strings.Index(host, ":") == -1 {
		addr = host + ":22"
	}

	conn, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, err
	}

	sftpClient, err := sftp.NewClient(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}

	log.Printf("SSH: Successfully connected to %s", target)
	client := &SSHClient{
		conn:   conn,
		sftp:   sftpClient,
		target: target,
		reqs:   make(chan func()),
		files:  make(map[uint64]*sftp.File),
	}
	go client.loop()
	return client, nil
}

func (m *SSHManager) GetClient(target string) (*SSHClient, error) {
	if c, ok := m.clients.Load(target); ok {
		return c.(*SSHClient), nil
	}
	client, err := m.connect(target)
	if err != nil {
		return nil, err
	}
	m.clients.Store(target, client)
	return client, nil
}

// SSHDir is the /peak/ssh directory.
type SSHDir struct {
	p9fs.BaseFile
	manager *SSHManager
	fs      *p9fs.FS
}

func NewSSHDir(fs *p9fs.FS, manager *SSHManager) *SSHDir {
	stat := fs.NewStat("ssh", "user", "user", 0755|proto.DMDIR)
	return &SSHDir{
		BaseFile: *p9fs.NewBaseFile(stat),
		manager:  manager,
		fs:       fs,
	}
}

func (d *SSHDir) Children() map[string]p9fs.FSNode {
	return nil
}

func (d *SSHDir) Walk(name string) (p9fs.FSNode, error) {
	client, err := d.manager.GetClient(name)
	if err != nil {
		return nil, err
	}

	stat := &proto.Stat{
		Name: name,
		Uid:  "user",
		Gid:  "user",
		Muid: "user",
		Mode: 0755 | proto.DMDIR,
	}
	return &SFTPNode{
		BaseFile: *p9fs.NewBaseFile(stat),
		manager:  d.manager,
		target:   name,
		path:     "/",
		isDir:    true,
		fs:       d.fs,
		client:   client,
	}, nil
}

// SFTPNode implements a 9P node backed by SFTP.
type SFTPNode struct {
	p9fs.BaseFile
	manager *SSHManager
	target  string
	path    string
	isDir   bool
	fs      *p9fs.FS
	client  *SSHClient
}

func (n *SFTPNode) Stat() proto.Stat {
	s := n.BaseFile.Stat()
	n.client.call(func() {
		if info, err := n.client.sftp.Stat(n.path); err == nil {
			s.Length = uint64(info.Size())
			if info.IsDir() {
				s.Mode |= proto.DMDIR
			} else {
				s.Mode &= ^uint32(proto.DMDIR)
			}
		}
	})
	return s
}

func (n *SFTPNode) Children() map[string]p9fs.FSNode {
	if !n.isDir {
		return nil
	}
	var entries []os.FileInfo
	n.client.call(func() {
		entries, _ = n.client.sftp.ReadDir(n.path)
	})

	res := make(map[string]p9fs.FSNode)
	for _, e := range entries {
		name := e.Name()
		if name == "." || name == ".." {
			continue
		}
		full := filepath.Join(n.path, name)
		mode := uint32(0644)
		if e.IsDir() {
			mode = 0755 | proto.DMDIR
		}
		stat := &proto.Stat{
			Name: name,
			Uid:  "user",
			Gid:  "user",
			Muid: "user",
			Mode: mode,
		}
		node := &SFTPNode{
			BaseFile: *p9fs.NewBaseFile(stat),
			manager:  n.manager,
			target:   n.target,
			path:     full,
			isDir:    e.IsDir(),
			fs:       n.fs,
			client:   n.client,
		}
		res[name] = node
	}
	return res
}

func (n *SFTPNode) Walk(name string) (p9fs.FSNode, error) {
	if !n.isDir {
		return nil, os.ErrInvalid
	}
	full := filepath.Join(n.path, name)
	var info os.FileInfo
	var err error
	n.client.call(func() {
		info, err = n.client.sftp.Stat(full)
	})
	if err != nil {
		return nil, err
	}
	mode := uint32(0644)
	if info.IsDir() {
		mode = 0755 | proto.DMDIR
	}
	stat := &proto.Stat{
		Name: name,
		Uid:  "user",
		Gid:  "user",
		Muid: "user",
		Mode: mode,
	}
	return &SFTPNode{
		BaseFile: *p9fs.NewBaseFile(stat),
		manager:  n.manager,
		target:   name,
		path:     full,
		isDir:    info.IsDir(),
		fs:       n.fs,
		client:   n.client,
	}, nil
}

func (n *SFTPNode) Open(fid uint64, omode proto.Mode) error {
	if n.isDir {
		return nil
	}
	var err error
	n.client.call(func() {
		flags := os.O_RDONLY
		switch omode & 3 {
		case proto.Owrite:
			flags = os.O_WRONLY
		case proto.Ordwr:
			flags = os.O_RDWR
		}
		if omode&proto.Otrunc != 0 {
			flags |= os.O_TRUNC
		}
		f, e := n.client.sftp.OpenFile(n.path, flags)
		if e != nil {
			err = e
			return
		}
		n.client.files[fid] = f
	})
	return err
}

func (n *SFTPNode) Read(fid uint64, offset uint64, count uint64) ([]byte, error) {
	if n.isDir {
		return nil, fmt.Errorf("is directory")
	}
	var data []byte
	var err error
	n.client.call(func() {
		f, ok := n.client.files[fid]
		if !ok {
			err = fmt.Errorf("not open")
			return
		}
		buf := make([]byte, count)
		nr, e := f.ReadAt(buf, int64(offset))
		if e != nil && e != io.EOF {
			err = e
			return
		}
		data = buf[:nr]
	})
	return data, err
}

func (n *SFTPNode) Write(fid uint64, offset uint64, data []byte) (uint32, error) {
	if n.isDir {
		return 0, fmt.Errorf("is directory")
	}
	var nw int
	var err error
	n.client.call(func() {
		f, ok := n.client.files[fid]
		if !ok {
			err = fmt.Errorf("not open")
			return
		}
		nw, err = f.WriteAt(data, int64(offset))
	})
	return uint32(nw), err
}

func (n *SFTPNode) Close(fid uint64) error {
	n.client.call(func() {
		if f, ok := n.client.files[fid]; ok {
			f.Close()
			delete(n.client.files, fid)
		}
	})
	return nil
}
