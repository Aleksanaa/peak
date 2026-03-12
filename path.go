package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func isSpecial(path string) bool {
	return strings.HasSuffix(path, "+Errors")
}

// toDir ensures a directory path ends with a trailing slash.
func toDir(path string) string {
	if path == "" {
		return ""
	}
	if !strings.HasSuffix(path, "/") {
		return path + "/"
	}
	return path
}

func isPeakPath(path string) bool {
	return strings.HasPrefix(path, "/peak/") || path == "/peak"
}

func isDir(path string) bool {
	if isSpecial(path) {
		return false
	}
	if isPeakPath(path) {
		if appEditor != nil && appEditor.ninep != nil {
			return appEditor.ninep.IsDirInternal(path)
		}
		return path == "/peak" || path == "/peak/"
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isFile(path string) bool {
	if isSpecial(path) {
		return false
	}
	if isPeakPath(path) {
		if appEditor != nil && appEditor.ninep != nil {
			return appEditor.ninep.IsFileInternal(path)
		}
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func hasVersion(path string) bool {
	return isFile(path) && !isPeakPath(path) && !isSpecial(path)
}

// getPathDir returns the directory associated with a path.
// If the path is a directory, it returns the path itself (with a trailing slash).
// Otherwise, it returns the directory containing the file (with a trailing slash).
func getPathDir(path string) string {
	if path == "" {
		return getwd()
	}
	if isSpecial(path) {
		return toDir(filepath.Dir(path))
	}
	if isPeakPath(path) {
		if isDir(path) {
			return toDir(path)
		}
		return toDir(filepath.Dir(path))
	}
	abs := resolvePath(path)
	if isDir(abs) {
		return toDir(abs)
	}
	return toDir(filepath.Dir(abs))
}

// resolvePath returns an absolute path, expanding ~ and handling relative segments.
func resolvePath(path string) string {
	if path == "" {
		return ""
	}
	if isPeakPath(path) {
		return path
	}
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		if path == "~" {
			return home
		}
		return filepath.Join(home, path[1:])
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
		res := resolvePath(path)
		if isDir(res) {
			return toDir(res)
		}
		return res
	}
	if contextDir == "" {
		contextDir = getwd()
	}
	if isPeakPath(contextDir) {
		res := filepath.Join(contextDir, path)
		if isDir(res) {
			return toDir(res)
		}
		return res
	}
	res := filepath.Join(contextDir, path)
	if isDir(res) {
		return toDir(res)
	}
	return res
}

// formatPath formats a full path relative to a context path.
func formatPath(fullPath, contextPath string) string {
	if isPeakPath(fullPath) {
		return fullPath
	}
	if contextPath == "" {
		return fullPath
	}

	if strings.HasPrefix(contextPath, "~") {
		if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(fullPath, home) {
			res := "~" + fullPath[len(home):]
			if isDir(fullPath) {
				return toDir(res)
			}
			return res
		}
	} else if !filepath.IsAbs(contextPath) {
		cwd, _ := os.Getwd()
		if rel, err := filepath.Rel(cwd, fullPath); err == nil {
			if !strings.HasPrefix(rel, ".") && !strings.HasPrefix(rel, "/") {
				res := "./" + rel
				if isDir(fullPath) {
					return toDir(res)
				}
				return res
			}
			if isDir(fullPath) {
				return toDir(rel)
			}
			return rel
		}
	}
	if isDir(fullPath) {
		return toDir(fullPath)
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
	if isDir(path) || isSpecial(path) {
		return nil, os.ErrInvalid
	}
	if isPeakPath(path) {
		if appEditor != nil && appEditor.ninep != nil {
			return appEditor.ninep.ReadInternal(path)
		}
		return nil, os.ErrNotExist
	}
	return os.ReadFile(path)
}

// writeFile writes data to a file.
func writeFile(path string, data []byte) error {
	if isSpecial(path) || isDir(path) {
		return os.ErrInvalid
	}
	if isPeakPath(path) {
		if appEditor != nil && appEditor.ninep != nil {
			return appEditor.ninep.WriteInternal(path, data)
		}
		return os.ErrInvalid
	}
	return os.WriteFile(path, data, 0644)
}

// readFileOrDir returns the content of a file or a listing if it's a directory.
func readFileOrDir(path string) (string, error) {
	if isDir(path) {
		return listDir(path)
	}
	data, err := readFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// listDir returns a formatted string listing the contents of a directory.
func listDir(path string) (string, error) {
	if isPeakPath(path) {
		if appEditor != nil && appEditor.ninep != nil {
			return appEditor.ninep.ListDirInternal(path)
		}
		return "", os.ErrNotExist
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return "", err
	}

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

type mockDirEntry struct {
	name  string
	isDir bool
}

func (m *mockDirEntry) Name() string               { return m.name }
func (m *mockDirEntry) IsDir() bool                { return m.isDir }
func (m *mockDirEntry) Type() os.FileMode          { return 0 }
func (m *mockDirEntry) Info() (os.FileInfo, error) { return nil, nil }

func join(elem ...string) string {
	return filepath.Join(elem...)
}

// runCommand runs a command with sh -c and returns the output and error.
func runCommand(cmd, dir, input string) (string, error) {
	if isPeakPath(dir) {
		return "", fmt.Errorf("%s: cannot execute external command in virtual filesystem", dir)
	}

	c := exec.Command("sh", "-c", cmd)
	c.Dir = dir
	if input != "" {
		c.Stdin = strings.NewReader(input)
	}

	out, err := c.CombinedOutput()
	return string(out), err
}
