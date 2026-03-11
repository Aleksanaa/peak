package main

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed doc
var docFS embed.FS

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
		if path == "/peak" || path == "/peak/" {
			return true
		}
		trimmed := trimPeak(path)
		if trimmed == "doc" {
			return true
		}
		info, err := fs.Stat(docFS, trimmed)
		return err == nil && info.IsDir()
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isFile(path string) bool {
	if isSpecial(path) {
		return false
	}
	if isPeakPath(path) {
		trimmed := trimPeak(path)
		info, err := fs.Stat(docFS, trimmed)
		return err == nil && !info.IsDir()
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
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
		trimmed := trimPeak(path)
		return fs.ReadFile(docFS, trimmed)
	}
	return os.ReadFile(path)
}

// writeFile writes data to a file.
func writeFile(path string, data []byte) error {
	if isSpecial(path) || isDir(path) || isPeakPath(path) {
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

func trimPeak(path string) string {
	p := strings.TrimPrefix(path, "/peak")
	p = strings.TrimPrefix(p, "/")
	return strings.TrimSuffix(p, "/")
}

// listDir returns a formatted string listing the contents of a directory.
func listDir(path string) (string, error) {
	var entries []fs.DirEntry
	var err error

	if isPeakPath(path) {
		if path == "/peak" || path == "/peak/" {
			entries = append(entries, &mockDirEntry{name: "doc", isDir: true})
		} else {
			trimmed := trimPeak(path)
			entries, err = fs.ReadDir(docFS, trimmed)
			if err != nil {
				return "", err
			}
		}
	} else {
		entries, err = os.ReadDir(path)
		if err != nil {
			return "", err
		}
		if path == "/" {
			entries = append(entries, &mockDirEntry{name: "peak", isDir: true})
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].Name() < entries[j].Name()
			})
		}
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
func (m *mockDirEntry) Type() fs.FileMode           { return 0 }
func (m *mockDirEntry) Info() (fs.FileInfo, error) { return nil, nil }

func join(elem ...string) string {
	return filepath.Join(elem...)
}
