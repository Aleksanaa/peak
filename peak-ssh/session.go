package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"

	"al.essio.dev/pkg/shellescape"
	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
	"golang.org/x/crypto/ssh"
)

// sshSession holds one remote PTY session and buffers its output for
// offset-based ReadAt (matching the 9P server's read model).
type sshSession struct {
	session *ssh.Session
	stdin   io.WriteCloser

	mu   sync.Mutex
	cond *sync.Cond
	buf  []byte
	done bool
}

func (s *sshSession) pump(r io.Reader) {
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			s.mu.Lock()
			s.buf = append(s.buf, tmp[:n]...)
			s.cond.Signal()
			s.mu.Unlock()
		}
		if err != nil {
			s.mu.Lock()
			s.done = true
			s.cond.Broadcast()
			s.mu.Unlock()
			return
		}
	}
}

func (s *sshSession) readAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for int64(len(s.buf)) <= off && !s.done {
		s.cond.Wait()
	}
	if int64(len(s.buf)) <= off {
		return 0, io.EOF
	}
	return copy(p, s.buf[off:]), nil
}

func (s *sshSession) resize(rows, cols int) { s.session.WindowChange(rows, cols) }
func (s *sshSession) close()                { s.session.Close() }

// ---- hostFs ----

// hostFs serves the peak-ssh 9P namespace with a connect-on-access model.
// The first path component is always a host identifier; the sub-namespace is:
//
//	/<host>/           — synthetic directory
//	/<host>/io         — open to start a new PTY session (connects on open)
//	/<host>/fs/        — SFTP root for this host
//	/<host>/fs/...     — remote files via SFTP (connects on open/stat)
//	/<host>/<n>/       — session n directory
//	/<host>/<n>/io     — session n PTY stream
//	/<host>/<n>/ctl    — resize / kill session n
//	/<host>/<n>/stat   — "open" or "closed"
//
// Stat never triggers a connection except for SFTP file paths under /fs/.
type hostFs struct {
	vfs.FsStub
	sftp *SftpFs

	mu     sync.Mutex
	hosts  map[string]map[int]*sshSession // host → id → session
	nextID map[string]int
}

func newHostFs(sftp *SftpFs) *hostFs {
	return &hostFs{
		sftp:   sftp,
		hosts:  make(map[string]map[int]*sshSession),
		nextID: make(map[string]int),
	}
}

func (fs *hostFs) addSession(host string, sh *sshSession) int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.hosts[host] == nil {
		fs.hosts[host] = make(map[int]*sshSession)
	}
	id := fs.nextID[host]
	fs.nextID[host]++
	fs.hosts[host][id] = sh
	return id
}

func (fs *hostFs) getSession(host string, id int) *sshSession {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if m := fs.hosts[host]; m != nil {
		return m[id]
	}
	return nil
}

