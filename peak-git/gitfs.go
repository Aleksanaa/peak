package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/aleksana/peak/internal/vfs/afero"
)

// ---- repoFs ----

type repoFs struct {
	path    string
	repo    *gogit.Repository
	mu      sync.Mutex
	staging map[string]map[string][]byte // branch → filepath → content
}

func newRepoFs(path string, repo *gogit.Repository) *repoFs {
	return &repoFs{
		path:    path,
		repo:    repo,
		staging: make(map[string]map[string][]byte),
	}
}

// ---- path parsing ----

type gpKind int

const (
	gpRoot gpKind = iota
	gpHEAD
	gpLog
	gpStatus
	gpDiff
	gpStaged
	gpCommit
	gpReset
	gpHeadsDir
	gpBranchDir
	gpBranchLog
	gpBranchDiff
	gpBranchTree
	gpRemotesDir
	gpRemoteDir
	gpRemoteBranchDir
	gpRemoteBranchLog
	gpRemoteBranchTree
	gpUnknown
)

type gpath struct {
	kind     gpKind
	branch   string
	remote   string
	treePath string // relative path within tree
}

func parsePath(name string) gpath {
	s := strings.TrimLeft(name, "/")
	if s == "" || s == "." {
		return gpath{kind: gpRoot}
	}
	parts := strings.SplitN(s, "/", 4)
	switch parts[0] {
	case "HEAD":
		return gpath{kind: gpHEAD}
	case "log":
		return gpath{kind: gpLog}
	case "status":
		return gpath{kind: gpStatus}
	case "diff":
		return gpath{kind: gpDiff}
	case "staged":
		return gpath{kind: gpStaged}
	case "commit":
		return gpath{kind: gpCommit}
	case "reset":
		return gpath{kind: gpReset}
	case "heads":
		if len(parts) == 1 {
			return gpath{kind: gpHeadsDir}
		}
		branch := parts[1]
		if len(parts) == 2 {
			return gpath{kind: gpBranchDir, branch: branch}
		}
		switch parts[2] {
		case "log":
			return gpath{kind: gpBranchLog, branch: branch}
		case "diff":
			return gpath{kind: gpBranchDiff, branch: branch}
		default:
			tree := strings.Join(parts[2:], "/")
			return gpath{kind: gpBranchTree, branch: branch, treePath: tree}
		}
	case "remotes":
		if len(parts) == 1 {
			return gpath{kind: gpRemotesDir}
		}
		remote := parts[1]
		if len(parts) == 2 {
			return gpath{kind: gpRemoteDir, remote: remote}
		}
		branch := parts[2]
		if len(parts) == 3 {
			return gpath{kind: gpRemoteBranchDir, remote: remote, branch: branch}
		}
		if parts[3] == "log" {
			return gpath{kind: gpRemoteBranchLog, remote: remote, branch: branch}
		}
		return gpath{kind: gpRemoteBranchTree, remote: remote, branch: branch, treePath: parts[3]}
	}
	return gpath{kind: gpUnknown}
}

// ---- afero.Fs implementation ----

func (fs *repoFs) Name() string { return "repoFs" }

func (fs *repoFs) Stat(name string) (os.FileInfo, error) {
	p := parsePath(name)
	switch p.kind {
	case gpRoot:
		return &gitFi{name: ".", isDir: true}, nil
	case gpHEAD:
		return &gitFi{name: "HEAD", mode: 0444}, nil
	case gpLog, gpStatus, gpDiff:
		return &gitFi{name: p.kind.filename(), mode: 0444}, nil
	case gpStaged:
		return &gitFi{name: "staged", mode: 0644}, nil
	case gpCommit, gpReset:
		return &gitFi{name: p.kind.filename(), mode: 0200}, nil
	case gpHeadsDir:
		return &gitFi{name: "heads", isDir: true}, nil
	case gpBranchDir:
		if _, err := fs.resolveBranch(p.branch); err != nil {
			return nil, os.ErrNotExist
		}
		return &gitFi{name: p.branch, isDir: true}, nil
	case gpBranchLog, gpBranchDiff:
		if _, err := fs.resolveBranch(p.branch); err != nil {
			return nil, os.ErrNotExist
		}
		return &gitFi{name: p.kind.filename(), mode: 0444}, nil
	case gpBranchTree:
		return fs.statTreePath(p.branch, p.treePath)
	case gpRemotesDir:
		return &gitFi{name: "remotes", isDir: true}, nil
	case gpRemoteDir:
		return &gitFi{name: p.remote, isDir: true}, nil
	case gpRemoteBranchDir:
		if _, err := fs.resolveRemoteBranch(p.remote, p.branch); err != nil {
			return nil, os.ErrNotExist
		}
		return &gitFi{name: p.branch, isDir: true}, nil
	case gpRemoteBranchLog:
		return &gitFi{name: "log", mode: 0444}, nil
	case gpRemoteBranchTree:
		return fs.statRemoteTreePath(p.remote, p.branch, p.treePath)
	}
	return nil, os.ErrNotExist
}

