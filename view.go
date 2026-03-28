package main

import "strings"

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

func (v *BaseView) GetPos() (x, y, w, h int) { return v.x, v.y, v.w, v.h }
func (v *BaseView) SetPos(x, y, w, h int)    { v.x, v.y, v.w, v.h = x, y, w, h }

func (s *ScrollState) Scroll(n int, total, visible int) {
	limit := total
	if total <= visible {
		limit = 0
	}
	s.Pos = max(0, min(limit, s.Pos+n))

	if s.Pos >= max(0, total-visible) {
		s.AutoScroll = true
	} else if n < 0 {
		s.AutoScroll = false
	}
}

func (s *ScrollState) Sync(cursorY int, total, visible int) {
	limit := total
	if total <= visible {
		limit = 0
	}
	if cursorY < s.Pos {
		s.Pos = cursorY
	} else if cursorY >= s.Pos+visible {
		if s.AutoScroll || s.Pos >= total-visible-1 {
			s.Pos = cursorY - visible + 1
		}
	}
	s.Pos = max(0, min(limit, s.Pos))
}

// LineProvider is an interface for types that can provide lines for searching.
type LineProvider interface {
	LineCount() int
	GetLine(y int) string
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
	startRX, startRY := start.x+1, start.y
	if startRY >= count {
		startRY, startRX = 0, 0
	}

	// Pass 1: startRY to end
	for y := startRY; y < count; y++ {
		line := lp.GetLine(y)
		sx := 0
		if y == startRY {
			sx = startRX
			if sx > len(line) {
				sx = len(line)
			}
		}
		if x := strings.Index(line[sx:], word); x != -1 {
			return y, Selection{
				Start:  Cursor{sx + x, y},
				End:    Cursor{sx + x + len(word), y},
				Active: true,
			}, true
		}
	}

	// Pass 2: 0 to startRY
	for y := 0; y <= startRY && y < count; y++ {
		line := lp.GetLine(y)
		limit := len(line)
		if y == startRY {
			limit = startRX
			if limit > len(line) {
				limit = len(line)
			}
		}
		if x := strings.Index(line[:limit], word); x != -1 {
			return y, Selection{
				Start:  Cursor{x, y},
				End:    Cursor{x + len(word), y},
				Active: true,
			}, true
		}
	}

	return -1, Selection{}, false
}
