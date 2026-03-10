package main

import (
	"strings"
)

// Cursor represents a 2D position in the text buffer.
type Cursor struct {
	x, y int
}

type bufferState struct {
	lines  [][]rune
	cursor Cursor
}

// Buffer handles the raw text data and selection state.
type Buffer struct {
	lines          [][]rune
	cursor         Cursor
	selectionStart *Cursor
	selectionEnd   *Cursor
	history        []bufferState
	redoStack      []bufferState
	version        int
}

// NewBuffer initializes a buffer with the given string content.
func NewBuffer(content string) *Buffer {
	b := &Buffer{
		lines: [][]rune{{}},
	}
	b.SetText(content)
	// NewBuffer should probably not have an initial undo state for the very first load
	b.history = nil
	b.redoStack = nil
	b.version = 0
	return b
}

func (b *Buffer) copyLines() [][]rune {
	newLines := make([][]rune, len(b.lines))
	for i := range b.lines {
		newLines[i] = make([]rune, len(b.lines[i]))
		copy(newLines[i], b.lines[i])
	}
	return newLines
}

func (b *Buffer) saveState() {
	b.history = append(b.history, bufferState{lines: b.copyLines(), cursor: b.cursor})
	b.redoStack = nil // This is where we clear the redo branch
	b.version++
}

func (b *Buffer) Undo() {
	if len(b.history) == 0 {
		return
	}

	// Save current state to redo stack
	b.redoStack = append(b.redoStack, bufferState{lines: b.copyLines(), cursor: b.cursor})

	// Restore last state
	last := b.history[len(b.history)-1]
	b.history = b.history[:len(b.history)-1]
	b.lines = last.lines
	b.cursor = last.cursor
	b.ClearSelection()
	b.version++
}

func (b *Buffer) Redo() {
	if len(b.redoStack) == 0 {
		return
	}

	// Save current state back to history
	b.history = append(b.history, bufferState{lines: b.copyLines(), cursor: b.cursor})

	// Restore from redo stack
	next := b.redoStack[len(b.redoStack)-1]
	b.redoStack = b.redoStack[:len(b.redoStack)-1]
	b.lines = next.lines
	b.cursor = next.cursor
	b.ClearSelection()
	b.version++
}

func (b *Buffer) ClearSelection() {
	b.selectionStart = nil
	b.selectionEnd = nil
}

func (b *Buffer) SetSelection(start, end Cursor) {
	s, e := start, end
	b.selectionStart, b.selectionEnd = &s, &e
}

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
	// Only save state if there is actual content or history already exists
	if len(b.history) > 0 || len(b.lines) > 1 || len(b.lines[0]) > 0 {
		b.saveState()
	}

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
	b.version++
}

func (b *Buffer) DeleteLine() {
	b.saveState()
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
	if b.cursor.x == 0 && b.cursor.y == 0 {
		return
	}
	b.saveState()
	if b.cursor.x == 0 {
		// Just perform backspace but don't double-save state
		// (saveState already called, so we can manually join lines)
		prevLine := b.lines[b.cursor.y-1]
		currLine := b.lines[b.cursor.y]
		b.cursor.x = len(prevLine)
		b.lines[b.cursor.y-1] = append(prevLine, currLine...)
		b.lines = append(b.lines[:b.cursor.y], b.lines[b.cursor.y+1:]...)
		b.cursor.y--
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
	b.lines[b.cursor.y] = append(line[:start], line[b.cursor.x:]...)
	b.cursor.x = start
}

func (b *Buffer) Insert(r rune) {
	b.saveState()
	line := b.lines[b.cursor.y]
	b.lines[b.cursor.y] = append(line[:b.cursor.x], append([]rune{r}, line[b.cursor.x:]...)...)
	b.cursor.x++
}

func (b *Buffer) NewLine() {
	b.saveState()
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

func (b *Buffer) DeleteSelection() {
	b.saveState()
	start, end := b.orderedSelection()
	if start.y == end.y {
		b.lines[start.y] = append(b.lines[start.y][:start.x], b.lines[start.y][end.x:]...)
	} else {
		newFirstLine := append(b.lines[start.y][:start.x], b.lines[end.y][end.x:]...)
		newLines := append(b.lines[:start.y], newFirstLine)
		newLines = append(newLines, b.lines[end.y+1:]...)
		b.lines = newLines
	}
	b.cursor = start
	b.ClearSelection()
}

func (b *Buffer) Backspace() {
	if b.selectionStart != nil && b.selectionEnd != nil {
		b.DeleteSelection()
		return
	}
	if b.cursor.x == 0 && b.cursor.y == 0 {
		return
	}
	b.saveState()
	if b.cursor.x > 0 {
		b.lines[b.cursor.y] = append(b.lines[b.cursor.y][:b.cursor.x-1], b.lines[b.cursor.y][b.cursor.x:]...)
		b.cursor.x--
	} else {
		prevLine := b.lines[b.cursor.y-1]
		newX := len(prevLine)
		b.lines[b.cursor.y-1] = append(prevLine, b.lines[b.cursor.y]...)
		b.lines = append(b.lines[:b.cursor.y], b.lines[b.cursor.y+1:]...)
		b.cursor.y--
		b.cursor.x = newX
	}
}

func (b *Buffer) Delete() {
	if b.selectionStart != nil && b.selectionEnd != nil {
		b.DeleteSelection()
		return
	}
	line := b.lines[b.cursor.y]
	if b.cursor.x < len(line) {
		b.saveState()
		b.lines[b.cursor.y] = append(line[:b.cursor.x], line[b.cursor.x+1:]...)
	} else if b.cursor.y < len(b.lines)-1 {
		b.saveState()
		b.lines[b.cursor.y] = append(line, b.lines[b.cursor.y+1]...)
		b.lines = append(b.lines[:b.cursor.y+1], b.lines[b.cursor.y+2:]...)
	}
}

func (b *Buffer) MoveHome() { b.cursor.x = 0 }
func (b *Buffer) MoveEnd()  { b.cursor.x = len(b.lines[b.cursor.y]) }

func (b *Buffer) MoveWordLeft() {
	if b.cursor.x == 0 {
		b.MoveLeft()
		return
	}
	line, x := b.lines[b.cursor.y], b.cursor.x
	for x > 0 && line[x-1] == ' ' {
		x--
	}
	for x > 0 && line[x-1] != ' ' {
		x--
	}
	b.cursor.x = x
}

func (b *Buffer) MoveWordRight() {
	line, x := b.lines[b.cursor.y], b.cursor.x
	if x >= len(line) {
		b.MoveRight()
		return
	}
	for x < len(line) && line[x] != ' ' {
		x++
	}
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