func (k gpKind) filename() string {
	switch k {
	case gpLog, gpBranchLog, gpRemoteBranchLog:
		return "log"
	case gpStatus:
		return "status"
	case gpDiff, gpBranchDiff:
		return "diff"
	case gpCommit:
		return "commit"
	case gpReset:
		return "reset"
	}
	return ""
}

func (fs *repoFs) Open(name string) (afero.File, error) {
	return fs.OpenFile(name, os.O_RDONLY, 0)
}

func (fs *repoFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	p := parsePath(name)
	switch p.kind {
	case gpRoot:
		return fs.rootDir(), nil
	case gpHEAD:
		return fs.snapFile("HEAD", fs.headContent)
	case gpLog:
		return fs.snapFile("log", func() (string, error) { return fs.logContent(nil) })
	case gpStatus:
		return fs.snapFile("status", fs.statusContent)
	case gpDiff:
		return fs.snapFile("diff", fs.diffContent)
	case gpStaged:
		if flag&os.O_WRONLY != 0 {
			return &writeCloseFile{name: "staged", onClose: fs.setStagedIndex}, nil
		}
		return fs.snapFile("staged", fs.stagedContent)
	case gpCommit:
		return &writeCloseFile{name: "commit", onClose: fs.doCommit}, nil
	case gpReset:
		return &writeCloseFile{name: "reset", onClose: fs.doReset}, nil
	case gpHeadsDir:
		return fs.headsDir()
	case gpBranchDir:
		return fs.branchDir(p.branch)
	case gpBranchLog:
		return fs.snapFile("log", func() (string, error) { return fs.branchLogContent(p.branch) })
	case gpBranchDiff:
		return fs.snapFile("diff", func() (string, error) { return fs.branchDiffContent(p.branch) })
	case gpBranchTree:
		return fs.openTreePath(p.branch, p.treePath, flag)
	case gpRemotesDir:
		return fs.remotesDir()
	case gpRemoteDir:
		return fs.remoteDir(p.remote)
	case gpRemoteBranchDir:
		return fs.remoteBranchDir(p.remote, p.branch)
	case gpRemoteBranchLog:
		return fs.snapFile("log", func() (string, error) { return fs.remoteBranchLogContent(p.remote, p.branch) })
	case gpRemoteBranchTree:
		return fs.openRemoteTreePath(p.remote, p.branch, p.treePath)
	}
	return nil, os.ErrNotExist
}

// Unsupported mutations.
func (fs *repoFs) Create(n string) (afero.File, error)    { return nil, os.ErrPermission }
func (fs *repoFs) Mkdir(n string, p os.FileMode) error    { return os.ErrPermission }
func (fs *repoFs) MkdirAll(n string, p os.FileMode) error { return os.ErrPermission }
func (fs *repoFs) Remove(n string) error                  { return os.ErrPermission }
func (fs *repoFs) RemoveAll(n string) error               { return os.ErrPermission }
func (fs *repoFs) Rename(o, n string) error               { return os.ErrPermission }
func (fs *repoFs) Chmod(n string, m os.FileMode) error    { return os.ErrPermission }
func (fs *repoFs) Chown(n string, u, g int) error         { return os.ErrPermission }
func (fs *repoFs) Chtimes(n string, a, m time.Time) error { return os.ErrPermission }

