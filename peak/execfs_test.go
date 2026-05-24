package main

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
	"github.com/gdamore/tcell/v2"
)

// setupExecFsTest creates an editor with one column and the peakNamespaceFs
// that mirrors what NineP.Listen() actually serves.
func setupExecFsTest(t *testing.T) (*Editor, *Column, *peakNamespaceFs, tcell.SimulationScreen) {
	t.Helper()
	e, s := setupTest(t, 120, 30)
	col := NewColumn(0, 1, e.width, e.height-1, e, e.Execute)
	e.columns = append(e.columns, col)
	e.Resize()
	inner := afero.NewBasePathFs(e.ninep.vfs, "/peak")
	nsFs := newPeakNamespaceFs(inner, e, e.ninep.bus)
	return e, col, nsFs, s
}

// ---- Stat ----

func TestNamespaceFsStatVirtualFiles(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	cases := []struct {
		name  string
		isDir bool
		mode  os.FileMode
	}{
		{"exec", false, 0600},
		{"event", false, 0444},
		{"bind", false, 0200},
		{"unbind", false, 0200},
		{"new", true, 0555},
	}
	for _, c := range cases {
		fi, err := nsFs.Stat(c.name)
		if err != nil {
			t.Errorf("Stat(%q): %v", c.name, err)
			continue
		}
		if fi.Name() != c.name {
			t.Errorf("Stat(%q).Name() = %q", c.name, fi.Name())
		}
		if fi.IsDir() != c.isDir {
			t.Errorf("Stat(%q).IsDir() = %v, want %v", c.name, fi.IsDir(), c.isDir)
		}
		if fi.Mode().Perm() != c.mode {
			t.Errorf("Stat(%q).Mode() = %v, want %v", c.name, fi.Mode().Perm(), c.mode)
		}
	}
}

func TestNamespaceFsStatForwardedToInner(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	// Root dir is always present in the inner VFS.
	fi, err := nsFs.Stat(".")
	if err != nil {
		t.Fatalf("Stat(.): %v", err)
	}
	if !fi.IsDir() {
		t.Error("Stat(.) is not a directory")
	}
	// Unknown name → ErrNotExist.
	_, err = nsFs.Stat("definitely-not-a-file-xyz")
	if !os.IsNotExist(err) {
		t.Errorf("Stat(nonexistent) = %v, want ErrNotExist", err)
	}
}

// ---- root directory listing ----

func TestNamespaceFsRootDirListing(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	f, err := nsFs.Open(".")
	if err != nil {
		t.Fatalf("Open(.): %v", err)
	}
	defer f.Close()
	infos, err := f.Readdir(-1)
	if err != nil {
		t.Fatalf("Readdir: %v", err)
	}

	counts := make(map[string]int)
	modes := make(map[string]os.FileMode)
	isDir := make(map[string]bool)
	for _, fi := range infos {
		counts[fi.Name()]++
		modes[fi.Name()] = fi.Mode().Perm()
		isDir[fi.Name()] = fi.IsDir()
	}

	for _, want := range []string{"exec", "event", "bind", "unbind", "new"} {
		if counts[want] == 0 {
			t.Errorf("missing %q in root dir listing", want)
		}
		if counts[want] > 1 {
			t.Errorf("%q appears %d times (duplicate)", want, counts[want])
		}
	}
	if modes["exec"] != 0600 {
		t.Errorf("exec mode = %v, want 0600", modes["exec"])
	}
	if modes["event"] != 0444 {
		t.Errorf("event mode = %v, want 0444", modes["event"])
	}
	if !isDir["new"] {
		t.Error("new: IsDir=false, want true")
	}
}

// ---- globalEventFile ----

func TestGlobalEventFileSubscribesOnOpen(t *testing.T) {
	e, _, nsFs, _ := setupExecFsTest(t)
	f, err := nsFs.Open("event")
	if err != nil {
		t.Fatalf("Open(event): %v", err)
	}
	defer f.Close()
	gef := f.(*globalEventFile)
	if gef.sub == nil {
		t.Error("sub is nil after opening /event")
	}
	e.ninep.bus.mu.Lock()
	found := false
	for _, s := range e.ninep.bus.subs {
		if s == gef.sub {
			found = true
		}
	}
	e.ninep.bus.mu.Unlock()
	if !found {
		t.Error("sub not registered in bus after open")
	}
}

