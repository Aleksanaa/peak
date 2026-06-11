package main

import (
	"path/filepath"
	"strings"
	"sync"
	"time"

	"unicode"

	"github.com/aleksana/peak/internal/session"
	"github.com/aleksana/peak/internal/wevent"
	"github.com/gdamore/tcell/v2"
	uwidth "golang.org/x/text/width"
)

type WinKind int

const (
	WinFile WinKind = iota // regular file (editable, tracks dirty)
	WinDir                 // directory listing
	WinOut                 // output/error sink
	WinTerm                // terminal emulator
)

type VisualLine struct {
	BufferLine int
	Start, End int
}

type View interface {
	// Layout computes visual-line wrapping and synchronises scroll position.
	// Must be called once per frame before Draw, and after every Resize.
	// Must not paint anything.
	Layout()
	Draw(tcell.Screen)
	ShowCursor(tcell.Screen)
	Resize(x, y, w, h int)
	HandleEvent(tcell.Event) bool
	GetClickWord(mx, my int) string
	GetSelectedText() string
	GetBuffer() *Buffer
	Scroll(n int)
	GetScroll() (scroll, total, visible int)
	Search(word string) int
	ShowLineAt(lineNum int)
	IsRaw() bool
}

type dragCursor interface {
	AdvanceDragCursor(dir int)
}

type TextView struct {
	BaseView
	buffer        *Buffer
	style         func() tcell.Style
	drag          bool
	singleLine    bool
	scrollable    bool
	underlineLast bool
	layout        []VisualLine
	lastWidth     int
	lastVersion   int
	theme         *Theme
	tabWidth      int
	typingStart   *Cursor
	typingEnd     *Cursor
	// colorAt, when non-nil, returns a foreground color override for a rune offset.
	colorAt func(runeOff int) (tcell.Color, bool)
}

func (tv *TextView) IsRaw() bool {
	return false
}

func NewTextView(text string, x, y, w, h int, style tcell.Style, singleLine, scrollable bool) *TextView {
	tv := &TextView{
		BaseView: BaseView{
			x: x, y: y, w: w, h: h,
		},
		buffer:      NewBuffer(text),
		style:       func() tcell.Style { return style },
		singleLine:  singleLine,
		scrollable:  scrollable,
		lastVersion: -1,
		tabWidth:    4,
	}
	tv.UpdateLayout()
	return tv
}

func (tv *TextView) runeWidth(r rune, visualPos int) int {
	if r == '\t' {
		return tv.tabWidth - (visualPos % tv.tabWidth)
	}
	if unicode.IsMark(r) || unicode.Is(unicode.Cf, r) || unicode.IsControl(r) {
		return 1
	}
	k := uwidth.LookupRune(r).Kind()
	if k == uwidth.EastAsianWide || k == uwidth.EastAsianFullwidth {
		return 2
	}
	return 1
}

func (tv *TextView) UpdateLayout() {
	if tv.w <= 0 {
		return
	}
	if len(tv.layout) > 0 && tv.w == tv.lastWidth && tv.buffer.version == tv.lastVersion {
		return
	}

	ratio := 0.0
	if len(tv.layout) > 0 {
		ratio = float64(tv.scroll.Pos) / float64(len(tv.layout))
	}

	tv.lastWidth = tv.w
	tv.lastVersion = tv.buffer.version
	tv.layout = nil
	for i, line := range tv.buffer.lines {
		if len(line) == 0 {
			tv.layout = append(tv.layout, VisualLine{i, 0, 0})
			continue
		}
		visualPos, start := 0, 0
		for idx, r := range line {
			width := tv.runeWidth(r, visualPos)
			if visualPos+width > tv.w && visualPos > 0 {
				tv.layout = append(tv.layout, VisualLine{i, start, idx})
				start, visualPos = idx, 0
				width = tv.runeWidth(r, visualPos)
			}
			visualPos += width
		}
		tv.layout = append(tv.layout, VisualLine{i, start, len(line)})
	}

	limit := len(tv.layout)
	if len(tv.layout) <= tv.h {
		limit = 0
	}
	tv.scroll.Pos = max(0, min(limit, int(ratio*float64(len(tv.layout)))))
}

func (tv *TextView) Layout() {
	if !tv.scrollable {
		tv.scroll.Pos = 0
	}
	tv.UpdateLayout()
	tv.SyncScroll()
}

func (tv *TextView) GetScroll() (scroll, total, visible int) {
	return tv.scroll.Pos, len(tv.layout), tv.h
}