// ---- content helpers ----

func (fs *repoFs) headContent() (string, error) {
	ref, err := fs.repo.Head()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("ref: %s\n%s\n", ref.Name(), ref.Hash()), nil
}

func (fs *repoFs) logContent(hash *plumbing.Hash) (string, error) {
	var opts gogit.LogOptions
	if hash != nil {
		opts.From = *hash
	}
	iter, err := fs.repo.Log(&opts)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	count := 0
	_ = iter.ForEach(func(c *object.Commit) error {
		if count >= 50 {
			return fmt.Errorf("stop")
		}
		fmt.Fprintf(&sb, "%s %s %s\n",
			c.Hash.String()[:8],
			c.Author.When.Format("2006-01-02"),
			firstLine(c.Message))
		count++
		return nil
	})
	return sb.String(), nil
}

func (fs *repoFs) statusContent() (string, error) {
	w, err := fs.repo.Worktree()
	if err != nil {
		return "", err
	}
	st, err := w.Status()
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for path, s := range st {
		fmt.Fprintf(&sb, "%c%c %s\n", statusRune(s.Staging), statusRune(s.Worktree), path)
	}
	return sb.String(), nil
}

func statusRune(c gogit.StatusCode) rune {
	switch c {
	case gogit.Modified:
		return 'M'
	case gogit.Added:
		return 'A'
	case gogit.Deleted:
		return 'D'
	case gogit.Renamed:
		return 'R'
	case gogit.Copied:
		return 'C'
	case gogit.Untracked:
		return '?'
	default:
		return ' '
	}
}

func (fs *repoFs) diffContent() (string, error) {
	head, err := fs.repo.Head()
	if err != nil {
		return "", err
	}
	commit, err := fs.repo.CommitObject(head.Hash())
	if err != nil {
		return "", err
	}
	headTree, err := commit.Tree()
	if err != nil {
		return "", err
	}

	w, err := fs.repo.Worktree()
	if err != nil {
		return "", err
	}
	status, err := w.Status()
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	for relPath, s := range status {
		if s.Worktree == gogit.Untracked || s.Worktree == gogit.Unmodified {
			continue
		}
		var oldContent, newContent string
		if s.Worktree != gogit.Added {
			if f, err := headTree.File(relPath); err == nil {
				oldContent, _ = f.Contents()
			}
		}
		if s.Worktree != gogit.Deleted {
			if b, err := os.ReadFile(filepath.Join(fs.path, filepath.FromSlash(relPath))); err == nil {
				newContent = string(b)
			}
		}
		sb.WriteString(unifiedDiff("a/"+relPath, "b/"+relPath, oldContent, newContent))
	}
	return sb.String(), nil
}

func (fs *repoFs) stagedContent() (string, error) {
	w, err := fs.repo.Worktree()
	if err != nil {
		return "", err
	}
	st, err := w.Status()
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for path, s := range st {
		if s.Staging != gogit.Unmodified && s.Staging != gogit.Untracked {
			sb.WriteString(path + "\n")
		}
	}
	return sb.String(), nil
}

func (fs *repoFs) branchLogContent(branch string) (string, error) {
	hash, err := fs.resolveBranch(branch)
	if err != nil {
		return "", err
	}
	return fs.logContent(&hash)
}

func (fs *repoFs) branchDiffContent(branch string) (string, error) {
	branchHash, err := fs.resolveBranch(branch)
	if err != nil {
		return "", err
	}
	head, err := fs.repo.Head()
	if err != nil {
		return "", err
	}
	if branchHash == head.Hash() {
		return "", nil
	}
	fromCommit, err := fs.repo.CommitObject(head.Hash())
	if err != nil {
		return "", err
	}
	toCommit, err := fs.repo.CommitObject(branchHash)
	if err != nil {
		return "", err
	}
	patch, err := fromCommit.Patch(toCommit)
	if err != nil {
		return "", err
	}
	return patch.String(), nil
}

func (fs *repoFs) remoteBranchLogContent(remote, branch string) (string, error) {
	hash, err := fs.resolveRemoteBranch(remote, branch)
	if err != nil {
		return "", err
	}
	return fs.logContent(&hash)
}