// newPTYSession dials host, allocates a PTY-backed shell, and returns the
// session with its assigned ID.
func (fs *hostFs) newPTYSession(host, dir, cmd string) (*sshSession, int, error) {
	client, err := fs.sftp.getClient(host)
	if err != nil {
		return nil, 0, err
	}
	sess, err := client.ssh.NewSession()
	if err != nil {
		return nil, 0, fmt.Errorf("ssh session: %w", err)
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 115200,
		ssh.TTY_OP_OSPEED: 115200,
	}
	if err := sess.RequestPty("xterm-256color", 24, 80, modes); err != nil {
		sess.Close()
		return nil, 0, fmt.Errorf("pty: %w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		return nil, 0, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return nil, 0, fmt.Errorf("stdout pipe: %w", err)
	}
	sh := &sshSession{session: sess, stdin: stdin}
	sh.cond = sync.NewCond(&sh.mu)

	start := sess.Shell
	if dir != "" || cmd != "" {
		run := "exec ${SHELL:-sh}"
		if cmd != "" {
			run = "exec ${SHELL:-sh} -c " + shellescape.Quote(cmd)
		}
		startCmd := remoteCmd(dir, run)
		start = func() error { return sess.Start(startCmd) }
	}
	if err := start(); err != nil {
		sess.Close()
		return nil, 0, fmt.Errorf("shell: %w", err)
	}
	go sh.pump(stdout)

	id := fs.addSession(host, sh)
	return sh, id, nil
}

// remoteCmd builds a shell command safe to run via SSH exec or CombinedOutput.
// It wraps in explicit sh so non-POSIX login shells (e.g. fish) are not an issue.
func remoteCmd(dir, run string) string {
	if dir == "" {
		return "sh -c " + shellescape.Quote(run)
	}
	return "sh -c " + shellescape.Quote(`cd "$1" && `+run) + " sh " + shellescape.Quote(dir)
}

// remoteDir extracts the remote working directory from a "host/fs/..." relPath.
// The "fs/" prefix maps to the remote root; other paths (sessions etc.) return "".
func remoteDir(relPath string) string {
	parts := strings.SplitN(relPath, "/", 2)
	if len(parts) < 2 || !strings.HasPrefix(parts[1], "fs/") {
		return ""
	}
	return path.Clean("/" + parts[1][len("fs/"):])
}

// ---- path parsing ----

const (
	kindRoot    = "root"
	kindNew     = "new"
	kindRun     = "run"
	kindHostDir = "hostdir"
	kindHostIO  = "hostio"
	kindFsRoot  = "fsroot"
	kindFsFile  = "fsfile"
	kindSess    = "sess"
)

// A ppath is a path in hostFs: its kind, the host it is under, and the rest
// of it within the host's fs or the session's directory.
type ppath struct {
	kind   string
	host   string
	sessID int
	rest   string
}

func parsePath(name string) ppath {
	clean := strings.TrimLeft(strings.TrimRight(name, "/"), "/")
	if clean == "" || clean == "." {
		return ppath{kind: kindRoot}
	}
	if clean == "new" {
		return ppath{kind: kindNew}
	}
	if clean == "run" {
		return ppath{kind: kindRun}
	}
	parts := strings.SplitN(clean, "/", 3)
	host := parts[0]
	if len(parts) == 1 {
		return ppath{kind: kindHostDir, host: host}
	}
	second, rest := parts[1], ""
	if len(parts) == 3 {
		rest = parts[2]
	}
	switch second {
	case "io":
		return ppath{kind: kindHostIO, host: host}
	case "fs":
		if rest == "" {
			return ppath{kind: kindFsRoot, host: host}
		}
		return ppath{kind: kindFsFile, host: host, rest: "/" + rest}
	}
	n, err := strconv.Atoi(second)
	if err != nil {
		return ppath{kind: kindRoot}
	}
	return ppath{kind: kindSess, host: host, sessID: n, rest: rest}
}

// sessionDir returns the directory of the session p is in.
func (fs *hostFs) sessionDir(p ppath) (*vfs.NamespaceFs, error) {
	sh := fs.getSession(p.host, p.sessID)
	if sh == nil {
		return nil, os.ErrNotExist
	}
	return sh.dir(p.sessID), nil
}

// ---- Stat: no connection except SFTP file paths ----

func (fs *hostFs) Stat(name string) (os.FileInfo, error) {
	p := parsePath(name)
	switch p.kind {
	case kindRoot:
		return vfs.NewFileInfo(".", 0755, true), nil
	case kindNew:
		return vfs.NewFileInfo("new", 0600, false), nil
	case kindRun:
		return vfs.NewFileInfo("run", 0600, false), nil
	case kindHostDir:
		return vfs.NewFileInfo(p.host, 0755, true), nil
	case kindHostIO:
		return vfs.NewFileInfo("io", 0600, false), nil
	case kindFsRoot:
		return vfs.NewFileInfo("fs", 0755, true), nil
	case kindFsFile:
		return fs.sftp.Stat("/" + p.host + p.rest)
	case kindSess:
		dir, err := fs.sessionDir(p)
		if err != nil {
			return nil, err
		}
		return dir.Stat(p.rest)
	}
	return nil, os.ErrNotExist
}

// ---- Open / OpenFile ----

func (fs *hostFs) Open(name string) (afero.File, error) {
	return fs.OpenFile(name, os.O_RDONLY, 0)
}

func (fs *hostFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	p := parsePath(name)
	switch p.kind {
	case kindRoot:
		return fs.rootDir(), nil
	case kindNew:
		return vfs.NamedFile(&newFile{fs: fs}, "new", 0600, false), nil
	case kindRun:
		return vfs.NamedFile(&runFile{fs: fs}, "run", 0600, false), nil
	case kindHostDir:
		return fs.hostDir(p.host), nil
	case kindHostIO:
		sh, _, err := fs.newPTYSession(p.host, "", "")
		if err != nil {
			return nil, err
		}
		return vfs.NamedFile(&ioFile{session: sh}, "io", 0600, false), nil
	case kindFsRoot:
		f, err := fs.sftp.OpenFile("/"+p.host+"/", flag, perm)
		if err != nil {
			return nil, err
		}
		return vfs.NamedFile(f, "fs", 0755, true), nil
	case kindFsFile:
		return fs.sftp.OpenFile("/"+p.host+p.rest, flag, perm)
	case kindSess:
		dir, err := fs.sessionDir(p)
		if err != nil {
			return nil, err
		}
		return dir.OpenFile(p.rest, flag, perm)
	}
	return nil, os.ErrNotExist
}

func (fs *hostFs) OpenWithStat(name string, fi os.FileInfo, flag int, perm os.FileMode) (afero.File, error) {
	p := parsePath(name)
	switch p.kind {
	case kindFsRoot:
		f, err := fs.sftp.OpenWithStat("/"+p.host+"/", fi, flag, perm)
		if err != nil {
			return nil, err
		}
		return vfs.NamedFile(f, "fs", 0755, true), nil
	case kindFsFile:
		return fs.sftp.OpenWithStat("/"+p.host+p.rest, fi, flag, perm)
	}
	return fs.OpenFile(name, flag, perm)
}

func (fs *hostFs) rootDir() afero.File {
	seen := make(map[string]bool)
	entries := []os.FileInfo{
		vfs.NewFileInfo("new", 0600, false),
		vfs.NewFileInfo("run", 0600, false),
	}
	fs.mu.Lock()
	for host := range fs.hosts {
		seen[host] = true
		entries = append(entries, vfs.NewFileInfo(host, 0755, true))
	}
	fs.mu.Unlock()
	fs.sftp.conns.Range(func(k, _ interface{}) bool {
		h := k.(string)
		if !seen[h] {
			seen[h] = true
			entries = append(entries, vfs.NewFileInfo(h, 0755, true))
		}
		return true
	})
	return &vfs.DirFile{Info: vfs.NewFileInfo(".", 0755, true), Entries: entries}
}

func (fs *hostFs) hostDir(host string) afero.File {
	entries := []os.FileInfo{
		vfs.NewFileInfo("io", 0600, false),
		vfs.NewFileInfo("fs", 0755, true),
	}
	fs.mu.Lock()
	for id := range fs.hosts[host] {
		entries = append(entries, vfs.NewFileInfo(strconv.Itoa(id), 0755, true))
	}
	fs.mu.Unlock()
	return &vfs.DirFile{Info: vfs.NewFileInfo(host, 0755, true), Entries: entries}
}

func (fs *hostFs) Name() string { return "hostFs" }

// ---- session files ----

// dir returns the directory of session s, numbered id.
func (s *sshSession) dir(id int) *vfs.NamespaceFs {
	return &vfs.NamespaceFs{
		RootName: strconv.Itoa(id),
		Entries: []vfs.FileEntry{
			{Name: "io", Mode: 0600, Open: func(int) (afero.File, error) { return &ioFile{session: s}, nil }},
			{Name: "ctl", Mode: 0200, Open: func(int) (afero.File, error) { return &ctlFile{session: s}, nil }},
			{Name: "stat", Mode: 0400, Open: func(int) (afero.File, error) {
				return &vfs.ReadonlyFile{Data: []byte(s.status() + "\n")}, nil
			}},
		},
	}
}

// status is "open" while the session runs, then "closed".
func (s *sshSession) status() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return "closed"
	}
	return "open"
}

