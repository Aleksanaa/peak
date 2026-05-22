package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"al.essio.dev/pkg/shellescape"
	"github.com/aleksana/peak/internal/vfs/afero"
)

func getVFS() afero.Fs {
	if appEditor != nil && appEditor.ninep != nil {
		return appEditor.ninep.vfs
	}
	return afero.NewOsFs()
}

func isSpecial(path string) bool {
	return strings.HasSuffix(path, "+Errors")
}

// toDir ensures a directory path ends with a trailing slash.
func toDir(path string) string {
	if path != "" && !strings.HasSuffix(path, "/") {
		return path + "/"
	}
	return path
}

func isPeakPath(path string) bool {
	return strings.HasPrefix(path, "/peak/") || path == "/peak"
}

// parseWinPath returns the window ID and optional file name for paths of the
// form /peak/<id> or /peak/<id>/<file>. Returns ok=false for any other path.
func parseWinPath(path string) (id int, file string, ok bool) {
	rest, found := strings.CutPrefix(path, "/peak/")
	if !found {
		return 0, "", false
	}
	idStr, file, _ := strings.Cut(rest, "/")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return 0, "", false
	}
	return id, file, true
}

// findWinByID returns the window with the given ID, or nil.
func findWinByID(id int) *Window {
	if appEditor == nil {
		return nil
	}
	for _, col := range appEditor.columns {
		for _, win := range col.windows {
			if win.ID == id {
				return win
			}
		}
	}
	return nil
}

func hasVersion(path string) bool {
	if isSpecial(path) {
		return false
	}
	// Window namespace files are virtual — they have no on-disk version.
	if _, _, ok := parseWinPath(path); ok {
		return false
	}
	if isDir(path) {
		return false
	}
	return !isPeakPath(path)
}

func isDir(path string) bool {
	if isSpecial(path) {
		return false
	}
	// /peak/<id> is a directory; /peak/<id>/<file> is not.
	// Resolved without touching the VFS to avoid editor.Call on the main goroutine.
	if id, file, ok := parseWinPath(path); ok {
		return file == "" && findWinByID(id) != nil
	}
	fi, err := getVFS().Stat(path)
	return err == nil && fi.IsDir()
}

// getPathDir returns the directory associated with a path.
func getPathDir(path string) string {
	if path == "" {
		return getwd()
	}
	if isSpecial(path) {
		return toDir(filepath.Dir(path))
	}
	// Purely string-based. If it ends in /, it's a dir.
	// Otherwise, we take the directory part of the path.
	if strings.HasSuffix(path, "/") {
		return path
	}
	return toDir(filepath.Dir(resolvePath(path)))
}

// resolvePath returns an absolute path, expanding ~ and handling relative segments.
func resolvePath(path string) string {
	if path == "" {
		return path
	}
	if isPeakPath(path) {
		return filepath.ToSlash(filepath.Clean(path))
	}
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			if path == "~" {
				return home
			}
			return filepath.Join(home, path[1:])
		}
	}
	abs, _ := filepath.Abs(path)
	return abs
}

// resolveWithContext resolves a path within a given context directory.
func resolveWithContext(path, contextDir string) string {
	if path == "" {
		return ""
	}
	if isPeakPath(path) || filepath.IsAbs(path) || strings.HasPrefix(path, "~") {
		return resolvePath(path)
	}
	if contextDir == "" {
		contextDir = getwd()
	}
	// Pure string joining and cleaning.
	res := filepath.Join(contextDir, path)
	if isPeakPath(res) {
		return filepath.ToSlash(filepath.Clean(res))
	}
	return res
}

// formatPath formats a full path relative to a context path.
func formatPath(fullPath, contextPath string) string {
	if isPeakPath(fullPath) || contextPath == "" {
		return fullPath
	}

	if strings.HasPrefix(contextPath, "~") {
		if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(fullPath, home) {
			return "~" + fullPath[len(home):]
		}
	} else if !filepath.IsAbs(contextPath) {
		cwd, _ := os.Getwd()
		if rel, err := filepath.Rel(cwd, fullPath); err == nil {
			if !strings.HasPrefix(rel, ".") && !strings.HasPrefix(rel, "/") {
				rel = "./" + rel
			}
			return rel
		}
	}
	return fullPath
}

// getwd returns the current working directory with a trailing slash.
func getwd() string {
	dir, _ := os.Getwd()
	return toDir(dir)
}