// ---- write-close callbacks ----

func (fs *repoFs) setStagedIndex(data string) error {
	w, err := fs.repo.Worktree()
	if err != nil {
		return err
	}
	// Reset index then stage listed files
	paths := strings.Fields(data)
	for _, p := range paths {
		if err := w.AddWithOptions(&gogit.AddOptions{Path: p}); err != nil {
			return fmt.Errorf("add %s: %w", p, err)
		}
	}
	return nil
}

func (fs *repoFs) doCommit(data string) error {
	// Strip comment lines (starting with #)
	var lines []string
	for _, l := range strings.Split(data, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			lines = append(lines, l)
		}
	}
	msg := strings.TrimSpace(strings.Join(lines, "\n"))
	if msg == "" {
		return fmt.Errorf("empty commit message")
	}
	w, err := fs.repo.Worktree()
	if err != nil {
		return err
	}
	_, err = w.Commit(msg, &gogit.CommitOptions{})
	return err
}

func (fs *repoFs) doReset(data string) error {
	mode := strings.TrimSpace(data)
	var resetMode gogit.ResetMode
	switch mode {
	case "hard":
		resetMode = gogit.HardReset
	case "soft":
		resetMode = gogit.SoftReset
	default:
		resetMode = gogit.MixedReset
	}
	w, err := fs.repo.Worktree()
	if err != nil {
		return err
	}
	head, err := fs.repo.Head()
	if err != nil {
		return err
	}
	return w.Reset(&gogit.ResetOptions{Commit: head.Hash(), Mode: resetMode})
}

// ---- tree access ----

func (fs *repoFs) statTreePath(branch, treePath string) (os.FileInfo, error) {
	head, err := fs.repo.Head()
	if err != nil {
		return nil, os.ErrNotExist
	}
	branchHash, err := fs.resolveBranch(branch)
	if err != nil {
		return nil, os.ErrNotExist
	}
	// Live worktree for current branch
	if branchHash == head.Hash() {
		return os.Stat(filepath.Join(fs.path, filepath.FromSlash(treePath)))
	}
	return fs.statGitTreePath(branchHash, treePath)
}

func (fs *repoFs) statGitTreePath(hash plumbing.Hash, treePath string) (os.FileInfo, error) {
	commit, err := fs.repo.CommitObject(hash)
	if err != nil {
		return nil, os.ErrNotExist
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, os.ErrNotExist
	}
	// Try as file
	if f, err := tree.File(treePath); err == nil {
		return &gitFi{name: filepath.Base(treePath), mode: entryMode(f.Mode)}, nil
	}
	// Try as directory
	if _, err := tree.Tree(treePath); err == nil {
		return &gitFi{name: filepath.Base(treePath), isDir: true}, nil
	}
	return nil, os.ErrNotExist
}

func (fs *repoFs) statRemoteTreePath(remote, branch, treePath string) (os.FileInfo, error) {
	hash, err := fs.resolveRemoteBranch(remote, branch)
	if err != nil {
		return nil, os.ErrNotExist
	}
	return fs.statGitTreePath(hash, treePath)
}

func (fs *repoFs) openTreePath(branch, treePath string, flag int) (afero.File, error) {
	head, err := fs.repo.Head()
	if err != nil {
		return nil, os.ErrNotExist
	}
	branchHash, err := fs.resolveBranch(branch)
	if err != nil {
		return nil, os.ErrNotExist
	}
	if branchHash == head.Hash() {
		// Live worktree — open real file
		diskPath := filepath.Join(fs.path, filepath.FromSlash(treePath))
		f, err := os.OpenFile(diskPath, flag, 0644)
		if err != nil {
			return nil, err
		}
		return &osFile{File: f}, nil
	}
	return fs.openGitTreePath(branchHash, treePath)
}

func (fs *repoFs) openGitTreePath(hash plumbing.Hash, treePath string) (afero.File, error) {
	commit, err := fs.repo.CommitObject(hash)
	if err != nil {
		return nil, os.ErrNotExist
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, os.ErrNotExist
	}
	// File
	if f, err := tree.File(treePath); err == nil {
		content, err := f.Contents()
		if err != nil {
			return nil, err
		}
		return &snapFile{name: filepath.Base(treePath), data: []byte(content)}, nil
	}
	// Directory
	if subtree, err := tree.Tree(treePath); err == nil {
		return fs.gitTreeDir(filepath.Base(treePath), subtree), nil
	}
	return nil, os.ErrNotExist
}

