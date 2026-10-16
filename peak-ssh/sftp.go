package main

import (
	"bytes"
	"cmp"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

type SSHClient struct {
	ssh  *ssh.Client
	sftp *sftp.Client
}

type SftpFs struct {
	conns     sync.Map // string -> *SSHClient
	refused   sync.Map // host -> refusal
	statMu    sync.RWMutex
	statCache map[string]os.FileInfo // "conn:path" -> FileInfo, populated by Readdir
	dirKeys   map[string][]string    // "conn:dirPath" -> child keys in statCache
}

func NewSftpFs() *SftpFs {
	return &SftpFs{
		statCache: make(map[string]os.FileInfo),
		dirKeys:   make(map[string][]string),
	}
}

func (s *SftpFs) invalidate(conn, p string) {
	s.statMu.Lock()
	defer s.statMu.Unlock()
	delete(s.statCache, conn+":"+p)
}

func (s *SftpFs) cacheEntries(conn, dir string, entries []os.FileInfo) {
	s.statMu.Lock()
	defer s.statMu.Unlock()
	dk := conn + ":" + dir
	for _, k := range s.dirKeys[dk] {
		delete(s.statCache, k)
	}
	keys := make([]string, len(entries))
	for i, fi := range entries {
		k := conn + ":" + path.Join(dir, fi.Name())
		s.statCache[k] = fi
		keys[i] = k
	}
	s.dirKeys[dk] = keys
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

	check, algos, err := s.hostKeys(host)
	if err != nil {
		return nil, err
	}
	config := &ssh.ClientConfig{
		User:              userStr,
		Auth:              auths,
		HostKeyCallback:   check,
		HostKeyAlgorithms: algos,
		Timeout:           10 * time.Second,
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

// hostKeys checks host's key against ~/.ssh/known_hosts. An unknown host's
// key is refused, then trusted if offered again a second or more later.
func (s *SftpFs) hostKeys(host string) (ssh.HostKeyCallback, []string, error) {
	home, _ := os.UserHomeDir()
	file := filepath.Join(home, ".ssh", "known_hosts")
	check, err := knownhosts.New(file)
	if errors.Is(err, fs.ErrNotExist) {
		check, err = knownhosts.New()
	}
	if err != nil {
		return nil, nil, err
	}
	trust := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := check(hostname, remote, key)
		if keyErr, ok := errors.AsType[*knownhosts.KeyError](err); !ok || len(keyErr.Want) > 0 {
			return err
		}
		r, ok := s.refused.LoadAndDelete(host)
		if !ok || !bytes.Equal(r.(refusal).key.Marshal(), key.Marshal()) || time.Since(r.(refusal).at) < time.Second {
			s.refused.Store(host, refusal{key, time.Now()})
			return fmt.Errorf("unknown host key %s; connect again to trust it", ssh.FingerprintSHA256(key))
		}
		return addKnownHost(file, hostname, key)
	}
	return trust, knownAlgos(check, host), nil
}

// knownAlgos returns the algorithms of the keys known for host, so that it
// offers one of them: Go would rather ask for RSA than ed25519.
func knownAlgos(check ssh.HostKeyCallback, host string) []string {
	// A key no host has is refused with the keys host has.
	pub, _, _ := ed25519.GenerateKey(nil)
	probe, _ := ssh.NewPublicKey(pub)
	var algos []string
	if keyErr, ok := errors.AsType[*knownhosts.KeyError](check(host, &net.TCPAddr{}, probe)); ok {
		for _, k := range keyErr.Want {
			if t := k.Key.Type(); t == ssh.KeyAlgoRSA {
				algos = append(algos, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256)
			} else {
				algos = append(algos, t)
			}
		}
	}
	return algos
}

// A refusal is an unknown host's key and when it was refused.
type refusal struct {
	key ssh.PublicKey
	at  time.Time
}

// addKnownHost adds hostname, known by key, to the known_hosts file.
func addKnownHost(file, hostname string, key ssh.PublicKey) error {
	os.MkdirAll(filepath.Dir(file), 0700)
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(f, knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key))
	return errors.Join(err, f.Close())
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
		return parts[0], cmp.Or(rel, ".")
	}
	return parts[0], "/" + rel
}

func (s *SftpFs) Stat(name string) (os.FileInfo, error) {
	conn, rel := s.parse(name)
	if conn == "" {
		return nil, os.ErrInvalid
	}
	s.statMu.RLock()
	fi, cached := s.statCache[conn+":"+rel]
	s.statMu.RUnlock()
	if cached && fi.Mode()&os.ModeSymlink == 0 {
		return fi, nil
	}
	client, err := s.getClient(conn)
	if err != nil {
		return nil, err
	}
	sfi, err := client.sftp.Stat(rel)
	if err != nil {
		if rel == "" || rel == "/" {
			return vfs.NewFileInfo(conn, 0755, true), nil
		}
		return nil, err
	}
	return named{sfi, path.Base(name)}, nil
}

// named is a FileInfo under another name: sftp names a file by the path it
// was asked for, which for the remote home (~) or root is not its name here.
type named struct {
	os.FileInfo
	name string
}

func (n named) Name() string { return n.name }

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
		return &SftpFile{client: client.sftp, fs: s, conn: conn, name: "/", isDir: true}, nil
	}
	fi, err := client.sftp.Stat(rel)
	if err == nil && fi.IsDir() {
		return &SftpFile{client: client.sftp, fs: s, conn: conn, name: rel, isDir: true}, nil
	}
	f, err := client.sftp.OpenFile(rel, flag)
	if err != nil {
		return nil, err
	}
	return &SftpFile{File: f, client: client.sftp, fs: s, conn: conn, name: rel}, nil
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
		return &SftpFile{client: client.sftp, fs: s, conn: conn, name: rel, isDir: true}, nil
	}
	f, err := client.sftp.OpenFile(rel, flag)
	if err != nil {
		return nil, err
	}
	return &SftpFile{File: f, client: client.sftp, fs: s, conn: conn, name: rel}, nil
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
	client *sftp.Client
	fs     *SftpFs
	conn   string
	name   string
	isDir  bool
	dir    *vfs.DirFile // the listing, once read
}

func (f *SftpFile) Name() string { return path.Base(f.name) }

// listing reads the directory, once, and caches its entries for Stat.
func (f *SftpFile) listing() (*vfs.DirFile, error) {
	if f.dir == nil {
		entries, err := f.client.ReadDir(f.name)
		if err != nil {
			f.fs.invalidate(f.conn, f.name)
			return nil, err
		}
		f.fs.cacheEntries(f.conn, f.name, entries)
		f.dir = &vfs.DirFile{Info: vfs.NewFileInfo(f.Name(), 0755, true), Entries: entries}
	}
	return f.dir, nil
}

func (f *SftpFile) Readdir(count int) ([]os.FileInfo, error) {
	d, err := f.listing()
	if err != nil {
		return nil, err
	}
	return d.Readdir(count)
}

func (f *SftpFile) Readdirnames(count int) ([]string, error) {
	d, err := f.listing()
	if err != nil {
		return nil, err
	}
	return d.Readdirnames(count)
}

func (f *SftpFile) Stat() (os.FileInfo, error) {
	if f.isDir {
		return vfs.NewFileInfo(f.Name(), 0755, true), nil
	}
	return f.File.Stat()
}
func (f *SftpFile) Sync() error { return nil }
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
