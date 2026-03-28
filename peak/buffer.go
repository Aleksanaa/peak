package main

import (
	"strings"
	"unicode/utf8"

	"github.com/atotto/clipboard"
)

type bufferState struct {
	lines   [][]rune
	cursor  Cursor
	version int
}

// Buffer handles the raw text data and selection state.
type Buffer struct {
	lines     [][]rune
	cursor    Cursor
	selection Selection
	history   []bufferState
	redoStack []bufferState
	version   int
	nextVer   int
}

// NewBuffer initializes a buffer with the given string content.
func NewBuffer(content string) *Buffer {
	b := &Buffer{
		lines:   [][]rune{{}},
		version: 0,
		nextVer: 1,
	}
	b.SetText(content)
	// After initial SetText, we want to reset history/version so it starts at 0
	b.history = nil
	b.redoStack = nil
	b.version = 0
	return b
}

func (b *Buffer) copyLines() [][]rune {
	return append([][]rune{}, b.lines...)
}

func (b *Buffer) saveState() {
	b.history = append(b.history, bufferState{lines: b.copyLines(), cursor: b.cursor, version: b.version})
	b.redoStack = nil
}

func (b *Buffer) Undo() {
	if len(b.history) == 0 {
		return
	}
	b.redoStack = append(b.redoStack, bufferState{lines: b.copyLines(), cursor: b.cursor, version: b.version})
	last := b.history[len(b.history)-1]
	b.history = b.history[:len(b.history)-1]
	b.lines = last.lines
	b.cursor = last.cursor
	b.version = last.version
	b.ClearSelection()
}

func (b *Buffer) Redo() {
	if len(b.redoStack) == 0 {
		return
	}
	b.history = append(b.history, bufferState{lines: b.copyLines(), cursor: b.cursor, version: b.version})
	next := b.redoStack[len(b.redoStack)-1]
	b.redoStack = b.redoStack[:len(b.redoStack)-1]
	b.lines = next.lines
	b.cursor = next.cursor
	b.version = next.version
	b.ClearSelection()
}

func (b *Buffer) ClearSelection() {
	b.selection.Active = false
}

func (b *Buffer) SetSelection(start, end Cursor) {
	b.selection.Start = start
	b.selection.End = end
	b.selection.Active = true
}

func (b *Buffer) LineCount() int {
	return len(b.lines)
}

func (b *Buffer) GetLine(y int) string {
	if y < 0 || y >= len(b.lines) {
		return ""
	}
	return string(b.lines[y])
}

func (b *Buffer) GetSelectedText() string {
	return GetTextInSelection(b, b.selection, false)
}

func (b *Buffer) GetTextInRange(start, end Cursor) string {
	return GetTextInSelection(b, Selection{Start: start, End: end, Active: true}, false)
}

func (b *Buffer) IsSelected(x, y int) bool {
	return b.selection.Contains(x, y, false)
}

func (b *Buffer) Len() int {
	count := 0
	for i, line := range b.lines {
		count += len(line)
		if i < len(b.lines)-1 {
			count++ // \n
		}
	}
	return count
}

func (b *Buffer) RunesInRange(q0, q1 int) []rune {
	all := b.GetRunes()
	if q0 < 0 {
		q0 = 0
	}
	if q1 > len(all) {
		q1 = len(all)
	}
	if q0 > q1 {
		return nil
	}
	return all[q0:q1]
}