func (tv *TextView) Scroll(n int) {
	_, total, visible := tv.GetScroll()
	tv.scroll.Scroll(n, total, visible)
}

func (tv *TextView) GotoLineCol(lineNum, colNum int) {
	lineNum = max(0, min(lineNum, len(tv.buffer.lines)-1))
	if colNum < 0 {
		end := Cursor{len(tv.buffer.lines[lineNum]), lineNum}
		tv.buffer.SetSelection(Cursor{0, lineNum}, end)
		tv.buffer.cursor = end
	} else {
		colNum = max(0, min(colNum, len(tv.buffer.lines[lineNum])))
		tv.buffer.cursor = Cursor{colNum, lineNum}
		tv.buffer.ClearSelection()
	}
	tv.ShowLineAt(lineNum)
}

// bufferToVisual translates a buffer position to visual coordinates (vx, vrow).
func (tv *TextView) bufferToVisual(bx, by int) (int, int) {
	for lidx, vl := range tv.layout {
		if vl.BufferLine == by && bx >= vl.Start && bx <= vl.End {
			vx := 0
			line := tv.buffer.lines[by]
			for i := vl.Start; i < bx; i++ {
				vx += tv.runeWidth(line[i], vx)
			}
			// Wrap edge case: if cursor is exactly at width, move to next visual line
			if vx >= tv.w && lidx+1 < len(tv.layout) && tv.layout[lidx+1].BufferLine == by {
				continue
			}
			return vx, lidx
		}
	}
	return 0, -1
}

// visualToBuffer translates visual coordinates (vx, vidx) to buffer position (bx, by).
func (tv *TextView) visualToBuffer(vx, vidx int) (int, int) {
	if vidx < 0 {
		vidx = 0
	}
	if vidx >= len(tv.layout) {
		vidx = len(tv.layout) - 1
	}
	vl := tv.layout[vidx]
	line := tv.buffer.lines[vl.BufferLine]
	bx, currVX := vl.Start, 0
	for i := vl.Start; i < vl.End; i++ {
		w := tv.runeWidth(line[i], currVX)
		if currVX+w > vx {
			break
		}
		currVX += w
		bx = i + 1
	}
	return bx, vl.BufferLine
}

func (tv *TextView) Draw(s tcell.Screen) {
	selStyle := tcell.StyleDefault.Background(tv.theme.SelectionBG).Foreground(tv.theme.SelectionFG)

	vrow := 0
	for lidx := tv.scroll.Pos; lidx < len(tv.layout) && vrow < tv.h; lidx++ {
		vl, vcol := tv.layout[lidx], 0
		line := tv.buffer.lines[vl.BufferLine]
		lineStyle := tv.style()
		if tv.underlineLast && lidx == len(tv.layout)-1 {
			lineStyle = lineStyle.Underline(true)
		}
		var lineRuneBase int
		if tv.colorAt != nil {
			lineRuneBase = tv.buffer.RuneOffsetOfPos(vl.BufferLine, vl.Start)
		}
		for idx := vl.Start; idx < vl.End && vcol < tv.w; idx++ {
			r, style := line[idx], lineStyle
			if tv.buffer.selection.Contains(idx, vl.BufferLine, false) {
				style = selStyle
			} else if tv.colorAt != nil {
				if c, ok := tv.colorAt(lineRuneBase + idx - vl.Start); ok {
					style = style.Foreground(c)
				}
			}

			width := tv.runeWidth(r, vcol)
			char := r
			if r == '\t' {
				char = ' '
			} else if unicode.IsMark(r) || unicode.Is(unicode.Cf, r) || unicode.IsControl(r) {
				char = '□'
			}

			for k := 0; k < width && vcol < tv.w; k++ {
				s.SetContent(tv.x+vcol, tv.y+vrow, char, nil, style)
				vcol++
				if r != '\t' {
					char = ' '
				} // Only draw character once if it's wide
			}
		}
		for ; vcol < tv.w; vcol++ {
			style := lineStyle
			if tv.buffer.selection.Contains(vl.End, vl.BufferLine, false) {
				style = selStyle
			}
			s.SetContent(tv.x+vcol, tv.y+vrow, ' ', nil, style)
		}
		vrow++
	}
	for ; vrow < tv.h; vrow++ {
		for col := 0; col < tv.w; col++ {
			s.SetContent(tv.x+col, tv.y+vrow, ' ', nil, tv.style())
		}
	}
}

