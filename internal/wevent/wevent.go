// Package wevent encodes and decodes peak's v2 window event records.
//
// OriginByte TypeByte Q0 SP Q1 SP Flag SP NR SP Text \n
package wevent

import (
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

// MaxTextRunes caps a record's Text length so a malicious or corrupt NR
// field cannot trigger an unbounded allocation in Read.
const MaxTextRunes = 1 << 20

// MaxIntDigits caps an integer field's encoded length so a stream of
// digits cannot consume unbounded memory in readInt.
const MaxIntDigits = 20

// Event is one record in the v2 stream.
type Event struct {
	Origin byte
	Type   byte
	Q0     int
	Q1     int
	Flag   int
	Text   string
}

// Reader is the minimal interface Read needs. Both *bufio.Reader and
// *bytes.Reader satisfy it.
type Reader interface {
	io.ByteReader
	io.RuneReader
}

// Format encodes ev as a single v2 record terminated by '\n'.
func Format(ev Event) []byte {
	nr := utf8.RuneCountInString(ev.Text)
	return fmt.Appendf(nil, "%c%c%d %d %d %d %s\n", ev.Origin, ev.Type, ev.Q0, ev.Q1, ev.Flag, nr, ev.Text)
}

// Read consumes one v2 record from r. At a clean record boundary it
// returns a bare io.EOF; mid-record truncation returns a wrapped error.
func Read(r Reader) (Event, error) {
	var ev Event
	origin, err := r.ReadByte()
	if err != nil {
		if err == io.EOF {
			return ev, io.EOF
		}
		return ev, fmt.Errorf("read event origin: %w", err)
	}
	typ, err := r.ReadByte()
	if err != nil {
		return ev, fmt.Errorf("read event type: %w", err)
	}
	ev.Origin = origin
	ev.Type = typ

	var nr int
	if ev.Q0, err = readInt(r); err != nil {
		return ev, fmt.Errorf("read event q0: %w", err)
	}
	if ev.Q1, err = readInt(r); err != nil {
		return ev, fmt.Errorf("read event q1: %w", err)
	}
	if ev.Flag, err = readInt(r); err != nil {
		return ev, fmt.Errorf("read event flag: %w", err)
	}
	if nr, err = readInt(r); err != nil {
		return ev, fmt.Errorf("read event nr: %w", err)
	}
	if nr < 0 || nr > MaxTextRunes {
		return ev, fmt.Errorf("read event nr: %d out of range", nr)
	}
	text := make([]rune, 0, nr)
	for len(text) < nr {
		rn, size, err := r.ReadRune()
		if err != nil {
			return ev, fmt.Errorf("read event text: %w", err)
		}
		if rn == utf8.RuneError && size == 1 {
			return ev, fmt.Errorf("read event text: invalid utf-8")
		}
		text = append(text, rn)
	}
	ev.Text = string(text)

	term, err := r.ReadByte()
	if err != nil {
		return ev, fmt.Errorf("read event terminator: %w", err)
	}
	if term != '\n' {
		return ev, fmt.Errorf("event terminator = %q, want newline", term)
	}
	return ev, nil
}

func readInt(r io.ByteReader) (int, error) {
	b := make([]byte, 0, MaxIntDigits)
	for {
		c, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		if c == ' ' || c == '\t' {
			if len(b) == 0 || string(b) == "-" {
				return 0, fmt.Errorf("missing integer")
			}
			return strconv.Atoi(string(b))
		}
		if c == '-' && len(b) == 0 {
			b = append(b, c)
			continue
		}
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid integer byte %q", c)
		}
		if len(b) >= MaxIntDigits {
			return 0, fmt.Errorf("integer too long")
		}
		b = append(b, c)
	}
}
