package main

import (
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/aleksana/peak/internal/quote"
	"github.com/gdamore/tcell/v3"
	uwidth "golang.org/x/text/width"
)

func IsWordChar(r rune) bool {
	return r != 0 && !unicode.IsSpace(r)
}

// clickRange returns what a click at q in b stands for, as the rune offsets
// [q0, q1) of b and their text. A click in the selection takes the
// selection; so does a click on blank space, since we cannot set the mouse
// cursor position like acme. Otherwise it takes the word at q: a field (see
// package quote), so a backtick-quoted name is one word. Quoted text stands
// for its contents, as if they were selected: the range is inside the
// backticks and the text is unquoted.
func clickRange(b *Buffer, q int) (q0, q1 int, text string) {
	q0, q1 = q, q
	if y, x := b.Pos(q); x < len(b.lines[y]) {
		line := b.lines[y]
		s, e := quote.FieldAt(x, len(line), func(i int) rune { return line[i] })
		q0, q1 = b.Offset(y, s), b.Offset(y, e)
	}
	if strings.TrimSpace(b.GetSelectedText()) != "" && (b.q0 <= q && q < b.q1 || q0 == q1) {
		q0, q1 = b.q0, b.q1
	}

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

// Search returns where word next occurs in buf from q on, wrapping around
// to the start.
func Search(buf *Buffer, word string, q int) (int, bool) {
	text, w := []rune(buf.GetText()), []rune(word)
	find := func(from, to int) int {
		for i := from; i < to && i+len(w) <= len(text); i++ {
			if slices.Equal(text[i:i+len(w)], w) {
				return i
			}
		}
		return -1
	}
	if len(w) == 0 {
		return 0, false
	}
	if i := find(q, len(text)); i >= 0 {
		return i, true
	}
	if i := find(0, q); i >= 0 {
		return i, true
	}
	return 0, false
}

// A frame shows a Buffer in an area w wide and h high: it wraps the
// buffer's lines to its width, scrolls through them, draws them with the
// cursor, and selects text with the mouse. A view is a frame and what it
// does with the text: TextView edits it, TermView runs a program that
// writes it.
type frame struct {
	w, h   int
	buffer *Buffer
	// org is the rune offset of the first character shown. Whatever changes
	// the text moves it with the text (a window's body hook, a terminal's
	// forget), so the view stays where it was.
	org int
	// autoScroll is whether the view follows what is written: the cursor
	// of a TextView, the screen of a TermView. Scrolling up stops it;
	// scrolling to the end starts it again.
	autoScroll    bool
	scrollable    bool // false for tags, which show their text from the top
	drag          bool // a selection is being swept
	anchor        int  // where the sweep began
	underlineLast bool // underline the last line, as the active window's tag
	// cursor is the rune offset the cursor is shown at, unless cursorHidden;
	// the view's Layout sets them.
	cursor       int
	cursorHidden bool
	// layout is the buffer's lines wrapped at the frame's width, laid out for
	// laidOut; read it through lines.
	layout   []VisualLine
	laidOut  layoutKey
	theme    *Theme
	colors   *colorPair // points into theme, so theme changes apply
	tabWidth int
	// styleAt, when non-nil, returns the style to draw the rune at (line,
	// col) in, given the frame's own style s for it.
	styleAt func(line, col int, s tcell.Style) tcell.Style
}

func newFrame(b *Buffer, w, h int, theme *Theme, colors *colorPair, scrollable bool) frame {
	return frame{w: w, h: h, buffer: b, theme: theme, colors: colors, scrollable: scrollable, tabWidth: 4}
}

// mouse handles the mouse in the frame: the wheel scrolls it, Button1 sweeps
// a selection, and another button puts the cursor where it is pressed,
// unless something is selected.
func (f *frame) mouse(ev *tcell.EventMouse) {
	buttons := ev.Buttons()
	if f.scrollable {
		if buttons&tcell.WheelUp != 0 {
			f.Scroll(-1)
			return
		}
		if buttons&tcell.WheelDown != 0 {
			f.Scroll(1)
			return
		}
	}
	if buttons == tcell.ButtonNone {
		f.drag = false
		return
	}
	q := f.PosAt(ev.Position())
	switch {
	case buttons == tcell.ButtonPrimary && !f.drag:
		f.drag, f.anchor = true, q
		f.buffer.SetDot(q, q)
	case buttons == tcell.ButtonPrimary:
		f.buffer.SetDot(f.anchor, q)
	case f.buffer.q0 == f.buffer.q1:
		f.buffer.SetDot(q, q)
	}
}

func (f *frame) runeWidth(r rune, visualPos int) int {
	if r == '\t' {
		return f.tabWidth - (visualPos % f.tabWidth)
	}
	if unicode.IsMark(r) || unicode.Is(unicode.Cf, r) || unicode.IsControl(r) {
		return 1
	}
	if wide(r) {
		return 2
	}
	return 1
}

// wide reports whether r takes two columns.
func wide(r rune) bool {
	k := uwidth.LookupRune(r).Kind()
	return k == uwidth.EastAsianWide || k == uwidth.EastAsianFullwidth
}

// A VisualLine is a row of the frame: the runes [Start, End) of a buffer line.
type VisualLine struct {
	BufferLine int
	Start, End int
}

// layoutKey is what a layout depends on.
type layoutKey struct{ w, tabWidth, version int }

// lines returns the buffer's lines wrapped at the frame's width, laying them
// out again if the text, the width or the tab width has changed.
func (f *frame) lines() []VisualLine {
	key := layoutKey{f.w, f.tabWidth, f.buffer.version}
	if f.w <= 0 || key == f.laidOut {
		return f.layout
	}
	f.laidOut = key
	f.layout = nil
	for i, line := range f.buffer.lines {
		if len(line) == 0 {
			f.layout = append(f.layout, VisualLine{i, 0, 0})
			continue
		}
		visualPos, start := 0, 0
		for idx, r := range line {
			width := f.runeWidth(r, visualPos)
			if visualPos+width > f.w && visualPos > 0 {
				f.layout = append(f.layout, VisualLine{i, start, idx})
				start, visualPos = idx, 0
				width = f.runeWidth(r, visualPos)
			}
			visualPos += width
		}
		f.layout = append(f.layout, VisualLine{i, start, len(line)})
	}
	return f.layout
}

// top returns the index of the visual line shown first: the one org is on.
func (f *frame) top() int {
	lines := f.lines()
	i := sort.Search(len(lines), func(i int) bool {
		return f.buffer.Offset(lines[i].BufferLine, lines[i].Start) > f.org
	})
	return max(0, i-1)
}

// setTop scrolls the view to show visual line i first.
func (f *frame) setTop(i int) {
	lines := f.lines()
	vl := lines[max(0, min(i, len(lines)-1))]
	f.org = f.buffer.Offset(vl.BufferLine, vl.Start)
}

func (f *frame) GetScroll() (scroll, total, visible int) {
	return f.top(), len(f.lines()), f.h
}

// Scroll scrolls n lines. Scrolling to the end makes the frame follow again;
// scrolling back up stops it.
func (f *frame) Scroll(n int) {
	f.setTop(f.top() + n)
	if f.top() >= len(f.lines())-f.h {
		f.autoScroll = true
	} else if n < 0 {
		f.autoScroll = false
	}
}

// visualOf returns the column and the visual line rune offset q is shown at.
func (f *frame) visualOf(q int) (vx, vrow int) {
	by, bx := f.buffer.Pos(q)
	lines := f.lines()
	for lidx, vl := range lines {
		if vl.BufferLine == by && bx >= vl.Start && bx <= vl.End {
			vx := 0
			line := f.buffer.lines[by]
			for i := vl.Start; i < bx; i++ {
				vx += f.runeWidth(line[i], vx)
			}
			// Wrap edge case: if cursor is exactly at width, move to next visual line
			if vx >= f.w && lidx+1 < len(lines) && lines[lidx+1].BufferLine == by {
				continue
			}
			return vx, lidx
		}
	}
	return 0, -1
}

// offsetAt returns the rune offset shown at column vx of visual line vidx.
func (f *frame) offsetAt(vx, vidx int) int {
	lines := f.lines()
	vl := lines[max(0, min(vidx, len(lines)-1))]
	line := f.buffer.lines[vl.BufferLine]
	bx, currVX := vl.Start, 0
	for i := vl.Start; i < vl.End; i++ {
		w := f.runeWidth(line[i], currVX)
		if currVX+w > vx {
			break
		}
		currVX += w
		bx = i + 1
	}
	return f.buffer.Offset(vl.BufferLine, bx)
}

func (f *frame) Draw(cv canvas) {
	selStyle := f.theme.Selection.style()
	selected := func(q int) bool { return f.buffer.q0 <= q && q < f.buffer.q1 }
	lines, vrow := f.lines(), 0
	for lidx := f.top(); lidx < len(lines) && vrow < f.h; lidx++ {
		vl, vcol := lines[lidx], 0
		line := f.buffer.lines[vl.BufferLine]
		start := f.buffer.Offset(vl.BufferLine, 0)
		lineStyle := f.colors.style()
		if f.underlineLast && lidx == len(lines)-1 {
			lineStyle = lineStyle.Underline(true)
		}
		for idx := vl.Start; idx < vl.End; idx++ {
			r, style := line[idx], lineStyle
			if selected(start + idx) {
				style = selStyle
			} else if f.styleAt != nil {
				style = f.styleAt(vl.BufferLine, idx, style)
			}

			width := f.runeWidth(r, vcol)
			if r == '\t' {
				cv.fill(rect{vcol, vrow, width, 1}, style)
			} else {
				str := string(r)
				if unicode.IsMark(r) || unicode.Is(unicode.Cf, r) || unicode.IsControl(r) {
					str = "□"
				}
				cv.put(vcol, vrow, str, style)
			}
			vcol += width
		}
		eolStyle := lineStyle
		if selected(start + vl.End) {
			eolStyle = selStyle
		}
		cv.fill(rect{vcol, vrow, f.w - vcol, 1}, eolStyle)
		vrow++
	}
	cv.fill(rect{0, vrow, f.w, f.h - vrow}, f.colors.style())
}

// PosAt returns the rune offset shown at (x, y).
func (f *frame) PosAt(x, y int) int {
	return f.offsetAt(x, y+f.top())
}

func (f *frame) ShowCursor(cv canvas) {
	if f.cursorHidden {
		return
	}
	vx, vrow := f.visualOf(f.cursor)
	cv.showCursor(max(0, min(vx, f.w-1)), vrow-f.top())
}

// fit gives f width w and the height its text needs at that width.
func (f *frame) fit(w int) {
	f.w = w
	f.h = max(1, len(f.lines()))
}

// AdvanceSweep moves the end of a sweep under way a line in direction
// dir.
func (f *frame) AdvanceSweep(dir int) {
	if !f.drag {
		return
	}
	end := f.buffer.q1
	if end == f.anchor {
		end = f.buffer.q0
	}
	y, x := f.buffer.Pos(end)
	y = max(0, min(y+dir, len(f.buffer.lines)-1))
	f.buffer.SetDot(f.anchor, f.buffer.Offset(y, min(x, len(f.buffer.lines[y]))))
}

// Search selects the next match of word from the end of dot on, wrapping
// around, and returns its line, or -1 if there is none.
func (f *frame) Search(word string) int {
	q, ok := Search(f.buffer, word, f.buffer.q1)
	if !ok {
		return -1
	}
	f.buffer.SetDot(q, q+len([]rune(word)))
	line, _ := f.buffer.Pos(q)
	return line
}

// showLine scrolls to show line n, if it is not shown, and reports whether
// it did.
func (f *frame) showLine(n int) bool {
	vidx := slices.IndexFunc(f.lines(), func(vl VisualLine) bool { return vl.BufferLine == n })
	if top := f.top(); vidx == -1 || (vidx >= top && vidx < top+f.h) {
		return false
	}
	f.setTop(vidx - f.h/4)
	return true
}
