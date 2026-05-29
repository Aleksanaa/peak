package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aleksana/peak/internal/vfs/afero"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type SSHClient struct {
	ssh  *ssh.Client
	sftp *sftp.Client
}

type SftpFs struct {
	conns sync.Map // string -> *SSHClient
}

func NewSftpFs() *SftpFs {
	return &SftpFs{}
}

func (s *SftpFs) getClient(connStr string) (*SSHClient, error) {
	if val, ok := s.conns.Load(connStr); ok {
		return val.(*SSHClient), nil
	}

	userStr, host, _ := strings.Cut(connStr, "@")
	if host == "" {
		host = userStr
		u, err := user.Current()
		if err != nil {
			return nil, err
		}
		userStr = u.Username
	}

	if !strings.Contains(host, ":") {
		host += ":22"
	}

	var auths []ssh.AuthMethod
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			auths = append(auths, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}

	config := &ssh.ClientConfig{
		User:            userStr,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	sshConn, err := ssh.Dial("tcp", host, config)
	if err != nil {
		return nil, fmt.Errorf("Unable to connect to %s: %v", host, err)
	}

	sftpClient, err := sftp.NewClient(sshConn)
	if err != nil {
		sshConn.Close()
		return nil, fmt.Errorf("Unable to start SFTP on %s: %v", host, err)
	}

	client := &SSHClient{ssh: sshConn, sftp: sftpClient}
	s.conns.Store(connStr, client)
	return client, nil
}

func (s *SftpFs) parse(name string) (string, string) {
	name = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(name)), "/")
	if name == "" || name == "." {
		return "", ""
	}
	parts := strings.SplitN(name, "/", 2)
	if len(parts) == 1 {
		return parts[0], "/"
	}
	rel := parts[1]
	if rel == "~" || strings.HasPrefix(rel, "~/") {
		rel = strings.TrimPrefix(rel, "~")
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			rel = "."
		}
		return parts[0], rel
	}
	return parts[0], "/" + rel
}

func (s *SftpFs) Stat(name string) (os.FileInfo, error) {
	conn, rel := s.parse(name)
	if conn == "" {
		return nil, os.ErrInvalid
	}
	client, err := s.getClient(conn)
	if err != nil {
		return nil, err
	}
	fi, err := client.sftp.Stat(rel)
	if err != nil {
		if rel == "" || rel == "/" {
			return &SimpleFileInfo{name: conn, isDir: true}, nil
		}
		return nil, err
	}
	return &SimpleFileInfo{name: path.Base(name), isDir: fi.IsDir(), size: fi.Size(), modTime: fi.ModTime(), mode: fi.Mode()}, nil
}

func (s *SftpFs) Open(name string) (afero.File, error) {
	return s.OpenFile(name, os.O_RDONLY, 0)
}

func (s *SftpFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	conn, rel := s.parse(name)
	if conn == "" {
		return nil, os.ErrInvalid
	}
	client, err := s.getClient(conn)
	if err != nil {
		return nil, err
	}
	if rel == "" || rel == "/" {
		return &SftpFile{client: client.sftp, name: "/", isDir: true}, nil
	}
	fi, err := client.sftp.Stat(rel)
	if err == nil && fi.IsDir() {
		return &SftpFile{client: client.sftp, name: rel, isDir: true}, nil
	}
	f, err := client.sftp.OpenFile(rel, flag)
	if err != nil {
		return nil, err
	}
	return &SftpFile{File: f, client: client.sftp, name: rel}, nil
}

// OpenWithStat implements vfs.StatOpener. The FileInfo from a prior Walk is
// passed in so we can skip the sftp.Stat call that OpenFile would otherwise
// need to determine whether this path is a file or directory.
func (s *SftpFs) OpenWithStat(name string, fi os.FileInfo, flag int, perm os.FileMode) (afero.File, error) {
	conn, rel := s.parse(name)
	if conn == "" {
		return nil, os.ErrInvalid
	}
	client, err := s.getClient(conn)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return &SftpFile{client: client.sftp, name: rel, isDir: true}, nil
	}
	f, err := client.sftp.OpenFile(rel, flag)
	if err != nil {
		return nil, err
	}
	return &SftpFile{File: f, client: client.sftp, name: rel}, nil
}