func (fs *repoFs) openRemoteTreePath(remote, branch, treePath string) (afero.File, error) {
	hash, err := fs.resolveRemoteBranch(remote, branch)
	if err != nil {
		return nil, os.ErrNotExist
	}
	return fs.openGitTreePath(hash, treePath)
}

// ---- ref resolution ----

func (fs *repoFs) resolveBranch(branch string) (plumbing.Hash, error) {
	ref, err := fs.repo.Reference(plumbing.NewBranchReferenceName(branch), true)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	return ref.Hash(), nil
}

func (fs *repoFs) resolveRemoteBranch(remote, branch string) (plumbing.Hash, error) {
	refName := plumbing.NewRemoteReferenceName(remote, branch)
	ref, err := fs.repo.Reference(refName, true)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	return ref.Hash(), nil
}

// ---- directory files ----

func (fs *repoFs) rootDir() afero.File {
	entries := []os.FileInfo{
		&gitFi{name: "HEAD", mode: 0444},
		&gitFi{name: "log", mode: 0444},
		&gitFi{name: "status", mode: 0444},
		&gitFi{name: "diff", mode: 0444},
		&gitFi{name: "staged", mode: 0644},
		&gitFi{name: "commit", mode: 0200},
		&gitFi{name: "reset", mode: 0200},
		&gitFi{name: "heads", isDir: true},
		&gitFi{name: "remotes", isDir: true},
	}
	return &dirFile{name: ".", entries: entries}
}

func (fs *repoFs) headsDir() (afero.File, error) {
	refs, err := fs.repo.Branches()
	if err != nil {
		return nil, err
	}
	var entries []os.FileInfo
	_ = refs.ForEach(func(ref *plumbing.Reference) error {
		entries = append(entries, &gitFi{name: ref.Name().Short(), isDir: true})
		return nil
	})
	return &dirFile{name: "heads", entries: entries}, nil
}

func (fs *repoFs) branchDir(branch string) (afero.File, error) {
	if _, err := fs.resolveBranch(branch); err != nil {
		return nil, os.ErrNotExist
	}
	// Base entries: log and diff
	entries := []os.FileInfo{
		&gitFi{name: "log", mode: 0444},
		&gitFi{name: "diff", mode: 0444},
	}
	// Tree entries from HEAD or git objects
	head, _ := fs.repo.Head()
	branchHash, _ := fs.resolveBranch(branch)
	if branchHash == head.Hash() {
		// Live worktree: list disk entries
		diskEntries, _ := os.ReadDir(fs.path)
		for _, e := range diskEntries {
			if e.Name() == ".git" {
				continue
			}
			fi, _ := e.Info()
			entries = append(entries, &gitFi{name: e.Name(), isDir: e.IsDir(), mode: fi.Mode()})
		}
	} else {
		commit, err := fs.repo.CommitObject(branchHash)
		if err == nil {
			tree, err := commit.Tree()
			if err == nil {
				for _, entry := range tree.Entries {
					entries = append(entries, &gitFi{
						name:  entry.Name,
						isDir: entry.Mode == 040000,
						mode:  entryMode(entry.Mode),
					})
				}
			}
		}
	}
	return &dirFile{name: branch, entries: entries}, nil
}

func (fs *repoFs) remotesDir() (afero.File, error) {
	remotes, err := fs.repo.Remotes()
	if err != nil {
		return nil, err
	}
	var entries []os.FileInfo
	for _, r := range remotes {
		entries = append(entries, &gitFi{name: r.Config().Name, isDir: true})
	}
	return &dirFile{name: "remotes", entries: entries}, nil
}