func (tv *TextView) GetClickWord(mx, my int) string {
	bx, by := tv.visualToBuffer(mx-tv.x, my-tv.y+tv.scroll.Pos)
	if tv.buffer.selection.Contains(bx, by, false) {
		word := strings.TrimSpace(tv.buffer.GetSelectedText())
		if word != "" {
			return word
		}
	}
	// double click on blank area is same as double click on selection
	// complement that we cannot set mouse cursor position like acme
	word := strings.TrimSpace(tv.buffer.GetWordAt(bx, by))
	if word == "" && tv.buffer.selection.Active {
		return strings.TrimSpace(tv.buffer.GetSelectedText())
	}
	return word
}

func (tv *TextView) ShowCursor(s tcell.Screen) {
	vx, vrow := tv.bufferToVisual(tv.buffer.cursor.x, tv.buffer.cursor.y)
	if vrow >= tv.scroll.Pos && vrow < tv.scroll.Pos+tv.h {
		if vx >= tv.w {
			vx = tv.w - 1
		}
		if vx < 0 {
			vx = 0
		}
		s.ShowCursor(tv.x+vx, tv.y+(vrow-tv.scroll.Pos))
	} else {
		s.HideCursor()
	}
}

func (tv *TextView) Resize(x, y, w, h int) {
	if tv.x == x && tv.y == y && tv.w == w && tv.h == h {
		return
	}
	tv.x, tv.y, tv.w, tv.h = x, y, w, h
	tv.UpdateLayout()
}

func (tv *TextView) GetBuffer() *Buffer {
	return tv.buffer
}

func (tv *TextView) GetSelectedText() string {
	return tv.buffer.GetSelectedText()
}

func (tv *TextView) prepareTyping() bool {
	tv.typingEnd = nil
	if tv.buffer.selection.Active {
		start, _ := tv.buffer.selection.Ordered()
		tv.typingStart = &Cursor{start.x, start.y}
		return true
	}
	if tv.typingStart == nil {
		tv.typingStart = &Cursor{tv.buffer.cursor.x, tv.buffer.cursor.y}
	}
	return false
}

