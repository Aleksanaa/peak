package main

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/pkg/sftp"
	"github.com/spf13/afero"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type SSHClient struct {
	ssh  *ssh.Client
	sftp *sftp.Client
}

type SftpMountFs struct {
	conns sync.Map // string -> *SSHClient
}

func NewSftpMountFs() *SftpMountFs {
	return &SftpMountFs{}
}

func (s *SftpMountFs) getClient(connStr string) (*SSHClient, error) {
	if val, ok := s.conns.Load(connStr); ok {
		return val.(*SSHClient), nil
	}

	userStr, host, _ := strings.Cut(connStr, "@")
	if host == "" {
		host, userStr = userStr, os.Getenv("USER")
		if userStr == "" {
			if u, err := user.Current(); err == nil {
				userStr = u.Username
			} else {
				userStr = "root"
			}
		}
	}
	if idx := strings.LastIndex(host, "::"); idx != -1 {
		host = host[:idx] + ":" + host[idx+2:]
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
		return nil, fmt.Errorf("Unable to connect to remote host: %v", err)
	}

	type sftpRes struct {
		client *sftp.Client
		err    error
	}
	ch := make(chan sftpRes, 1)
	go func() {
		sc, err := sftp.NewClient(sshConn)
		ch <- sftpRes{sc, err}
	}()

	var sftpClient *sftp.Client
	select {
	case res := <-ch:
		if res.err != nil {
			sshConn.Close()
			return nil, fmt.Errorf("Unable to connect to remote host: %v", res.err)
		}
		sftpClient = res.client
	case <-time.After(10 * time.Second):
		sshConn.Close()
		return nil, fmt.Errorf("Unable to connect to remote host: sftp connection timeout")
	}

	client := &SSHClient{ssh: sshConn, sftp: sftpClient}
	s.conns.Store(connStr, client)
	return client, nil
}

func (s *SftpMountFs) parse(name string) (string, string) {
	name = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(name)), "/")
	if name == "" || name == "." {
		return "", ""
	}
	parts := strings.SplitN(name, "/", 2)
	if len(parts) == 1 {
		return parts[0], "/"
	}
	return parts[0], "/" + parts[1]
}

func (s *SftpMountFs) withClient(name string, fn func(cli *sftp.Client, rel string) error) error {
	conn, rel := s.parse(name)
	if conn == "" {
		return os.ErrInvalid
	}
	client, err := s.getClient(conn)
	if err != nil {
		return err
	}
	return fn(client.sftp, rel)
}

