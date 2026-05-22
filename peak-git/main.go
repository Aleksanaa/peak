package main

import (
	"bufio"
	"crypto/sha256"
	"flag"
	"fmt"
	"log"
	"net"
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
	sockPath string
	listener net.Listener
	windows  map[string]bool // set of window IDs currently open in this repo
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
		listener, sockPath, err := startRepoServer(repoPath)
		if err != nil {
			log.Printf("peak-git: start %s: %v", repoPath, err)
			return
		}
		repos[repoPath] = &repoState{
			sockPath: sockPath,
			listener: listener,
			windows:  make(map[string]bool),
		}
		go bindRepo(peakFs, sockPath, repoPath)
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
	state.listener.Close()
	os.Remove(state.sockPath)
	delete(repos, repoPath)
	go unbindRepo(peakFs, repoPath)
}

// startRepoServer opens the git repo, listens on a per-repo Unix socket, and
// starts serving 9P. Returns the listener and socket path.
func startRepoServer(repoPath string) (net.Listener, string, error) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return nil, "", fmt.Errorf("open repo: %w", err)
	}

	h := sha256.Sum256([]byte(repoPath))
	sockPath := fmt.Sprintf("/tmp/peak-git-%x.sock", h[:4])
	os.Remove(sockPath)

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, "", fmt.Errorf("listen: %w", err)
	}

	srv := vfs.NewNinePSrv(newRepoFs(repoPath, repo))
	go srv.ServeListener(l)
	return l, sockPath, nil
}

func bindRepo(peakFs afero.Fs, sockPath, repoPath string) {
	f, err := peakFs.OpenFile("/bind", os.O_WRONLY, 0)
	if err != nil {
		log.Printf("peak-git: open /bind: %v", err)
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", sockPath, filepath.Join(repoPath, ".git", "fs"))
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