func (tv *TextView) HandleEvent(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *tcell.EventKey:
		switch ev.Key() {
		case tcell.KeyEsc:
			if tv.typingStart != nil && tv.typingEnd == nil {
				tv.typingEnd = &Cursor{tv.buffer.cursor.x, tv.buffer.cursor.y}
				tv.buffer.SetSelection(*tv.typingStart, *tv.typingEnd)
				tv.ShowLineAt(tv.typingStart.y)
			} else if tv.buffer.selection.Active {
				start, _ := tv.buffer.selection.Ordered()
				tv.buffer.cursor = start
				tv.buffer.ClearSelection()
			} else if tv.typingStart != nil && tv.typingEnd != nil {
				tv.buffer.SetSelection(*tv.typingStart, *tv.typingEnd)
				tv.ShowLineAt(tv.typingStart.y)
			}
		case tcell.KeyCtrlZ:
			tv.typingStart = nil
			if ev.Modifiers()&tcell.ModShift != 0 {
				tv.buffer.Redo()
			} else {
				tv.buffer.Undo()
			}
		case tcell.KeyCtrlY:
			tv.typingStart = nil
			tv.buffer.Redo()
		case tcell.KeyCtrlA:
			tv.typingStart = nil
			last := len(tv.buffer.lines) - 1
			tv.buffer.SetSelection(Cursor{0, 0}, Cursor{len(tv.buffer.lines[last]), last})
		case tcell.KeyCtrlC:
			tv.buffer.Snarf()
		case tcell.KeyCtrlX:
			tv.typingStart = nil
			tv.buffer.Cut()
		case tcell.KeyCtrlV:
			tv.prepareTyping()
			tv.buffer.Paste()
		case tcell.KeyCtrlU:
			tv.typingStart = nil
			tv.buffer.ClearSelection()
			tv.buffer.DeleteLine()
		case tcell.KeyCtrlW:
			tv.typingStart = nil
			tv.buffer.ClearSelection()
			tv.buffer.DeleteWordBefore()
		case tcell.KeyCtrlH, tcell.KeyBackspace, tcell.KeyBackspace2:
			tv.prepareTyping()
			tv.buffer.Backspace()
		case tcell.KeyDelete:
			tv.prepareTyping()
			tv.buffer.Delete()
		case tcell.KeyPgUp:
			tv.typingStart = nil
			tv.buffer.ClearSelection()
			tv.scroll.Pos = max(0, tv.scroll.Pos-tv.h)
			_, vrow := tv.bufferToVisual(tv.buffer.cursor.x, tv.buffer.cursor.y)
			if vrow >= tv.scroll.Pos+tv.h {
				bx, by := tv.visualToBuffer(0, tv.scroll.Pos)
				tv.buffer.cursor = Cursor{bx, by}
			}
		case tcell.KeyPgDn:
			tv.typingStart = nil
			tv.buffer.ClearSelection()
			tv.scroll.Pos = min(len(tv.layout)-1, tv.scroll.Pos+tv.h)
			tv.scroll.Pos = max(0, tv.scroll.Pos)
			_, vrow := tv.bufferToVisual(tv.buffer.cursor.x, tv.buffer.cursor.y)
			if vrow < tv.scroll.Pos {
				bx, by := tv.visualToBuffer(0, tv.scroll.Pos)
				tv.buffer.cursor = Cursor{bx, by}
			}
		case tcell.KeyUp:
			tv.typingStart = nil
			tv.buffer.ClearSelection()
			if !tv.singleLine {
				tv.buffer.MoveUp()
			}
		case tcell.KeyDown:
			tv.typingStart = nil
			tv.buffer.ClearSelection()
			if !tv.singleLine {
				tv.buffer.MoveDown()
			}
		case tcell.KeyLeft:
			tv.typingStart = nil
			tv.buffer.ClearSelection()
			if ev.Modifiers()&tcell.ModCtrl != 0 {
				tv.buffer.MoveWordLeft()
			} else {
				tv.buffer.MoveLeft()
			}
		case tcell.KeyRight:
			tv.typingStart = nil
			tv.buffer.ClearSelection()
			if ev.Modifiers()&tcell.ModCtrl != 0 {
				tv.buffer.MoveWordRight()
			} else {
				tv.buffer.MoveRight()
			}
		case tcell.KeyHome:
			tv.typingStart = nil
			tv.buffer.ClearSelection()
			tv.buffer.MoveHome()
		case tcell.KeyEnd:
			tv.typingStart = nil
			tv.buffer.ClearSelection()
			tv.buffer.MoveEnd()
		case tcell.KeyEnter:
			if tv.prepareTyping() {
				tv.buffer.DeleteSelection()
				tv.typingStart = nil
			}
			if !tv.singleLine {
				tv.buffer.NewLine()
			}
		case tcell.KeyTab:
			if tv.prepareTyping() {
				tv.buffer.DeleteSelection()
			}
			tv.buffer.Insert('\t')
		case tcell.KeyRune:
			if tv.prepareTyping() {
				tv.buffer.DeleteSelection()
			}
			tv.buffer.Insert(ev.Rune())
		}
		tv.scroll.AutoScroll = true
		tv.UpdateLayout()
		_, vrow := tv.bufferToVisual(tv.buffer.cursor.x, tv.buffer.cursor.y)
		if vrow < tv.scroll.Pos {
			tv.scroll.Pos = vrow
		} else if vrow >= tv.scroll.Pos+tv.h {
			tv.scroll.Pos = vrow - tv.h + 1
		}
		tv.scroll.Clamp(len(tv.layout), tv.h)
		return false
	case *tcell.EventMouse:
		buttons := ev.Buttons()
		if buttons != tcell.ButtonNone {
			tv.typingStart = nil
		}
		if tv.scrollable {
			if buttons&tcell.WheelUp != 0 {
				tv.Scroll(-1)
				return false
			}
			if buttons&tcell.WheelDown != 0 {
				tv.Scroll(1)
				return false
			}
		}
		mx, my := ev.Position()
		if buttons != tcell.ButtonNone {
			bx, by := tv.visualToBuffer(mx-tv.x, my-tv.y+tv.scroll.Pos)
			if buttons == tcell.Button1 && !tv.drag {
				tv.buffer.ClearSelection()
			}
			if buttons == tcell.Button1 {
				if !tv.drag {
					tv.drag, tv.buffer.cursor = true, Cursor{bx, by}
					tv.buffer.SetSelection(tv.buffer.cursor, tv.buffer.cursor)
				} else {
					tv.buffer.cursor = Cursor{bx, by}
					tv.buffer.selection.End = Cursor{bx, by}
				}
			} else if !tv.buffer.selection.Active {
				tv.buffer.cursor = Cursor{bx, by}
			}
		} else {
			tv.drag = false
			if tv.buffer.selection.Active && tv.buffer.selection.Start == tv.buffer.selection.End {
				tv.buffer.ClearSelection()
			}
		}
	}
	return false
}

func (tv *TextView) AdvanceDragCursor(dir int) {
	if !tv.drag {
		return
	}
	if dir > 0 {
		tv.buffer.MoveDown()
	} else {
		tv.buffer.MoveUp()
	}
	tv.buffer.selection.End = tv.buffer.cursor
}

