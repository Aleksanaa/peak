package main

import (
	"strings"
)

// Cursor represents a 2D position in the text buffer.
type Cursor struct {
	x, y int
}

// Buffer handles the raw text data and selection state.
type Buffer struct {
	lines          [][]rune
	cursor         Cursor
	selectionStart *Cursor
	selectionEnd   *Cursor
}

// NewBuffer initializes a buffer with the given string content.
func NewBuffer(content string) *Buffer {
	b := &Buffer{
		lines: [][]rune{{}},
	}
	b.SetText(content)
	return b
}

// ClearSelection removes any active text selection.
func (b *Buffer) ClearSelection() {
	b.selectionStart = nil
	b.selectionEnd = nil
}

// SetSelection explicitly sets the selection range.
func (b *Buffer) SetSelection(start, end Cursor) {
	s, e := start, end
	b.selectionStart = &s
	b.selectionEnd = &e
}

// GetSelectedText returns the string content of the current selection.
func (b *Buffer) GetSelectedText() string {
	if b.selectionStart == nil || b.selectionEnd == nil {
		return ""
	}
	start, end := b.orderedSelection()

	var sb strings.Builder
	for y := start.y; y <= end.y; y++ {
		line := b.lines[y]
		x1, x2 := 0, len(line)
		if y == start.y {
			x1 = start.x
		}
		if y == end.y {
			x2 = end.x
		}

		if x1 < 0 {
			x1 = 0
		}
		if x2 > len(line) {
			x2 = len(line)
		}

		if x1 < x2 {
			sb.WriteString(string(line[x1:x2]))
		}
		if y < end.y {
			sb.WriteRune('\n')
		}
	}
	return sb.String()
}

// IsSelected returns true if the given character position is within the selection.
func (b *Buffer) IsSelected(x, y int) bool {
	if b.selectionStart == nil || b.selectionEnd == nil {
		return false
	}
	start, end := b.orderedSelection()

	if y < start.y || y > end.y {
		return false
	}
	if y == start.y && y == end.y {
		return x >= start.x && x < end.x
	}
	if y == start.y {
		return x >= start.x
	}
	if y == end.y {
		return x < end.x
	}
	return true
}

func (b *Buffer) orderedSelection() (Cursor, Cursor) {
	start, end := *b.selectionStart, *b.selectionEnd
	if start.y > end.y || (start.y == end.y && start.x > end.x) {
		return end, start
	}
	return start, end
}

// GetText returns the entire buffer content as a string.
func (b *Buffer) GetText() string {
	var sb strings.Builder
	for i, line := range b.lines {
		sb.WriteString(string(line))
		if i < len(b.lines)-1 {
			sb.WriteRune('\n')
		}
	}
	return sb.String()
}

// SetText replaces the buffer content and resets the cursor.
func (b *Buffer) SetText(content string) {
	b.lines = [][]rune{{}}
	for _, r := range content {
		if r == '\n' {
			b.lines = append(b.lines, []rune{})
		} else {
			last := len(b.lines) - 1
			b.lines[last] = append(b.lines[last], r)
		}
	}
	b.cursor = Cursor{0, 0}
	b.ClearSelection()
}

func (b *Buffer) DeleteLine() {
	if len(b.lines) <= 1 {
		b.lines = [][]rune{{}}
		b.cursor = Cursor{0, 0}
		return
	}
	b.lines = append(b.lines[:b.cursor.y], b.lines[b.cursor.y+1:]...)
	if b.cursor.y >= len(b.lines) {
		b.cursor.y = len(b.lines) - 1
	}
	b.cursor.x = 0
}

func (b *Buffer) DeleteWordBefore() {
	if b.cursor.x == 0 {
		b.Backspace()
		return
	}
	line := b.lines[b.cursor.y]
	end := b.cursor.x
	for end > 0 && line[end-1] == ' ' {
		end--
	}
	start := end
	for start > 0 && line[start-1] != ' ' {
		start--
	}
	newLine := append(line[:start], line[b.cursor.x:]...)
	b.lines[b.cursor.y] = newLine
	b.cursor.x = start
}

// Insert adds a rune at the current cursor position.
func (b *Buffer) Insert(r rune) {
	line := b.lines[b.cursor.y]
	newLine := append(line[:b.cursor.x], append([]rune{r}, line[b.cursor.x:]...)...)
	b.lines[b.cursor.y] = newLine
	b.cursor.x++
}

