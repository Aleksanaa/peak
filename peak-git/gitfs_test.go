package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ---- test repo setup ----

var testAuthor = &object.Signature{
	Name:  "Test User",
	Email: "test@example.com",
	When:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
}

type testRepo struct {
	path  string
	repo  *gogit.Repository
	fs    *repoFs
	hash1 plumbing.Hash // initial commit ("feature" branch)
	hash2 plumbing.Hash // second commit (HEAD main)
}

func setupTestRepo(t *testing.T) *testRepo {
	t.Helper()
	dir := t.TempDir()

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}

	// Configure repo-local identity so doCommit works without a global gitconfig
	cfg, _ := repo.Config()
	cfg.User.Name = "Test User"
	cfg.User.Email = "test@example.com"
	repo.SetConfig(cfg)

	w, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}

	// Initial files
	write := func(rel, content string) {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(abs), 0755)
		os.WriteFile(abs, []byte(content), 0644)
	}

	write("file1.txt", "hello\n")
	write("file2.txt", "world\n")
	write("subdir/nested.txt", "nested\n")

	w.Add("file1.txt")
	w.Add("file2.txt")
	w.Add("subdir/nested.txt")

	hash1, err := w.Commit("initial commit", &gogit.CommitOptions{Author: testAuthor})
	if err != nil {
		t.Fatalf("commit 1: %v", err)
	}

	// Create "feature" branch pointing to commit 1
	ref := plumbing.NewHashReference(plumbing.NewBranchReferenceName("feature"), hash1)
	if err := repo.Storer.SetReference(ref); err != nil {
		t.Fatalf("create feature branch: %v", err)
	}

	// Second commit on main: modify file1.txt
	write("file1.txt", "hello modified\n")
	w.Add("file1.txt")
	hash2, err := w.Commit("modify file1", &gogit.CommitOptions{Author: testAuthor})
	if err != nil {
		t.Fatalf("commit 2: %v", err)
	}

	// Add a fake remote ref so remotes/ tests work
	remoteRef := plumbing.NewHashReference(
		plumbing.NewRemoteReferenceName("origin", "main"),
		hash2,
	)
	repo.Storer.SetReference(remoteRef)
	repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{"https://example.com/repo"}})

	// Leave file2.txt modified in worktree (unstaged)
	write("file2.txt", "world changed\n")

	return &testRepo{
		path:  dir,
		repo:  repo,
		fs:    newRepoFs(dir, repo),
		hash1: hash1,
		hash2: hash2,
	}
}

