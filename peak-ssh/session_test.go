package main

import (
	"net"
	"slices"
	"sync"
	"testing"

	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
)

// The directories and session files read over 9P as peak reads them,
// without connecting anywhere: a session is registered by hand.
func TestHostFsOver9P(t *testing.T) {
	fs := newHostFs(NewSftpFs())
	sh := &sshSession{}
	sh.cond = sync.NewCond(&sh.mu)
	fs.addSession("host", sh)

	c, s := net.Pipe()
	go vfs.NewNinePSrv(fs).ServeConn(s)
	peer, err := vfs.NewNinePClientFsFromConn(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })

	for dir, want := range map[string][]string{
		"/":       {"host/", "new", "run"},
		"/host":   {"0/", "fs/", "io"},
		"/host/0": {"ctl", "io", "stat"},
	} {
		infos, err := afero.ReadDir(peer, dir)
		if err != nil {
			t.Fatalf("ReadDir(%s): %v", dir, err)
		}
		var got []string
		for _, fi := range infos {
			name := fi.Name()
			if fi.IsDir() {
				name += "/"
			}
			got = append(got, name)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s lists %q, want %q", dir, got, want)
		}
	}

	for _, done := range []bool{false, true} {
		sh.done = done
		want := map[bool]string{false: "open\n", true: "closed\n"}[done]
		if got, err := afero.ReadFile(peer, "/host/0/stat"); err != nil || string(got) != want {
			t.Errorf("stat reads %q, %v; want %q", got, err, want)
		}
	}
	if _, err := peer.Stat("/host/1/stat"); err == nil {
		t.Error("a session that does not exist stats")
	}
}
