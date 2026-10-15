package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
)

// A file with a NUL in its first 512 bytes is binary; one with a NUL only
// after them is text.
func TestReadFileOrDirBinary(t *testing.T) {
	setupTest(t, 80, 24) // the namespace
	path := filepath.Join(t.TempDir(), "f")
	for _, tt := range []struct {
		data string
		want error
	}{
		{"", nil},
		{"hello", nil},
		{"\x00\x01", errBinary},
		{strings.Repeat("A", 512) + "\x00", nil},
	} {
		if err := os.WriteFile(path, []byte(tt.data), 0644); err != nil {
			t.Fatal(err)
		}
		got, _, _, err := readFileOrDir(path)
		if err != tt.want || err == nil && got != tt.data {
			t.Errorf("%q: got %q, %v; want %v", tt.data, got, err, tt.want)
		}
	}
}

// --- Integration: readFileOrDir with wrong Stat().Size() ---

type wrongSizeFs struct {
	afero.Fs
}

func (fs *wrongSizeFs) Stat(name string) (os.FileInfo, error) {
	fi, err := fs.Fs.Stat(name)
	if err != nil {
		return nil, err
	}
	return &wrongSizeInfo{FileInfo: fi}, nil
}
func (fs *wrongSizeFs) Name() string { return "wrongSizeFs" }

type wrongSizeInfo struct {
	os.FileInfo
}

func (fi *wrongSizeInfo) Size() int64 { return 1 }

func TestReadFileOrDir_IgnoresWrongSize(t *testing.T) {
	setupTest(t, 100, 24)

	mountPath := "/peak/mirage/wrongsize"
	content := "Hello World - this file is 62 bytes but Stat claims size=1"

	mem := afero.NewMemMapFs()
	afero.WriteFile(mem, "/test.txt", []byte(content), 0644)
	ns.Mount(mountPath, &wrongSizeFs{Fs: mem}, "")

	// Call readFileOrDir directly (bypasses the async Get command).
	got, isDir, _, err := readFileOrDir(mountPath + "/test.txt")
	if err != nil {
		t.Fatal(err)
	}
	if isDir {
		t.Fatal("expected file, got directory")
	}
	if got != content {
		t.Errorf("file truncated: got %q (%d chars), want %q (%d chars)",
			got, len(got), content, len(content))
	}
}

// A local command runs in the window's directory with the selection on its
// standard input, and $samfile and $winid name the window, as in acme.
func TestRunCommandLocal(t *testing.T) {
	setupTest(t, 80, 24) // the namespace
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := dir + "/x.txt"
	out, err := runCommand(`echo "$samfile $winid"; pwd; cat`, path, "input", 7)
	if want := path + " 7\n" + dir + "\ninput"; err != nil || out != want {
		t.Errorf("output %q, %v; want %q", out, err, want)
	}
	if _, err := runCommand("exit 3", path, "", 7); err == nil {
		t.Error("a failing command reported no error")
	}
}

// runStub is a file server's run file: it takes the request written to it
// and replies with output.
type runStub struct {
	vfs.ReadonlyFile
	request *string
}

func (f *runStub) WriteAt(p []byte, _ int64) (int, error) {
	*f.request = string(p)
	f.Data = []byte("remote output")
	return len(p), nil
}

// In a directory a file server mounts, the server runs the command: it is
// asked for it, relative to its root, through its run file.
func TestRunCommandRemote(t *testing.T) {
	setupTest(t, 80, 24)
	var request string
	const mount = "/peak/run-test"
	ns.Mount(mount, &vfs.NamespaceFs{Entries: []vfs.FileEntry{
		{Name: "run", Mode: 0600, Open: func(int) (afero.File, error) { return &runStub{request: &request}, nil }},
	}}, "")
	t.Cleanup(func() { ns.Umount(mount) })

	out, err := runCommand("ls -l", mount+"/sub/x.txt", "", 7)
	if err != nil || out != "remote output" {
		t.Errorf("output %q, %v; want %q", out, err, "remote output")
	}
	if want := "sub/\nls -l\n"; request != want {
		t.Errorf("the server was asked %q, want %q", request, want)
	}
}
