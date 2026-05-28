package main

import (
	"strings"
	"unicode"
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

// BaseView provides common fields and methods for all views.
type BaseView struct {
	x, y, w, h int
	scroll     ScrollState
}

func (v *BaseView) GetPos() (x, y, w, h int)          { return v.x, v.y, v.w, v.h }
func (v *BaseView) SetPos(x, y, w, h int)              { v.x, v.y, v.w, v.h = x, y, w, h }
func (v *BaseView) GetRect() (int, int, int, int)      { return v.x, v.y, v.w, v.h }

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

func GetWordBoundaries(x int, length int, getChar func(int) rune) (int, int) {
	if x < 0 || x >= length {
		return x, x
	}
	start, end := x, x
	for start > 0 && IsWordChar(getChar(start-1)) {
		start--
	}
	for end < length && IsWordChar(getChar(end)) {
		end++
	}
	return start, end
}

// LineProvider is an interface for types that can provide lines for searching.
// GetLine returns the rune sequence for line y; the slice must not be modified.
type LineProvider interface {
	LineCount() int
	GetLine(y int) []rune
}

// Search performs a two-pass search (forward from start, then wrap around).
// It returns the line number, the resulting selection, and true if found.
func Search(lp LineProvider, word string, start Cursor) (int, Selection, bool) {
	if word == "" {
		return -1, Selection{}, false
	}

	count := lp.LineCount()
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
		line := lp.GetLine(y)
		sx := 0
		if y == startRY {
			sx = min(startRX, len(line))
		}
		if x := find(line, sx); x != -1 {
			return y, Selection{
				Start:  Cursor{x, y},
				End:    Cursor{x + wn, y},
				Active: true,
			}, true
		}
	}

	// Pass 2: 0 to startRY
	for y := 0; y <= startRY && y < count; y++ {
		line := lp.GetLine(y)
		limit := len(line)
		if y == startRY {
			limit = min(startRX, len(line))
		}
		if x := find(line[:limit], 0); x != -1 {
			return y, Selection{
				Start:  Cursor{x, y},
				End:    Cursor{x + wn, y},
				Active: true,
			}, true
		}
	}

	return -1, Selection{}, false
}

func GetTextInSelection(lp LineProvider, s Selection, trimRight bool) string {
	if !s.Active {
		return ""
	}
	start, end := s.Ordered()
	count := lp.LineCount()
	var sb strings.Builder
	for y := start.y; y <= end.y; y++ {
		if y < 0 || y >= count {
			continue
		}
		line := lp.GetLine(y)
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
			content := string(line[x1:x2])
			if trimRight {
				content = strings.TrimRight(content, " ")
			}
			sb.WriteString(content)
		}
		if y < end.y {
			sb.WriteRune('\n')
		}
	}
	return sb.String()
}
