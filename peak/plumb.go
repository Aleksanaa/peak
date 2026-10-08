package main

import (
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/aleksana/peak/internal/quote"
)

var (
	plumbRx  = regexp.MustCompile(`^([^\x00]*?)(?:([^:]):(\d+)(?::(\d+))?)?$`)
	httpRx   = regexp.MustCompile(`https?://[a-zA-Z0-9][-a-zA-Z0-9.]*(?::\d+)?(?:/[^\s"'>]*)?`)
	mailtoRx = regexp.MustCompile(`mailto:[^\s"'>]+@[^\s"'>]+(\?[^\s"'>]*)?`)
	magnetRx = regexp.MustCompile(`magnet:\?xt=urn:[a-z0-9]+:[a-z0-9]{32,128}[^"'\s<>]*`)
)

// OpenExternal opens a path using the system's default application
func OpenExternal(path string) error {
	cmdName := "xdg-open"
	if runtime.GOOS == "darwin" {
		cmdName = "open"
	}
	return exec.Command(cmdName, path).Start()
}

// Plumb attempts to handle a string (path or search).
func (e *Editor) Plumb(win *Window, word string) bool {
	word = strings.TrimSpace(word)
	if word == "" {
		return false
	}
	// A quoted name, as clicked in a listing, plumbs as the name it quotes.
	if f := quote.Fields(word); len(f) == 1 {
		word = f[0]
	}

	// Try protocol handlers first
	if httpRx.MatchString(word) || mailtoRx.MatchString(word) || magnetRx.MatchString(word) {
		if err := OpenExternal(word); err == nil {
			return false
		}
	}

	m := plumbRx.FindStringSubmatch(word)
	if m == nil {
		return false
	}
	path := m[1] + m[2]
	line, _ := strconv.Atoi(m[3])
	col := -1
	if m[4] != "" {
		col, _ = strconv.Atoi(m[4])
	}
	// A word that is no file is looked for; a file that is not text is the
	// system's to open.
	e.OpenLine(win, path, line-1, col, func(full string, err error) {
		switch {
		case os.IsNotExist(err):
			e.Execute(nil, win, "Look "+word)
		case err == errBinary:
			OpenExternal(full)
		default:
			e.showError(nil, win, full+": "+normalizeError(err))
		}
	})
	return false
}
