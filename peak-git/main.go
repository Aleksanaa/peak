package main

import (
	"bufio"
	"crypto/sha256"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	gogit "github.com/go-git/go-git/v5"

	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
)

func main() {
	peakSocket := flag.String("p", "", "peak 9P socket (default: ~/.peak/9p)")
	flag.Parse()
	if *peakSocket == "" {
		home, _ := os.UserHomeDir()
		*peakSocket = filepath.Join(home, ".peak", "9p")
	}
	peakFs, err := vfs.NewNinePClientFs("unix", *peakSocket)
	if err != nil {
		log.Fatalf("connect to peak: %v", err)
	}
	log.Printf("connected to peak at %s", *peakSocket)
	watchEvents(peakFs)
}

// repoState tracks one mounted git repository.
type repoState struct {
	srvF    afero.File       // server end of the /srv virtual socket; Close tears it down
	windows map[string]bool  // set of window IDs currently open in this repo
}

// watchEvents opens /event and processes window lifecycle events. All map
// accesses happen in this single goroutine — no mutex needed.
func watchEvents(peakFs afero.Fs) {
	repos    := make(map[string]*repoState) // repoPath → state
	winRepos := make(map[string]string)     // windowID → repoPath

	// Open the event stream before snapshotting current windows so we don't
	// miss windows that open during the snapshot.
	eventF, err := peakFs.Open("/event")
	if err != nil {
		log.Fatalf("open /event: %v", err)
	}
	defer eventF.Close()

	// Bootstrap: treat all currently open windows as just-opened.
	if entries, err := afero.ReadDir(peakFs, "/"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				handleNew(peakFs, e.Name(), repos, winRepos)
			}
		}
	}

	scanner := bufio.NewScanner(eventF)
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) < 2 {
			continue
		}
		switch parts[0] {
		case "new":
			handleNew(peakFs, parts[1], repos, winRepos)
		case "close":
			handleClose(peakFs, parts[1], repos, winRepos)
		}
	}
}

func handleNew(peakFs afero.Fs, winID string, repos map[string]*repoState, winRepos map[string]string) {
	if _, already := winRepos[winID]; already {
		return
	}
	tag, err := afero.ReadFile(peakFs, "/"+winID+"/tag")
	if err != nil {
		return
	}
	fields := strings.Fields(string(tag))
	if len(fields) == 0 {
		return
	}
	repoPath := findRepo(fields[0])
	if repoPath == "" {
		return
	}

	if repos[repoPath] == nil {
		srvF, err := startAndBindRepo(peakFs, repoPath)
		if err != nil {
			log.Printf("peak-git: start %s: %v", repoPath, err)
			return
		}
		repos[repoPath] = &repoState{
			srvF:    srvF,
			windows: make(map[string]bool),
		}
	}
	repos[repoPath].windows[winID] = true
	winRepos[winID] = repoPath
}

func handleClose(peakFs afero.Fs, winID string, repos map[string]*repoState, winRepos map[string]string) {
	repoPath, ok := winRepos[winID]
	if !ok {
		return
	}
	delete(winRepos, winID)

	state := repos[repoPath]
	if state == nil {
		return
	}
	delete(state.windows, winID)
	if len(state.windows) > 0 {
		return
	}

	// Last window in this repo closed — tear down the server.
	state.srvF.Close()
	delete(repos, repoPath)
	go unbindRepo(peakFs, repoPath)
}

// startAndBindRepo opens the git repo, posts a virtual socket to /srv, serves
// 9P over it, and mounts it into peak's VFS. Returns the server-side file whose
// Close tears down the service.
func startAndBindRepo(peakFs afero.Fs, repoPath string) (afero.File, error) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return nil, fmt.Errorf("open repo: %w", err)
	}

	h := sha256.Sum256([]byte(repoPath))
	name := fmt.Sprintf("git-%x", h[:4])

	srvF, err := peakFs.OpenFile("/srv/"+name, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("srv/%s: %w", name, err)
	}

	srv := vfs.NewNinePSrv(newRepoFs(repoPath, repo))
	go srv.ServeConn(srvF)

	bindF, err := peakFs.OpenFile("/bind", os.O_WRONLY, 0)
	if err != nil {
		srvF.Close()
		return nil, fmt.Errorf("/bind: %w", err)
	}
	fmt.Fprintf(bindF, "/srv/%s %s\n", name, filepath.Join(repoPath, ".git", "fs"))
	bindF.Close()

	return srvF, nil
}

func unbindRepo(peakFs afero.Fs, repoPath string) {
	f, err := peakFs.OpenFile("/unbind", os.O_WRONLY, 0)
	if err != nil {
		log.Printf("peak-git: open /unbind: %v", err)
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", filepath.Join(repoPath, ".git", "fs"))
}

// findRepo walks up from path to find the nearest git worktree root.
func findRepo(path string) string {
	dir := path
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		dir = filepath.Dir(dir)
	}
	for {
		if fi, err := os.Stat(filepath.Join(dir, ".git")); err == nil && fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
