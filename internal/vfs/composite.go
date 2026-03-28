package vfs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aleksana/peak/internal/vfs/afero"
)

// CompositeFs merges multiple afero.Fs into a single view.
type CompositeFs struct {
	root   afero.Fs
	mounts map[string]afero.Fs
	mu     sync.RWMutex
}

func NewCompositeFs() *CompositeFs {
	return &CompositeFs{
		root:   afero.NewMemMapFs(),
		mounts: make(map[string]afero.Fs),
	}
}

// Mount attaches an afero.Fs at the given path.
func (fs *CompositeFs) Mount(path string, mountFs afero.Fs) {
	cleanPath := filepath.Clean(path)
	// Ensure the mount point exists in the root memory FS
	_ = fs.root.MkdirAll(cleanPath, 0755)

	fs.mu.Lock()
	fs.mounts[cleanPath] = mountFs
	fs.mu.Unlock()
}

// Umount detaches an afero.Fs from the given path.
func (fs *CompositeFs) Umount(path string) {
	cleanPath := filepath.Clean(path)
	fs.mu.Lock()
	delete(fs.mounts, cleanPath)
	fs.mu.Unlock()
}

func (fs *CompositeFs) getFs(name string) (afero.Fs, string) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	cleanName := filepath.Clean(name)

	bestMatch := ""
	for m := range fs.mounts {
		if cleanName == m || strings.HasPrefix(cleanName, m+string(os.PathSeparator)) || (m == "/" && strings.HasPrefix(cleanName, "/")) {
			if len(m) > len(bestMatch) {
				bestMatch = m
			}
		}
	}

	if bestMatch != "" {
		rel, _ := filepath.Rel(bestMatch, cleanName)
		if rel == "." {
			rel = "/"
		} else {
			rel = "/" + rel
		}
		return fs.mounts[bestMatch], rel
	}

	return fs.root, name
}

func (fs *CompositeFs) Create(name string) (afero.File, error) {
	targetFs, relPath := fs.getFs(name)
	return targetFs.Create(relPath)
}

func (fs *CompositeFs) Mkdir(name string, perm os.FileMode) error {
	targetFs, relPath := fs.getFs(name)
	return targetFs.Mkdir(relPath, perm)
}

func (fs *CompositeFs) MkdirAll(path string, perm os.FileMode) error {
	targetFs, relPath := fs.getFs(path)
	return targetFs.MkdirAll(relPath, perm)
}

func (fs *CompositeFs) Open(name string) (afero.File, error) {
	targetFs, relPath := fs.getFs(name)
	f, err := targetFs.Open(relPath)
	if err != nil {
		return nil, err
	}
	return &CompositeFile{File: f, fs: fs, name: name}, nil
}

func (fs *CompositeFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	targetFs, relPath := fs.getFs(name)
	f, err := targetFs.OpenFile(relPath, flag, perm)
	if err != nil {
		return nil, err
	}
	return &CompositeFile{File: f, fs: fs, name: name}, nil
}

func (fs *CompositeFs) Remove(name string) error {
	targetFs, relPath := fs.getFs(name)
	return targetFs.Remove(relPath)
}

func (fs *CompositeFs) RemoveAll(path string) error {
	targetFs, relPath := fs.getFs(path)
	return targetFs.RemoveAll(relPath)
}

func (fs *CompositeFs) Rename(oldname, newname string) error {
	// Renaming across different filesystems is not supported.
	fs1, rel1 := fs.getFs(oldname)
	fs2, rel2 := fs.getFs(newname)
	if fs1 != fs2 {
		return fmt.Errorf("cross-filesystem rename not supported")
	}
	return fs1.Rename(rel1, rel2)
}

func (fs *CompositeFs) Stat(name string) (os.FileInfo, error) {
	targetFs, relPath := fs.getFs(name)
	return targetFs.Stat(relPath)
}

func (fs *CompositeFs) Name() string { return "CompositeFs" }

func (fs *CompositeFs) Chmod(name string, mode os.FileMode) error {
	targetFs, relPath := fs.getFs(name)
	return targetFs.Chmod(relPath, mode)
}

func (fs *CompositeFs) Chown(name string, uid, gid int) error {
	targetFs, relPath := fs.getFs(name)
	return targetFs.Chown(relPath, uid, gid)
}

func (fs *CompositeFs) Chtimes(name string, atime time.Time, mtime time.Time) error {
	targetFs, relPath := fs.getFs(name)
	return targetFs.Chtimes(relPath, atime, mtime)
}

// CompositeFile wraps an afero.File to support Readdir across mounts.
type CompositeFile struct {
	afero.File
	fs   *CompositeFs
	name string
}

func (f *CompositeFile) Readdir(count int) ([]os.FileInfo, error) {
	entries, err := f.File.Readdir(count)
	if count > 0 || (err != nil && err != io.EOF) {
		return entries, err
	}

	// Merge with root FS entries to show mount points and other structure
	targetFs, _ := f.fs.getFs(f.name)
	if targetFs == f.fs.root {
		return entries, err
	}

	rootEntries, rerr := afero.ReadDir(f.fs.root, f.name)
	if rerr == nil {
		seen := make(map[string]bool)
		for _, e := range entries {
			seen[e.Name()] = true
		}
		for _, e := range rootEntries {
			if !seen[e.Name()] {
				entries = append(entries, e)
				seen[e.Name()] = true
			}
		}
	}
	return entries, err
}