func (fs *repoFs) remoteDir(remote string) (afero.File, error) {
	refs, err := fs.repo.References()
	if err != nil {
		return nil, err
	}
	prefix := "refs/remotes/" + remote + "/"
	var entries []os.FileInfo
	_ = refs.ForEach(func(ref *plumbing.Reference) error {
		if strings.HasPrefix(ref.Name().String(), prefix) {
			branch := strings.TrimPrefix(ref.Name().String(), prefix)
			entries = append(entries, &gitFi{name: branch, isDir: true})
		}
		return nil
	})
	return &dirFile{name: remote, entries: entries}, nil
}

func (fs *repoFs) remoteBranchDir(remote, branch string) (afero.File, error) {
	hash, err := fs.resolveRemoteBranch(remote, branch)
	if err != nil {
		return nil, os.ErrNotExist
	}
	entries := []os.FileInfo{
		&gitFi{name: "log", mode: 0444},
	}
	commit, err := fs.repo.CommitObject(hash)
	if err == nil {
		tree, err := commit.Tree()
		if err == nil {
			for _, entry := range tree.Entries {
				entries = append(entries, &gitFi{
					name:  entry.Name,
					isDir: entry.Mode == 040000,
					mode:  entryMode(entry.Mode),
				})
			}
		}
	}
	return &dirFile{name: branch, entries: entries}, nil
}

func (fs *repoFs) gitTreeDir(name string, tree *object.Tree) afero.File {
	var entries []os.FileInfo
	for _, entry := range tree.Entries {
		entries = append(entries, &gitFi{
			name:  entry.Name,
			isDir: entry.Mode == 040000,
			mode:  entryMode(entry.Mode),
		})
	}
	return &dirFile{name: name, entries: entries}
}

// ---- snap and write-close helpers ----

func (fs *repoFs) snapFile(name string, fn func() (string, error)) (afero.File, error) {
	content, err := fn()
	if err != nil {
		content = fmt.Sprintf("error: %v\n", err)
	}
	return &snapFile{name: name, data: []byte(content)}, nil
}

// ---- file types ----

// snapFile: read-only snapshot.
type snapFile struct {
	name string
	data []byte
}

func (f *snapFile) Name() string { return f.name }
func (f *snapFile) Stat() (os.FileInfo, error) {
	return &gitFi{name: f.name, size: int64(len(f.data)), mode: 0444}, nil
}
func (f *snapFile) ReadAt(p []byte, off int64) (int, error) { return snapReadAt(f.data, p, off) }
func (f *snapFile) Read(p []byte) (int, error)              { return 0, io.EOF }
func (f *snapFile) Seek(off int64, w int) (int64, error)    { return 0, nil }
func (f *snapFile) Close() error                            { return nil }
func (f *snapFile) Write(p []byte) (int, error)             { return 0, os.ErrPermission }
func (f *snapFile) WriteAt(p []byte, _ int64) (int, error)  { return 0, os.ErrPermission }
func (f *snapFile) WriteString(s string) (int, error)       { return 0, os.ErrPermission }
func (f *snapFile) Readdir(int) ([]os.FileInfo, error)      { return nil, nil }
func (f *snapFile) Readdirnames(int) ([]string, error)      { return nil, nil }
func (f *snapFile) Sync() error                             { return nil }
func (f *snapFile) Truncate(int64) error                    { return os.ErrPermission }

// writeCloseFile: accumulates writes, calls onClose on Close.
type writeCloseFile struct {
	name    string
	buf     strings.Builder
	onClose func(string) error
}

func (f *writeCloseFile) Name() string                           { return f.name }
func (f *writeCloseFile) Stat() (os.FileInfo, error)             { return &gitFi{name: f.name, mode: 0200}, nil }
func (f *writeCloseFile) WriteAt(p []byte, _ int64) (int, error) { f.buf.Write(p); return len(p), nil }
func (f *writeCloseFile) Write(p []byte) (int, error)            { return f.WriteAt(p, 0) }
func (f *writeCloseFile) WriteString(s string) (int, error)      { return f.WriteAt([]byte(s), 0) }
func (f *writeCloseFile) Close() error                           { return f.onClose(f.buf.String()) }
func (f *writeCloseFile) Read(p []byte) (int, error)             { return 0, io.EOF }
func (f *writeCloseFile) ReadAt(p []byte, _ int64) (int, error)  { return 0, io.EOF }
func (f *writeCloseFile) Seek(off int64, w int) (int64, error)   { return 0, nil }
func (f *writeCloseFile) Readdir(int) ([]os.FileInfo, error)     { return nil, nil }
func (f *writeCloseFile) Readdirnames(int) ([]string, error)     { return nil, nil }
func (f *writeCloseFile) Sync() error                            { return nil }
func (f *writeCloseFile) Truncate(int64) error                   { return os.ErrPermission }

