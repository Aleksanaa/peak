package main

import (
	"strings"

	"github.com/atotto/clipboard"
)

type bufferState struct {
	lines   [][]rune
	q0, q1  int
	version int
}

// Buffer is text, kept as lines, and dot: the rune offsets [q0, q1) of what
// is selected. An empty dot, q0 == q1, is the cursor.
type Buffer struct {
	lines     [][]rune
	q0, q1    int
	history   []bufferState
	redoStack []bufferState
	version   int
	nextVer   int

	// onMutate is called after replace() or SetText() with pre/post rune offsets.
	// q0=start, q1Old=old end, q1New=new end, text=inserted text.
	// Called on the main goroutine.
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

func (b *Buffer) state() bufferState {
	return bufferState{append([][]rune{}, b.lines...), b.q0, b.q1, b.version}
}

func (b *Buffer) saveState() {
	b.history = append(b.history, b.state())
	b.redoStack = nil
}

func (b *Buffer) Undo() { b.restore(&b.history, &b.redoStack) }
func (b *Buffer) Redo() { b.restore(&b.redoStack, &b.history) }

// restore makes the last state of from the buffer's, keeping the current one
// on to. It reports only the text that differs, so that what is kept in
// place across edits stays in place.
func (b *Buffer) restore(from, to *[]bufferState) {
	if len(*from) == 0 {
		return
	}
	var old []rune
	if b.onMutate != nil {
		old = []rune(b.GetText())
	}
	*to = append(*to, b.state())
	s := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	b.lines, b.q0, b.q1, b.version = s.lines, s.q0, s.q1, s.version
	if b.onMutate != nil {
		cur := []rune(b.GetText())
		p := 0
		for p < len(old) && p < len(cur) && old[p] == cur[p] {
			p++
		}
		n := 0
		for n < len(old)-p && n < len(cur)-p && old[len(old)-1-n] == cur[len(cur)-1-n] {
			n++
		}
		b.onMutate(p, len(old)-n, len(cur)-n, string(cur[p:len(cur)-n]))
	}
}

// SetDot selects the text between q0 and q1, in either order.
func (b *Buffer) SetDot(q0, q1 int) {
	b.q0, b.q1 = min(q0, q1), max(q0, q1)
}

func (b *Buffer) GetSelectedText() string {
	return string(b.RunesInRange(b.q0, b.q1))
}

func (b *Buffer) Len() int {
	b.ensureLSR()
	last := len(b.lsruns) - 1
	return b.lsruns[last] + len(b.lines[last])
}

func (b *Buffer) RunesInRange(q0, q1 int) []rune {
	if q0 >= q1 {
		return nil
	}
	l0, c0 := b.Pos(q0)
	l1, c1 := b.Pos(q1)
	if l0 == l1 {
		return append([]rune{}, b.lines[l0][c0:c1]...)
	}
	r := append([]rune{}, b.lines[l0][c0:]...)
	for l := l0 + 1; l < l1; l++ {
		r = append(append(r, '\n'), b.lines[l]...)
	}
	return append(append(r, '\n'), b.lines[l1][:c1]...)
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

// Offset returns the rune offset of column col of line.
func (b *Buffer) Offset(line, col int) int {
	b.ensureLSR()
	return b.lsruns[line] + col
}

// Pos returns the line and column of rune offset q, which is clamped to the
// text.
func (b *Buffer) Pos(q int) (line, col int) {
	b.ensureLSR()
	if q <= 0 {
		return 0, 0
	}
	// Binary search: find last line whose start ≤ q.
	lo, hi := 0, len(b.lsruns)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if b.lsruns[mid] <= q {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo, min(q-b.lsruns[lo], len(b.lines[lo]))
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
	b.q0, b.q1 = 0, 0
	b.bumpVersion()
	if b.onMutate != nil {
		q1New := b.Len()
		b.onMutate(0, q1Old, q1New, content)
	}
}

// replace replaces the text [q0, q1) with content, leaves the cursor after
// it, and returns where that is.
func (b *Buffer) replace(q0, q1 int, content string) int {
	l0, c0 := b.Pos(q0)
	l1, c1 := b.Pos(q1)

	midLines := strings.Split(content, "\n")
	mid := make([][]rune, len(midLines))
	for i, l := range midLines {
		mid[i] = []rune(l)
	}
	last := len(mid) - 1
	mid[0] = append(append([]rune{}, b.lines[l0][:c0]...), mid[0]...)
	mid[last] = append(mid[last], b.lines[l1][c1:]...)
	b.lines = append(b.lines[:l0], append(mid, b.lines[l1+1:]...)...)

	end := q0 + len([]rune(content))
	b.q0, b.q1 = end, end
	b.bumpVersion()
	if b.onMutate != nil {
		b.onMutate(q0, q1, end, content)
	}
	return end
}

// DeleteLine removes the line dot starts on and leaves the cursor at the
// start of the line that takes its place (the previous line, when the last is
// removed).
func (b *Buffer) DeleteLine() {
	b.mutate(func() {
		y, _ := b.Pos(b.q0)
		switch {
		case len(b.lines) == 1:
			b.replace(0, len(b.lines[0]), "")
		case y < len(b.lines)-1:
			b.replace(b.Offset(y, 0), b.Offset(y+1, 0), "")
		default:
			b.replace(b.Offset(y, 0)-1, b.Len(), "")
			b.moveTo(b.Offset(y-1, 0))
		}
	})
}

// DeleteWordBefore removes the word before dot, or the newline if dot starts
// a line.
func (b *Buffer) DeleteWordBefore() {
	if b.q0 == 0 {
		return
	}
	b.mutate(func() {
		start := b.q0 - 1
		if y, x := b.Pos(b.q0); x > 0 {
			line := b.lines[y]
			for x > 0 && line[x-1] == ' ' {
				x--
			}
			for x > 0 && line[x-1] != ' ' {
				x--
			}
			start = b.Offset(y, x)
		}
		b.replace(start, b.q0, "")
	})
}

// Insert replaces dot with s.
func (b *Buffer) Insert(s string) { b.mutate(func() { b.replace(b.q0, b.q1, s) }) }

func (b *Buffer) DeleteSelection() { b.Insert("") }

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

// Paste replaces dot with the clipboard and selects what it pasted.
func (b *Buffer) Paste() {
	text, _ := readClipboard()
	if text == "" {
		return
	}
	b.mutate(func() {
		q0 := b.q0
		b.SetDot(q0, b.replace(q0, b.q1, text))
	})
}

// Backspace removes the selection, or else the rune before the cursor.
func (b *Buffer) Backspace() {
	switch {
	case b.q0 < b.q1:
		b.DeleteSelection()
	case b.q0 > 0:
		b.mutate(func() { b.replace(b.q0-1, b.q0, "") })
	}
}

// Delete removes the selection, or else the rune after the cursor.
func (b *Buffer) Delete() {
	switch {
	case b.q0 < b.q1:
		b.DeleteSelection()
	case b.q0 < b.Len():
		b.mutate(func() { b.replace(b.q0, b.q0+1, "") })
	}
}

// ReplaceRangeRunes replaces the text [q0, q1), clamped to the buffer, with
// runes.
func (b *Buffer) ReplaceRangeRunes(q0, q1 int, runes []rune) {
	b.mutate(func() { b.replaceRangeRunesNoSave(q0, q1, runes) })
}

func (b *Buffer) replaceRangeRunesNoSave(q0, q1 int, runes []rune) {
	n := b.Len()
	q0 = max(0, min(q0, n))
	q1 = max(q0, min(q1, n))
	b.replace(q0, q1, string(runes))
}

// The motions leave the cursor where they move to. One that moves back
// starts from the start of dot, one that moves on from its end.

func (b *Buffer) moveTo(q int) { b.q0, b.q1 = q, q }

func (b *Buffer) MoveLeft()  { b.moveTo(max(0, b.q0-1)) }
func (b *Buffer) MoveRight() { b.moveTo(min(b.Len(), b.q1+1)) }

func (b *Buffer) MoveHome() {
	y, _ := b.Pos(b.q0)
	b.moveTo(b.Offset(y, 0))
}

func (b *Buffer) MoveEnd() {
	y, _ := b.Pos(b.q1)
	b.moveTo(b.Offset(y, len(b.lines[y])))
}

func (b *Buffer) MoveUp() {
	y, x := b.Pos(b.q0)
	if y > 0 {
		y--
		x = min(x, len(b.lines[y]))
	}
	b.moveTo(b.Offset(y, x))
}

func (b *Buffer) MoveDown() {
	y, x := b.Pos(b.q1)
	if y < len(b.lines)-1 {
		y++
		x = min(x, len(b.lines[y]))
	}
	b.moveTo(b.Offset(y, x))
}

func (b *Buffer) MoveWordLeft() {
	y, x := b.Pos(b.q0)
	if x == 0 {
		b.MoveLeft()
		return
	}
	line := b.lines[y]
	for x > 0 && !IsWordChar(line[x-1]) {
		x--
	}
	for x > 0 && IsWordChar(line[x-1]) {
		x--
	}
	b.moveTo(b.Offset(y, x))
}

func (b *Buffer) MoveWordRight() {
	y, x := b.Pos(b.q1)
	line := b.lines[y]
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
	b.moveTo(b.Offset(y, x))
}
