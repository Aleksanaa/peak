package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	gogit "github.com/go-git/go-git/v5"

	"github.com/aleksana/peak/internal/peakfs"
	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
)

func main() {
	peakSocket := flag.String("p", peakfs.Socket(), "peak 9P socket")
	flag.Parse()
	peakFs, err := vfs.NewNinePClientFs("unix", *peakSocket)
	if err != nil {
		log.Fatalf("connect to peak: %v", err)
	}
	log.Printf("connected to peak at %s", *peakSocket)
	watchEvents(peakFs)
}

// repoState tracks one mounted git repository.
type repoState struct {
	serverF   afero.File // tears down the /srv entry when closed
	mountPath string     // peak VFS path where the repo FS is mounted
	windows   map[string]bool
}

// watchEvents opens /event and processes window lifecycle events. All map
// accesses happen in this single goroutine — no mutex needed.
func watchEvents(peakFs afero.Fs) {
	repos := make(map[string]*repoState) // repoPath → state
	winRepos := make(map[string]string)  // windowID → repoPath

	err := peakfs.Watch(peakFs, func(ev peakfs.Event) {
		id := strconv.Itoa(ev.ID)
		switch ev.Kind {
		case "new":
			handleNew(peakFs, id, repos, winRepos)
		case "close":
			handleClose(peakFs, id, repos, winRepos)
		}
	})
	if err != nil {
		log.Fatalf("watch peak events: %v", err)
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
		serverF, mountPath, err := startAndBindRepo(peakFs, repoPath)
		if err != nil {
			log.Printf("peak-git: start %s: %v", repoPath, err)
			return
		}
		repos[repoPath] = &repoState{
			serverF:   serverF,
			mountPath: mountPath,
			windows:   make(map[string]bool),
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
	state.serverF.Close()
	mp := state.mountPath
	delete(repos, repoPath)
	go unbindRepo(peakFs, mp)
}

// startAndBindRepo opens the git repo, posts a virtual socket to /srv, serves
// 9P over it, and mounts it into peak's VFS. Returns the server file whose
// Close tears down the service, and the peak VFS path where the FS is mounted.
func startAndBindRepo(peakFs afero.Fs, repoPath string) (afero.File, string, error) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return nil, "", fmt.Errorf("open repo: %w", err)
	}

	h := sha256.Sum256([]byte(repoPath))
	name := fmt.Sprintf("git-%x", h[:4])

	serverF, err := peakFs.OpenFile("/srv/"+name, os.O_RDWR, 0)
	if err != nil {
		return nil, "", fmt.Errorf("srv/%s: %w", name, err)
	}

	srv := vfs.NewNinePSrv(newRepoFs(repoPath, repo))
	go srv.ServeConn(serverF)

	mountF, err := peakFs.OpenFile("/mount", os.O_WRONLY, 0)
	if err != nil {
		serverF.Close()
		return nil, "", fmt.Errorf("/mount: %w", err)
	}
	mountPath := repoPath + "/.git/fs"
	fmt.Fprintf(mountF, "/peak/srv/%s %s\n", name, mountPath)
	mountF.Close()

	return serverF, mountPath, nil
}

func unbindRepo(peakFs afero.Fs, mountPath string) {
	f, err := peakFs.OpenFile("/unmount", os.O_WRONLY, 0)
	if err != nil {
		log.Printf("peak-git: open /unmount: %v", err)
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", mountPath)
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