func TestGlobalEventFileReceivesLifecycleEvent(t *testing.T) {
	_, col, nsFs, _ := setupExecFsTest(t)
	f, err := nsFs.Open("event")
	if err != nil {
		t.Fatalf("Open(event): %v", err)
	}
	defer f.Close()
	gef := f.(*globalEventFile)
	er := &eventReader{sub: gef.sub}

	col.AddWindow(" /tmp/ns-lifecycle.txt Get Put Del ", "")

	line, ok := er.ReadLine(2 * time.Second)
	if !ok {
		t.Fatal("timeout waiting for new event from global /event")
	}
	if !strings.HasPrefix(line, "new ") {
		t.Errorf("expected 'new' event, got %q", line)
	}
	if !strings.Contains(line, "/tmp/ns-lifecycle.txt") {
		t.Errorf("event missing filename: %q", line)
	}
}

func TestGlobalEventFileCloseUnsubscribes(t *testing.T) {
	e, _, nsFs, _ := setupExecFsTest(t)
	f, err := nsFs.Open("event")
	if err != nil {
		t.Fatalf("Open(event): %v", err)
	}
	gef := f.(*globalEventFile)
	sub := gef.sub

	e.ninep.bus.mu.Lock()
	before := len(e.ninep.bus.subs)
	e.ninep.bus.mu.Unlock()

	f.Close()

	e.ninep.bus.mu.Lock()
	after := len(e.ninep.bus.subs)
	e.ninep.bus.mu.Unlock()

	if after >= before {
		t.Errorf("bus sub count did not decrease: before=%d after=%d", before, after)
	}
	_, open := <-sub.ch
	if open {
		t.Error("sub channel not closed after Close")
	}
}

func TestGlobalEventFileIndependentSubscribers(t *testing.T) {
	_, col, nsFs, _ := setupExecFsTest(t)
	f1, err := nsFs.Open("event")
	if err != nil {
		t.Fatalf("Open(event) #1: %v", err)
	}
	defer f1.Close()
	f2, err := nsFs.Open("event")
	if err != nil {
		t.Fatalf("Open(event) #2: %v", err)
	}
	defer f2.Close()

	gef1 := f1.(*globalEventFile)
	gef2 := f2.(*globalEventFile)
	if gef1.sub == gef2.sub {
		t.Fatal("both opens share the same sub — want independent subscribers")
	}

	er1 := &eventReader{sub: gef1.sub}
	er2 := &eventReader{sub: gef2.sub}

	col.AddWindow(" /tmp/ns-both.txt Get Put Del ", "")

	l1, ok1 := er1.ReadLine(2 * time.Second)
	l2, ok2 := er2.ReadLine(2 * time.Second)
	if !ok1 || !ok2 {
		t.Fatalf("timeout: both subscribers must receive event (ok1=%v ok2=%v)", ok1, ok2)
	}
	if !strings.HasPrefix(l1, "new ") || !strings.HasPrefix(l2, "new ") {
		t.Errorf("expected 'new' from both subs, got %q and %q", l1, l2)
	}
}

func TestGlobalEventFileReadAtBlocks(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	f, err := nsFs.Open("event")
	if err != nil {
		t.Fatalf("Open(event): %v", err)
	}
	defer f.Close()
	gef := f.(*globalEventFile)

	// ReadAt with no data pending should block until Close delivers EOF.
	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 32)
		_, err := gef.ReadAt(buf, 0)
		readDone <- err
	}()

	// Closing the file should unblock the read with EOF.
	time.Sleep(20 * time.Millisecond)
	gef.Close()

	select {
	case err := <-readDone:
		if err != io.EOF {
			t.Errorf("ReadAt returned %v after close, want io.EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadAt did not unblock after Close")
	}
}

// ---- bindFile ----

func TestBindFileShortWriteNoop(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	f, err := nsFs.OpenFile("bind", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("Open(bind): %v", err)
	}
	defer f.Close()
	// Only one field — not enough to mount, should silently succeed.
	msg := "only-one-word\n"
	n, err := f.WriteString(msg)
	if err != nil {
		t.Errorf("bind short write: %v", err)
	}
	if n != len(msg) {
		t.Errorf("bind short write n=%d, want %d", n, len(msg))
	}
}

// ---- unbindFile ----