func (tv *TextView) SyncScroll() {
	if !tv.scrollable || !tv.scroll.AutoScroll {
		return
	}
	_, vrow := tv.bufferToVisual(tv.buffer.cursor.x, tv.buffer.cursor.y)
	if vrow >= tv.scroll.Pos+tv.h {
		tv.scroll.Pos = vrow - tv.h + 1
	}
	tv.scroll.Clamp(len(tv.layout), tv.h)
}

func (tv *TextView) Search(word string) int {
	line, sel, ok := Search(tv.buffer, word, tv.buffer.cursor)
	if ok {
		tv.buffer.cursor = sel.End
		tv.buffer.selection = sel
		return line
	}
	return -1
}

func (tv *TextView) ShowLineAt(lineNum int) {
	tv.UpdateLayout()
	vidx := -1
	for i, vl := range tv.layout {
		if vl.BufferLine == lineNum {
			vidx = i
			break
		}
	}
	if vidx == -1 || (vidx >= tv.scroll.Pos && vidx < tv.scroll.Pos+tv.h) {
		return
	}
	tv.scroll.Pos = vidx - tv.h/4
	tv.scroll.Clamp(len(tv.layout), tv.h)
	tv.scroll.AutoScroll = true
	tv.SyncScroll()
}

type Handle struct {
	BaseView
	color tcell.Color
}

func (hd *Handle) Draw(s tcell.Screen) {
	style := tcell.StyleDefault.Background(hd.color).Foreground(tcell.ColorBlack)
	for i := 0; i < hd.h; i++ {
		s.SetContent(hd.x, hd.y+i, ' ', nil, style)
	}
}
func (hd *Handle) Resize(x, y, w, h int) { hd.x, hd.y, hd.w, hd.h = x, y, w, h }

type Scrollbar struct {
	BaseView
	thumbStyle   func() tcell.Style
	scrollPos    int
	totalLines   int
	visibleLines int
}

func (sb *Scrollbar) Draw(s tcell.Screen) {
	if sb.visibleLines == 0 || sb.totalLines <= sb.visibleLines {
		return
	}
	thumbHeight := max(1, (sb.visibleLines*sb.visibleLines)/sb.totalLines)
	thumbStart := min(sb.visibleLines-thumbHeight, (sb.scrollPos*sb.visibleLines)/sb.totalLines)
	for i := 0; i < thumbHeight; i++ {
		s.SetContent(sb.x, sb.y+thumbStart+i, ' ', nil, sb.thumbStyle())
	}
}
func (sb *Scrollbar) Resize(x, y, w, h int) { sb.x, sb.y, sb.w, sb.h = x, y, w, h }

type BodyView struct {
	TreeNode
	content View
	scroll  *Scrollbar
}

func (bv *BodyView) Resize(x, y, w, h int) {
	bv.x, bv.y, bv.w, bv.h = x, y, w, h
	bv.scroll.Resize(x, y, 1, h)
	bv.content.Resize(x+1, y, w-1, h)
}

type Window struct {
	TreeNode
	ID             int
	tag            *TextView
	body           View
	bodyView       *BodyView
	handle         *Handle
	parent         *Column
	editor         *Editor
	onExec         func(*Column, *Window, string) bool
	explicitHeight int

	kind          WinKind
	writable      bool
	savedVersion  int
	warnedVersion int

	lk        sync.Mutex
	eventSubs []*eventSub

	addrQ0, addrQ1 int

	spans []colorSpan

	mutSeq, bodySnapSeq uint64
}

func (w *Window) Layout()            {}
func (w *Window) Draw(tcell.Screen)  {}
func (w *Window) PreferredSize() int { return w.explicitHeight }
func (w *Window) MinSize() int       { return w.tagHeight() }

func (w *Window) WalkDraw(s tcell.Screen) {
	w.tag.underlineLast = w.editor.active == w

	handleColor := w.editor.theme.Handle
	switch w.kind {
	case WinOut, WinTerm:
		handleColor = w.editor.theme.HandleError
	case WinFile:
		if w.IsDirty() {
			handleColor = w.editor.theme.HandleDirty
		} else if w.writable {
			handleColor = w.editor.theme.HandleWritable
		} else {
			handleColor = w.editor.theme.HandleUnwritable
		}
	}
	w.handle.color = handleColor

	w.lk.Lock()
	w.tag.Layout()
	w.tag.Draw(s)
	spans := append([]colorSpan(nil), w.spans...)
	w.lk.Unlock()

	w.handle.Draw(s)

	if tv, ok := w.body.(*TextView); ok {
		if len(spans) > 0 {
			tv.colorAt = w.colorAtFunc(spans)
		} else {
			tv.colorAt = nil
		}
	}

	w.lk.Lock()
	w.body.Layout()
	sb := w.bodyView.scroll
	sb.scrollPos, sb.totalLines, sb.visibleLines = w.body.GetScroll()
	w.bodyView.scroll.Draw(s)
	w.body.Draw(s)
	w.lk.Unlock()
}

