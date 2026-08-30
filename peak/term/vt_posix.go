//go:build linux || darwin || dragonfly || solaris || openbsd || netbsd || freebsd

package terminal

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"unicode"
	"unicode/utf8"

	"github.com/creack/pty"
)

// VT represents the virtual terminal emulator.
type VT struct {
	dest *State
	rc   io.ReadCloser
	pty  *os.File
	buf  []byte // read buffer; the first `rem` bytes are a carried partial rune
	rem  int
}

// Start initializes a virtual terminal emulator with the target state
// and a new pty file by starting the *exec.Command. The returned
// *os.File is the pty file.
func Start(state *State, cmd *exec.Cmd) (*VT, *os.File, error) {
	var err error
	t := &VT{
		dest: state,
	}
	t.pty, err = pty.Start(cmd)
	if err != nil {
		return nil, nil, err
	}
	t.rc = t.pty
	t.init()
	t.dest.ResponseWriter = t.pty
	return t, t.pty, nil
}

// Create initializes a virtual terminal emulator with the target state
// and io.ReadCloser input.
func Create(state *State, rc io.ReadCloser) (*VT, error) {
	t := &VT{
		dest: state,
		rc:   rc,
	}
	t.init()
	return t, nil
}

func (t *VT) init() {
	t.buf = make([]byte, 4096)
	t.dest.numlock = true
	t.dest.state = stParse
	t.dest.cur.attr.fg = DefaultFG
	t.dest.cur.attr.bg = DefaultBG
	t.Resize(80, 24)
	t.dest.reset()
}

// File returns the pty file.
func (t *VT) File() *os.File {
	return t.pty
}

// Write parses input and writes terminal changes to state.
func (t *VT) Write(p []byte) (int, error) {
	var written int
	r := bytes.NewReader(p)
	t.dest.lock()
	defer t.dest.unlock()
	for {
		c, sz, err := r.ReadRune()
		if err != nil {
			if err == io.EOF {
				break
			}
			return written, err
		}
		written += sz
		if c == unicode.ReplacementChar && sz == 1 {
			if r.Len() == 0 {
				// not enough bytes for a full rune
				return written - 1, nil
			}
			t.dest.logln("invalid utf8 sequence")
			continue
		}
		t.dest.put(c)
	}
	return written, nil
}

// Close closes the pty or io.ReadCloser.
func (t *VT) Close() error {
	return t.rc.Close()
}

// Parse blocks on a single read from the pty or io.ReadCloser, then decodes and
// parses the whole chunk under one lock. A trailing incomplete rune is carried
// over to the next call. Returns the read error (e.g. io.EOF) after processing
// any bytes that came with it.
func (t *VT) Parse() error {
	n, err := t.rc.Read(t.buf[t.rem:])
	if n > 0 {
		data := t.buf[:t.rem+n]
		t.dest.lock()
		consumed := t.parse(data)
		t.dest.unlock()
		t.rem = len(data) - consumed
		if t.rem > 0 {
			copy(t.buf, data[consumed:])
		}
	}
	return err
}

// parse decodes UTF-8 directly over data, feeding each rune to the state
// machine, and returns the number of bytes consumed. It stops before a trailing
// incomplete rune so the caller can carry those bytes over. The state mutex
// must be held.
func (t *VT) parse(data []byte) int {
	i := 0
	for i < len(data) {
		c := data[i]
		if c < utf8.RuneSelf { // ASCII: no decode needed
			// While collecting CSI parameters, bulk-copy the digit/';' run
			// instead of dispatching each byte through put().
			if t.dest.state == stEscCSI {
				if n := t.dest.feedCSIParams(data[i:]); n > 0 {
					i += n
					continue
				}
			}
			t.dest.put(rune(c))
			i++
			continue
		}
		if !utf8.FullRune(data[i:]) {
			break // incomplete trailing rune; carry over
		}
		r, sz := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && sz == 1 {
			t.dest.logln("invalid utf8 sequence")
		} else {
			t.dest.put(r)
		}
		i += sz
	}
	return i
}

// Resize reports new size to pty and updates state.
func (t *VT) Resize(cols, rows int) {
	t.dest.lock()
	defer t.dest.unlock()
	_ = t.dest.resize(cols, rows)
	t.ptyResize()
}
