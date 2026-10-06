package main

import (
	"strings"
	"unicode"

	"github.com/aleksana/peak/internal/quote"
)

// Cursor represents a 2D position.
type Cursor struct {
	x, y int
}

// Selection represents a selected range.
type Selection struct {
	Start  Cursor
	End    Cursor
	Active bool
}

func (s Selection) Ordered() (Cursor, Cursor) {
	if s.Start.y > s.End.y || (s.Start.y == s.End.y && s.Start.x > s.End.x) {
		return s.End, s.Start
	}
	return s.Start, s.End
}

func (s Selection) Contains(x, y int, inclusive bool) bool {
	if !s.Active {
		return false
	}
	start, end := s.Ordered()
	if y < start.y || y > end.y {
		return false
	}
	if y == start.y && y == end.y {
		if inclusive {
			return x >= start.x && x <= end.x
		}
		return x >= start.x && x < end.x
	}
	if y == start.y {
		return x >= start.x
	}
	if y == end.y {
		if inclusive {
			return x <= end.x
		}
		return x < end.x
	}
	return true
}

// ScrollState handles scrolling logic.
type ScrollState struct {
	Pos        int
	AutoScroll bool
}

func (s *ScrollState) Clamp(total, visible int) {
	s.Pos = max(0, min(total, s.Pos))
}

func (s *ScrollState) Scroll(n int, total, visible int) {
	s.Pos += n
	s.Clamp(total, visible)
	if s.Pos >= max(0, total-visible) {
		s.AutoScroll = true
	} else if n < 0 {
		s.AutoScroll = false
	}
}

func IsWordChar(r rune) bool {
	return r != 0 && !unicode.IsSpace(r)
}

// clickRange returns what a click at p in b stands for, as the rune offsets
// [q0, q1) of b and their text. A click in the selection takes the
// selection; so does a click on blank space, since we cannot set the mouse
// cursor position like acme. Otherwise it takes the word at p: a field (see
// package quote), so a backtick-quoted name is one word. Quoted text stands
// for its contents, as if they were selected: the range is inside the
// backticks and the text is unquoted.
func clickRange(b *Buffer, p Cursor) (q0, q1 int, text string) {
	start, end := p, p
	if p.y >= 0 && p.y < len(b.lines) {
		if line := b.lines[p.y]; p.x >= 0 && p.x < len(line) {
			s, e := quote.FieldAt(p.x, len(line), func(i int) rune { return line[i] })
			start, end = Cursor{s, p.y}, Cursor{e, p.y}
		}
	}
	if strings.TrimSpace(b.GetSelectedText()) != "" && (b.selection.Contains(p.x, p.y, false) || start == end) {
		start, end = b.selection.Ordered()
	}

	q0, q1 = b.RuneOffsetOfPos(start.y, start.x), b.RuneOffsetOfPos(end.y, end.x)
	r := b.RunesInRange(q0, q1)
	for len(r) > 0 && unicode.IsSpace(r[0]) {
		r, q0 = r[1:], q0+1
	}
	for len(r) > 0 && unicode.IsSpace(r[len(r)-1]) {
		r, q1 = r[:len(r)-1], q1-1
	}
	text = string(r)
	if f := quote.Unquote(text); f != text {
		if strings.ContainsFunc(f, unicode.IsSpace) { // Quote wrapped it in backticks
			q0, q1 = q0+1, q1-1
		}
		text = f
	}
	return q0, q1, text
}

// Search performs a two-pass search (forward from start, then wrap around).
// It returns the line number, the resulting selection, and true if found.
func Search(buf *Buffer, word string, start Cursor) (int, Selection, bool) {
	if word == "" {
		return -1, Selection{}, false
	}
	lines := buf.lines
	count := len(lines)
	if count == 0 {
		return -1, Selection{}, false
	}
	wordRunes := []rune(word)
	wn := len(wordRunes)
	startRX, startRY := start.x+1, start.y
	if startRY >= count {
		startRY, startRX = 0, 0
	}

	// find returns the rune index of wordRunes in line[from:], or -1.
	find := func(line []rune, from int) int {
		for i := from; i+wn <= len(line); i++ {
			j := 0
			for j < wn && line[i+j] == wordRunes[j] {
				j++
			}
			if j == wn {
				return i
			}
		}
		return -1
	}

	// Pass 1: startRY to end
	for y := startRY; y < count; y++ {
		line := lines[y]
		sx := 0
		if y == startRY {
			sx = min(startRX, len(line))
		}
		if x := find(line, sx); x != -1 {
			return y, Selection{Start: Cursor{x, y}, End: Cursor{x + wn, y}, Active: true}, true
		}
	}

	// Pass 2: 0 to startRY
	for y := 0; y <= startRY && y < count; y++ {
		line := lines[y]
		limit := len(line)
		if y == startRY {
			limit = min(startRX, len(line))
		}
		if x := find(line[:limit], 0); x != -1 {
			return y, Selection{Start: Cursor{x, y}, End: Cursor{x + wn, y}, Active: true}, true
		}
	}

	return -1, Selection{}, false
}

func GetTextInSelection(buf *Buffer, s Selection) string {
	if !s.Active {
		return ""
	}
	start, end := s.Ordered()
	lines := buf.lines
	count := len(lines)
	var sb strings.Builder
	for y := start.y; y <= end.y; y++ {
		if y < 0 || y >= count {
			continue
		}
		line := lines[y]
		x1, x2 := 0, len(line)
		if y == start.y {
			x1 = start.x
		}
		if y == end.y {
			x2 = end.x
		}
		x1 = max(0, min(x1, len(line)))
		x2 = max(0, min(x2, len(line)))
		if x1 < x2 {
			sb.WriteString(string(line[x1:x2]))
		}
		if y < end.y {
			sb.WriteRune('\n')
		}
	}
	return sb.String()
}
