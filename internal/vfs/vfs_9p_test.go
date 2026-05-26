package vfs

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/aleksana/peak/internal/vfs/afero"
)

// newTestPair sets up a NinePSrv backed by fs and a NinePClientFs connected
// over an in-process pipe. Both are torn down when t ends.
func newTestPair(t *testing.T, fs afero.Fs) *NinePClientFs {
	t.Helper()
	srvConn, cliConn := net.Pipe()
	srv := NewNinePSrv(fs)
	go srv.ServeConn(srvConn)
	cli, err := NewNinePClientFsFromConn(cliConn)
	if err != nil {
		srvConn.Close()
		cliConn.Close()
		t.Fatalf("NewNinePClientFsFromConn: %v", err)
	}
	t.Cleanup(func() {
		cliConn.Close()
		srvConn.Close()
	})
	return cli
}

func mustMkdirAll(t *testing.T, fs afero.Fs, p string) {
	t.Helper()
	if err := fs.MkdirAll(p, 0755); err != nil {
		t.Fatalf("MkdirAll %s: %v", p, err)
	}
}

func mustWriteFile(t *testing.T, fs afero.Fs, p, content string) {
	t.Helper()
	if err := afero.WriteFile(fs, p, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile %s: %v", p, err)
	}
}

func sortedNames(infos []os.FileInfo) []string {
	names := make([]string, len(infos))
	for i, fi := range infos {
		names[i] = fi.Name()
	}
	sort.Strings(names)
	return names
}

// --- basic round-trip tests ---

func TestNinePStatFile(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/hello.txt", "hello")
	cli := newTestPair(t, mem)

	fi, err := cli.Stat("/hello.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.IsDir() {
		t.Error("expected file, got dir")
	}
	if fi.Name() != "hello.txt" {
		t.Errorf("Name: got %q, want %q", fi.Name(), "hello.txt")
	}
	if fi.Size() != int64(len("hello")) {
		t.Errorf("Size: got %d, want %d", fi.Size(), len("hello"))
	}
}

func TestNinePStatDir(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustMkdirAll(t, mem, "/mydir")
	cli := newTestPair(t, mem)

	fi, err := cli.Stat("/mydir")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !fi.IsDir() {
		t.Error("expected dir")
	}
}

