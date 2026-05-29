package main

import (
	"fmt"
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
	col := NewColumn(0, 1, e.w, e.h-1, e, e.Execute)
	e.columns = append(e.columns, col)
	e.Resize()
	return e, col, e.ninep.nsFs, s
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
		{"mount", false, 0600},
		{"unmount", false, 0200},
		{"bind", false, 0600},
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

func TestNamespaceFsStatRootAndUnknown(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
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

	for _, want := range []string{"exec", "event", "mount", "unmount", "bind", "new"} {
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

// ---- mountFile ----

func TestMountFileShortWriteNoop(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	f, err := nsFs.OpenFile("mount", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("Open(mount): %v", err)
	}
	defer f.Close()
	// Only one field — not enough to mount, should silently succeed.
	msg := "only-one-word\n"
	n, err := f.WriteString(msg)
	if err != nil {
		t.Errorf("mount short write: %v", err)
	}
	if n != len(msg) {
		t.Errorf("mount short write n=%d, want %d", n, len(msg))
	}
}

func TestMountFileReadsCurrentMounts(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)

	serverF, err := nsFs.OpenFile("srv/mount-read-srv", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile(srv): %v", err)
	}
	go vfs.NewNinePSrv(afero.NewMemMapFs()).ServeConn(serverF)

	dst := "/peak/mount-read-test"
	writeControl(t, nsFs, "mount", "/srv/mount-read-srv "+dst+"\n")

	f, err := nsFs.OpenFile("mount", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("Open(mount): %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := "/peak/srv/mount-read-srv " + dst
	if !strings.Contains(string(data), want) {
		t.Errorf("mount listing missing %q, got:\n%s", want, data)
	}
}

func TestMountFileSnapshotOnOpen(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)

	f, err := nsFs.OpenFile("mount", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("Open(mount): %v", err)
	}
	defer f.Close()

	// Mount after opening — should not appear in already-opened file's snapshot.
	serverF, err := nsFs.OpenFile("srv/mount-snapshot-srv", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile(srv): %v", err)
	}
	go vfs.NewNinePSrv(afero.NewMemMapFs()).ServeConn(serverF)
	dst := "/peak/mount-snapshot-test"
	writeControl(t, nsFs, "mount", "/srv/mount-snapshot-srv "+dst+"\n")

	data, _ := io.ReadAll(f)
	if strings.Contains(string(data), dst) {
		t.Errorf("mount file exposed mount added after open; want snapshot semantics")
	}
}

// ---- unmountFile ----

func TestUnmountFileUnmountsByPath(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	src := t.TempDir()
	dst := "/peak/execfs-test-unmount-sentinel"
	writeControl(t, nsFs, "bind", src+" "+dst+"\n")

	writeControl(t, nsFs, "unmount", dst+"\n")

	// VFS entry gone.
	mp, _ := nsFs.editor.ninep.FindMount(dst)
	if mp == dst {
		t.Errorf("VFS mount still registered at %s after unmount", dst)
	}
}

func TestUnmountRemovesBindEntry(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	src := t.TempDir()
	dst := "/peak/execfs-test-unmount-bind"
	writeControl(t, nsFs, "bind", src+" "+dst+"\n")

	// Confirm the bind entry is recorded.
	f, _ := nsFs.OpenFile("bind", os.O_RDONLY, 0)
	data, _ := io.ReadAll(f)
	f.Close()
	if !strings.Contains(string(data), dst) {
		t.Fatalf("pre-condition: bind entry missing before unmount")
	}

	writeControl(t, nsFs, "unmount", dst+"\n")

	// Bind entry must be gone from the listing.
	f2, _ := nsFs.OpenFile("bind", os.O_RDONLY, 0)
	data2, _ := io.ReadAll(f2)
	f2.Close()
	if strings.Contains(string(data2), dst) {
		t.Errorf("bind entry still listed at %s after unmount", dst)
	}
}

func TestUnmountFileBlankWriteNoop(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	f, err := nsFs.OpenFile("unmount", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("Open(unmount): %v", err)
	}
	defer f.Close()
	n, err := f.WriteString("   \n")
	if err != nil {
		t.Errorf("unmount blank write: %v", err)
	}
	if n != len("   \n") {
		t.Errorf("unmount blank write n=%d", n)
	}
}

// ---- bindFile ----

func TestBindFileOverlaysLocalPath(t *testing.T) {
	e, _, nsFs, _ := setupExecFsTest(t)
	src := t.TempDir()
	dst := "/peak/execfs-test-bind-overlay"

	f, err := nsFs.OpenFile("bind", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("Open(bind): %v", err)
	}
	f.WriteString(src + " " + dst + "\n")
	f.Close()

	mp, _ := e.ninep.FindMount(dst)
	if mp != dst {
		t.Errorf("bind: mount not registered at %s (got %q)", dst, mp)
	}
}

func TestBindFileShortWriteNoop(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	f, err := nsFs.OpenFile("bind", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("Open(bind): %v", err)
	}
	defer f.Close()
	msg := "only-one-word\n"
	n, err := f.WriteString(msg)
	if err != nil {
		t.Errorf("bind short write: %v", err)
	}
	if n != len(msg) {
		t.Errorf("bind short write n=%d, want %d", n, len(msg))
	}
}

func writeControl(t *testing.T, nsFs afero.Fs, name, msg string) {
	t.Helper()
	f, err := nsFs.OpenFile(name, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("Open(%s): %v", name, err)
	}
	defer f.Close()
	if _, err := f.WriteString(msg); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestBindFileReadsCurrentBinds(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	src := t.TempDir()
	dst := "/peak/bind-read-test"
	writeControl(t, nsFs, "bind", src+" "+dst+"\n")

	f, err := nsFs.OpenFile("bind", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("Open(bind): %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := src + " " + dst
	if !strings.Contains(string(data), want) {
		t.Errorf("bind listing missing %q, got:\n%s", want, data)
	}
}

func TestMountAndBindListsSeparate(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	bindSrc := t.TempDir()
	bindDst := "/peak/separate-bind"
	writeControl(t, nsFs, "bind", bindSrc+" "+bindDst+"\n")

	mountF, err := nsFs.OpenFile("mount", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("Open(mount): %v", err)
	}
	defer mountF.Close()
	bindF, err := nsFs.OpenFile("bind", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("Open(bind): %v", err)
	}
	defer bindF.Close()

	mountData, _ := io.ReadAll(mountF)
	bindData, _ := io.ReadAll(bindF)

	// bind entry must appear in /bind, not in /mount
	if strings.Contains(string(mountData), bindDst) {
		t.Errorf("/mount listing contains bind entry %q", bindDst)
	}
	if !strings.Contains(string(bindData), bindSrc+" "+bindDst) {
		t.Errorf("/bind listing missing %q %q, got:\n%s", bindSrc, bindDst, bindData)
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

	// Verify the window files are accessible through the composite at /peak.
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

func TestSrvOpenReadOnlyNonexistent(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	// O_RDONLY on a non-existent entry must fail (no service posted there).
	_, err := nsFs.OpenFile("srv/myconn", os.O_RDONLY, 0)
	if err == nil {
		t.Error("OpenFile(srv/nonexistent, O_RDONLY) succeeded, want error")
	}
}

// TestSrvReadyBeforeClientMounts verifies the Plan 9-equivalent model: the
// service can handle requests the moment it opens /srv/X, before any client
// connects or mounts.
func TestSrvReadyBeforeClientMounts(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)

	srvFs := afero.NewMemMapFs()
	afero.WriteFile(srvFs, "/sentinel", []byte("ok"), 0644)

	f, err := nsFs.OpenFile("srv/eager", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer f.Close()

	// Service is ready immediately — mux is live when OpenFile returns.
	go vfs.NewNinePSrv(srvFs).ServeConn(f)

	// Dial directly without going through Mount to verify mux is live.
	conn, err := nsFs.openSocket(t.Context(), "srv/eager")
	if err != nil {
		t.Fatalf("openSocket: %v", err)
	}
	defer conn.Close()

	clientFs, err := vfs.NewNinePClientFsFromConn(conn)
	if err != nil {
		t.Fatalf("NewNinePClientFsFromConn: %v", err)
	}
	data, err := afero.ReadFile(clientFs, "/sentinel")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "ok" {
		t.Errorf("data = %q, want %q", data, "ok")
	}
}

func TestSrvCloseRemovesFromRegistry(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	f, err := nsFs.OpenFile("srv/temp", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	f.Close()
	_, err = nsFs.openSocket(t.Context(), "srv/temp")
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
	go vfs.NewNinePSrv(afero.NewMemMapFs()).ServeConn(f)
	conn, err := nsFs.openSocket(t.Context(), "srv/xfer")
	if err != nil {
		t.Fatalf("openSocket: %v", err)
	}
	conn.Close()
}

func TestSrvOpenSocketMultipleDials(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	f, err := nsFs.OpenFile("srv/multi", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer f.Close()
	// The mux multiplexes all dials onto the one service connection.
	go vfs.NewNinePSrv(afero.NewMemMapFs()).ServeConn(f)
	// Each openSocket call must succeed and return a distinct connection.
	conn1, err := nsFs.openSocket(t.Context(), "srv/multi")
	if err != nil {
		t.Fatalf("first openSocket: %v", err)
	}
	defer conn1.Close()
	conn2, err := nsFs.openSocket(t.Context(), "srv/multi")
	if err != nil {
		t.Fatalf("second openSocket: %v", err)
	}
	defer conn2.Close()
	if conn1 == conn2 {
		t.Error("two openSocket calls returned the same connection")
	}
}

func TestSrvDataFlowBidirectional(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)

	srvFs := afero.NewMemMapFs()
	afero.WriteFile(srvFs, "/from-server", []byte("hello from server"), 0644)

	serverF, err := nsFs.OpenFile("srv/pipe", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer serverF.Close()
	go vfs.NewNinePSrv(srvFs).ServeConn(serverF)

	// openSocket returns a raw 9P transport; wrap it in a NinePClientFs.
	clientConn, err := nsFs.openSocket(t.Context(), "srv/pipe")
	if err != nil {
		t.Fatalf("openSocket: %v", err)
	}
	defer clientConn.Close()

	clientFs, err := vfs.NewNinePClientFsFromConn(clientConn)
	if err != nil {
		t.Fatalf("NewNinePClientFsFromConn: %v", err)
	}

	// Client reads a file served by the service (server→client data).
	got, err := afero.ReadFile(clientFs, "/from-server")
	if err != nil {
		t.Fatalf("ReadFile from service: %v", err)
	}
	if string(got) != "hello from server" {
		t.Errorf("got %q, want %q", got, "hello from server")
	}

	// Client writes a file to the service (client→server data).
	if err := afero.WriteFile(clientFs, "/from-client", []byte("hello from client"), 0644); err != nil {
		t.Fatalf("WriteFile to service: %v", err)
	}
	written, err := afero.ReadFile(srvFs, "/from-client")
	if err != nil {
		t.Fatalf("ReadFile from backing fs: %v", err)
	}
	if string(written) != "hello from client" {
		t.Errorf("backing got %q, want %q", written, "hello from client")
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
	e, _, nsFs, _ := setupExecFsTest(t)

	serverF, err := nsFs.OpenFile("srv/mounttest", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile(srv/mounttest): %v", err)
	}
	defer serverF.Close()

	go vfs.NewNinePSrv(afero.NewMemMapFs()).ServeConn(serverF)

	mountTarget := "/peak/test-virtual-mount"
	if _, err := e.ninep.Mount("/srv/mounttest", mountTarget); err != nil {
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
	if _, err := e.ninep.Mount(sockPath, mountTarget); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	mp, _ := e.ninep.FindMount(mountTarget)
	if mp != mountTarget {
		t.Errorf("mount not registered at %s after unix mount", mountTarget)
	}
}

// ---- reverse-mount (auto-unmount) ----

// dialPeakSrv creates an in-process 9P connection to peak's server, mirroring
// exactly what NineP.Listen serves. The returned done channel closes after the
// server-side connection exits (i.e., after NinePConn.cleanup has run).
// Closing conn simulates a crash.
func dialPeakSrv(t *testing.T, e *Editor) (peakFs afero.Fs, conn net.Conn, done <-chan struct{}) {
	t.Helper()
	client, server := net.Pipe()
	srv := vfs.NewNinePSrv(vfs.NewRootedFs(e.ninep.vfs, "/peak"))
	ch := make(chan struct{})
	go func() {
		srv.ServeConn(server)
		close(ch)
	}()
	fs, err := vfs.NewNinePClientFsFromConn(client)
	if err != nil {
		client.Close()
		t.Fatalf("NewNinePClientFsFromConn: %v", err)
	}
	return fs, client, ch
}

// TestSrvDirectServeConn tests the direct ServeConn workflow using nsFs
// (no 9P transport). ServeConn serves requests and the mounted filesystem
// is readable.
func TestSrvDirectServeConn(t *testing.T) {
	e, _, nsFs, _ := setupExecFsTest(t)

	srvFs := afero.NewMemMapFs()
	if err := afero.WriteFile(srvFs, "/hello", []byte("world"), 0644); err != nil {
		t.Fatal(err)
	}

	f, err := nsFs.OpenFile("srv/direct", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile(srv/direct): %v", err)
	}
	serveDone := make(chan struct{})
	go func() {
		vfs.NewNinePSrv(srvFs).ServeConn(f)
		close(serveDone)
	}()

	const mountPath = "/peak/direct-test"
	if _, err := e.ninep.Mount("/srv/direct", mountPath); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	data, err := afero.ReadFile(e.ninep.vfs, mountPath+"/hello")
	if err != nil {
		t.Fatalf("ReadFile through mount: %v", err)
	}
	if string(data) != "world" {
		t.Errorf("data = %q, want %q", data, "world")
	}

	e.ninep.Umount(mountPath)
	f.Close()
	<-serveDone
}

// TestMountAutoUnmountOnConnDrop verifies that a mount created by writing to
// /mount over a 9P connection is automatically unmounted when that connection
// drops (simulating a crash of the service process).
func TestMountAutoUnmountOnConnDrop(t *testing.T) {
	e, _, nsFs, _ := setupExecFsTest(t)

	serverF, err := nsFs.OpenFile("srv/drop-svc", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer serverF.Close()
	go vfs.NewNinePSrv(afero.NewMemMapFs()).ServeConn(serverF)

	peakFs, conn, serveConnDone := dialPeakSrv(t, e)

	mountF, err := peakFs.OpenFile("/mount", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(/mount): %v", err)
	}
	if _, err := fmt.Fprintf(mountF, "/srv/drop-svc /peak/auto-unmount\n"); err != nil {
		t.Fatalf("write /mount: %v", err)
	}
	mountF.Close()

	if mp, _ := e.ninep.FindMount("/peak/auto-unmount"); mp != "/peak/auto-unmount" {
		t.Fatal("mount not found after mounting")
	}

	conn.Close()         // simulate crash
	<-serveConnDone      // cleanup() has run by the time this fires

	if mp, _ := e.ninep.FindMount("/peak/auto-unmount"); mp == "/peak/auto-unmount" {
		t.Fatal("mount should have been cleaned up after connection drop")
	}
}

// TestMountMultipleAutoUnmount verifies that all mounts created by a single 9P
// connection are cleaned up when that connection drops.
func TestMountMultipleAutoUnmount(t *testing.T) {
	e, _, nsFs, _ := setupExecFsTest(t)

	for _, name := range []string{"multi-a", "multi-b"} {
		serverF, err := nsFs.OpenFile("srv/"+name, os.O_RDWR, 0)
		if err != nil {
			t.Fatalf("OpenFile(srv/%s): %v", name, err)
		}
		defer serverF.Close()
		go vfs.NewNinePSrv(afero.NewMemMapFs()).ServeConn(serverF)
	}

	peakFs, conn, serveConnDone := dialPeakSrv(t, e)

	mounts := map[string]string{
		"multi-a": "/peak/multi-mount-a",
		"multi-b": "/peak/multi-mount-b",
	}
	for svc, dst := range mounts {
		mountF, err := peakFs.OpenFile("/mount", os.O_WRONLY, 0)
		if err != nil {
			t.Fatalf("OpenFile(/mount): %v", err)
		}
		fmt.Fprintf(mountF, "/srv/%s %s\n", svc, dst)
		mountF.Close()
	}
	for _, dst := range mounts {
		if mp, _ := e.ninep.FindMount(dst); mp != dst {
			t.Fatalf("mount %s not found before drop", dst)
		}
	}

	conn.Close()
	<-serveConnDone

	for _, dst := range mounts {
		if mp, _ := e.ninep.FindMount(dst); mp == dst {
			t.Errorf("mount %s should have been cleaned up", dst)
		}
	}
}

// TestSrvServeConnVia9P tests the full cross-process pattern: a service opens
// /srv/X via a 9P client connection and calls ServeConn. The mount is
// established in peak's VFS.
func TestSrvServeConnVia9P(t *testing.T) {
	e, _, _, _ := setupExecFsTest(t)

	peakFs, conn, _ := dialPeakSrv(t, e)
	t.Cleanup(func() { conn.Close() })

	f, err := peakFs.OpenFile("/srv/p9svc", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile(/srv/p9svc): %v", err)
	}
	serveDone := make(chan struct{})
	go func() {
		vfs.NewNinePSrv(afero.NewMemMapFs()).ServeConn(f)
		close(serveDone)
	}()

	mountF, err := peakFs.OpenFile("/mount", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(/mount): %v", err)
	}
	const mountPath = "/peak/p9svc-mount"
	fmt.Fprintf(mountF, "/srv/p9svc %s\n", mountPath)
	mountF.Close()

	if mp, _ := e.ninep.FindMount(mountPath); mp != mountPath {
		t.Fatal("mount not found after ServeConn-based setup")
	}

	e.ninep.Umount(mountPath)
	f.Close()
	<-serveDone
}
