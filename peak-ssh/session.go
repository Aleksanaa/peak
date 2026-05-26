package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"al.essio.dev/pkg/shellescape"
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
	sftp   *SftpFs
	peakFs afero.Fs // nil when not bridging to a peak editor

	mu     sync.Mutex
	hosts  map[string]map[int]*sshSession // host → id → session
	nextID map[string]int
}

func newHostFs(sftp *SftpFs, peakFs afero.Fs) *hostFs {
	return &hostFs{
		sftp:   sftp,
		peakFs: peakFs,
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
func (fs *hostFs) newPTYSession(host, dir string) (*sshSession, int, error) {
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
	if dir != "" {
		cmd := "sh -c " + shellescape.Quote("cd "+shellescape.Quote(dir)+" && exec ${SHELL:-sh}")
		start = func() error { return sess.Start(cmd) }
	}
	if err := start(); err != nil {
		sess.Close()
		return nil, 0, fmt.Errorf("shell: %w", err)
	}
	go sh.pump(stdout)

	id := fs.addSession(host, sh)
	return sh, id, nil
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
	kindRoot     = "root"
	kindNew      = "new"
	kindRun      = "run"
	kindHostDir  = "hostdir"
	kindHostIO   = "hostio"
	kindFsRoot   = "fsroot"
	kindFsFile   = "fsfile"
	kindSessDir  = "sessdir"
	kindSessIO   = "sessio"
	kindSessCtl  = "sessctl"
	kindSessStat = "sessstat"
)

type ppath struct {
	kind   string
	host   string
	sessID int
	fsRel  string
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
		return ppath{kind: kindFsFile, host: host, fsRel: "/" + rest}
	}
	n, err := strconv.Atoi(second)
	if err != nil {
		return ppath{kind: kindRoot}
	}
	switch rest {
	case "":
		return ppath{kind: kindSessDir, host: host, sessID: n}
	case "io":
		return ppath{kind: kindSessIO, host: host, sessID: n}
	case "ctl":
		return ppath{kind: kindSessCtl, host: host, sessID: n}
	case "stat":
		return ppath{kind: kindSessStat, host: host, sessID: n}
	}
	return ppath{kind: kindRoot}
}

// ---- Stat: no connection except SFTP file paths ----

func (fs *hostFs) Stat(name string) (os.FileInfo, error) {
	p := parsePath(name)
	switch p.kind {
	case kindRoot:
		return &SimpleFileInfo{name: ".", isDir: true}, nil
	case kindNew:
		return &SimpleFileInfo{name: "new", mode: 0600}, nil
	case kindRun:
		return &SimpleFileInfo{name: "run", mode: 0600}, nil
	case kindHostDir:
		return &SimpleFileInfo{name: p.host, isDir: true}, nil
	case kindHostIO:
		return &SimpleFileInfo{name: "io", mode: 0600}, nil
	case kindFsRoot:
		return &SimpleFileInfo{name: "fs", isDir: true}, nil
	case kindFsFile:
		return fs.sftp.Stat("/" + p.host + p.fsRel)
	case kindSessDir:
		if fs.getSession(p.host, p.sessID) == nil {
			return nil, os.ErrNotExist
		}
		return &SimpleFileInfo{name: strconv.Itoa(p.sessID), isDir: true}, nil
	case kindSessIO:
		if fs.getSession(p.host, p.sessID) == nil {
			return nil, os.ErrNotExist
		}
		return &SimpleFileInfo{name: "io", mode: 0600}, nil
	case kindSessCtl:
		if fs.getSession(p.host, p.sessID) == nil {
			return nil, os.ErrNotExist
		}
		return &SimpleFileInfo{name: "ctl", mode: 0200}, nil
	case kindSessStat:
		if fs.getSession(p.host, p.sessID) == nil {
			return nil, os.ErrNotExist
		}
		return &SimpleFileInfo{name: "stat", mode: 0400}, nil
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
		return &newFile{fs: fs}, nil
	case kindRun:
		return &runFile{fs: fs}, nil
	case kindHostDir:
		return fs.hostDir(p.host), nil
	case kindHostIO:
		sh, _, err := fs.newPTYSession(p.host, "")
		if err != nil {
			return nil, err
		}
		if fs.peakFs != nil {
			go fs.bridgePeakWindow(sh, p.host)
		}
		return &ioFile{session: sh}, nil
	case kindFsRoot:
		f, err := fs.sftp.OpenFile("/"+p.host+"/", flag, perm)
		if err != nil {
			return nil, err
		}
		return &namedDir{File: f, name: "fs"}, nil
	case kindFsFile:
		return fs.sftp.OpenFile("/"+p.host+p.fsRel, flag, perm)
	case kindSessDir:
		sh := fs.getSession(p.host, p.sessID)
		if sh == nil {
			return nil, os.ErrNotExist
		}
		return &MemDirFile{
			name: strconv.Itoa(p.sessID),
			entries: []os.FileInfo{
				&SimpleFileInfo{name: "io", mode: 0600},
				&SimpleFileInfo{name: "ctl", mode: 0200},
				&SimpleFileInfo{name: "stat", mode: 0400},
			},
		}, nil
	case kindSessIO:
		sh := fs.getSession(p.host, p.sessID)
		if sh == nil {
			return nil, os.ErrNotExist
		}
		return &ioFile{session: sh}, nil
	case kindSessCtl:
		sh := fs.getSession(p.host, p.sessID)
		if sh == nil {
			return nil, os.ErrNotExist
		}
		return &ctlFile{session: sh}, nil
	case kindSessStat:
		sh := fs.getSession(p.host, p.sessID)
		if sh == nil {
			return nil, os.ErrNotExist
		}
		status := "open"
		sh.mu.Lock()
		if sh.done {
			status = "closed"
		}
		sh.mu.Unlock()
		return &statFile{snap: []byte(status + "\n")}, nil
	}
	return nil, os.ErrNotExist
}

func (fs *hostFs) rootDir() afero.File {
	seen := make(map[string]bool)
	entries := []os.FileInfo{
		&SimpleFileInfo{name: "new", mode: 0600},
		&SimpleFileInfo{name: "run", mode: 0600},
	}
	fs.mu.Lock()
	for host := range fs.hosts {
		seen[host] = true
		entries = append(entries, &SimpleFileInfo{name: host, isDir: true})
	}
	fs.mu.Unlock()
	fs.sftp.conns.Range(func(k, _ interface{}) bool {
		h := k.(string)
		if !seen[h] {
			seen[h] = true
			entries = append(entries, &SimpleFileInfo{name: h, isDir: true})
		}
		return true
	})
	return &MemDirFile{name: ".", entries: entries}
}

func (fs *hostFs) hostDir(host string) afero.File {
	entries := []os.FileInfo{
		&SimpleFileInfo{name: "io", mode: 0600},
		&SimpleFileInfo{name: "fs", isDir: true},
	}
	fs.mu.Lock()
	for id := range fs.hosts[host] {
		entries = append(entries, &SimpleFileInfo{name: strconv.Itoa(id), isDir: true})
	}
	fs.mu.Unlock()
	return &MemDirFile{name: host, entries: entries}
}

// Unsupported mutations.
func (fs *hostFs) Create(n string) (afero.File, error)                  { return nil, os.ErrPermission }
func (fs *hostFs) Mkdir(n string, p os.FileMode) error                  { return os.ErrPermission }
func (fs *hostFs) MkdirAll(n string, p os.FileMode) error               { return os.ErrPermission }
func (fs *hostFs) Remove(n string) error                                 { return os.ErrPermission }
func (fs *hostFs) RemoveAll(n string) error                              { return os.ErrPermission }
func (fs *hostFs) Rename(o, n string) error                              { return os.ErrPermission }
func (fs *hostFs) Chmod(n string, m os.FileMode) error                   { return os.ErrPermission }
func (fs *hostFs) Chown(n string, u, g int) error                        { return os.ErrPermission }
func (fs *hostFs) Chtimes(n string, a, m time.Time) error                { return os.ErrPermission }
func (fs *hostFs) Name() string                                           { return "hostFs" }

// ---- sessStub ----

type sessStub struct{}

func (sessStub) Close() error                              { return nil }
func (sessStub) Read(p []byte) (int, error)                { return 0, io.EOF }
func (sessStub) ReadAt(p []byte, off int64) (int, error)   { return 0, io.EOF }
func (sessStub) Seek(off int64, w int) (int64, error)      { return 0, nil }
func (sessStub) Write(p []byte) (int, error)               { return 0, os.ErrPermission }
func (sessStub) WriteAt(p []byte, _ int64) (int, error)    { return 0, os.ErrPermission }
func (sessStub) WriteString(s string) (int, error)         { return 0, os.ErrPermission }
func (sessStub) Readdir(n int) ([]os.FileInfo, error)      { return nil, nil }
func (sessStub) Readdirnames(n int) ([]string, error)      { return nil, nil }
func (sessStub) Sync() error                               { return nil }
func (sessStub) Truncate(int64) error                      { return os.ErrPermission }
func (sessStub) Name() string                              { return "" }
func (sessStub) Stat() (os.FileInfo, error)                { return nil, os.ErrNotExist }

// ---- ioFile ----

type ioFile struct {
	sessStub
	session *sshSession
}

func (f *ioFile) Name() string { return "io" }
func (f *ioFile) Stat() (os.FileInfo, error) {
	return &SimpleFileInfo{name: "io", mode: 0600}, nil
}
func (f *ioFile) ReadAt(p []byte, off int64) (int, error) { return f.session.readAt(p, off) }
func (f *ioFile) WriteAt(p []byte, _ int64) (int, error)  { return f.session.stdin.Write(p) }
func (f *ioFile) Write(p []byte) (int, error)             { return f.WriteAt(p, 0) }
func (f *ioFile) WriteString(s string) (int, error)       { return f.WriteAt([]byte(s), 0) }

// ---- ctlFile ----

type ctlFile struct {
	sessStub
	session *sshSession
}

func (f *ctlFile) Name() string { return "ctl" }
func (f *ctlFile) Stat() (os.FileInfo, error) {
	return &SimpleFileInfo{name: "ctl", mode: 0200}, nil
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
func (f *ctlFile) Write(p []byte) (int, error)       { return f.WriteAt(p, 0) }
func (f *ctlFile) WriteString(s string) (int, error) { return f.WriteAt([]byte(s), 0) }

func snapReadAt(data, p []byte, off int64) (int, error) {
	if off >= int64(len(data)) {
		return 0, io.EOF
	}
	n := copy(p, data[off:])
	if off+int64(n) >= int64(len(data)) {
		return n, io.EOF
	}
	return n, nil
}

// ---- statFile ----

type statFile struct {
	sessStub
	snap []byte
}

func (f *statFile) Name() string { return "stat" }
func (f *statFile) Stat() (os.FileInfo, error) {
	return &SimpleFileInfo{name: "stat", mode: 0400, size: int64(len(f.snap))}, nil
}
func (f *statFile) ReadAt(p []byte, off int64) (int, error) { return snapReadAt(f.snap, p, off) }

// ---- namedDir: wraps a dir file with an overridden Name ----

type namedDir struct {
	afero.File
	name string
}

func (d *namedDir) Name() string { return d.name }
func (d *namedDir) Stat() (os.FileInfo, error) {
	return &SimpleFileInfo{name: d.name, isDir: true}, nil
}

// ---- newFile: write relative path → read back "<host>/<id>\n" ----

type newFile struct {
	sessStub
	fs   *hostFs
	resp []byte
}

func (f *newFile) Name() string { return "new" }
func (f *newFile) Stat() (os.FileInfo, error) {
	return &SimpleFileInfo{name: "new", mode: 0600}, nil
}
func (f *newFile) WriteAt(p []byte, _ int64) (int, error) {
	if f.resp != nil {
		return 0, os.ErrPermission
	}
	relPath := strings.TrimSpace(string(p))
	if relPath == "" {
		return len(p), nil
	}
	host := strings.SplitN(relPath, "/", 2)[0]
	_, id, err := f.fs.newPTYSession(host, remoteDir(relPath))
	if err != nil {
		return 0, err
	}
	f.resp = []byte(fmt.Sprintf("%s/%d\n", host, id))
	return len(p), nil
}
func (f *newFile) Write(p []byte) (int, error) { return f.WriteAt(p, 0) }
func (f *newFile) ReadAt(p []byte, off int64) (int, error) { return snapReadAt(f.resp, p, off) }

// ---- runFile: write "<relpath>\n<cmd>\n" → read combined output ----

type runFile struct {
	sessStub
	fs   *hostFs
	resp []byte
}

func (f *runFile) Name() string { return "run" }
func (f *runFile) Stat() (os.FileInfo, error) {
	return &SimpleFileInfo{name: "run", mode: 0600}, nil
}
func (f *runFile) WriteAt(p []byte, _ int64) (int, error) {
	if f.resp != nil {
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
	if dir := remoteDir(relPath); dir != "" {
		cmd = "sh -c " + shellescape.Quote("cd "+shellescape.Quote(dir)+" && "+cmd)
	}
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
		f.resp = append([]byte(err.Error()+"\n"), out...)
	} else {
		f.resp = out
	}
	return len(p), nil
}
func (f *runFile) Write(p []byte) (int, error) { return f.WriteAt(p, 0) }
func (f *runFile) ReadAt(p []byte, off int64) (int, error) { return snapReadAt(f.resp, p, off) }

// ---- bridgePeakWindow ----

// bridgePeakWindow creates a terminal window in the connected peak editor and
// pipes the SSH session's PTY through it. Runs in a background goroutine.
func (fs *hostFs) bridgePeakWindow(sh *sshSession, title string) {
	execF, err := fs.peakFs.OpenFile("/exec", os.O_RDWR, 0)
	if err != nil {
		return
	}
	if _, err := execF.WriteAt([]byte(title), 0); err != nil {
		execF.Close()
		return
	}
	buf := make([]byte, 32)
	n, err := execF.ReadAt(buf, 0)
	execF.Close()
	if err != nil && err != io.EOF {
		return
	}
	winID := strings.TrimSpace(string(buf[:n]))

	// Open io twice: independent offsets for read (user input) and write (output).
	ioRead, err := fs.peakFs.OpenFile("/"+winID+"/io", os.O_RDONLY, 0)
	if err != nil {
		return
	}
	ioWrite, err := fs.peakFs.OpenFile("/"+winID+"/io", os.O_WRONLY, 0)
	if err != nil {
		ioRead.Close()
		return
	}
	eventF, err := fs.peakFs.OpenFile("/"+winID+"/event", os.O_RDONLY, 0)
	if err != nil {
		ioRead.Close()
		ioWrite.Close()
		return
	}

	// SSH stdout → peak window display
	go func() {
		tmp := make([]byte, 4096)
		var off int64
		for {
			n, err := sh.readAt(tmp, off)
			if n > 0 {
				ioWrite.Write(tmp[:n])
				off += int64(n)
			}
			if err != nil {
				break
			}
		}
		ioWrite.Close()
	}()

	// peak window keystrokes → SSH stdin
	go func() {
		tmp := make([]byte, 4096)
		for {
			n, err := ioRead.Read(tmp)
			if n > 0 {
				sh.stdin.Write(tmp[:n])
			}
			if err != nil {
				break
			}
		}
		ioRead.Close()
	}()

	// peak resize events → remote PTY
	go func() {
		scanner := bufio.NewScanner(eventF)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "R ") {
				var rows, cols int
				fmt.Sscanf(line[2:], "%d %d", &rows, &cols)
				if rows > 0 && cols > 0 {
					sh.resize(rows, cols)
				}
			}
		}
		eventF.Close()
	}()
}
