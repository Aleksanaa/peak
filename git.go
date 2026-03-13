package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aleksana/peak/gitfs"
	"github.com/spf13/afero"
)

type GitFs struct {
	conns sync.Map // spec -> *GitRepo
}

type GitRepo struct {
	url      string
	repoSrv  *gitfs.Repo
	branches sync.Map // branch -> afero.Fs
	refList  []string
	refTime  time.Time
	mu       sync.Mutex
}

func NewGitFs() *GitFs {
	return &GitFs{}
}

func (fs *GitFs) parse(name string) (spec, branch, rel string) {
	name = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(name)), "/")
	if name == "" || name == "." {
		return "", "", ""
	}
	parts := strings.SplitN(name, "/", 3)
	if len(parts) >= 1 {
		spec = parts[0]
	}
	if len(parts) >= 2 {
		branch = parts[1]
	}
	if len(parts) >= 3 {
		rel = parts[2]
	} else {
		rel = "."
	}
	if rel == "" {
		rel = "."
	}
	return spec, branch, rel
}

func parseRemoteSpec(spec string) (host, user, repo string) {
	parts := strings.Split(spec, ":")
	if len(parts) < 3 {
		return "", "", ""
	}
	repo = parts[len(parts)-1]
	user = parts[len(parts)-2]
	host = strings.Join(parts[:len(parts)-2], ":")
	return host, user, repo
}

func (fs *GitFs) getRepo(spec string) (*GitRepo, error) {
	if val, ok := fs.conns.Load(spec); ok {
		return val.(*GitRepo), nil
	}

	host, user, repo := parseRemoteSpec(spec)
	if host == "" {
		return nil, fmt.Errorf("invalid git spec: %s", spec)
	}

	urlHost := strings.ReplaceAll(host, "::", ":")
	url := fmt.Sprintf("https://%s/%s/%s", urlHost, user, repo)

	repoSrv, err := gitfs.NewRepo(url)
	if err != nil {
		return nil, fmt.Errorf("Unable to open remote repository: %v", err)
	}

	r := &GitRepo{url: url, repoSrv: repoSrv}
	fs.conns.Store(spec, r)
	return r, nil
}

func (r *GitRepo) getBranches() ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.refList) > 0 && time.Since(r.refTime) < 5*time.Minute {
		return r.refList, nil
	}

	refs, err := r.repoSrv.Refs("refs/heads/")
	if err != nil {
		return nil, fmt.Errorf("Unable to list branches: %v", err)
	}

	var branches []string
	for _, ref := range refs {
		branches = append(branches, strings.TrimPrefix(ref.Name, "refs/heads/"))
	}
	r.refList = branches
	r.refTime = time.Now()
	return branches, nil
}

func (r *GitRepo) getBranchFs(branch string) (afero.Fs, error) {
	if val, ok := r.branches.Load(branch); ok {
		return val.(afero.Fs), nil
	}

	ref := branch
	if branch == "::" {
		ref = "HEAD"
	} else {
		// Try to use full ref name for branches
		ref = "refs/heads/" + branch
	}

	_, iofs, err := r.repoSrv.Clone(ref)
	if err != nil && branch != "::" {
		// Fallback to literal ref if refs/heads/ failed
		_, iofs, err = r.repoSrv.Clone(branch)
	}
	if err != nil {
		return nil, fmt.Errorf("Unable to clone branch %s: %v", branch, err)
	}

	fs := afero.NewReadOnlyFs(afero.FromIOFS{FS: iofs})
	r.branches.Store(branch, fs)
	return fs, nil
}

func (fs *GitFs) getTarget(name string) (afero.Fs, string, error) {
	spec, branch, rel := fs.parse(name)
	if spec == "" || branch == "" {
		return nil, "", nil
	}
	repo, err := fs.getRepo(spec)
	if err != nil {
		return nil, "", err
	}
	bfs, err := repo.getBranchFs(branch)
	if err != nil {
		return nil, "", err
	}
	return bfs, rel, nil
}

func (fs *GitFs) Stat(name string) (os.FileInfo, error) {
	target, rel, err := fs.getTarget(name)
	if err != nil {
		return nil, err
	}
	if target != nil {
		return target.Stat(rel)
	}

	spec, branch, _ := fs.parse(name)
	if spec == "" {
		return &SimpleFileInfo{name: "git", isDir: true}, nil
	}

	// We check if the repo is valid when we stat the repo directory itself.
	if branch == "" {
		_, err := fs.getRepo(spec)
		if err != nil {
			return nil, err
		}
		return &SimpleFileInfo{name: spec, isDir: true}, nil
	}
	return &SimpleFileInfo{name: branch, isDir: true}, nil
}

func (fs *GitFs) Open(name string) (afero.File, error) {
	return fs.OpenFile(name, os.O_RDONLY, 0)
}

func (fs *GitFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	target, rel, err := fs.getTarget(name)
	if err != nil {
		return nil, err
	}
	if target != nil {
		return target.OpenFile(rel, flag, perm)
	}

	spec, branch, _ := fs.parse(name)
	if spec == "" {
		var entries []os.FileInfo
		fs.conns.Range(func(k, v interface{}) bool {
			entries = append(entries, &SimpleFileInfo{name: k.(string), isDir: true})
			return true
		})
		return &memDirFile{name: "git", entries: entries}, nil
	}
	if branch == "" {
		repo, err := fs.getRepo(spec)
		if err != nil {
			return nil, err
		}
		branches, err := repo.getBranches()
		if err != nil {
			return nil, err
		}
		var entries []os.FileInfo
		entries = append(entries, &SimpleFileInfo{name: "::", isDir: true})
		for _, b := range branches {
			entries = append(entries, &SimpleFileInfo{name: b, isDir: true})
		}
		return &memDirFile{name: spec, entries: entries}, nil
	}
	return nil, os.ErrNotExist
}

func (fs *GitFs) Remove(n string) error                  { return os.ErrPermission }
func (fs *GitFs) RemoveAll(n string) error               { return os.ErrPermission }
func (fs *GitFs) Create(n string) (afero.File, error)    { return nil, os.ErrPermission }
func (fs *GitFs) Mkdir(n string, p os.FileMode) error    { return os.ErrPermission }
func (fs *GitFs) MkdirAll(n string, p os.FileMode) error { return os.ErrPermission }
func (fs *GitFs) Rename(o, n string) error               { return os.ErrPermission }
func (fs *GitFs) Chmod(n string, m os.FileMode) error    { return os.ErrPermission }
func (fs *GitFs) Chown(n string, u, g int) error         { return os.ErrPermission }
func (fs *GitFs) Chtimes(n string, a, m time.Time) error { return os.ErrPermission }
func (fs *GitFs) Name() string                           { return "GitFs" }
