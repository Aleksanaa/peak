package main

import (
	"os"
	"strings"
	"testing"

	"github.com/aleksana/peak/internal/vfs/afero"
)

func TestIsBinary(t *testing.T) {
	if err := isBinary(nil); err != nil {
		t.Errorf("nil should not be binary: %v", err)
	}
	if err := isBinary([]byte("hello")); err != nil {
		t.Errorf("text should not be binary: %v", err)
	}
	if err := isBinary([]byte{0, 1, 2}); err == nil {
		t.Error("zero byte should be detected as binary")
	}
}

func openMemFile(t *testing.T, content string) afero.File {
	t.Helper()
	fs := afero.NewMemMapFs()
	f, err := fs.Create("test")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(content)
	f.Close()
	f, err = fs.Open("test")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestReadFileTail_PrefixNil(t *testing.T) {
	f := openMemFile(t, "hello world")
	s, err := readFileTail(f, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if s != "hello world" {
		t.Errorf("got %q, want %q", s, "hello world")
	}
}

func TestReadFileTail_PrefixPartial(t *testing.T) {
	f := openMemFile(t, "hello world")
	s, err := readFileTail(f, []byte("hello"), 5)
	if err != nil {
		t.Fatal(err)
	}
	if s != "hello world" {
		t.Errorf("got %q, want %q", s, "hello world")
	}
}

func TestReadFileTail_PrefixFullButFileLarger(t *testing.T) {
	content := strings.Repeat("x", 5000)
	f := openMemFile(t, content)
	prefix := []byte(content[:5])
	s, err := readFileTail(f, prefix, 5)
	if err != nil {
		t.Fatal(err)
	}
	if s != content {
		t.Errorf("got %d bytes, want %d", len(s), len(content))
	}
}

func TestReadFileTail_OffsetAfterStart(t *testing.T) {
	f := openMemFile(t, "0123456789")
	s, err := readFileTail(f, nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	if s != "56789" {
		t.Errorf("got %q, want %q", s, "56789")
	}
}

func TestReadFileTail_BinaryDetection(t *testing.T) {
	fs := afero.NewMemMapFs()
	f, _ := fs.Create("test")
	f.Write([]byte{0, 1, 2, 3})
	f.Close()
	f, _ = fs.Open("test")

	_, err := readFileTail(f, nil, 0)
	if err == nil {
		t.Fatal("expected binary detection error")
	}
}

func TestReadFileTail_BinaryDetectionAfterPrefix(t *testing.T) {
	// Binary detection already happened in the fast path (on prefix).
	// readFileTail must NOT re-detect on subsequent chunks.
	data := make([]byte, 4096)
	for i := range data {
		data[i] = 'A'
	}
	data[4095] = 0
	fs := afero.NewMemMapFs()
	f, _ := fs.Create("test")
	f.Write(data)
	f.Close()
	f, _ = fs.Open("test")

	prefix := make([]byte, 512)
	for i := range prefix {
		prefix[i] = 'A'
	}
	s, err := readFileTail(f, prefix, 512)
	if err != nil {
		t.Fatalf("should not re-detect binary after prefix: %v", err)
	}
	if len(s) != len(data) {
		t.Errorf("got %d bytes, want %d", len(s), len(data))
	}
}

func TestReadFileTail_EOFDuringRead(t *testing.T) {
	f := openMemFile(t, "short")
	s, err := readFileTail(f, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if s != "" {
		t.Errorf("got %q, want empty", s)
	}
}

func TestReadFileTail_EmptyFile(t *testing.T) {
	f := openMemFile(t, "")
	s, err := readFileTail(f, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if s != "" {
		t.Errorf("got %q, want empty", s)
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
	e, _ := setupTest(t, 100, 24)

	mountPath := "/peak/mirage/wrongsize"
	content := "Hello World - this file is 62 bytes but Stat claims size=1"

	mem := afero.NewMemMapFs()
	afero.WriteFile(mem, "/test.txt", []byte(content), 0644)
	e.ninep.vfs.Mount(mountPath, &wrongSizeFs{Fs: mem})

	// Call readFileOrDir directly (bypasses the async Get command).
	got, isDir, err := readFileOrDir(mountPath + "/test.txt")
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