func (win *Window) subscribeEvent() *eventSub {
	sub := newEventSub()
	win.lk.Lock()
	win.eventSubs = append(win.eventSubs, sub)
	win.lk.Unlock()
	return sub
}

func (win *Window) unsubscribeEvent(sub *eventSub) {
	win.lk.Lock()
	for i, s := range win.eventSubs {
		if s == sub {
			win.eventSubs = append(win.eventSubs[:i], win.eventSubs[i+1:]...)
			break
		}
	}
	win.lk.Unlock()
}

// broadcastEvent delivers a counted event record to all open event file subscribers.
// Caller must hold win.lk. deliver is non-blocking so holding lk is safe.
func (win *Window) broadcastEvent(origin, typ byte, q0, q1, flag int, text string) {
	record := wevent.Format(wevent.Event{Origin: origin, Type: typ, Q0: q0, Q1: q1, Flag: flag, Text: text})
	for _, s := range win.eventSubs {
		s.deliver(record)
	}
}

// adjustPoint shifts a single rune offset after a buffer mutation
// [q0, q1Old) → [q0, q1New). Offsets inside the deleted region clamp to q0.
func adjustPoint(q, q0, q1Old, q1New int) int {
	if q <= q0 {
		return q
	}
	if q >= q1Old {
		return q + (q1New - q1Old)
	}
	return q0
}

// adjustSpans shifts or drops color spans to stay consistent with a body
// mutation [q0, q1Old) → [q0, q1New). Caller must hold win.lk.
func (win *Window) adjustSpans(q0, q1Old, q1New int) {
	if len(win.spans) == 0 {
		return
	}
	delta := q1New - q1Old
	spans := win.spans
	j := 0
	for _, sp := range spans {
		switch {
		case sp.q1 <= q0:
			// entirely before the change: unchanged
			spans[j] = sp
			j++
		case sp.q0 >= q1Old:
			// entirely after the change: shift both endpoints
			spans[j] = colorSpan{sp.q0 + delta, sp.q1 + delta, sp.attr}
			j++
		case sp.q0 < q0 && sp.q1 >= q1Old:
			// surrounds the changed region: only the end endpoint shifts
			spans[j] = colorSpan{sp.q0, sp.q1 + delta, sp.attr}
			j++
			// else: partially overlaps — drop; peak-lsp will rewrite shortly
		}
	}
	win.spans = spans[:j]
}

// colorAtFunc returns a closure that looks up a rune offset in the given spans.
func (win *Window) colorAtFunc(spans []colorSpan) func(int) (tcell.Color, bool) {
	theme := win.editor.theme
	i := 0
	lastOff := -1
	return func(runeOff int) (tcell.Color, bool) {
		if runeOff < lastOff {
			i = 0
		}
		lastOff = runeOff

		for i < len(spans) && spans[i].q1 <= runeOff {
			i++
		}
		if i < len(spans) && spans[i].q0 <= runeOff && runeOff < spans[i].q1 {
			return theme.colorForAttr(spans[i].attr), true
		}
		return 0, false
	}
}

func newWindow(tag string, parent *Column, editor *Editor, x, y, w, h int, onExec func(*Column, *Window, string) bool) *Window {
	tagStyle := tcell.StyleDefault.Background(editor.theme.TagBG).Foreground(editor.theme.TagFG)
	handle := &Handle{BaseView: BaseView{x: x, y: y, w: 1, h: 1}, color: editor.theme.Handle}
	bodyView := &BodyView{TreeNode: TreeNode{BaseView: BaseView{x: x + 1, y: y + 1, w: w - 1, h: h - 1}}}
	bodyView.scroll = &Scrollbar{
		BaseView:   BaseView{x: x + 1, y: y + 1, w: 1, h: h - 1},
		thumbStyle: func() tcell.Style { return tcell.StyleDefault.Background(editor.theme.ScrollThumb) },
	}
	win := &Window{
		TreeNode: TreeNode{BaseView: BaseView{x: x, y: y, w: w, h: h}},
		tag:      NewTextView(tag, x+1, y, w-1, 1, tagStyle, false, false),
		parent:   parent, editor: editor, onExec: onExec,
		handle: handle, bodyView: bodyView,
	}
	win.tag.theme = &editor.theme
	win.tag.style = func() tcell.Style {
		return tcell.StyleDefault.Background(editor.theme.TagBG).Foreground(editor.theme.TagFG)
	}
	win.tag.buffer.onMutate = func(_, _, _ int, _ string) {
		prev := len(win.tag.layout)
		win.tag.UpdateLayout()
		if len(win.tag.layout) != prev {
			win.reflow()
		}
	}
	return win
}

