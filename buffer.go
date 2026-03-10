package main

import "strings"

type Cursor struct {
	x, y int
}

type Buffer struct {
	lines          [][]rune
	cursor         Cursor
	selectionStart *Cursor
	selectionEnd   *Cursor
}

func (b *Buffer) ClearSelection() {
	b.selectionStart = nil
	b.selectionEnd = nil
}

func (b *Buffer) SetSelection(start, end Cursor) {
	s := start
	e := end
	b.selectionStart = &s
	b.selectionEnd = &e
}

func (b *Buffer) GetSelectedText() string {
	if b.selectionStart == nil || b.selectionEnd == nil {
		return ""
	}
	start, end := *b.selectionStart, *b.selectionEnd
	if start.y > end.y || (start.y == end.y && start.x > end.x) {
		start, end = end, start
	}

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
		if x1 < 0 { x1 = 0 }
		if x2 > len(line) { x2 = len(line) }
		if x1 < x2 {
			sb.WriteString(string(line[x1:x2]))
		}
		if y < end.y {
			sb.WriteRune('\n')
		}
	}
	return sb.String()
}

func (b *Buffer) IsSelected(x, y int) bool {
	if b.selectionStart == nil || b.selectionEnd == nil {
		return false
	}
	start, end := *b.selectionStart, *b.selectionEnd
	if start.y > end.y || (start.y == end.y && start.x > end.x) {
		start, end = end, start
	}
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

func NewBuffer(content string) *Buffer {
	b := &Buffer{
		lines: [][]rune{{}},
	}
	// Initial population from string
	for _, r := range content {
		if r == '\n' {
			b.lines = append(b.lines, []rune{})
		} else {
			last := len(b.lines) - 1
			b.lines[last] = append(b.lines[last], r)
		}
	}
	return b
}

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
}

func (b *Buffer) Insert(r rune) {
	line := b.lines[b.cursor.y]
	// Insert at cursor position
	newLine := append(line[:b.cursor.x], append([]rune{r}, line[b.cursor.x:]...)...)
	b.lines[b.cursor.y] = newLine
	b.cursor.x++
}

func (b *Buffer) NewLine() {
	line := b.lines[b.cursor.y]
	remaining := line[b.cursor.x:]
	b.lines[b.cursor.y] = line[:b.cursor.x]
	
	// Insert new line after current one
	newLines := make([][]rune, 0, len(b.lines)+1)
	newLines = append(newLines, b.lines[:b.cursor.y+1]...)
	newLines = append(newLines, remaining)
	newLines = append(newLines, b.lines[b.cursor.y+1:]...)
	b.lines = newLines
	
	b.cursor.y++
	b.cursor.x = 0
}

func (b *Buffer) Backspace() {
	if b.cursor.x > 0 {
		line := b.lines[b.cursor.y]
		b.lines[b.cursor.y] = append(line[:b.cursor.x-1], line[b.cursor.x:]...)
		b.cursor.x--
	} else if b.cursor.y > 0 {
		// Join with previous line
		prevLine := b.lines[b.cursor.y-1]
		currentLine := b.lines[b.cursor.y]
		newX := len(prevLine)
		
		b.lines[b.cursor.y-1] = append(prevLine, currentLine...)
		// Remove current line
		b.lines = append(b.lines[:b.cursor.y], b.lines[b.cursor.y+1:]...)
		
		b.cursor.y--
		b.cursor.x = newX
	}
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
