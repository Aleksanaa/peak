package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"al.essio.dev/pkg/shellescape"
	"github.com/aleksana/peak/internal/vfs/afero"
)

func getVFS() afero.Fs {
	return appEditor.ninep.vfs
}

// toDir ensures a directory path ends with a trailing slash.
func toDir(path string) string {
	if path != "" && !strings.HasSuffix(path, "/") {
		return path + "/"
	}
	return path
}

// getPathDir returns the directory associated with a normalized path.
func getPathDir(path string) string {
	if path == "" {
		return getwd()
	}
	if strings.HasSuffix(path, "/") {
		return path
	}
	return toDir(filepath.Dir(path))
}

// normalizePath converts any user-input path to a canonical absolute form.
// Expands ~, ./, and relative segments; relative paths are resolved against
// base (cwd if base is empty). If the target exists in the VFS, a trailing
// slash is added for directories and stripped for files. For paths that do
// not yet exist, the trailing slash from the input is preserved.
func normalizePath(path, base string) string {
	if path == "" {
		return path
	}
	if !filepath.IsAbs(path) && !strings.HasPrefix(path, "~") {
		if base == "" {
			base = getwd()
		}
		joined := filepath.Join(base, path)
		if strings.HasSuffix(path, "/") && !strings.HasSuffix(joined, "/") {
			joined += "/"
		}
		path = joined
	}
	trailingSlash := strings.HasSuffix(path, "/")
	var abs string
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" || path == "~/" {
				abs = home
			} else {
				abs = filepath.Join(home, path[1:])
			}
		} else {
			abs = path
		}
	} else {
		abs, _ = filepath.Abs(path)
	}
	if fi, err := getVFS().Stat(abs); err == nil {
		if fi.IsDir() {
			return abs + "/"
		}
		return abs
	}
	if trailingSlash {
		return abs + "/"
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
	return toDir(dir)
}

// readFile reads data from a file.
func readFile(path string) ([]byte, error) {
	return afero.ReadFile(getVFS(), path)
}

// writeFile writes data to a file.
func writeFile(path string, data []byte) error {
	return afero.WriteFile(getVFS(), path, data, 0644)
}

// readFileOrDir returns the content of a file or a listing if it's a directory,
// and whether the file is writable (owner-write permission bit set).
func readFileOrDir(path string) (string, bool, bool, error) {
	fi, err := getVFS().Stat(path)
	if err != nil {
		return "", false, false, err
	}
	writable := !fi.IsDir() && fi.Mode().Perm()&0200 != 0
	if fi.IsDir() {
		content, err := listDir(path)
		return content, true, writable, err
	}

	f, err := getVFS().Open(path)
	if err != nil {
		return "", false, writable, err
	}
	defer f.Close()

	size := fi.Size()
	if size > 0 {
		data := make([]byte, size)
		n, err := f.ReadAt(data, 0)
		if err != nil && err != io.EOF {
			return "", false, writable, err
		}
		if int64(n) < int64(len(data)) || err == io.EOF {
			if err := isBinary(data[:n]); err != nil {
				return "", false, writable, err
			}
			return string(data[:n]), false, writable, nil
		}
		if err := isBinary(data); err != nil {
			return "", false, writable, err
		}
		content, err := readFileTail(f, data, int64(len(data)))
		return content, false, writable, err
	}
	content, err := readFileTail(f, nil, 0)
	return content, false, writable, err
}

func isBinary(data []byte) error {
	checkLen := len(data)
	if checkLen > 512 {
		checkLen = 512
	}
	for i := 0; i < checkLen; i++ {
		if data[i] == 0 {
			return fmt.Errorf("binary file")
		}
	}
	return nil
}

func readFileTail(f afero.File, prefix []byte, off int64) (string, error) {
	chunks := prefix
	buf := make([]byte, 4096)
	for {
		n, err := f.ReadAt(buf, off)
		if n > 0 {
			if len(chunks) == 0 {
				if err := isBinary(buf[:n]); err != nil {
					return "", err
				}
			}
			chunks = append(chunks, buf[:n]...)
			off += int64(n)
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return string(chunks), nil
}

// listDir returns a formatted string listing the contents of a directory.
func listDir(path string) (string, error) {
	entries, err := afero.ReadDir(getVFS(), path)
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	names := make([]string, len(entries))
	for i, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names[i] = name
	}
	return strings.Join(names, "\n"), nil
}

// runCommand runs a command with sh -c and returns the output and error.
func runCommand(cmd, path, input string, winid int) (string, error) {
	ninep := appEditor.ninep
	dir := getPathDir(path)
	if mountPath, mountFs := ninep.FindMount(dir); mountPath != "" {
		relPath, _ := filepath.Rel(mountPath, dir)
		if runF, err := mountFs.OpenFile("run", os.O_RDWR, 0); err == nil {
			out, rerr := remoteRun(runF, toDir(relPath), cmd)
			runF.Close()
			return out, rerr
		}
	}
	if localDir, ok := ninep.ResolveLocalPath(dir); ok {
		return runLocalCommand(cmd, path, localDir, input, winid)
	}
	return "", fmt.Errorf("%s: don't know how to run command", path)
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
func runLocalCommand(cmd, path, dir, input string, winid int) (string, error) {
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