func TestNinePStatNonexistent(t *testing.T) {
	mem := afero.NewMemMapFs()
	cli := newTestPair(t, mem)

	if _, err := cli.Stat("/nope"); err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestNinePReadFile(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/data.txt", "hello world")
	cli := newTestPair(t, mem)

	got, err := afero.ReadFile(cli, "/data.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestNinePWriteFile(t *testing.T) {
	mem := afero.NewMemMapFs()
	cli := newTestPair(t, mem)

	if err := afero.WriteFile(cli, "/out.txt", []byte("via 9P"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := afero.ReadFile(mem, "/out.txt")
	if err != nil {
		t.Fatalf("ReadFile backing: %v", err)
	}
	if string(got) != "via 9P" {
		t.Errorf("got %q", got)
	}
}

func TestNinePReaddir(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustMkdirAll(t, mem, "/d")
	mustWriteFile(t, mem, "/d/a.txt", "a")
	mustWriteFile(t, mem, "/d/b.txt", "b")
	mustMkdirAll(t, mem, "/d/sub")
	cli := newTestPair(t, mem)

	f, err := cli.Open("/d")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	infos, err := f.Readdir(-1)
	if err != nil {
		t.Fatalf("Readdir: %v", err)
	}
	names := sortedNames(infos)
	want := []string{"a.txt", "b.txt", "sub"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", names, want)
	}
	for _, fi := range infos {
		if fi.Name() == "sub" && !fi.IsDir() {
			t.Error("sub should be a directory")
		}
	}
}

func TestNinePCreateFile(t *testing.T) {
	mem := afero.NewMemMapFs()
	cli := newTestPair(t, mem)

	f, err := cli.Create("/new.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = f.WriteString("created")
	f.Close()
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := afero.ReadFile(mem, "/new.txt")
	if err != nil {
		t.Fatalf("backing ReadFile: %v", err)
	}
	if string(got) != "created" {
		t.Errorf("got %q", got)
	}
}

func TestNinePCreateDir(t *testing.T) {
	mem := afero.NewMemMapFs()
	cli := newTestPair(t, mem)

	if err := cli.Mkdir("/newdir", 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	fi, err := cli.Stat("/newdir")
	if err != nil {
		t.Fatalf("Stat after Mkdir: %v", err)
	}
	if !fi.IsDir() {
		t.Error("expected directory")
	}
}

func TestNinePRemove(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/bye.txt", "goodbye")
	cli := newTestPair(t, mem)

	if err := cli.Remove("/bye.txt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := cli.Stat("/bye.txt"); err == nil {
		t.Error("file still exists after Remove")
	}
}

func TestNinePRename(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/old.txt", "content")
	cli := newTestPair(t, mem)

	if err := cli.Rename("/old.txt", "/new.txt"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	// go9p caches fids by path; verify removal through the backing fs instead.
	if _, err := mem.Stat("/old.txt"); err == nil {
		t.Error("old path still exists in backing store")
	}
	got, err := afero.ReadFile(cli, "/new.txt")
	if err != nil {
		t.Fatalf("ReadFile after rename: %v", err)
	}
	if string(got) != "content" {
		t.Errorf("got %q", got)
	}
}

func TestNinePWalkMultiStep(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustMkdirAll(t, mem, "/a/b/c")
	mustWriteFile(t, mem, "/a/b/c/deep.txt", "deep")
	cli := newTestPair(t, mem)

	got, err := afero.ReadFile(cli, "/a/b/c/deep.txt")
	if err != nil {
		t.Fatalf("ReadFile deep: %v", err)
	}
	if string(got) != "deep" {
		t.Errorf("got %q", got)
	}
}

func TestNinePTruncate(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/f.txt", "hello world")
	cli := newTestPair(t, mem)

	f, err := cli.OpenFile("/f.txt", os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatalf("OpenFile O_TRUNC: %v", err)
	}
	f.Close()

	got, err := afero.ReadFile(cli, "/f.txt")
	if err != nil {
		t.Fatalf("ReadFile after truncate: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty file, got %d bytes", len(got))
	}
}

// TestNinePLargeDir verifies that reading a directory larger than one 9P
// message (iounit bytes) works correctly and returns all entries.
func TestNinePLargeDir(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustMkdirAll(t, mem, "/big")
	const n = 1000
	for i := 0; i < n; i++ {
		mustWriteFile(t, mem, fmt.Sprintf("/big/f%04d.txt", i), "x")
	}
	cli := newTestPair(t, mem)

	f, err := cli.Open("/big")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	infos, err := f.Readdir(-1)
	if err != nil {
		t.Fatalf("Readdir: %v", err)
	}
	if len(infos) != n {
		t.Errorf("got %d entries, want %d", len(infos), n)
	}
}

// TestNinePDirSnapshot verifies that a fresh open after modifying the
// underlying fs reflects the new state, confirming per-fid snapshot semantics.
func TestNinePDirSnapshot(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustMkdirAll(t, mem, "/snap")
	mustWriteFile(t, mem, "/snap/a.txt", "a")
	cli := newTestPair(t, mem)

	// First open: only a.txt visible.
	infos1, err := afero.ReadDir(cli, "/snap")
	if err != nil {
		t.Fatalf("ReadDir 1: %v", err)
	}
	names1 := sortedNames(infos1)
	if len(names1) != 1 || names1[0] != "a.txt" {
		t.Errorf("first open: got %v, want [a.txt]", names1)
	}

	// Add b.txt directly to the backing fs.
	mustWriteFile(t, mem, "/snap/b.txt", "b")

	// Second open: should now see both files.
	infos2, err := afero.ReadDir(cli, "/snap")
	if err != nil {
		t.Fatalf("ReadDir 2: %v", err)
	}
	names2 := sortedNames(infos2)
	want := []string{"a.txt", "b.txt"}
	if strings.Join(names2, ",") != strings.Join(want, ",") {
		t.Errorf("second open: got %v, want %v", names2, want)
	}
}

// --- StatOpener tests ---

// recordingFs wraps an afero.Fs and records method calls by name.
type recordingFs struct {
	afero.Fs
	mu  sync.Mutex
	ops []string
}

func (r *recordingFs) record(op string) {
	r.mu.Lock()
	r.ops = append(r.ops, op)
	r.mu.Unlock()
}

func (r *recordingFs) countPrefix(prefix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, op := range r.ops {
		if strings.HasPrefix(op, prefix) {
			n++
		}
	}
	return n
}

func (r *recordingFs) Stat(name string) (os.FileInfo, error) {
	r.record("Stat:" + name)
	return r.Fs.Stat(name)
}

func (r *recordingFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	r.record("OpenFile:" + name)
	return r.Fs.OpenFile(name, flag, perm)
}

// statOpenerFs additionally implements StatOpener.
type statOpenerFs struct {
	recordingFs
}

func (s *statOpenerFs) Stat(name string) (os.FileInfo, error) {
	s.record("Stat:" + name)
	return s.Fs.Stat(name)
}

func (s *statOpenerFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	s.record("OpenFile:" + name)
	return s.Fs.OpenFile(name, flag, perm)
}

func (s *statOpenerFs) OpenWithStat(name string, fi os.FileInfo, flag int, perm os.FileMode) (afero.File, error) {
	s.record("OpenWithStat:" + name)
	return s.Fs.OpenFile(name, flag, perm)
}

// TestNinePStatOpenerUsed verifies that when the backing fs implements
// StatOpener, TOpen routes through OpenWithStat (not OpenFile), so the
// FileInfo from TWalk is reused and no extra Stat round-trip occurs.
func TestNinePStatOpenerUsed(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/f.txt", "data")
	spy := &statOpenerFs{recordingFs: recordingFs{Fs: mem}}
	cli := newTestPair(t, spy)

	got, err := afero.ReadFile(cli, "/f.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "data" {
		t.Errorf("got %q", got)
	}

	// Walk calls Stat; Open must call OpenWithStat, not OpenFile.
	if spy.countPrefix("OpenFile:") > 0 {
		t.Errorf("OpenFile was called %d time(s); expected 0 when StatOpener is implemented",
			spy.countPrefix("OpenFile:"))
	}
	if spy.countPrefix("OpenWithStat:") == 0 {
		t.Error("OpenWithStat was never called; StatOpener not being used")
	}
}

// TestNinePStatOpenerNotUsed verifies that without StatOpener, Open falls
// back to OpenFile.
func TestNinePStatOpenerNotUsed(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/f.txt", "data")
	spy := &recordingFs{Fs: mem}
	cli := newTestPair(t, spy)

	if _, err := afero.ReadFile(cli, "/f.txt"); err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if spy.countPrefix("OpenFile:") == 0 {
		t.Error("expected OpenFile to be called when no StatOpener")
	}
}

// TestNinePStatOpenerDir verifies that StatOpener works for directories:
// opening a directory calls OpenWithStat(isDir=true) and the listing is
// served correctly without an extra Stat.
func TestNinePStatOpenerDir(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustMkdirAll(t, mem, "/d")
	mustWriteFile(t, mem, "/d/x.txt", "x")
	spy := &statOpenerFs{recordingFs: recordingFs{Fs: mem}}
	cli := newTestPair(t, spy)

	infos, err := afero.ReadDir(cli, "/d")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(infos) != 1 || infos[0].Name() != "x.txt" {
		t.Errorf("unexpected entries: %v", sortedNames(infos))
	}
	if spy.countPrefix("OpenFile:") > 0 {
		t.Errorf("OpenFile called %d time(s); want 0", spy.countPrefix("OpenFile:"))
	}
	if spy.countPrefix("OpenWithStat:") == 0 {
		t.Error("OpenWithStat not called for directory open")
	}
}

// --- WalkRedirector test ---

// redirectorFs redirects "magic" to "/real" from any directory.
type redirectorFs struct {
	afero.Fs
}

func (r *redirectorFs) WalkRedirect(dir, name string) (string, os.FileInfo, bool) {
	if name != "magic" {
		return "", nil, false
	}
	fi, err := r.Fs.Stat("/real")
	if err != nil {
		return "", nil, false
	}
	return "/real", fi, true
}

func TestNinePWalkRedirector(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/real", "the real content")
	spy := &redirectorFs{Fs: mem}
	cli := newTestPair(t, spy)

	// Accessing /magic should be redirected to /real.
	got, err := afero.ReadFile(cli, "/magic")
	if err != nil {
		t.Fatalf("ReadFile /magic: %v", err)
	}
	if string(got) != "the real content" {
		t.Errorf("got %q, want %q", got, "the real content")
	}
}