// readSnap reads all content from a repoFs file by name.
func (tr *testRepo) readSnap(t *testing.T, name string) string {
	t.Helper()
	f, err := tr.fs.Open(name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer f.Close()
	var buf []byte
	tmp := make([]byte, 512)
	var off int64
	for {
		n, err := f.ReadAt(tmp, off)
		buf = append(buf, tmp[:n]...)
		off += int64(n)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
	}
	return string(buf)
}

func (tr *testRepo) readdirNames(t *testing.T, name string) []string {
	t.Helper()
	f, err := tr.fs.Open(name)
	if err != nil {
		t.Fatalf("open dir %s: %v", name, err)
	}
	defer f.Close()
	infos, err := f.Readdir(-1)
	if err != nil {
		t.Fatalf("readdir %s: %v", name, err)
	}
	names := make([]string, len(infos))
	for i, fi := range infos {
		names[i] = fi.Name()
	}
	return names
}

func hasName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// ---- parsePath ----

func TestParsePath(t *testing.T) {
	cases := []struct {
		path string
		kind gpKind
	}{
		{"", gpRoot},
		{"/", gpRoot},
		{".", gpRoot},
		{"HEAD", gpHEAD},
		{"log", gpLog},
		{"status", gpStatus},
		{"diff", gpDiff},
		{"staged", gpStaged},
		{"commit", gpCommit},
		{"reset", gpReset},
		{"heads", gpHeadsDir},
		{"heads/main", gpBranchDir},
		{"heads/main/log", gpBranchLog},
		{"heads/main/diff", gpBranchDiff},
		{"heads/main/file.txt", gpBranchTree},
		{"heads/main/subdir/file.txt", gpBranchTree},
		{"remotes", gpRemotesDir},
		{"remotes/origin", gpRemoteDir},
		{"remotes/origin/main", gpRemoteBranchDir},
		{"remotes/origin/main/log", gpRemoteBranchLog},
		{"remotes/origin/main/file.txt", gpRemoteBranchTree},
		{"bogus", gpUnknown},
	}
	for _, tc := range cases {
		p := parsePath(tc.path)
		if p.kind != tc.kind {
			t.Errorf("parsePath(%q).kind = %v, want %v", tc.path, p.kind, tc.kind)
		}
	}
}

func TestParsePathBranchTree(t *testing.T) {
	p := parsePath("heads/feature/subdir/a/b.txt")
	if p.kind != gpBranchTree {
		t.Errorf("kind = %v", p.kind)
	}
	if p.branch != "feature" {
		t.Errorf("branch = %q", p.branch)
	}
	if p.treePath != "subdir/a/b.txt" {
		t.Errorf("treePath = %q", p.treePath)
	}
}

// ---- root and stat ----

func TestRootDirListing(t *testing.T) {
	tr := setupTestRepo(t)
	names := tr.readdirNames(t, ".")
	for _, want := range []string{"HEAD", "log", "status", "diff", "staged", "commit", "reset", "heads", "remotes"} {
		if !hasName(names, want) {
			t.Errorf("root dir missing %q; got %v", want, names)
		}
	}
}

func TestStatValidPaths(t *testing.T) {
	tr := setupTestRepo(t)
	head, _ := tr.repo.Head()
	branch := head.Name().Short()

	paths := []string{
		"HEAD", "log", "status", "diff", "staged", "commit", "reset",
		"heads", "heads/" + branch, "heads/" + branch + "/log", "heads/" + branch + "/diff",
		"remotes", "remotes/origin", "remotes/origin/main", "remotes/origin/main/log",
	}
	for _, p := range paths {
		fi, err := tr.fs.Stat(p)
		if err != nil {
			t.Errorf("Stat(%q): %v", p, err)
			continue
		}
		if fi.Name() == "" {
			t.Errorf("Stat(%q): empty name", p)
		}
	}
}

func TestStatInvalidPath(t *testing.T) {
	tr := setupTestRepo(t)
	_, err := tr.fs.Stat("bogus/path/that/does/not/exist")
	if err == nil {
		t.Error("expected error for unknown path")
	}
}

func TestStatNonexistentBranch(t *testing.T) {
	tr := setupTestRepo(t)
	_, err := tr.fs.Stat("heads/nonexistent")
	if err == nil {
		t.Error("expected ErrNotExist for nonexistent branch")
	}
}

// ---- HEAD ----

func TestHeadContent(t *testing.T) {
	tr := setupTestRepo(t)
	content := tr.readSnap(t, "HEAD")
	if !strings.Contains(content, "ref:") {
		t.Errorf("HEAD content %q missing 'ref:'", content)
	}
	if !strings.Contains(content, tr.hash2.String()) {
		t.Errorf("HEAD content %q missing current hash", content)
	}
}

// ---- log ----

func TestLogContent(t *testing.T) {
	tr := setupTestRepo(t)
	content := tr.readSnap(t, "log")
	if !strings.Contains(content, "modify file1") {
		t.Errorf("log missing second commit: %q", content)
	}
	if !strings.Contains(content, "initial commit") {
		t.Errorf("log missing first commit: %q", content)
	}
	// Each line: hash date message
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		parts := strings.Fields(line)
		if len(parts) < 3 {
			t.Errorf("log line too short: %q", line)
		}
	}
}

// ---- status ----

func TestStatusContent(t *testing.T) {
	tr := setupTestRepo(t)
	content := tr.readSnap(t, "status")
	// file2.txt was modified in worktree but not staged
	if !strings.Contains(content, "file2.txt") {
		t.Errorf("status %q missing file2.txt", content)
	}
	if !strings.Contains(content, "M") {
		t.Errorf("status %q missing modified marker", content)
	}
}

// ---- diff ----

func TestDiffContent(t *testing.T) {
	tr := setupTestRepo(t)
	content := tr.readSnap(t, "diff")
	if !strings.Contains(content, "file2.txt") {
		t.Errorf("diff %q missing file2.txt", content)
	}
	if !strings.Contains(content, "---") || !strings.Contains(content, "+++") {
		t.Errorf("diff %q missing unified diff headers", content)
	}
	if !strings.Contains(content, "world") {
		t.Errorf("diff %q missing original content", content)
	}
}

func TestDiffEmptyWhenClean(t *testing.T) {
	dir := t.TempDir()
	repo, _ := gogit.PlainInit(dir, false)
	w, _ := repo.Worktree()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("clean\n"), 0644)
	w.Add("a.txt")
	w.Commit("init", &gogit.CommitOptions{Author: testAuthor})
	// No modifications
	fs := newRepoFs(dir, repo)
	f, err := fs.Open("diff")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var buf []byte
	tmp := make([]byte, 512)
	var off int64
	for {
		n, err := f.ReadAt(tmp, off)
		buf = append(buf, tmp[:n]...)
		off += int64(n)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if strings.TrimSpace(string(buf)) != "" {
		t.Errorf("diff on clean repo = %q, want empty", string(buf))
	}
}