func (s *SftpFs) Remove(n string) error {
	conn, rel := s.parse(n)
	if conn == "" {
		return os.ErrInvalid
	}
	cli, err := s.getClient(conn)
	if err != nil {
		return err
	}
	return cli.sftp.Remove(rel)
}
func (s *SftpFs) RemoveAll(n string) error { return s.Remove(n) }
func (s *SftpFs) Create(n string) (afero.File, error) {
	return s.OpenFile(n, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
}
func (s *SftpFs) Mkdir(n string, p os.FileMode) error {
	conn, rel := s.parse(n)
	if conn == "" {
		return os.ErrInvalid
	}
	cli, err := s.getClient(conn)
	if err != nil {
		return err
	}
	return cli.sftp.Mkdir(rel)
}
func (s *SftpFs) MkdirAll(n string, p os.FileMode) error { return s.Mkdir(n, p) }
func (s *SftpFs) Rename(o, n string) error {
	oc, or := s.parse(o)
	nc, nr := s.parse(n)
	if oc != nc || oc == "" {
		return fmt.Errorf("cross-fs rename not supported")
	}
	cli, err := s.getClient(oc)
	if err != nil {
		return err
	}
	return cli.sftp.Rename(or, nr)
}
func (s *SftpFs) Chmod(n string, m os.FileMode) error {
	conn, rel := s.parse(n)
	if conn == "" {
		return os.ErrInvalid
	}
	cli, err := s.getClient(conn)
	if err != nil {
		return err
	}
	return cli.sftp.Chmod(rel, m)
}
func (s *SftpFs) Chown(n string, u, g int) error {
	conn, rel := s.parse(n)
	if conn == "" {
		return os.ErrInvalid
	}
	cli, err := s.getClient(conn)
	if err != nil {
		return err
	}
	return cli.sftp.Chown(rel, u, g)
}
func (s *SftpFs) Chtimes(n string, a, m time.Time) error {
	conn, rel := s.parse(n)
	if conn == "" {
		return os.ErrInvalid
	}
	cli, err := s.getClient(conn)
	if err != nil {
		return err
	}
	return cli.sftp.Chtimes(rel, a, m)
}
func (s *SftpFs) Name() string { return "SftpFs" }

type SftpFile struct {
	*sftp.File
	client  *sftp.Client
	name    string
	isDir   bool
	offset  int
	entries []os.FileInfo
}

func (f *SftpFile) Name() string { return path.Base(f.name) }
func (f *SftpFile) Readdir(count int) ([]os.FileInfo, error) {
	if f.entries == nil {
		raw, err := f.client.ReadDir(f.name)
		if err != nil {
			return nil, err
		}
		f.entries = make([]os.FileInfo, len(raw))
		for i, fi := range raw {
			f.entries[i] = &SimpleFileInfo{
				name:    fi.Name(),
				isDir:   fi.IsDir(),
				size:    fi.Size(),
				modTime: fi.ModTime(),
				mode:    fi.Mode(),
			}
		}
	}
	if count <= 0 {
		return f.entries, nil
	}
	if f.offset >= len(f.entries) {
		return nil, io.EOF
	}
	end := f.offset + count
	if end > len(f.entries) {
		end = len(f.entries)
	}
	res := f.entries[f.offset:end]
	f.offset = end
	return res, nil
}
func (f *SftpFile) Readdirnames(n int) ([]string, error) {
	entries, err := f.Readdir(n)
	if err != nil {
		return nil, err
	}
	res := make([]string, len(entries))
	for i, e := range entries {
		res[i] = e.Name()
	}
	return res, nil
}
func (f *SftpFile) Stat() (os.FileInfo, error) {
	if f.isDir {
		return &SimpleFileInfo{name: f.Name(), isDir: true}, nil
	}
	fi, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	return &SimpleFileInfo{name: f.Name(), isDir: fi.IsDir(), size: fi.Size(), modTime: fi.ModTime(), mode: fi.Mode()}, nil
}
func (f *SftpFile) Sync() error {
	if f.File == nil {
		return nil
	}
	return nil
}
func (f *SftpFile) Truncate(size int64) error {
	if f.File == nil {
		return os.ErrInvalid
	}
	return f.File.Truncate(size)
}
func (f *SftpFile) WriteString(s string) (ret int, err error) {
	return f.Write([]byte(s))
}
func (f *SftpFile) WriteAt(p []byte, off int64) (n int, err error) {
	if f.File == nil {
		return 0, os.ErrInvalid
	}
	return f.File.WriteAt(p, off)
}
func (f *SftpFile) Close() error {
	if f.File != nil {
		return f.File.Close()
	}
	return nil
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
	if s.mode != 0 {
		return s.mode
	}
	if s.isDir {
		return os.ModeDir | 0755
	}
	return 0644
}

type MemDirFile struct {
	name    string
	entries []os.FileInfo
	offset  int
}

func (v *MemDirFile) Close() error                                   { return nil }
func (v *MemDirFile) Read(p []byte) (n int, err error)               { return 0, io.EOF }
func (v *MemDirFile) ReadAt(p []byte, off int64) (n int, err error)  { return 0, io.EOF }
func (v *MemDirFile) Seek(offset int64, whence int) (int64, error)   { return 0, nil }
func (v *MemDirFile) Write(p []byte) (n int, err error)              { return 0, os.ErrPermission }
func (v *MemDirFile) WriteAt(p []byte, off int64) (n int, err error) { return 0, os.ErrPermission }
func (v *MemDirFile) Name() string                                   { return v.name }
func (v *MemDirFile) Readdir(count int) ([]os.FileInfo, error) {
	if count <= 0 {
		return v.entries, nil
	}
	if v.offset >= len(v.entries) {
		return nil, io.EOF
	}
	end := v.offset + count
	if end > len(v.entries) {
		end = len(v.entries)
	}
	res := v.entries[v.offset:end]
	v.offset = end
	return res, nil
}
func (v *MemDirFile) Readdirnames(n int) ([]string, error) {
	entries, err := v.Readdir(n)
	if err != nil {
		return nil, err
	}
	res := make([]string, len(entries))
	for i, e := range entries {
		res[i] = e.Name()
	}
	return res, nil
}
func (v *MemDirFile) Stat() (os.FileInfo, error) {
	return &SimpleFileInfo{name: v.name, isDir: true}, nil
}
func (v *MemDirFile) Sync() error                               { return nil }
func (v *MemDirFile) Truncate(size int64) error                 { return os.ErrPermission }
func (v *MemDirFile) WriteString(s string) (ret int, err error) { return 0, os.ErrPermission }