func NewTermWindow(tag string, parent *Column, editor *Editor, x, y, w, h int, cmd, dir string, onExec func(*Column, *Window, string) bool) (*Window, error) {
	sess, err := session.NewLocal(cmd, dir)
	if err != nil {
		return nil, err
	}
	return newTermWindowFromSession(tag, sess, parent, editor, x, y, w, h, onExec)
}

func newTermWindowFromSession(tag string, sess session.Session, parent *Column, editor *Editor, x, y, w, h int, onExec func(*Column, *Window, string) bool) (*Window, error) {
	win := newWindow(tag, parent, editor, x, y, w, h, onExec)
	term, err := NewTermView(editor, sess, x+1, y+1, w-1, h-1, func() {
		editor.RemoveWindow(win)
	})
	if err != nil {
		sess.Close()
		return nil, err
	}
	win.kind = WinTerm
	win.body = term
	win.bodyView.content = term
	if pty, ok := sess.(*ExternalPTY); ok {
		pty.onResize = func(rows, cols int) {
			win.broadcastEvent('P', 'Z', rows, cols, 0, "")
		}
	}
	filename := win.GetFilename()
	suffix := ""
	if base := filepath.Base(filename); strings.HasPrefix(base, "-") {
		suffix = base
	}
	tagDir := getPathDir(filename)
	var (
		tagDirBase    string
		remoteDirBase string
		enabled       bool
	)
	term.OnCWD = func(path string) {
		if !enabled {
			tagDirBase, remoteDirBase = commonPathBase(tagDir, path)
			enabled = true
		}
		var newPath string
		if strings.HasPrefix(path+"/", remoteDirBase+"/") {
			newPath = tagDirBase + path[len(remoteDirBase):]
		} else {
			newPath = tagDirBase + "/"
		}
		for {
			if _, err := getVFS().Stat(newPath); err == nil {
				break
			}
			if parent := filepath.Dir(newPath); parent != newPath {
				newPath = parent
			} else {
				newPath = tagDirBase + "/"
				break
			}
		}
		if suffix != "" {
			win.SetName(filepath.Join(newPath, suffix))
		} else {
			win.SetName(newPath + "/")
		}
	}
	return win, nil
}

func NewWindow(tag, body string, parent *Column, editor *Editor, x, y, w, h int, onExec func(*Column, *Window, string) bool) *Window {
	bodyStyle := tcell.StyleDefault.Background(editor.theme.BodyBG).Foreground(editor.theme.BodyFG)
	win := newWindow(tag, parent, editor, x, y, w, h, onExec)
	tv := NewTextView(body, x+1, y+1, w-1, h-1, bodyStyle, false, true)
	tv.theme = &editor.theme
	tv.style = func() tcell.Style {
		return tcell.StyleDefault.Background(editor.theme.BodyBG).Foreground(editor.theme.BodyFG)
	}
	win.body = tv
	win.bodyView.content = tv
	tv.buffer.onMutate = func(q0, q1Old, q1New int, text string) {
		win.mutSeq++
		win.adjustSpans(q0, q1Old, q1New)
		win.addrQ0 = adjustPoint(win.addrQ0, q0, q1Old, q1New)
		win.addrQ1 = adjustPoint(win.addrQ1, q0, q1Old, q1New)
		if q1Old > q0 {
			win.broadcastEvent('K', 'D', q0, q1Old, 0, "")
		}
		if text != "" {
			win.broadcastEvent('K', 'I', q0, q1New, 0, text)
		}
	}
	return win
}

func (win *Window) bodyTextView() *TextView {
	if tv, ok := win.body.(*TextView); ok {
		return tv
	}
	return nil
}

func (win *Window) IsDirty() bool {
	if win.kind != WinFile || !win.writable {
		return false
	}
	return win.body.GetBuffer().version != win.savedVersion
}

func (win *Window) Warned() bool {
	return win.warnedVersion == win.body.GetBuffer().version
}

