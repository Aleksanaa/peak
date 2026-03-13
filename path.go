package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"al.essio.dev/pkg/shellescape"
	"github.com/spf13/afero"
)

func vfs() afero.Fs {
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

func hasVersion(path string) bool {
	if isSpecial(path) || isDir(path) {
		return false
	}
	if !isPeakPath(path) {
		return true
	}
	if c, ok := vfs().(*CompositeFs); ok {
		return c.HasVersion(path)
	}
	return false
}

func isDir(path string) bool {
	if isSpecial(path) {
		return false
	}
	fi, err := vfs().Stat(path)
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
	return afero.ReadFile(vfs(), path)
}

// writeFile writes data to a file.
func writeFile(path string, data []byte) error {
	if isSpecial(path) {
		return os.ErrInvalid
	}
	return afero.WriteFile(vfs(), path, data, 0644)
}

// readFileOrDir returns the content of a file or a listing if it's a directory.
func readFileOrDir(path string) (string, bool, error) {
	fi, err := vfs().Stat(path)
	if err != nil {
		return "", false, err
	}
	if fi.IsDir() {
		content, err := listDir(path)
		return content, true, err
	}
	data, err := readFile(path)
	if err != nil {
		return "", false, err
	}
	return string(data), false, nil
}

// listDir returns a formatted string listing the contents of a directory.
func listDir(path string) (string, error) {
	entries, err := afero.ReadDir(vfs(), path)
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
	if isPeakPath(path) {
		if appEditor != nil && appEditor.ninep != nil {
			return appEditor.ninep.RunInternal(path, cmd, input, winid)
		}
		return "", fmt.Errorf("%s: virtual path is not initialized to run command", path)
	}
	return runLocalCommand(cmd, path, input, winid)
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