// ioFile is the session's terminal: reads return its output, writes are
// typed into it.
type ioFile struct {
	vfs.FileStub
	session *sshSession
}

func (f *ioFile) ReadAt(p []byte, off int64) (int, error) { return f.session.readAt(p, off) }
func (f *ioFile) WriteAt(p []byte, _ int64) (int, error)  { return f.session.stdin.Write(p) }

// ctlFile takes "kill" and "resize <cols>x<rows>".
type ctlFile struct {
	vfs.FileStub
	session *sshSession
}

func (f *ctlFile) WriteAt(p []byte, _ int64) (int, error) {
	cmd := strings.TrimSpace(string(p))
	switch {
	case cmd == "kill":
		f.session.close()
	case strings.HasPrefix(cmd, "resize "):
		var cols, rows int
		fmt.Sscanf(cmd[7:], "%dx%d", &cols, &rows)
		f.session.resize(rows, cols)
	}
	return len(p), nil
}

// ---- newFile: write relative path → read back "<host>/<id>\n" ----

type newFile struct {
	vfs.ReadonlyFile // the new session, once written
	fs               *hostFs
}

func (f *newFile) WriteAt(p []byte, _ int64) (int, error) {
	if f.Data != nil {
		return 0, os.ErrPermission
	}
	parts := strings.SplitN(strings.TrimRight(string(p), "\n"), "\n", 2)
	relPath := strings.TrimSpace(parts[0])
	if relPath == "" {
		return len(p), nil
	}
	cmd := ""
	if len(parts) > 1 {
		cmd = strings.TrimSpace(parts[1])
	}
	host := strings.SplitN(relPath, "/", 2)[0]
	_, id, err := f.fs.newPTYSession(host, remoteDir(relPath), cmd)
	if err != nil {
		return 0, err
	}
	f.Data = fmt.Appendf(nil, "%s/%d\n", host, id)
	return len(p), nil
}

// ---- runFile: write "<relpath>\n<cmd>\n" → read combined output ----

type runFile struct {
	vfs.ReadonlyFile // the command's output, once written
	fs               *hostFs
}

func (f *runFile) WriteAt(p []byte, _ int64) (int, error) {
	if f.Data != nil {
		return 0, os.ErrPermission
	}
	s := strings.TrimRight(string(p), "\n")
	lines := strings.SplitN(s, "\n", 2)
	if len(lines) < 2 {
		return len(p), nil
	}
	relPath := strings.TrimSpace(lines[0])
	cmd := strings.TrimSpace(lines[1])

	host := strings.SplitN(relPath, "/", 2)[0]
	cmd = remoteCmd(remoteDir(relPath), cmd)
	client, err := f.fs.sftp.getClient(host)
	if err != nil {
		return 0, err
	}
	sess, err := client.ssh.NewSession()
	if err != nil {
		return 0, err
	}
	defer sess.Close()

	out, err := sess.CombinedOutput(cmd)
	if err != nil {
		f.Data = append([]byte(err.Error()+"\n"), out...)
	} else {
		f.Data = out
	}
	return len(p), nil
}
