package main

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/aleksana/peak/internal/quote"
	"github.com/aleksana/peak/internal/session"
	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
)

// toDir ensures a directory path ends with a trailing slash.
func toDir(p string) string {
	if p != "" && !strings.HasSuffix(p, "/") {
		return p + "/"
	}
	return p
}

// getPathDir returns the directory associated with a normalized path.
func getPathDir(p string) string {
	if p == "" {
		return getwd()
	}
	if strings.HasSuffix(p, "/") {
		return p
	}
	return toDir(path.Dir(p))
}

// relPath returns p, which is in dir base, relative to base.
func relPath(base, p string) string {
	if rel := strings.Trim(strings.TrimPrefix(p, base), "/"); rel != "" {
		return rel
	}
	return "."
}

// normalizePath converts any user-input path to a canonical absolute form.
// Expands ~, ./, and relative segments; relative paths are resolved against
// base (cwd if base is empty). An OS path (C:\Users on Windows) becomes the
// namespace's (/c/Users). If the target exists in the VFS, a trailing
// slash is added for directories and stripped for files. For paths that do
// not yet exist, the trailing slash from the input is preserved.
func normalizePath(p, base string) string {
	if p == "" {
		return p
	}
	p = vfs.FromOS(p)
	if !path.IsAbs(p) && !strings.HasPrefix(p, "~") {
		if base == "" {
			base = getwd()
		}
		joined := path.Join(base, p)
		if strings.HasSuffix(p, "/") && !strings.HasSuffix(joined, "/") {
			joined += "/"
		}
		p = joined
	}
	trailingSlash := strings.HasSuffix(p, "/")
	var abs string
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			abs = path.Join(vfs.FromOS(home), p[1:])
		} else {
			abs = p
		}
	} else {
		abs = path.Clean(p)
	}
	if fi, err := ns.Stat(abs); err == nil {
		if fi.IsDir() {
			return toDir(abs)
		}
		return abs
	}
	if trailingSlash {
		return toDir(abs)
	}
	return abs
}

// commonPathBase finds the longest common path-component suffix between tagDir
// (which must have a trailing slash) and firstPath, returning the preceding
// prefixes: tagBase (the local/ninep side) and remoteBase (the shell side).
// These are used to map subsequent OSC 7 paths: strip remoteBase, prepend tagBase.
// Returns ("", "") if tagDir is not absolute.
func commonPathBase(tagDir, firstPath string) (tagBase, remoteBase string) {
	if !strings.HasPrefix(tagDir, "/") {
		return
	}
	ta := strings.Split(strings.TrimSuffix(tagDir, "/"), "/")
	tb := strings.Split(firstPath, "/")
	i, j := len(ta)-1, len(tb)-1
	for i >= 0 && j >= 0 && ta[i] == tb[j] {
		i--
		j--
	}
	return strings.Join(ta[:i+1], "/"), strings.Join(tb[:j+1], "/")
}

// getwd returns the current working directory with a trailing slash.
func getwd() string {
	dir, _ := os.Getwd()
	return toDir(vfs.FromOS(dir))
}

// readFile reads data from a file.
func readFile(path string) ([]byte, error) {
	return afero.ReadFile(ns, path)
}

// writeText writes text to a file, stored in format f.
func writeText(path, text string, f textFormat) error {
	return afero.WriteFile(ns, path, f.encode(text), 0644)
}

// errBinary is how reading a file that is not text fails.
var errBinary = errors.New("binary file")

// readFileOrDir returns the text of a file and its format, or the listing
// of a directory, and whether the file is writable (owner-write permission
// bit set). A file that is not text is errBinary. It reads to EOF, since a
// served file's size need not be its length.
func readFileOrDir(path string) (content string, format textFormat, isDir, writable bool, err error) {
	fi, err := ns.Stat(path)
	if err != nil {
		return "", format, false, false, err
	}
	if fi.IsDir() {
		content, err := listDir(path)
		return content, format, true, false, err
	}
	f, err := ns.Open(path)
	if err != nil {
		return "", format, false, false, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.NewSectionReader(f, 0, math.MaxInt64))
	if err != nil {
		return "", format, false, false, err
	}
	content, format, err = decodeText(data)
	return content, format, false, fi.Mode().Perm()&0200 != 0, err
}

// listDir returns a formatted string listing the contents of a directory.
func listDir(path string) (string, error) {
	entries, err := afero.ReadDir(ns, path)
	if err != nil {
		return "", err
	}
	// Some file systems, such as Windows' WebDAV client, list these too.
	entries = slices.DeleteFunc(entries, func(e os.FileInfo) bool { return e.Name() == "." || e.Name() == ".." })
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	names := make([]string, len(entries))
	for i, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names[i] = quote.Quote(name)
	}
	return strings.Join(names, "\n"), nil
}

// runCommand runs cmd with sh -c for the window named path, numbered winid,
// with input on its standard input, and returns what it printed. In a
// directory a file server mounts, the server runs it, through its run file,
// which takes no input. In a local one it runs here, with $samfile and
// $winid naming the window, as acme does.
func runCommand(cmd, path, input string, winid int) (string, error) {
	dir := getPathDir(path)
	if mountPath, mountFs := ns.FindMount(dir); mountPath != "" {
		if f, err := mountFs.OpenFile("run", os.O_RDWR, 0); err == nil {
			defer f.Close()
			relPath := relPath(mountPath, dir)
			if _, err := f.WriteAt([]byte(toDir(relPath)+"\n"+cmd+"\n"), 0); err != nil {
				return "", err
			}
			out, err := io.ReadAll(io.NewSectionReader(f, 0, math.MaxInt64))
			return string(out), err
		}
	}
	localDir, ok := ns.ResolveLocalPath(dir)
	if !ok {
		return "", fmt.Errorf("%s: don't know how to run command", path)
	}
	c := session.Command(cmd)
	c.Dir = localDir
	c.Env = append(os.Environ(), "samfile="+path, "winid="+strconv.Itoa(winid))
	c.Stdin = strings.NewReader(input)
	out, err := c.CombinedOutput()
	return session.Output(out), err
}
