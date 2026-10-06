// Package peakfs holds what peak and the programs that talk to it over 9P
// must agree on: where the socket is and how the /event stream reads.
package peakfs

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aleksana/peak/internal/vfs/afero"
)

// Socket returns the path of peak's 9P socket.
func Socket() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".peak", "9p")
}

// Event is one line of peak's /event stream: "<kind> <id> [name]".
type Event struct {
	Kind string // new, close, focus, get, put
	ID   int
	Name string // the window's file name, which may contain spaces
}

// Watch reports peak's window events to fn: first a "new" event (without a
// name) for every window already open, then each event as it happens. It
// returns when the stream ends.
func Watch(fs afero.Fs, fn func(Event)) error {
	// Open the stream before listing the windows, so one opened in between
	// is still reported (possibly twice).
	f, err := fs.Open("/event")
	if err != nil {
		return err
	}
	defer f.Close()

	entries, _ := afero.ReadDir(fs, "/")
	for _, e := range entries {
		if id, err := strconv.Atoi(e.Name()); err == nil && e.IsDir() {
			fn(Event{Kind: "new", ID: id})
		}
	}

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		kind, rest, _ := strings.Cut(sc.Text(), " ")
		idText, name, _ := strings.Cut(rest, " ")
		if id, err := strconv.Atoi(idText); err == nil {
			fn(Event{Kind: kind, ID: id, Name: name})
		}
	}
	return sc.Err()
}