func (b *Buffer) orderedSelection() (Cursor, Cursor) {
	return b.selection.Ordered()
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

func (b *Buffer) GetRunes() []rune {
	var res []rune
	for i, line := range b.lines {
		res = append(res, line...)
		if i < len(b.lines)-1 {
			res = append(res, '\n')
		}
	}
	return res
}

func (b *Buffer) mutate(fn func()) {
	b.saveState()
	fn()
}

func (b *Buffer) SetText(content string) {
	if len(b.history) > 0 || len(b.lines) > 1 || len(b.lines[0]) > 0 {
		b.saveState()
	}
	b.lines = nil
	for _, l := range strings.Split(content, "\n") {
		b.lines = append(b.lines, []rune(l))
	}
	b.cursor = Cursor{0, 0}
	b.ClearSelection()
	b.version, b.nextVer = b.nextVer, b.nextVer+1
}

func (b *Buffer) replace(start, end Cursor, content string) Cursor {
	midLines := strings.Split(content, "\n")
	mid := make([][]rune, len(midLines))
	for i, l := range midLines {
		mid[i] = []rune(l)
	}

	prefix := b.lines[start.y][:start.x]
	suffix := b.lines[end.y][end.x:]

	mid[0] = append(append([]rune{}, prefix...), mid[0]...)
	last := len(mid) - 1
	newEndCol := len(mid[last])
	mid[last] = append(mid[last], suffix...)

	b.lines = append(b.lines[:start.y], append(mid, b.lines[end.y+1:]...)...)
	b.cursor = Cursor{newEndCol, start.y + last}
	b.ClearSelection()
	b.version, b.nextVer = b.nextVer, b.nextVer+1
	return b.cursor
}

func (b *Buffer) SetTextInRange(start, end Cursor, content string) Cursor {
	var res Cursor
	b.mutate(func() { res = b.replace(start, end, content) })
	return res
}

func (b *Buffer) DeleteLine() {
	b.mutate(func() {
		if len(b.lines) <= 1 {
			b.lines = [][]rune{{}}
			b.cursor = Cursor{0, 0}
		} else {
			b.lines = append(b.lines[:b.cursor.y], b.lines[b.cursor.y+1:]...)
			b.cursor.y = min(b.cursor.y, len(b.lines)-1)
			b.cursor.x = 0
		}
		b.version, b.nextVer = b.nextVer, b.nextVer+1
	})
}

func (b *Buffer) DeleteWordBefore() {
	if b.cursor.x == 0 && b.cursor.y == 0 {
		return
	}
	b.mutate(func() {
		start := b.cursor
		if start.x == 0 {
			start.y--
			start.x = len(b.lines[start.y])
		} else {
			line := b.lines[start.y]
			for start.x > 0 && line[start.x-1] == ' ' {
				start.x--
			}
			for start.x > 0 && line[start.x-1] != ' ' {
				start.x--
			}
		}
		b.replace(start, b.cursor, "")
	})
}

func (b *Buffer) Insert(r rune) { b.mutate(func() { b.replace(b.cursor, b.cursor, string(r)) }) }
func (b *Buffer) NewLine()      { b.mutate(func() { b.replace(b.cursor, b.cursor, "\n") }) }
func (b *Buffer) DeleteSelection() {
	b.mutate(func() { start, end := b.orderedSelection(); b.replace(start, end, "") })
}

func (b *Buffer) Snarf() {
	if text := b.GetSelectedText(); text != "" {
		go clipboard.WriteAll(text)
	}
}

func (b *Buffer) Cut() {
	if text := b.GetSelectedText(); text != "" {
		go clipboard.WriteAll(text)
		b.DeleteSelection()
	}
}

func (b *Buffer) Paste() {
	text, _ := clipboard.ReadAll()
	if text == "" {
		return
	}
	b.mutate(func() {
		if b.selection.Active {
			start, end := b.orderedSelection()
			b.replace(start, end, text)
		} else {
			b.replace(b.cursor, b.cursor, text)
		}
	})
}

func (b *Buffer) Backspace() {
	if b.selection.Active {
		b.DeleteSelection()
		return
	}
	if b.cursor.x == 0 && b.cursor.y == 0 {
		return
	}
	b.mutate(func() {
		start := b.cursor
		if start.x > 0 {
			start.x--
		} else {
			start.y--
			start.x = len(b.lines[start.y])
		}
		b.replace(start, b.cursor, "")
	})
}

func (b *Buffer) Delete() {
	if b.selection.Active {
		b.DeleteSelection()
		return
	}
	if b.cursor.y == len(b.lines)-1 && b.cursor.x == len(b.lines[b.cursor.y]) {
		return
	}
	b.mutate(func() {
		end := b.cursor
		if end.x < len(b.lines[end.y]) {
			end.x++
		} else {
			end.y++
			end.x = 0
		}
		b.replace(b.cursor, end, "")
	})
}

func (b *Buffer) ReplaceRangeRunes(q0, q1 int, runes []rune) {
	b.mutate(func() { b.replaceRangeRunesNoSave(q0, q1, runes) })
}

func (b *Buffer) replaceRangeRunesNoSave(q0, q1 int, runes []rune) {
	if q0 < 0 {
		q0 = 0
	}
	if q1 < q0 {
		q1 = q0
	}
	start := b.RuneOffsetToCursor(q0)
	end := b.RuneOffsetToCursor(q1)
	b.replace(start, end, string(runes))
}

func (b *Buffer) CursorToByteOffset(c Cursor) int {
	offset := 0
	for y := 0; y < c.y; y++ {
		for _, r := range b.lines[y] {
			offset += utf8.RuneLen(r)
		}
		offset++
	}
	for _, r := range b.lines[c.y][:c.x] {
		offset += utf8.RuneLen(r)
	}
	return offset
}

func (b *Buffer) ByteOffsetToCursor(offset int) Cursor {
	curr := 0
	for y, line := range b.lines {
		lineBytes := 0
		for _, r := range line {
			lineBytes += utf8.RuneLen(r)
		}
		if offset <= curr+lineBytes {
			byteIdx, rIdx := 0, 0
			for byteIdx < offset-curr {
				byteIdx += utf8.RuneLen(line[rIdx])
				rIdx++
			}
			return Cursor{rIdx, y}
		}
		curr += lineBytes + 1
	}
	lastY := len(b.lines) - 1
	return Cursor{len(b.lines[lastY]), lastY}
}

func (b *Buffer) runeOffsetToByteOffset(off int) int {
	full := b.GetRunes()
	if off > len(full) {
		off = len(full)
	}
	if off < 0 {
		off = 0
	}
	return len(string(full[:off]))
}

func (b *Buffer) CursorToRuneOffset(c Cursor) int {
	byteOff := b.CursorToByteOffset(c)
	text := b.GetText()
	if byteOff > len(text) {
		byteOff = len(text)
	}
	return len([]rune(text[:byteOff]))
}

func (b *Buffer) RuneOffsetToCursor(off int) Cursor {
	return b.ByteOffsetToCursor(b.runeOffsetToByteOffset(off))
}

func (b *Buffer) MoveHome() { b.cursor.x = 0 }
func (b *Buffer) MoveEnd()  { b.cursor.x = len(b.lines[b.cursor.y]) }

func (b *Buffer) MoveWordLeft() {
	if b.cursor.x == 0 {
		b.MoveLeft()
		return
	}
	line, x := b.lines[b.cursor.y], b.cursor.x
	for x > 0 && !IsWordChar(line[x-1]) {
		x--
	}
	for x > 0 && IsWordChar(line[x-1]) {
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
	for x < len(line) && IsWordChar(line[x]) {
		x++
	}
	for x < len(line) && !IsWordChar(line[x]) {
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