func TestUnbindFileUnmountsByPath(t *testing.T) {
	e, _, nsFs, _ := setupExecFsTest(t)
	mountPath := "/peak/execfs-test-unbind-sentinel"
	e.ninep.vfs.Mount(mountPath, afero.NewMemMapFs())

	// Confirm it's mounted.
	mp, _ := e.ninep.FindMount(mountPath)
	if mp != mountPath {
		t.Fatalf("pre-condition: mount not found at %s", mountPath)
	}

	f, err := nsFs.OpenFile("unbind", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("Open(unbind): %v", err)
	}
	f.WriteString(mountPath + "\n")
	f.Close()

	// After unbind, FindMount should return a shallower path, not the exact one.
	mp2, _ := e.ninep.FindMount(mountPath)
	if mp2 == mountPath {
		t.Errorf("mount still registered at %s after unbind", mountPath)
	}
}

func TestUnbindFileBlankWriteNoop(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	f, err := nsFs.OpenFile("unbind", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("Open(unbind): %v", err)
	}
	defer f.Close()
	n, err := f.WriteString("   \n")
	if err != nil {
		t.Errorf("unbind blank write: %v", err)
	}
	if n != len("   \n") {
		t.Errorf("unbind blank write n=%d", n)
	}
}

// ---- execFile ----

func TestExecFileReadBeforeWrite(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	f, err := nsFs.OpenFile("exec", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("Open(exec): %v", err)
	}
	defer f.Close()
	ef := f.(*execFile)
	buf := make([]byte, 32)
	n, err := ef.ReadAt(buf, 0)
	if n != 0 || err != io.EOF {
		t.Errorf("ReadAt before write: n=%d err=%v, want 0/EOF", n, err)
	}
}

func TestExecFileCreatesTerminalWindow(t *testing.T) {
	e, _, nsFs, s := setupExecFsTest(t)
	f, err := nsFs.OpenFile("exec", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("Open(exec): %v", err)
	}
	defer f.Close()
	ef := f.(*execFile)

	errCh := make(chan error, 1)
	go func() {
		_, err := ef.WriteString("my-test-window\n")
		errCh <- err
	}()

	// Drive the tcell event loop so the PostEvent callback runs.
	var writeErr error
	waitFor(t, e, s, func() bool {
		select {
		case writeErr = <-errCh:
			return true
		default:
			return false
		}
	})

	if writeErr != nil {
		t.Skipf("exec: %v (PTY unavailable in this environment)", writeErr)
	}

	// Read back the window ID.
	buf := make([]byte, 32)
	n, _ := ef.ReadAt(buf, 0)
	idStr := strings.TrimSpace(string(buf[:n]))
	id, err := strconv.Atoi(idStr)
	if err != nil {
		t.Fatalf("exec resp %q is not a valid int: %v", idStr, err)
	}
	if id < 0 {
		t.Errorf("exec returned ID %d, want >=0", id)
	}

	var found bool
	e.Call(func() {
		for _, col := range e.columns {
			for _, w := range col.windows {
				if w.ID == id {
					found = true
				}
			}
		}
	})
	if !found {
		t.Errorf("window with ID %d not found in editor after exec", id)
	}
}

func TestExecFileDoubleWriteFails(t *testing.T) {
	e, _, nsFs, s := setupExecFsTest(t)
	f, err := nsFs.OpenFile("exec", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("Open(exec): %v", err)
	}
	defer f.Close()
	ef := f.(*execFile)

	errCh := make(chan error, 1)
	go func() {
		_, err := ef.WriteString("first-window\n")
		errCh <- err
	}()

	var firstErr error
	waitFor(t, e, s, func() bool {
		select {
		case firstErr = <-errCh:
			return true
		default:
			return false
		}
	})
	if firstErr != nil {
		t.Skipf("exec: %v (PTY unavailable)", firstErr)
	}

	// Second write must fail.
	_, err = ef.WriteString("second-window\n")
	if err != os.ErrPermission {
		t.Errorf("second write = %v, want ErrPermission", err)
	}
}

// ---- WalkRedirect ----

func TestWalkRedirectNewCreatesWindow(t *testing.T) {
	e, _, nsFs, _ := setupExecFsTest(t)
	before := 0
	e.Call(func() {
		for _, col := range e.columns {
			before += len(col.windows)
		}
	})

	redirectPath, fi, ok := nsFs.WalkRedirect("/", "new")
	if !ok {
		t.Fatal("WalkRedirect('/','new') returned ok=false")
	}
	if !fi.IsDir() {
		t.Error("returned fi.IsDir()=false, want true")
	}
	if !strings.HasPrefix(redirectPath, "/") {
		t.Errorf("redirectPath %q does not start with /", redirectPath)
	}
	id, err := strconv.Atoi(strings.TrimPrefix(redirectPath, "/"))
	if err != nil || id < 0 {
		t.Errorf("redirectPath %q: expected numeric window ID, got %v", redirectPath, err)
	}

	var after int
	e.Call(func() {
		for _, col := range e.columns {
			after += len(col.windows)
		}
	})
	if after != before+1 {
		t.Errorf("window count: before=%d after=%d, want +1", before, after)
	}
}