// readFile reads data from a file.
func readFile(path string) ([]byte, error) {
	return afero.ReadFile(getVFS(), path)
}

// writeFile writes data to a file.
func writeFile(path string, data []byte) error {
	if isSpecial(path) {
		return os.ErrInvalid
	}
	return afero.WriteFile(getVFS(), path, data, 0644)
}

// readFileOrDir returns the content of a file or a listing if it's a directory.
// It is called from a background goroutine, so editor.Call inside VFS reads is safe.
func readFileOrDir(path string) (string, bool, error) {
	fi, err := getVFS().Stat(path)
	if err != nil {
		return "", false, err
	}
	if fi.IsDir() {
		content, err := listDir(path)
		return content, true, err
	}

	f, err := getVFS().Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	size := fi.Size()
	if size > 0 {
		data := make([]byte, size)
		n, err := f.ReadAt(data, 0)
		if err != nil && err != io.EOF {
			return "", false, err
		}
		checkLen := n
		if checkLen > 512 {
			checkLen = 512
		}
		for i := 0; i < checkLen; i++ {
			if data[i] == 0 {
				return "", false, fmt.Errorf("binary file")
			}
		}
		return string(data[:n]), false, nil
	}

	// Unknown size: accumulate with ReadAt, check first 512 bytes for binary.
	var chunks []byte
	buf := make([]byte, 4096)
	var off int64
	for {
		n, err := f.ReadAt(buf, off)
		if n > 0 {
			if len(chunks) == 0 {
				checkLen := n
				if checkLen > 512 {
					checkLen = 512
				}
				for i := 0; i < checkLen; i++ {
					if buf[i] == 0 {
						return "", false, fmt.Errorf("binary file")
					}
				}
			}
			chunks = append(chunks, buf[:n]...)
			off += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", false, err
		}
	}
	return string(chunks), false, nil
}

// listDir returns a formatted string listing the contents of a directory.
func listDir(path string) (string, error) {
	entries, err := afero.ReadDir(getVFS(), path)
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var sb strings.Builder
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		sb.WriteString(name + "\n")
	}
	return sb.String(), nil
}

func join(elem ...string) string {
	return filepath.Join(elem...)
}

// runCommand runs a command with sh -c and returns the output and error.
func runCommand(cmd, path, input string, winid int) (string, error) {
	// Check for remote execution before isPeakPath, so mounts under /peak/
	// (e.g. /peak/ssh/...) can delegate to their filesystem's "run" file.
	if appEditor != nil && appEditor.ninep != nil {
		if mountPath, mountFs := appEditor.ninep.FindMount(path); mountPath != "" {
			relPath, _ := filepath.Rel(mountPath, path)
			runF, err := mountFs.OpenFile("run", os.O_RDWR, 0)
			if err == nil {
				out, rerr := remoteRun(runF, relPath, cmd)
				runF.Close()
				return out, rerr
			}
		}
	}
	if isPeakPath(path) {
		if appEditor != nil && appEditor.ninep != nil {
			return appEditor.ninep.RunInternal(path, cmd, input, winid)
		}
		return "", fmt.Errorf("%s: virtual path is not initialized to run command", path)
	}
	return runLocalCommand(cmd, path, input, winid)
}

func remoteRun(f afero.File, relPath, cmd string) (string, error) {
	if _, err := f.WriteAt([]byte(relPath+"\n"+cmd+"\n"), 0); err != nil {
		return "", err
	}
	var sb strings.Builder
	buf := make([]byte, 4096)
	var off int64
	for {
		n, err := f.ReadAt(buf, off)
		if n > 0 {
			sb.Write(buf[:n])
			off += int64(n)
		}
		if err != nil {
			break
		}
	}
	return sb.String(), nil
}

// runLocalCommand executes a command on the local OS.
func runLocalCommand(cmd, path, input string, winid int) (string, error) {
	dir := getPathDir(path)
	wrappedCmd := fmt.Sprintf("env samfile=%s winid=%d sh -c %s",
		shellescape.Quote(path),
		winid,
		shellescape.Quote(cmd))

	c := exec.Command("sh", "-c", wrappedCmd)
	c.Dir = dir
	if input != "" {
		c.Stdin = strings.NewReader(input)
	}

	out, err := c.CombinedOutput()
	return string(out), err
}