// ---- staged ----

func TestStagedContentEmpty(t *testing.T) {
	tr := setupTestRepo(t)
	content := tr.readSnap(t, "staged")
	// Nothing is staged (file2.txt change is in worktree only)
	if strings.TrimSpace(content) != "" {
		t.Errorf("staged = %q, want empty", content)
	}
}

func TestStagedWriteAndRead(t *testing.T) {
	tr := setupTestRepo(t)

	// Open staged for writing — stages file2.txt
	f, err := tr.fs.OpenFile("staged", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open staged write: %v", err)
	}
	f.WriteString("file2.txt\n")
	if err := f.Close(); err != nil {
		t.Fatalf("stage file2.txt: %v", err)
	}

	content := tr.readSnap(t, "staged")
	if !strings.Contains(content, "file2.txt") {
		t.Errorf("staged after add = %q, want file2.txt", content)
	}
}

// ---- commit ----

func TestCommitWrite(t *testing.T) {
	tr := setupTestRepo(t)

	// Stage file2.txt first
	f, _ := tr.fs.OpenFile("staged", os.O_WRONLY, 0)
	f.WriteString("file2.txt\n")
	f.Close()

	// Commit
	cf, err := tr.fs.OpenFile("commit", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open commit: %v", err)
	}
	cf.WriteString("update file2\n")
	if err := cf.Close(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Verify the new commit appears in log
	content := tr.readSnap(t, "log")
	if !strings.Contains(content, "update file2") {
		t.Errorf("log after commit = %q, missing new message", content)
	}
}

func TestCommitEmptyMessageFails(t *testing.T) {
	tr := setupTestRepo(t)
	cf, _ := tr.fs.OpenFile("commit", os.O_WRONLY, 0)
	cf.WriteString("# this is a comment\n")
	err := cf.Close()
	if err == nil {
		t.Error("expected error for commit with only comment lines")
	}
}

func TestCommitStripsCommentLines(t *testing.T) {
	tr := setupTestRepo(t)

	f, _ := tr.fs.OpenFile("staged", os.O_WRONLY, 0)
	f.WriteString("file2.txt\n")
	f.Close()

	cf, _ := tr.fs.OpenFile("commit", os.O_WRONLY, 0)
	cf.WriteString("# ignored\nreal message\n# also ignored\n")
	if err := cf.Close(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	content := tr.readSnap(t, "log")
	if !strings.Contains(content, "real message") {
		t.Errorf("log = %q, missing 'real message'", content)
	}
}

// ---- reset ----

func TestResetMixed(t *testing.T) {
	tr := setupTestRepo(t)

	// Stage something
	f, _ := tr.fs.OpenFile("staged", os.O_WRONLY, 0)
	f.WriteString("file2.txt\n")
	f.Close()

	// Reset mixed (unstages but keeps worktree changes)
	rf, err := tr.fs.OpenFile("reset", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open reset: %v", err)
	}
	rf.WriteString("mixed\n")
	if err := rf.Close(); err != nil {
		t.Fatalf("reset mixed: %v", err)
	}

	content := tr.readSnap(t, "staged")
	if strings.Contains(content, "file2.txt") {
		t.Errorf("after mixed reset staged = %q, expected empty", content)
	}
}

func TestResetHard(t *testing.T) {
	tr := setupTestRepo(t)

	rf, err := tr.fs.OpenFile("reset", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open reset: %v", err)
	}
	rf.WriteString("hard\n")
	if err := rf.Close(); err != nil {
		t.Fatalf("reset hard: %v", err)
	}

	// After hard reset, file2.txt in worktree should be back to HEAD content
	b, err := os.ReadFile(filepath.Join(tr.path, "file2.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "world\n" {
		t.Errorf("file2.txt after hard reset = %q, want %q", string(b), "world\n")
	}
}

// ---- heads/ ----

func TestHeadsDirListing(t *testing.T) {
	tr := setupTestRepo(t)
	names := tr.readdirNames(t, "heads")
	if !hasName(names, "feature") {
		t.Errorf("heads/ missing 'feature': %v", names)
	}
	// main or master depending on git version
	hasCurrent := hasName(names, "main") || hasName(names, "master")
	if !hasCurrent {
		t.Errorf("heads/ missing current branch: %v", names)
	}
}

func TestBranchDirListing(t *testing.T) {
	tr := setupTestRepo(t)
	// Get current branch name
	head, _ := tr.repo.Head()
	branch := head.Name().Short()

	names := tr.readdirNames(t, "heads/"+branch)
	if !hasName(names, "log") {
		t.Errorf("branch dir missing 'log': %v", names)
	}
	if !hasName(names, "diff") {
		t.Errorf("branch dir missing 'diff': %v", names)
	}
	// Live worktree: should also have disk files
	if !hasName(names, "file1.txt") {
		t.Errorf("branch dir missing 'file1.txt': %v", names)
	}
}

func TestBranchLog(t *testing.T) {
	tr := setupTestRepo(t)
	content := tr.readSnap(t, "heads/feature/log")
	if !strings.Contains(content, "initial commit") {
		t.Errorf("feature log = %q, missing 'initial commit'", content)
	}
	// feature branch should NOT have the second commit
	if strings.Contains(content, "modify file1") {
		t.Errorf("feature log = %q, should not contain second commit", content)
	}
}

func TestBranchDiff(t *testing.T) {
	tr := setupTestRepo(t)
	content := tr.readSnap(t, "heads/feature/diff")
	// feature is behind HEAD by one commit that changed file1.txt
	if !strings.Contains(content, "file1.txt") {
		t.Errorf("feature diff = %q, missing file1.txt", content)
	}
}

func TestBranchTreeFileLiveWorktree(t *testing.T) {
	tr := setupTestRepo(t)
	head, _ := tr.repo.Head()
	branch := head.Name().Short()

	// Reading from the current branch's tree serves the live worktree file
	content := tr.readSnap(t, "heads/"+branch+"/file1.txt")
	if content != "hello modified\n" {
		t.Errorf("heads/%s/file1.txt = %q, want %q", branch, content, "hello modified\n")
	}
}

func TestBranchTreeFileHistorical(t *testing.T) {
	tr := setupTestRepo(t)
	// feature branch has file1.txt at "hello\n" (from initial commit)
	content := tr.readSnap(t, "heads/feature/file1.txt")
	if content != "hello\n" {
		t.Errorf("heads/feature/file1.txt = %q, want %q", content, "hello\n")
	}
}

func TestBranchTreeNestedDir(t *testing.T) {
	tr := setupTestRepo(t)
	content := tr.readSnap(t, "heads/feature/subdir/nested.txt")
	if content != "nested\n" {
		t.Errorf("heads/feature/subdir/nested.txt = %q", content)
	}
}

func TestBranchTreeStatFile(t *testing.T) {
	tr := setupTestRepo(t)
	fi, err := tr.fs.Stat("heads/feature/file1.txt")
	if err != nil {
		t.Fatalf("Stat heads/feature/file1.txt: %v", err)
	}
	if fi.IsDir() {
		t.Error("expected file, got dir")
	}
}

func TestBranchTreeStatDir(t *testing.T) {
	tr := setupTestRepo(t)
	fi, err := tr.fs.Stat("heads/feature/subdir")
	if err != nil {
		t.Fatalf("Stat heads/feature/subdir: %v", err)
	}
	if !fi.IsDir() {
		t.Error("expected dir, got file")
	}
}

// ---- remotes/ ----

func TestRemotesDirListing(t *testing.T) {
	tr := setupTestRepo(t)
	names := tr.readdirNames(t, "remotes")
	if !hasName(names, "origin") {
		t.Errorf("remotes/ missing 'origin': %v", names)
	}
}

func TestRemoteDirListing(t *testing.T) {
	tr := setupTestRepo(t)
	names := tr.readdirNames(t, "remotes/origin")
	if !hasName(names, "main") {
		t.Errorf("remotes/origin missing 'main': %v", names)
	}
}

func TestRemoteBranchDirListing(t *testing.T) {
	tr := setupTestRepo(t)
	names := tr.readdirNames(t, "remotes/origin/main")
	if !hasName(names, "log") {
		t.Errorf("remotes/origin/main missing 'log': %v", names)
	}
}

func TestRemoteBranchLog(t *testing.T) {
	tr := setupTestRepo(t)
	content := tr.readSnap(t, "remotes/origin/main/log")
	if !strings.Contains(content, "modify file1") {
		t.Errorf("remote/origin/main/log = %q, missing expected commit", content)
	}
}

func TestRemoteBranchTree(t *testing.T) {
	tr := setupTestRepo(t)
	content := tr.readSnap(t, "remotes/origin/main/file1.txt")
	if content != "hello modified\n" {
		t.Errorf("remotes/origin/main/file1.txt = %q, want %q", content, "hello modified\n")
	}
}

func TestRemoteStatBranchDir(t *testing.T) {
	tr := setupTestRepo(t)
	fi, err := tr.fs.Stat("remotes/origin/main")
	if err != nil {
		t.Fatalf("Stat remotes/origin/main: %v", err)
	}
	if !fi.IsDir() {
		t.Error("expected dir")
	}
}

func TestRemoteNonexistentBranch(t *testing.T) {
	tr := setupTestRepo(t)
	_, err := tr.fs.Stat("remotes/origin/nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent remote branch")
	}
}

// ---- write-only files reject reads ----

func TestCommitReadReturnsEOF(t *testing.T) {
	tr := setupTestRepo(t)
	f, err := tr.fs.OpenFile("commit", os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, 10)
	n, err := f.ReadAt(buf, 0)
	if n != 0 || err != io.EOF {
		t.Errorf("read commit: n=%d err=%v, want 0/EOF", n, err)
	}
}

// ---- unsupported operations ----

func TestUnsupportedMutations(t *testing.T) {
	tr := setupTestRepo(t)
	if err := tr.fs.Mkdir("newdir", 0755); err == nil {
		t.Error("Mkdir should return error")
	}
	if err := tr.fs.Remove("file1.txt"); err == nil {
		t.Error("Remove should return error")
	}
	if err := tr.fs.Rename("a", "b"); err == nil {
		t.Error("Rename should return error")
	}
	if _, err := tr.fs.Create("new.txt"); err == nil {
		t.Error("Create should return error")
	}
}

// ---- utility helpers ----

func TestUnifiedDiff(t *testing.T) {
	got := unifiedDiff("a/f.txt", "b/f.txt", "old\n", "new\n")
	if !strings.Contains(got, "--- a/f.txt") {
		t.Errorf("diff missing --- header: %q", got)
	}
	if !strings.Contains(got, "+++ b/f.txt") {
		t.Errorf("diff missing +++ header: %q", got)
	}
	if !strings.Contains(got, "-old") {
		t.Errorf("diff missing -old: %q", got)
	}
	if !strings.Contains(got, "+new") {
		t.Errorf("diff missing +new: %q", got)
	}
}

func TestUnifiedDiffIdentical(t *testing.T) {
	got := unifiedDiff("a/f.txt", "b/f.txt", "same\n", "same\n")
	if got != "" {
		t.Errorf("diff of identical content = %q, want empty", got)
	}
}

func TestFirstLine(t *testing.T) {
	cases := [][2]string{
		{"hello\nworld", "hello"},
		{"hello", "hello"},
		{"  hello  \n", "hello"},
		{"", ""},
	}
	for _, tc := range cases {
		got := firstLine(tc[0])
		if got != tc[1] {
			t.Errorf("firstLine(%q) = %q, want %q", tc[0], got, tc[1])
		}
	}
}

func TestSnapReadAt(t *testing.T) {
	data := []byte("hello world")
	buf := make([]byte, 5)

	n, err := snapReadAt(data, buf, 0)
	if n != 5 || err != nil {
		t.Errorf("snapReadAt offset 0: n=%d err=%v", n, err)
	}
	if string(buf[:n]) != "hello" {
		t.Errorf("snapReadAt offset 0 got %q", buf[:n])
	}

	n, err = snapReadAt(data, buf, 6)
	if n != 5 || err != io.EOF {
		t.Errorf("snapReadAt offset 6 (end): n=%d err=%v", n, err)
	}
	if string(buf[:n]) != "world" {
		t.Errorf("snapReadAt offset 6 got %q", buf[:n])
	}

	n, err = snapReadAt(data, buf, 100)
	if n != 0 || err != io.EOF {
		t.Errorf("snapReadAt past end: n=%d err=%v", n, err)
	}
}

// ---- findRepo ----

func TestFindRepo(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git"), 0755)
	subdir := filepath.Join(dir, "a", "b", "c")
	os.MkdirAll(subdir, 0755)

	got := findRepo(subdir)
	if got != dir {
		t.Errorf("findRepo(%q) = %q, want %q", subdir, got, dir)
	}
}

func TestFindRepoFromFile(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git"), 0755)
	f := filepath.Join(dir, "file.txt")
	os.WriteFile(f, []byte("x"), 0644)

	got := findRepo(f)
	if got != dir {
		t.Errorf("findRepo(%q) = %q, want %q", f, got, dir)
	}
}

func TestFindRepoNotFound(t *testing.T) {
	dir := t.TempDir()
	got := findRepo(dir)
	if got != "" {
		t.Errorf("findRepo(%q) = %q, want empty", dir, got)
	}
}
