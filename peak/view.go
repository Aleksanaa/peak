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
	underlineLast bool // underline the last line, as the active window's tag
	cursorHidden  bool // the program hid the cursor
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
// unless something is selected. A selection of nothing is dropped.
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
		if f.buffer.selection.Active && f.buffer.selection.Start == f.buffer.selection.End {
			f.buffer.ClearSelection()
		}
		return
	}
	p := f.PosAt(ev.Position())
	switch {
	case buttons == tcell.ButtonPrimary && !f.drag:
		f.drag, f.buffer.cursor = true, p
		f.buffer.SetSelection(p, p)
	case buttons == tcell.ButtonPrimary:
		f.buffer.cursor, f.buffer.selection.End = p, p
	case !f.buffer.selection.Active:
		f.buffer.cursor = p
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
		return f.buffer.RuneOffsetOfPos(lines[i].BufferLine, lines[i].Start) > f.org
	})
	return max(0, i-1)
}

// setTop scrolls the view to show visual line i first.
func (f *frame) setTop(i int) {
	lines := f.lines()
	vl := lines[max(0, min(i, len(lines)-1))]
	f.org = f.buffer.RuneOffsetOfPos(vl.BufferLine, vl.Start)
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

// bufferToVisual translates a buffer position to visual coordinates (vx, vrow).
func (f *frame) bufferToVisual(bx, by int) (int, int) {
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

// visualToBuffer translates visual coordinates (vx, vidx) to buffer position (bx, by).
func (f *frame) visualToBuffer(vx, vidx int) (int, int) {
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
	return bx, vl.BufferLine
}

func (f *frame) Draw(cv canvas) {
	selStyle := f.theme.Selection.style()
	lines, vrow := f.lines(), 0
	for lidx := f.top(); lidx < len(lines) && vrow < f.h; lidx++ {
		vl, vcol := lines[lidx], 0
		line := f.buffer.lines[vl.BufferLine]
		lineStyle := f.colors.style()
		if f.underlineLast && lidx == len(lines)-1 {
			lineStyle = lineStyle.Underline(true)
		}
		for idx := vl.Start; idx < vl.End; idx++ {
			r, style := line[idx], lineStyle
			if f.buffer.selection.Contains(idx, vl.BufferLine, false) {
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
		if f.buffer.selection.Contains(vl.End, vl.BufferLine, false) {
			eolStyle = selStyle
		}
		cv.fill(rect{vcol, vrow, f.w - vcol, 1}, eolStyle)
		vrow++
	}
	cv.fill(rect{0, vrow, f.w, f.h - vrow}, f.colors.style())
}

func (f *frame) PosAt(x, y int) Cursor {
	bx, by := f.visualToBuffer(x, y+f.top())
	return Cursor{bx, by}
}

func (f *frame) ShowCursor(cv canvas) {
	if f.cursorHidden {
		return
	}
	vx, vrow := f.bufferToVisual(f.buffer.cursor.x, f.buffer.cursor.y)
	cv.showCursor(max(0, min(vx, f.w-1)), vrow-f.top())
}

// fit gives f width w and the height its text needs at that width.
func (f *frame) fit(w int) {
	f.w = w
	f.h = max(1, len(f.lines()))
}

func (f *frame) AdvanceDragCursor(dir int) {
	if !f.drag {
		return
	}
	f.buffer.cursor = f.buffer.selection.End
	if dir > 0 {
		f.buffer.MoveDown()
	} else {
		f.buffer.MoveUp()
	}
	f.buffer.selection.End = f.buffer.cursor
}

// Search selects the next match of word after the selection, or else after
// the cursor, wrapping around.
func (f *frame) Search(word string) int {
	start := f.buffer.cursor
	if f.buffer.selection.Active {
		start = f.buffer.selection.End
	}
	line, sel, ok := Search(f.buffer, word, start)
	if ok {
		f.buffer.cursor = sel.End
		f.buffer.selection = sel
		return line
	}
	return -1
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
