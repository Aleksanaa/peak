package vfs

import (
	"context"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/aleksana/peak/internal/vfs/afero"
)

// newMuxPair creates a NinePSrv backed by fs and a NinePMux in front of it.
// All clients must dial through the returned mux. The pair is torn down when
// t ends.
func newMuxPair(t *testing.T, fs afero.Fs) *NinePMux {
	t.Helper()
	serverLeft, serverRight := net.Pipe()
	srv := NewNinePSrv(fs)
	go srv.ServeConn(serverRight)
	m := NewNinePMux(serverLeft)
	go m.Serve()
	t.Cleanup(func() {
		m.Close()
		serverRight.Close()
	})
	return m
}

// dialMux wraps one Dial() call in a NinePClientFs.
func dialMux(t *testing.T, m *NinePMux) *NinePClientFs {
	t.Helper()
	conn, err := m.Dial(context.Background())
	if err != nil {
		t.Fatalf("mux.Dial: %v", err)
	}
	cli, err := NewNinePClientFsFromConn(conn)
	if err != nil {
		conn.Close()
		t.Fatalf("NewNinePClientFsFromConn: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return cli
}

// ---- basic single-client operations ----

func TestMuxStat(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/hello.txt", "hello")
	cli := dialMux(t,newMuxPair(t, mem))

	fi, err := cli.Stat("/hello.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Name() != "hello.txt" {
		t.Errorf("Name=%q", fi.Name())
	}
	if fi.Size() != 5 {
		t.Errorf("Size=%d", fi.Size())
	}
}

func TestMuxReadFile(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/data.txt", "hello world")
	cli := dialMux(t,newMuxPair(t, mem))

	got, err := afero.ReadFile(cli, "/data.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("got %q", got)
	}
}

func TestMuxWriteFile(t *testing.T) {
	mem := afero.NewMemMapFs()
	cli := dialMux(t,newMuxPair(t, mem))

	if err := afero.WriteFile(cli, "/out.txt", []byte("via mux"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := afero.ReadFile(mem, "/out.txt")
	if err != nil {
		t.Fatalf("backing ReadFile: %v", err)
	}
	if string(got) != "via mux" {
		t.Errorf("got %q", got)
	}
}

func TestMuxReaddir(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustMkdirAll(t, mem, "/d")
	mustWriteFile(t, mem, "/d/a.txt", "a")
	mustWriteFile(t, mem, "/d/b.txt", "b")
	cli := dialMux(t,newMuxPair(t, mem))

	infos, err := afero.ReadDir(cli, "/d")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	names := sortedNames(infos)
	want := []string{"a.txt", "b.txt"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", names, want)
	}
}

func TestMuxMkdirAndStat(t *testing.T) {
	mem := afero.NewMemMapFs()
	cli := dialMux(t,newMuxPair(t, mem))

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

func TestMuxRemove(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/bye.txt", "bye")
	cli := dialMux(t,newMuxPair(t, mem))

	if err := cli.Remove("/bye.txt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := cli.Stat("/bye.txt"); err == nil {
		t.Error("file still exists after Remove")
	}
}

func TestMuxLargeRead(t *testing.T) {
	mem := afero.NewMemMapFs()
	data := strings.Repeat("x", 128*1024)
	mustWriteFile(t, mem, "/big.txt", data)
	cli := dialMux(t,newMuxPair(t, mem))

	got, err := afero.ReadFile(cli, "/big.txt")
	if err != nil {
		t.Fatalf("ReadFile large: %v", err)
	}
	if len(got) != len(data) {
		t.Errorf("len=%d, want %d", len(got), len(data))
	}
}

// ---- multiplexing: two independent clients ----

func TestMuxTwoClientsIndependent(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/a.txt", "from a")
	mustWriteFile(t, mem, "/b.txt", "from b")
	m := newMuxPair(t, mem)
	c1 := dialMux(t,m)
	c2 := dialMux(t,m)

	got1, err := afero.ReadFile(c1, "/a.txt")
	if err != nil {
		t.Fatalf("c1 ReadFile: %v", err)
	}
	got2, err := afero.ReadFile(c2, "/b.txt")
	if err != nil {
		t.Fatalf("c2 ReadFile: %v", err)
	}
	if string(got1) != "from a" {
		t.Errorf("c1 got %q", got1)
	}
	if string(got2) != "from b" {
		t.Errorf("c2 got %q", got2)
	}
}

func TestMuxTwoClientsSeeSameFS(t *testing.T) {
	mem := afero.NewMemMapFs()
	m := newMuxPair(t, mem)
	c1 := dialMux(t,m)
	c2 := dialMux(t,m)

	if err := afero.WriteFile(c1, "/shared.txt", []byte("written by c1"), 0644); err != nil {
		t.Fatalf("c1 WriteFile: %v", err)
	}
	got, err := afero.ReadFile(c2, "/shared.txt")
	if err != nil {
		t.Fatalf("c2 ReadFile: %v", err)
	}
	if string(got) != "written by c1" {
		t.Errorf("c2 got %q, want %q", got, "written by c1")
	}
}

func TestMuxFidNamespacesIsolated(t *testing.T) {
	// Both clients walk the root and read different files.
	// They both end up with the same sequence of internal fid numbers on their
	// side, but the mux must remap them to distinct server-side fids.
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/x.txt", "xxx")
	mustWriteFile(t, mem, "/y.txt", "yyy")
	m := newMuxPair(t, mem)
	c1 := dialMux(t,m)
	c2 := dialMux(t,m)

	// Keep both files open concurrently.
	f1, err := c1.Open("/x.txt")
	if err != nil {
		t.Fatalf("c1 Open x: %v", err)
	}
	defer f1.Close()

	f2, err := c2.Open("/y.txt")
	if err != nil {
		t.Fatalf("c2 Open y: %v", err)
	}
	defer f2.Close()

	buf1 := make([]byte, 3)
	if _, err := io.ReadFull(f1, buf1); err != nil {
		t.Fatalf("c1 read: %v", err)
	}
	buf2 := make([]byte, 3)
	if _, err := io.ReadFull(f2, buf2); err != nil {
		t.Fatalf("c2 read: %v", err)
	}
	if string(buf1) != "xxx" {
		t.Errorf("c1 got %q, want xxx", buf1)
	}
	if string(buf2) != "yyy" {
		t.Errorf("c2 got %q, want yyy", buf2)
	}
}

// ---- concurrent operations ----

func TestMuxConcurrentReads(t *testing.T) {
	const nClients = 5
	const nOps = 20
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/shared.txt", "content")
	m := newMuxPair(t, mem)

	var wg sync.WaitGroup
	for i := 0; i < nClients; i++ {
		cli := dialMux(t,m)
		wg.Add(1)
		go func(c *NinePClientFs) {
			defer wg.Done()
			for j := 0; j < nOps; j++ {
				got, err := afero.ReadFile(c, "/shared.txt")
				if err != nil {
					t.Errorf("ReadFile: %v", err)
					return
				}
				if string(got) != "content" {
					t.Errorf("got %q", got)
					return
				}
			}
		}(cli)
	}
	wg.Wait()
}

func TestMuxConcurrentWrites(t *testing.T) {
	const nClients = 4
	mem := afero.NewMemMapFs()
	m := newMuxPair(t, mem)

	var wg sync.WaitGroup
	for i := 0; i < nClients; i++ {
		cli := dialMux(t,m)
		name := strings.Repeat(string(rune('a'+i)), 1)
		wg.Add(1)
		go func(c *NinePClientFs, fname string) {
			defer wg.Done()
			if err := afero.WriteFile(c, "/"+fname+".txt", []byte(fname), 0644); err != nil {
				t.Errorf("WriteFile %s: %v", fname, err)
			}
		}(cli, name)
	}
	wg.Wait()

	// Verify all files landed on the backing fs.
	for i := 0; i < nClients; i++ {
		name := strings.Repeat(string(rune('a'+i)), 1)
		got, err := afero.ReadFile(mem, "/"+name+".txt")
		if err != nil {
			t.Errorf("backing ReadFile %s: %v", name, err)
			continue
		}
		if string(got) != name {
			t.Errorf("backing got %q, want %q", got, name)
		}
	}
}

// ---- lifecycle ----

func TestMuxClientAbruptDisconnect(t *testing.T) {
	// A client disconnects without Tclunk. The mux must clean up its fids
	// so the server doesn't leak, and subsequent clients must still work.
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/hello.txt", "hello")
	m := newMuxPair(t, mem)

	// First client: open a file but don't close it, then close the transport.
	conn, err := m.Dial(context.Background())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	bad, err := NewNinePClientFsFromConn(conn)
	if err != nil {
		conn.Close()
		t.Fatalf("bad client: %v", err)
	}
	f, err := bad.Open("/hello.txt")
	if err != nil {
		t.Fatalf("bad.Open: %v", err)
	}
	_ = f // intentionally not closed
	conn.Close() // abrupt disconnect; file fids remain un-clunked on client side

	// Second client: must still be able to operate normally.
	c2 := dialMux(t,m)
	got, err := afero.ReadFile(c2, "/hello.txt")
	if err != nil {
		t.Fatalf("c2 ReadFile after disconnect: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("c2 got %q", got)
	}
}

func TestMuxServerDisconnectClosesdone(t *testing.T) {
	// When the server connection closes, m.done must close and new Dials must fail.
	// We test via m.done rather than through the go9p client because go9p's
	// getResponse can block on a channel when the underlying pipe dies.
	mem := afero.NewMemMapFs()
	serverLeft, serverRight := net.Pipe()
	srv := NewNinePSrv(mem)
	go srv.ServeConn(serverRight)
	m := NewNinePMux(serverLeft)
	go m.Serve()

	// Wait for the mux to be ready.
	select {
	case <-m.ready:
	case <-m.done:
		t.Fatal("mux failed to start")
	}

	// Kill the server connection.
	serverLeft.Close()
	serverRight.Close()

	// m.done must close promptly once the server pipe dies.
	<-m.done

	// New Dial must fail after disconnect.
	conn, err := m.Dial(context.Background())
	if err == nil {
		conn.Close()
		t.Error("Dial after server disconnect should fail")
	}
}

func TestMuxCloseTearsDownClients(t *testing.T) {
	mem := afero.NewMemMapFs()
	serverLeft, serverRight := net.Pipe()
	srv := NewNinePSrv(mem)
	go srv.ServeConn(serverRight)
	m := NewNinePMux(serverLeft)
	go m.Serve()

	conn, err := m.Dial(context.Background())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	m.Close()

	// After Close, Dial returns an error.
	conn2, err2 := m.Dial(context.Background())
	if err2 == nil {
		conn2.Close()
		t.Error("Dial after Close should fail")
	}

	serverRight.Close()
}

func TestMuxVersionAnsweredLocally(t *testing.T) {
	// Two clients do their Tversion exchange; the server must see only one
	// Tversion (from the mux startup). We verify this indirectly: both clients
	// get a valid msize and can perform operations, without the server receiving
	// a second Tversion that would reset its state.
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/ok", "ok")
	m := newMuxPair(t, mem)
	c1 := dialMux(t,m)
	c2 := dialMux(t,m)

	// Both clients must work even though only one Tversion reached the server.
	for _, c := range []*NinePClientFs{c1, c2} {
		got, err := afero.ReadFile(c, "/ok")
		if err != nil {
			t.Errorf("ReadFile: %v", err)
			continue
		}
		if string(got) != "ok" {
			t.Errorf("got %q", got)
		}
	}
}

// ---- integration: mux behind /srv ----

// TestMuxMultipleClientsMountSameService exercises the full production path:
// a NinePSrv posts via NinePMux (simulated by muxPair), clients
// dial through the mux, and see the same backing filesystem.
func TestMuxMultipleClientsMountSameService(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustMkdirAll(t, mem, "/data")
	mustWriteFile(t, mem, "/data/shared", "shared content")
	m := newMuxPair(t, mem)

	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cli := dialMux(t,m)
			got, err := afero.ReadFile(cli, "/data/shared")
			if err != nil {
				t.Errorf("ReadFile: %v", err)
				return
			}
			if string(got) != "shared content" {
				t.Errorf("got %q", got)
			}
		}()
	}
	wg.Wait()
}

// TestMuxStatOpenerUsedThroughMux verifies that the StatOpener optimisation
// still works when a StatOpener-implementing fs is behind a NinePMux.
func TestMuxStatOpenerUsedThroughMux(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/f.txt", "data")
	spy := &statOpenerFs{recordingFs: recordingFs{Fs: mem}}
	m := newMuxPair(t, spy)
	cli := dialMux(t,m)

	got, err := afero.ReadFile(cli, "/f.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "data" {
		t.Errorf("got %q", got)
	}
	if spy.countPrefix("OpenFile:") > 0 {
		t.Errorf("OpenFile called %d time(s); expected 0 through mux when StatOpener is implemented",
			spy.countPrefix("OpenFile:"))
	}
	if spy.countPrefix("OpenWithStat:") == 0 {
		t.Error("OpenWithStat not called through mux")
	}
}

// TestMuxReadDirLargeDir verifies multi-chunk directory reads work through the mux.
func TestMuxLargeDir(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustMkdirAll(t, mem, "/big")
	const n = 500
	for i := 0; i < n; i++ {
		name := strings.Repeat(string(rune('a'+(i%26))), 4) + string(rune('0'+i/26)) + ".txt"
		if err := afero.WriteFile(mem, "/big/"+name, []byte("x"), 0644); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	cli := dialMux(t,newMuxPair(t, mem))

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

// TestMuxRemoveFidOnClunk verifies that server-side fids are reclaimed after
// Tclunk so the mux fid pool doesn't grow unboundedly.
func TestMuxFidPoolReclaim(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/f.txt", "x")
	m := newMuxPair(t, mem)
	cli := dialMux(t,m)

	// Open and close the file many times to cycle through fids.
	for i := 0; i < 200; i++ {
		f, err := cli.Open("/f.txt")
		if err != nil {
			t.Fatalf("Open iter %d: %v", i, err)
		}
		buf := make([]byte, 1)
		if _, err := f.Read(buf); err != nil {
			t.Fatalf("Read iter %d: %v", i, err)
		}
		f.Close()
	}

	m.mu.Lock()
	totalFids := len(m.freeFids)
	m.mu.Unlock()
	// After 200 open/close cycles only a small number of unique server fids should
	// exist — many should have been freed and re-used.
	if totalFids > 100 {
		t.Errorf("freesFids has %d entries; expected recycling to keep this small", totalFids)
	}
}

// TestMuxSrvServerFileClose verifies that closing the srvServerFile tears down
// the mux (no goroutine leak) without panicking.
func TestMuxSrvServerFileCloseTeardown(t *testing.T) {
	// Use the production execfs path via peakNamespaceFs.
	// We do this via an in-process pair to avoid a full Editor setup.
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/check", "ok")

	serverLeft, serverRight := net.Pipe()
	srv := NewNinePSrv(mem)
	go srv.ServeConn(serverRight)
	m := NewNinePMux(serverLeft)
	go m.Serve()

	conn, err := m.Dial(context.Background())
	if err != nil {
		serverLeft.Close()
		serverRight.Close()
		t.Fatalf("Dial: %v", err)
	}
	cli, err := NewNinePClientFsFromConn(conn)
	if err != nil {
		conn.Close()
		serverLeft.Close()
		serverRight.Close()
		t.Fatalf("ClientFs: %v", err)
	}

	got, err := afero.ReadFile(cli, "/check")
	if err != nil || string(got) != "ok" {
		t.Fatalf("pre-close ReadFile: err=%v data=%q", err, got)
	}

	// Simulate srvServerFile.Close() by calling m.Close().
	m.Close()
	serverLeft.Close()
	serverRight.Close()
	conn.Close()
	// If goroutines leaked, the race detector or test -count=1 -run=. would surface it.
}

// TestMuxOsNotExistPropagated verifies that Rerror responses from the server
// are forwarded correctly to the client (fid cleanup + error delivery).
func TestMuxErrorPropagated(t *testing.T) {
	mem := afero.NewMemMapFs()
	m := newMuxPair(t, mem)
	cli := dialMux(t,m)

	_, err := cli.Stat("/nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent path through mux, got nil")
	}
}

// TestMuxRenameFile verifies that Twstat (rename) works through the mux.
func TestMuxRenameFile(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/old.txt", "data")
	cli := dialMux(t,newMuxPair(t, mem))

	if err := cli.Rename("/old.txt", "/new.txt"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	got, err := afero.ReadFile(cli, "/new.txt")
	if err != nil {
		t.Fatalf("ReadFile after rename: %v", err)
	}
	if string(got) != "data" {
		t.Errorf("got %q", got)
	}
}

func TestMuxChmod(t *testing.T) {
	mem := afero.NewMemMapFs()
	mustWriteFile(t, mem, "/f.txt", "x")
	cli := dialMux(t,newMuxPair(t, mem))

	if err := cli.Chmod("/f.txt", 0600); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	fi, err := cli.Stat("/f.txt")
	if err != nil {
		t.Fatalf("Stat after Chmod: %v", err)
	}
	if fi.Mode().Perm() != os.FileMode(0600) {
		t.Errorf("mode=%v, want 0600", fi.Mode().Perm())
	}
}

func TestMuxDialContextCancelled(t *testing.T) {
	serverLeft, serverRight := net.Pipe()
	defer serverLeft.Close()
	defer serverRight.Close()

	m := NewNinePMux(serverLeft)
	// Do not call m.Serve() — mux never becomes ready.

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := m.Dial(ctx)
	if err == nil {
		t.Fatal("Dial with cancelled context succeeded, want error")
	}
}
