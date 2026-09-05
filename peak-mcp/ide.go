package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Claude Code discovers editors by scanning ~/.claude/ide for <port>.lock
// files, then connects to that port over a WebSocket authenticated with the
// token from the lock file. A terminal whose environment names the port skips
// the picker and connects on its own, which is what peak-mcp publishes to
// /peak/env so any Win opened afterwards is already wired up.
const (
	ideDirName   = ".claude/ide"
	portEnvKey   = "CLAUDE_CODE_SSE_PORT"
	enableEnvKey = "ENABLE_IDE_INTEGRATION"
)

// ideLock is the on-disk advertisement Claude Code reads.
type ideLock struct {
	PID              int      `json:"pid"`
	WorkspaceFolders []string `json:"workspaceFolders"`
	IDEName          string   `json:"ideName"`
	Transport        string   `json:"transport"`
	AuthToken        string   `json:"authToken"`
}

// newAuthToken returns a 128-bit lowercase hex token from the OS CSPRNG.
func newAuthToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("auth token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// writeIDELock advertises the server at port and returns the lock file path.
func writeIDELock(port int, token string, workspace []string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ideDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	body, err := json.Marshal(ideLock{
		PID:              os.Getpid(),
		WorkspaceFolders: workspace,
		IDEName:          "Peak",
		Transport:        "ws",
		AuthToken:        token,
	})
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("%d.lock", port))
	if err := os.WriteFile(path, body, 0600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// publishEnv puts the two variables Claude Code looks for into peak's global
// environment overlay, so terminals opened after this point auto-connect.
// Terminals already running are unaffected; that is peak's rule, not ours.
func publishEnv(p *peakConn, port int) error {
	return p.write("/env", fmt.Sprintf("%s=%d\n%s=true\n", portEnvKey, port, enableEnvKey))
}

// withdrawEnv removes what publishEnv set, so a terminal opened after peak-mcp
// exits does not try to connect to a port nobody is listening on.
func withdrawEnv(p *peakConn) error {
	return p.write("/env", fmt.Sprintf("%s\n%s\n", portEnvKey, enableEnvKey))
}

// workspaceFolders reports the directories peak currently has open, falling
// back to the working directory peak-mcp was started in. Claude Code uses
// these as the roots of the project it is working in.
func workspaceFolders(p *peakConn) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(dir string) {
		if dir == "" || dir == "." || seen[dir] {
			return
		}
		seen[dir] = true
		out = append(out, dir)
	}

	wins, err := p.Index()
	if err != nil {
		log.Printf("workspace folders: %v", err)
	}
	for _, w := range wins {
		if w.Name == "" || !strings.HasPrefix(w.Name, "/") {
			continue // scratch windows (+Errors, New) have no path
		}
		if w.IsDir {
			add(strings.TrimSuffix(w.Name, "/"))
			continue
		}
		add(filepath.Dir(w.Name))
	}
	if len(out) == 0 {
		if cwd, err := os.Getwd(); err == nil {
			add(cwd)
		}
	}
	return out
}

// sweepStaleLocks removes lock files this program left behind on a previous
// run whose process is gone. A stale lock advertises a port nobody is
// listening on; an agent that picks it waits out its connect timeout instead
// of finding the peak that is actually running.
func sweepStaleLocks() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ideDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".lock") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var lock ideLock
		if err := json.Unmarshal(body, &lock); err != nil {
			continue
		}
		// Only ever clean up after ourselves: another editor's lock is none
		// of our business, however dead it looks.
		if lock.IDEName != "Peak" || processAlive(lock.PID) {
			continue
		}
		if err := os.Remove(path); err != nil {
			log.Printf("remove stale lock %s: %v", path, err)
			continue
		}
		log.Printf("removed stale lock %s (pid %d is gone)", path, lock.PID)
	}
}

// processAlive reports whether pid is still running. Signal 0 performs the
// permission and existence checks without delivering anything.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
