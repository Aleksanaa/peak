package main

import (
	"strings"

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

	// onMutate is called after replace() or SetText() with pre/post rune offsets.
	// q0=start, q1Old=old end, q1New=new end, text=inserted text.
	// Called on the main goroutine; never nil-checked by callers.
	onMutate func(q0, q1Old, q1New int, text string)

	// line-start rune offset cache (invalidated on version change)
	lsruns    []int
	lsrunsVer int
}

// readClipboard is indirection so paste tests do not race with async
// clipboard writes from earlier cut tests.
var readClipboard = clipboard.ReadAll

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
	var q1Old int
	if b.onMutate != nil {
		q1Old = b.Len()
	}
	b.redoStack = append(b.redoStack, bufferState{lines: b.copyLines(), cursor: b.cursor, version: b.version})
	last := b.history[len(b.history)-1]
	b.history = b.history[:len(b.history)-1]
	b.lines = last.lines
	b.cursor = last.cursor
	b.version = last.version
	b.ClearSelection()
	if b.onMutate != nil {
		content := b.GetText()
		b.onMutate(0, q1Old, b.Len(), content)
	}
}

func (b *Buffer) Redo() {
	if len(b.redoStack) == 0 {
		return
	}
	var q1Old int
	if b.onMutate != nil {
		q1Old = b.Len()
	}
	b.history = append(b.history, bufferState{lines: b.copyLines(), cursor: b.cursor, version: b.version})
	next := b.redoStack[len(b.redoStack)-1]
	b.redoStack = b.redoStack[:len(b.redoStack)-1]
	b.lines = next.lines
	b.cursor = next.cursor
	b.version = next.version
	b.ClearSelection()
	if b.onMutate != nil {
		content := b.GetText()
		b.onMutate(0, q1Old, b.Len(), content)
	}
}

func (b *Buffer) ClearSelection() {
	b.selection.Active = false
}

func (b *Buffer) SetSelection(start, end Cursor) {
	b.selection.Start = start
	b.selection.End = end
	b.selection.Active = true
}

func (b *Buffer) GetSelectedText() string {
	return GetTextInSelection(b, b.selection, false)
}

func (b *Buffer) Len() int {
	b.ensureLSR()
	last := len(b.lsruns) - 1
	return b.lsruns[last] + len(b.lines[last])
}

func (b *Buffer) RunesInRange(q0, q1 int) []rune {
	if q0 < 0 {
		q0 = 0
	}
	if n := b.Len(); q1 > n {
		q1 = n
	}
	if q0 >= q1 {
		return nil
	}
	q0c, q1c := b.RuneOffsetToCursor(q0), b.RuneOffsetToCursor(q1)
	return []rune(GetTextInSelection(b, Selection{Start: q0c, End: q1c, Active: true}, false))
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

func (b *Buffer) bumpVersion() { b.version, b.nextVer = b.nextVer, b.nextVer+1 }

func (b *Buffer) mutate(fn func()) {
	b.saveState()
	fn()
}

// ensureLSR rebuilds the line-start rune offset cache if stale.
func (b *Buffer) ensureLSR() {
	if b.lsruns != nil && b.lsrunsVer == b.version {
		return
	}
	b.lsruns = make([]int, len(b.lines))
	off := 0
	for i, line := range b.lines {
		b.lsruns[i] = off
		off += len(line) + 1 // +1 for the implicit \n between lines
	}
	b.lsrunsVer = b.version
}

// RuneOffsetOfPos returns the rune offset in the buffer's flat text for position (line, col).
func (b *Buffer) RuneOffsetOfPos(line, col int) int {
	b.ensureLSR()
	if line < 0 {
		return 0
	}
	if line >= len(b.lsruns) {
		line = len(b.lsruns) - 1
		col = len(b.lines[line])
	}
	return b.lsruns[line] + col
}

func (b *Buffer) SetText(content string) {
	var q1Old int
	if b.onMutate != nil {
		q1Old = b.Len()
	}
	if len(b.history) > 0 || len(b.lines) > 1 || len(b.lines[0]) > 0 {
		b.saveState()
	}
	b.lines = nil
	for _, l := range strings.Split(content, "\n") {
		b.lines = append(b.lines, []rune(l))
	}
	b.cursor = Cursor{0, 0}
	b.ClearSelection()
	b.bumpVersion()
	if b.onMutate != nil {
		q1New := b.Len()
		b.onMutate(0, q1Old, q1New, content)
	}
}

func (b *Buffer) replace(start, end Cursor, content string) Cursor {
	var q0, q1Old int
	if b.onMutate != nil {
		q0 = b.RuneOffsetOfPos(start.y, start.x)
		q1Old = b.RuneOffsetOfPos(end.y, end.x)
	}

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
	b.bumpVersion()

	if b.onMutate != nil {
		q1New := q0 + len([]rune(content))
		b.onMutate(q0, q1Old, q1New, content)
	}
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
		b.bumpVersion()
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
	b.mutate(func() { start, end := b.selection.Ordered(); b.replace(start, end, "") })
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
	text, _ := readClipboard()
	if text == "" {
		return
	}
	b.mutate(func() {
		start, end := b.cursor, b.cursor
		if b.selection.Active {
			start, end = b.selection.Ordered()
		}
		pasteEnd := b.replace(start, end, text)
		b.SetSelection(start, pasteEnd)
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

func (b *Buffer) CursorToRuneOffset(c Cursor) int {
	return b.RuneOffsetOfPos(c.y, c.x)
}

func (b *Buffer) RuneOffsetToCursor(off int) Cursor {
	b.ensureLSR()
	if off <= 0 {
		return Cursor{0, 0}
	}
	// Binary search: find last line whose start ≤ off.
	lo, hi := 0, len(b.lsruns)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if b.lsruns[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	col := off - b.lsruns[lo]
	if col > len(b.lines[lo]) {
		col = len(b.lines[lo])
	}
	return Cursor{col, lo}
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