func TestWalkRedirectNonRootIgnored(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	_, _, ok := nsFs.WalkRedirect("/1", "new")
	if ok {
		t.Error("WalkRedirect from non-root should return ok=false")
	}
}

func TestWalkRedirectNonNewNameIgnored(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	for _, name := range []string{"exec", "event", "body", "new2", ""} {
		_, _, ok := nsFs.WalkRedirect("/", name)
		if ok {
			t.Errorf("WalkRedirect('/','%s') returned ok=true, want false", name)
		}
	}
}

func TestWalkRedirectWindowFilesAccessible(t *testing.T) {
	e, _, nsFs, _ := setupExecFsTest(t)
	redirectPath, _, ok := nsFs.WalkRedirect("/", "new")
	if !ok {
		t.Fatal("WalkRedirect returned ok=false")
	}

	// The inner fs is a BasePathFs over /peak; after the window is mounted,
	// /<id>/body etc. should stat correctly through it.
	inner := afero.NewBasePathFs(e.ninep.vfs, "/peak")
	for _, file := range []string{"body", "tag", "ctl", "event", "addr", "data"} {
		path := redirectPath + "/" + file
		if _, err := inner.Stat(path); err != nil {
			t.Errorf("Stat(%s): %v", path, err)
		}
	}
}

func TestWalkRedirectEachCallCreatesDistinctWindow(t *testing.T) {
	e, _, nsFs, _ := setupExecFsTest(t)
	p1, _, ok1 := nsFs.WalkRedirect("/", "new")
	p2, _, ok2 := nsFs.WalkRedirect("/", "new")
	if !ok1 || !ok2 {
		t.Fatal("WalkRedirect returned ok=false")
	}
	if p1 == p2 {
		t.Errorf("two WalkRedirect calls returned same path %q — each should create a distinct window", p1)
	}
	var total int
	e.Call(func() {
		for _, col := range e.columns {
			total += len(col.windows)
		}
	})
	if total < 2 {
		t.Errorf("expected at least 2 windows after two WalkRedirect calls, got %d", total)
	}
}

// ---- /srv virtual sockets ----

func TestSrvStatIsDirectory(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	fi, err := nsFs.Stat("srv")
	if err != nil {
		t.Fatalf("Stat(srv): %v", err)
	}
	if !fi.IsDir() {
		t.Error("srv: IsDir=false, want true")
	}
	if fi.Mode().Perm() != 0555 {
		t.Errorf("srv: mode=%v, want 0555", fi.Mode().Perm())
	}
}

func TestSrvEntryStatAlwaysSucceeds(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	fi, err := nsFs.Stat("srv/anyname")
	if err != nil {
		t.Fatalf("Stat(srv/anyname): %v", err)
	}
	if fi.Name() != "anyname" {
		t.Errorf("Name()=%q, want anyname", fi.Name())
	}
	if fi.IsDir() {
		t.Error("srv/anyname: IsDir=true, want false")
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("mode=%v, want 0600", fi.Mode().Perm())
	}
}

func TestSrvInRootDirListing(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	f, err := nsFs.Open(".")
	if err != nil {
		t.Fatalf("Open(.): %v", err)
	}
	defer f.Close()
	infos, _ := f.Readdir(-1)
	for _, fi := range infos {
		if fi.Name() == "srv" {
			if !fi.IsDir() {
				t.Error("srv in listing: IsDir=false, want true")
			}
			return
		}
	}
	t.Error("srv missing from root dir listing")
}

func TestSrvOpenRDWRCreatesServerFile(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	f, err := nsFs.OpenFile("srv/myconn", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile(srv/myconn, O_RDWR): %v", err)
	}
	defer f.Close()
	if _, ok := f.(*srvServerFile); !ok {
		t.Errorf("got %T, want *srvServerFile", f)
	}
}

func TestSrvOpenReadOnlyDenied(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	_, err := nsFs.OpenFile("srv/myconn", os.O_RDONLY, 0)
	if err != os.ErrPermission {
		t.Errorf("OpenFile(srv/myconn, O_RDONLY) = %v, want ErrPermission", err)
	}
}