// NewLine splits the current line at the cursor.
func (b *Buffer) NewLine() {
	line := b.lines[b.cursor.y]
	remaining := line[b.cursor.x:]
	b.lines[b.cursor.y] = line[:b.cursor.x]

	newLines := make([][]rune, 0, len(b.lines)+1)
	newLines = append(newLines, b.lines[:b.cursor.y+1]...)
	newLines = append(newLines, remaining)
	newLines = append(newLines, b.lines[b.cursor.y+1:]...)
	b.lines = newLines

	b.cursor.y++
	b.cursor.x = 0
}

// DeleteSelection removes the selected text.
func (b *Buffer) Delete() {
	if b.selectionStart != nil && b.selectionEnd != nil {
		b.DeleteSelection()
		return
	}
	line := b.lines[b.cursor.y]
	if b.cursor.x < len(line) {
		b.lines[b.cursor.y] = append(line[:b.cursor.x], line[b.cursor.x+1:]...)
	} else if b.cursor.y < len(b.lines)-1 {
		// Join with next line
		nextLine := b.lines[b.cursor.y+1]
		b.lines[b.cursor.y] = append(line, nextLine...)
		b.lines = append(b.lines[:b.cursor.y+1], b.lines[b.cursor.y+2:]...)
	}
}

func (b *Buffer) DeleteSelection() {
	if b.selectionStart == nil || b.selectionEnd == nil {
		return
	}
	start, end := b.orderedSelection()

	if start.y == end.y {
		line := b.lines[start.y]
		newLine := append(line[:start.x], line[end.x:]...)
		b.lines[start.y] = newLine
	} else {
		firstLine := b.lines[start.y][:start.x]
		lastLine := b.lines[end.y][end.x:]
		newFirstLine := append(firstLine, lastLine...)

		newLines := append(b.lines[:start.y], newFirstLine)
		newLines = append(newLines, b.lines[end.y+1:]...)
		b.lines = newLines
	}

	b.cursor = start
	b.ClearSelection()
}

// Backspace deletes the selection or the character before the cursor.
func (b *Buffer) Backspace() {
	if b.selectionStart != nil && b.selectionEnd != nil {
		b.DeleteSelection()
		return
	}
	if b.cursor.x > 0 {
		line := b.lines[b.cursor.y]
		b.lines[b.cursor.y] = append(line[:b.cursor.x-1], line[b.cursor.x:]...)
		b.cursor.x--
	} else if b.cursor.y > 0 {
		prevLine := b.lines[b.cursor.y-1]
		currentLine := b.lines[b.cursor.y]
		newX := len(prevLine)
		b.lines[b.cursor.y-1] = append(prevLine, currentLine...)
		b.lines = append(b.lines[:b.cursor.y], b.lines[b.cursor.y+1:]...)
		b.cursor.y--
		b.cursor.x = newX
	}
}

// Cursor movement methods
func (b *Buffer) MoveHome() {
	b.cursor.x = 0
}

func (b *Buffer) MoveEnd() {
	b.cursor.x = len(b.lines[b.cursor.y])
}

func (b *Buffer) MoveWordLeft() {
	if b.cursor.x == 0 {
		b.MoveLeft()
		return
	}
	line := b.lines[b.cursor.y]
	x := b.cursor.x
	// Skip current spaces
	for x > 0 && line[x-1] == ' ' {
		x--
	}
	// Find start of word
	for x > 0 && line[x-1] != ' ' {
		x--
	}
	b.cursor.x = x
}

func (b *Buffer) MoveWordRight() {
	line := b.lines[b.cursor.y]
	if b.cursor.x >= len(line) {
		b.MoveRight()
		return
	}
	x := b.cursor.x
	// Skip current word chars
	for x < len(line) && line[x] != ' ' {
		x++
	}
	// Skip following spaces
	for x < len(line) && line[x] == ' ' {
		x++
	}
	b.cursor.x = x
}

func (b *Buffer) MoveLeft() {
	if b.cursor.x > 0 {
		b.cursor.x--
	} else if b.cursor.y > 0 {
		b.cursor.y--
		b.cursor.x = len(b.lines[b.cursor.y])
	}
}

func (b *Buffer) MoveRight() {
	if b.cursor.x < len(b.lines[b.cursor.y]) {
		b.cursor.x++
	} else if b.cursor.y < len(b.lines)-1 {
		b.cursor.y++
		b.cursor.x = 0
	}
}

func (b *Buffer) MoveUp() {
	if b.cursor.y > 0 {
		b.cursor.y--
		if b.cursor.x > len(b.lines[b.cursor.y]) {
			b.cursor.x = len(b.lines[b.cursor.y])
		}
	}
}

func (b *Buffer) MoveDown() {
	if b.cursor.y < len(b.lines)-1 {
		b.cursor.y++
		if b.cursor.x > len(b.lines[b.cursor.y]) {
			b.cursor.x = len(b.lines[b.cursor.y])
		}
	}
}