// dirFile: virtual directory.
type dirFile struct {
	name    string
	entries []os.FileInfo
	offset  int
}

func (f *dirFile) Name() string { return f.name }
func (f *dirFile) Stat() (os.FileInfo, error) {
	return &gitFi{name: f.name, isDir: true}, nil
}
func (f *dirFile) Readdir(count int) ([]os.FileInfo, error) {
	if count <= 0 {
		return f.entries, nil
	}
	if f.offset >= len(f.entries) {
		return nil, io.EOF
	}
	end := f.offset + count
	if end > len(f.entries) {
		end = len(f.entries)
	}
	res := f.entries[f.offset:end]
	f.offset = end
	return res, nil
}
func (f *dirFile) Readdirnames(n int) ([]string, error) {
	entries, err := f.Readdir(n)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names, nil
}
func (f *dirFile) Close() error                           { return nil }
func (f *dirFile) Read(p []byte) (int, error)             { return 0, io.EOF }
func (f *dirFile) ReadAt(p []byte, _ int64) (int, error)  { return 0, io.EOF }
func (f *dirFile) Seek(off int64, w int) (int64, error)   { return 0, nil }
func (f *dirFile) Write(p []byte) (int, error)            { return 0, os.ErrPermission }
func (f *dirFile) WriteAt(p []byte, _ int64) (int, error) { return 0, os.ErrPermission }
func (f *dirFile) WriteString(s string) (int, error)      { return 0, os.ErrPermission }
func (f *dirFile) Sync() error                            { return nil }
func (f *dirFile) Truncate(int64) error                   { return os.ErrPermission }

// osFile wraps *os.File to satisfy afero.File.
type osFile struct{ *os.File }

func (f *osFile) WriteString(s string) (int, error)        { return f.File.WriteString(s) }
func (f *osFile) WriteAt(p []byte, off int64) (int, error) { return f.File.WriteAt(p, off) }
func (f *osFile) Readdir(n int) ([]os.FileInfo, error) {
	entries, err := f.File.ReadDir(n)
	if err != nil {
		return nil, err
	}
	infos := make([]os.FileInfo, len(entries))
	for i, e := range entries {
		infos[i], _ = e.Info()
	}
	return infos, nil
}
func (f *osFile) Stat() (os.FileInfo, error) { return f.File.Stat() }

// ---- gitFi: os.FileInfo for virtual files ----

type gitFi struct {
	name  string
	isDir bool
	size  int64
	mode  os.FileMode
}

func (fi *gitFi) Name() string       { return fi.name }
func (fi *gitFi) Size() int64        { return fi.size }
func (fi *gitFi) IsDir() bool        { return fi.isDir }
func (fi *gitFi) ModTime() time.Time { return time.Time{} }
func (fi *gitFi) Sys() interface{}   { return nil }
func (fi *gitFi) Mode() os.FileMode {
	if fi.isDir {
		return os.ModeDir | 0755
	}
	if fi.mode == 0 {
		return 0644
	}
	return fi.mode
}

// ---- utilities ----

func entryMode(m interface{ ToOSFileMode() (os.FileMode, error) }) os.FileMode {
	mode, _ := m.ToOSFileMode()
	return mode
}

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

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// unifiedDiff generates a simple unified diff. Not minimal but always correct.
func unifiedDiff(aName, bName, aContent, bContent string) string {
	if aContent == bContent {
		return ""
	}
	aLines := strings.Split(aContent, "\n")
	bLines := strings.Split(bContent, "\n")
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s\n", aName, bName)
	fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", 1, len(aLines), 1, len(bLines))
	for _, l := range aLines {
		sb.WriteByte('-')
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	for _, l := range bLines {
		sb.WriteByte('+')
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	return sb.String()
}