func TestSrvDuplicateNameReturnsExist(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	f, err := nsFs.OpenFile("srv/dup", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	defer f.Close()
	_, err = nsFs.OpenFile("srv/dup", os.O_RDWR, 0)
	if !os.IsExist(err) {
		t.Errorf("second open = %v, want ErrExist", err)
	}
}

func TestSrvCloseRemovesFromRegistry(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	f, err := nsFs.OpenFile("srv/temp", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	f.Close()
	_, err = nsFs.openSocket("srv/temp")
	if err == nil {
		t.Error("openSocket succeeded after Close, want error")
	}
}

func TestSrvOpenSocketReturnsClientConn(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	f, err := nsFs.OpenFile("srv/xfer", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer f.Close()
	conn, err := nsFs.openSocket("srv/xfer")
	if err != nil {
		t.Fatalf("openSocket: %v", err)
	}
	conn.Close()
}

func TestSrvOpenSocketTwiceFails(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	f, err := nsFs.OpenFile("srv/once", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer f.Close()
	if _, err := nsFs.openSocket("srv/once"); err != nil {
		t.Fatalf("first openSocket: %v", err)
	}
	if _, err := nsFs.openSocket("srv/once"); err == nil {
		t.Error("second openSocket succeeded, want error")
	}
}

func TestSrvDataFlowBidirectional(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)

	serverF, err := nsFs.OpenFile("srv/pipe", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer serverF.Close()

	clientConn, err := nsFs.openSocket("srv/pipe")
	if err != nil {
		t.Fatalf("openSocket: %v", err)
	}
	defer clientConn.Close()

	toServer := []byte("hello from client")
	go clientConn.Write(toServer)
	buf := make([]byte, len(toServer))
	if _, err := io.ReadFull(serverF, buf); err != nil || string(buf) != string(toServer) {
		t.Errorf("client→server: err=%v data=%q, want %q", err, buf, toServer)
	}

	toClient := []byte("hello from server")
	go serverF.Write(toClient)
	buf2 := make([]byte, len(toClient))
	if _, err := io.ReadFull(clientConn, buf2); err != nil || string(buf2) != string(toClient) {
		t.Errorf("server→client: err=%v data=%q, want %q", err, buf2, toClient)
	}
}

func TestSrvDirListsActiveNames(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)

	f1, _ := nsFs.OpenFile("srv/alpha", os.O_RDWR, 0)
	defer f1.Close()
	f2, _ := nsFs.OpenFile("srv/beta", os.O_RDWR, 0)
	defer f2.Close()

	dir, err := nsFs.OpenFile("srv", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("Open(srv): %v", err)
	}
	defer dir.Close()
	infos, err := dir.Readdir(-1)
	if err != nil {
		t.Fatalf("Readdir: %v", err)
	}
	names := make(map[string]bool)
	for _, fi := range infos {
		names[fi.Name()] = true
	}
	for _, want := range []string{"alpha", "beta"} {
		if !names[want] {
			t.Errorf("srv dir missing %q", want)
		}
	}
}

// ---- NineP.Mount dispatch ----

func TestMountDispatchVirtualSocket(t *testing.T) {
	e, _, _, _ := setupExecFsTest(t)

	serverF, err := e.ninep.nsFs.OpenFile("srv/mounttest", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile(srv/mounttest): %v", err)
	}
	defer serverF.Close()

	go vfs.NewNinePSrv(afero.NewMemMapFs()).ServeConn(serverF)

	mountTarget := "/peak/test-virtual-mount"
	if err := e.ninep.Mount("/srv/mounttest", mountTarget); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	mp, _ := e.ninep.FindMount(mountTarget)
	if mp != mountTarget {
		t.Errorf("mount not registered at %s after virtual mount", mountTarget)
	}
}

func TestMountDispatchUnixSocket(t *testing.T) {
	e, _, _, _ := setupExecFsTest(t)

	sockPath := filepath.Join(t.TempDir(), "test.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()
	go vfs.NewNinePSrv(afero.NewMemMapFs()).ServeListener(l)

	mountTarget := "/peak/test-unix-mount"
	if err := e.ninep.Mount(sockPath, mountTarget); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	mp, _ := e.ninep.FindMount(mountTarget)
	if mp != mountTarget {
		t.Errorf("mount not registered at %s after unix mount", mountTarget)
	}
}