func (s *SftpMountFs) Stat(name string) (os.FileInfo, error) {
	conn, rel := s.parse(name)
	if conn == "" {
		return &SimpleFileInfo{name: "ssh", isDir: true}, nil
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
	return &SimpleFileInfo{name: filepath.Base(name), isDir: fi.IsDir(), size: fi.Size(), modTime: fi.ModTime(), mode: fi.Mode()}, nil
}

func (s *SftpMountFs) Open(name string) (afero.File, error) {
	return s.OpenFile(name, os.O_RDONLY, 0)
}

func (s *SftpMountFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	conn, rel := s.parse(name)
	if conn == "" {
		var entries []os.FileInfo
		s.conns.Range(func(k, v interface{}) bool {
			entries = append(entries, &SimpleFileInfo{name: k.(string), isDir: true})
			return true
		})
		return &memDirFile{name: "ssh", entries: entries}, nil
	}

	client, err := s.getClient(conn)
	if err != nil {
		return nil, err
	}

	if rel == "" || rel == "/" {
		return &sftpFile{client: client.sftp, name: "/", isDir: true}, nil
	}
	fi, err := client.sftp.Stat(rel)
	if err == nil && fi.IsDir() {
		return &sftpFile{client: client.sftp, name: rel, isDir: true}, nil
	}

	f, err := client.sftp.OpenFile(rel, flag)
	if err != nil {
		return nil, err
	}
	return &sftpFile{File: f, client: client.sftp, name: rel}, nil
}

func (s *SftpMountFs) Remove(n string) error {
	return s.withClient(n, func(c *sftp.Client, r string) error { return c.Remove(r) })
}
func (s *SftpMountFs) RemoveAll(n string) error { return s.Remove(n) }
func (s *SftpMountFs) Create(n string) (afero.File, error) {
	return s.OpenFile(n, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
}
func (s *SftpMountFs) Mkdir(n string, p os.FileMode) error {
	return s.withClient(n, func(c *sftp.Client, r string) error { return c.Mkdir(r) })
}
func (s *SftpMountFs) MkdirAll(n string, p os.FileMode) error { return s.Mkdir(n, p) }
func (s *SftpMountFs) Rename(o, n string) error {
	oc, or := s.parse(o)
	nc, nr := s.parse(n)
	if oc != nc || oc == "" {
		return fmt.Errorf("cross-fs rename")
	}
	cli, err := s.getClient(oc)
	if err != nil {
		return err
	}
	return cli.sftp.Rename(or, nr)
}
func (s *SftpMountFs) Chmod(n string, m os.FileMode) error {
	return s.withClient(n, func(c *sftp.Client, r string) error { return c.Chmod(r, m) })
}
func (s *SftpMountFs) Chown(n string, u, g int) error {
	return s.withClient(n, func(c *sftp.Client, r string) error { return c.Chown(r, u, g) })
}
func (s *SftpMountFs) Chtimes(n string, a, m time.Time) error {
	return s.withClient(n, func(c *sftp.Client, r string) error { return c.Chtimes(r, a, m) })
}
func (s *SftpMountFs) Name() string { return "SftpMountFs" }

func (s *SftpMountFs) Run(path, cmd, input string, winid int) (string, error) {
	conn, rel := s.parse(path)
	if conn == "" {
		return "", fmt.Errorf("not an ssh path")
	}
	client, err := s.getClient(conn)
	if err != nil {
		return "", err
	}

	session, err := client.ssh.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	if input != "" {
		session.Stdin = strings.NewReader(input)
	}

	dir := rel
	if info, err := client.sftp.Stat(rel); err == nil && !info.IsDir() {
		dir = filepath.Dir(rel)
	}

	remoteCmd := fmt.Sprintf("cd %s && env %s %s sh -c %s",
		shellescape.Quote(dir),
		shellescape.Quote("samfile="+rel),
		shellescape.Quote(fmt.Sprintf("winid=%d", winid)),
		shellescape.Quote(cmd))

	out, err := session.CombinedOutput(remoteCmd)
	return string(out), err
}

type sftpFile struct {
	*sftp.File
	client  *sftp.Client
	name    string
	isDir   bool
	offset  int
	entries []os.FileInfo
}

func (f *sftpFile) Name() string { return filepath.Base(f.name) }
func (f *sftpFile) Readdir(count int) ([]os.FileInfo, error) {
	if f.entries == nil {
		raw, err := f.client.ReadDir(f.name)
		if err != nil {
			return nil, err
		}
		f.entries = convertEntries(raw)
	}
	return sliceReaddir(&f.offset, f.entries, count)
}
func (f *sftpFile) Readdirnames(n int) ([]string, error) { return nil, nil }
func (f *sftpFile) Stat() (os.FileInfo, error) {
	if f.isDir {
		return &SimpleFileInfo{name: f.Name(), isDir: true}, nil
	}
	fi, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	return NewFileInfo(fi), nil
}
func (f *sftpFile) Sync() error {
	if f.File == nil {
		return nil
	}
	return f.File.Sync()
}
func (f *sftpFile) Truncate(size int64) error {
	if f.File == nil {
		return os.ErrInvalid
	}
	return f.File.Truncate(size)
}
func (f *sftpFile) WriteString(s string) (ret int, err error) {
	if f.File == nil {
		return 0, os.ErrInvalid
	}
	return f.File.Write([]byte(s))
}
func (f *sftpFile) Write(p []byte) (n int, err error) {
	if f.File == nil {
		return 0, os.ErrInvalid
	}
	return f.File.Write(p)
}
func (f *sftpFile) WriteAt(p []byte, off int64) (n int, err error) {
	if f.File == nil {
		return 0, os.ErrInvalid
	}
	return f.File.WriteAt(p, off)
}
func (f *sftpFile) Close() error {
	if f.File != nil {
		return f.File.Close()
	}
	return nil
}