func (win *Window) Warn() {
	win.warnedVersion = win.body.GetBuffer().version
}

func (win *Window) GetFilename() string {
	fields := strings.Fields(string(win.tag.buffer.lines[0]))
	if len(fields) > 0 {
		return fields[0]
	}
	return ""
}

func (win *Window) GetDir() string {
	return getPathDir(win.GetFilename())
}

func (win *Window) SetName(name string) {
	tag := win.tag.buffer.GetText()
	fields := strings.Fields(tag)
	if len(fields) > 0 {
		fields[0] = name
		win.tag.buffer.SetText(" " + strings.Join(fields, " ") + " ")
	} else {
		win.tag.buffer.SetText(" " + name + " Get Put Del ")
	}
}

// clickWordOffsets returns the rune offsets [q0, q1) of word in the target view.
func (win *Window) clickWordOffsets(target View, mx, my int, word string) (q0, q1 int) {
	tv, ok := target.(*TextView)
	if !ok {
		return 0, len([]rune(word))
	}
	bx, by := tv.visualToBuffer(mx-tv.x, my-tv.y+tv.scroll.Pos)
	if by < 0 || by >= len(tv.buffer.lines) {
		return 0, len([]rune(word))
	}
	wStart, wEnd := GetWordBoundaries(bx, len(tv.buffer.lines[by]), func(i int) rune {
		return tv.buffer.lines[by][i]
	})
	q0 = tv.buffer.RuneOffsetOfPos(by, wStart)
	q1 = tv.buffer.RuneOffsetOfPos(by, wEnd)
	return
}

func (win *Window) Contains(x, y int) bool {
	return x >= win.x && x < win.x+win.w && y >= win.y && y < win.y+win.h
}

func (win *Window) tagHeight() int {
	h := len(win.tag.layout)
	if h < 1 {
		return 1
	}
	return h
}

func (win *Window) reflow() {
	win.tag.Resize(win.x+1, win.y, win.w-1, 0)
	th := win.tagHeight()
	win.tag.h = th
	win.handle.Resize(win.x, win.y, 1, th)
	bh := max(0, win.h-th)
	win.bodyView.Resize(win.x, win.y+th, win.w, bh)
}

func (win *Window) Resize(x, y, w, h int) {
	win.x, win.y, win.w, win.h = x, y, w, h
	win.reflow()
}

func (win *Window) HandleEvent(ev tcell.Event) bool {
	me, ok := ev.(*tcell.EventMouse)
	if !ok {
		return false
	}

	mx, my := me.Position()
	win.tag.UpdateLayout()
	th := win.tagHeight()

	if mx == win.x {
		if my < win.y+th {
			return false
		}
		amount := my - (win.y + th) + 1
		btns := me.Buttons()
		if btns&tcell.Button1 != 0 {
			if win.editor.scrollWin == nil {
				win.body.Scroll(-amount)
				win.editor.scrollStartTime = time.Now()
			}
			win.editor.scrollWin, win.editor.scrollAmount, win.editor.scrollDir = win, amount, -1
		} else if btns&tcell.Button2 != 0 {
			if win.editor.scrollWin == nil {
				win.body.Scroll(amount)
				win.editor.scrollStartTime = time.Now()
			}
			win.editor.scrollWin, win.editor.scrollAmount, win.editor.scrollDir = win, amount, 1
		} else if btns&tcell.Button3 != 0 {
			if scroll, total, visible := win.body.GetScroll(); visible > 0 && total > 0 {
				newScroll := ((my - (win.y + th)) * total) / visible
				win.body.Scroll(newScroll - scroll)
			}
		}
		return false
	}

	target := win.body
	if my < win.y+th {
		target = win.tag
	}

	win.lk.Lock()
	target.HandleEvent(ev)
	btns := me.Buttons()
	var word string
	var q0, q1 int
	if btns&(tcell.Button3|tcell.Button2) != 0 && (!target.IsRaw() || me.Modifiers()&tcell.ModCtrl != 0) {
		word = target.GetClickWord(mx, my)
		if word != "" {
			q0, q1 = win.clickWordOffsets(target, mx, my, word)
			if btns&tcell.Button3 != 0 {
				win.broadcastEvent('M', 'x', q0, q1, 0, word)
			} else {
				win.broadcastEvent('M', 'l', q0, q1, 0, word)
			}
		}
	}
	win.lk.Unlock()

	if word == "" {
		return false
	}
	if btns&tcell.Button3 != 0 {
		return win.onExec(win.parent, win, word)
	}
	return win.editor.Plumb(win, word)
}
