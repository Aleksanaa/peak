package main

import (
	"strings"
	"sync"
	"time"

	"github.com/aleksana/peak/internal/session"
	"github.com/aleksana/peak/internal/wevent"
	"github.com/aleksana/peak/peak/tview"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

type VisualLine struct {
	BufferLine int
	Start, End int
}

type View interface {
	tview.Primitive
	Layout()
	ShowCursor(tcell.Screen)
	HandleEvent(tcell.Event) bool
	GetPos() (x, y, w, h int)
	SetPos(x, y, w, h int)
	GetClickWord(mx, my int) string
	GetSelectedText() string
	GetBuffer() *Buffer
	Scroll(n int)
	GetScroll() (scroll, total, visible int)
	Search(word string) int
	ShowLineAt(lineNum, vrow int)
	IsRaw() bool
}

type TextView struct {
	BaseView
	buffer        *Buffer
	style         tcell.Style
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
	fillBox       *tview.Box
	// colorAt, when non-nil, returns a foreground color override for a rune offset.
	colorAt func(runeOff int) (tcell.Color, bool)
}

func (tv *TextView) IsRaw() bool {
	return false
}

func (tv *TextView) SetRect(x, y, w, h int) {
	tv.Resize(x, y, w, h)
}

func NewTextView(text string, x, y, w, h int, style tcell.Style, singleLine, scrollable bool) *TextView {
	_, bg, _ := style.Decompose()
	tv := &TextView{
		BaseView: BaseView{
			x: x, y: y, w: w, h: h,
		},
		buffer:      NewBuffer(text),
		style:       style,
		singleLine:  singleLine,
		scrollable:  scrollable,
		lastVersion: -1,
		tabWidth:    4,
		fillBox:     tview.NewBox(),
	}
	tv.fillBox.SetBackgroundColor(bg)
	tv.UpdateLayout()
	return tv
}

func (tv *TextView) runeWidth(r rune, visualPos int) int {
	if r == '\t' {
		return tv.tabWidth - (visualPos % tv.tabWidth)
	}
	w := uniseg.StringWidth(string(r))
	if w == 0 {
		return 0
	}
	return w
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
	colNum = max(0, min(colNum, len(tv.buffer.lines[lineNum])))

	tv.buffer.cursor = Cursor{colNum, lineNum}
	tv.buffer.ClearSelection()
	tv.UpdateLayout()
	// Find the visual line for this buffer line and scroll to it
	for i, vl := range tv.layout {
		if vl.BufferLine == lineNum {
			tv.scroll.Pos = i
			break
		}
	}
	tv.scroll.AutoScroll = true
	tv.SyncScroll()
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
	selStyle := tcell.StyleDefault.Background(tcell.ColorSilver).Foreground(tcell.ColorBlack)
	if tv.theme != nil {
		selStyle = tcell.StyleDefault.Background(tv.theme.SelectionBG).Foreground(tv.theme.SelectionFG)
	}

	vrow := 0
	for lidx := tv.scroll.Pos; lidx < len(tv.layout) && vrow < tv.h; lidx++ {
		vl, vcol := tv.layout[lidx], 0
		line := tv.buffer.lines[vl.BufferLine]
		lineStyle := tv.style
		if tv.underlineLast && lidx == len(tv.layout)-1 {
			lineStyle = lineStyle.Underline(true)
		}
		var lineRuneBase int
		if tv.colorAt != nil {
			lineRuneBase = tv.buffer.RuneOffsetOfPos(vl.BufferLine, vl.Start)
		}
		for idx := vl.Start; idx < vl.End && vcol < tv.w; idx++ {
			r, style := line[idx], lineStyle
			if tv.buffer.IsSelected(idx, vl.BufferLine) {
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
			if tv.buffer.IsSelected(vl.End, vl.BufferLine) {
				style = selStyle
			}
			s.SetContent(tv.x+vcol, tv.y+vrow, ' ', nil, style)
		}
		vrow++
	}
	if vrow < tv.h {
		tv.fillBox.SetRect(tv.x, tv.y+vrow, tv.w, tv.h-vrow)
		tv.fillBox.Draw(s)
	}
}

func (tv *TextView) GetClickWord(mx, my int) string {
	bx, by := tv.visualToBuffer(mx-tv.x, my-tv.y+tv.scroll.Pos)
	if tv.buffer.IsSelected(bx, by) {
		word := strings.TrimSpace(tv.buffer.GetSelectedText())
		if word != "" {
			return word
		}
	}
	return strings.TrimSpace(tv.buffer.GetWordAt(bx, by))
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
			if tv.typingStart != nil {
				tv.buffer.SetSelection(*tv.typingStart, tv.buffer.cursor)
				tv.typingStart = nil
			} else if tv.buffer.selection.Active {
				start, _ := tv.buffer.selection.Ordered()
				tv.buffer.cursor, tv.typingStart = start, nil
				tv.buffer.ClearSelection()
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

func (tv *TextView) LineCount() int {
	return len(tv.buffer.lines)
}

func (tv *TextView) GetLine(y int) []rune {
	if y < 0 || y >= len(tv.buffer.lines) {
		return nil
	}
	return tv.buffer.lines[y]
}

func (tv *TextView) Search(word string) int {
	line, sel, ok := Search(tv, word, tv.buffer.cursor)
	if ok {
		tv.buffer.cursor = sel.End
		tv.buffer.selection = sel
		return line
	}
	return -1
}

func (tv *TextView) ShowLineAt(lineNum int, vrow int) {
	tv.UpdateLayout()
	vidx := -1
	for i, vl := range tv.layout {
		if vl.BufferLine == lineNum {
			vidx = i
			break
		}
	}
	if vidx != -1 {
		tv.scroll.Pos = vidx - vrow
		tv.scroll.Clamp(len(tv.layout), tv.h)
	}
	tv.scroll.AutoScroll = true
	tv.SyncScroll()
}

type Window struct {
	ID     int
	tag    *TextView
	body   View
	parent *Column
	editor *Editor
	onExec func(*Column, *Window, string) bool

	isDir         bool
	hasVersion    bool
	savedVersion  int
	warnedVersion int

	// lk protects eventSubs, spans, addrQ0/Q1, and mutSeq/bodySnapSeq.
	// Held briefly by the UI thread (Draw, HandleEvent → onMutate) and by
	// 9P goroutines (file Open/Close). Never nested.
	lk        sync.Mutex
	eventSubs []*eventSub

	// current addr (rune offsets) for external tool use
	addrQ0, addrQ1 int

	// color spans applied during Draw
	spans            []colorSpan
	spansVersion     uint64
	lastSpansVersion uint64
	cachedSpans      []colorSpan
	handleBox        *tview.Box
	gutterBox        *tview.Box
	thumbBox         *tview.Box
	tagRowFlex       *tview.Flex
	bodyRowFlex      *tview.Flex
	winRootFlex      *tview.Flex

	// mutSeq is incremented on every body mutation.
	// bodySnapSeq is set to mutSeq when the body is snapped for a 9P read.
	// winColorFile.Close discards spans if mutSeq != bodySnapSeq.
	mutSeq, bodySnapSeq uint64
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

// hasEventSubs is safe to call from any goroutine.
func (win *Window) hasEventSubs() bool {
	win.lk.Lock()
	n := len(win.eventSubs)
	win.lk.Unlock()
	return n > 0
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
	win.spansVersion++
}

// colorAtFunc returns a closure that looks up a rune offset in the given spans.
func (win *Window) colorAtFunc(spans []colorSpan) func(int) (tcell.Color, bool) {
	theme := win.editor.theme
	return func(runeOff int) (tcell.Color, bool) {
		for _, sp := range spans {
			if runeOff >= sp.q0 && runeOff < sp.q1 {
				return theme.colorForAttr(sp.attr), true
			}
		}
		return 0, false
	}
}

func newWindow(tag string, parent *Column, editor *Editor, x, y, w, h int, onExec func(*Column, *Window, string) bool) *Window {
	tagStyle := tcell.StyleDefault.Background(editor.theme.TagBG).Foreground(editor.theme.TagFG)
	handleBox := tview.NewBox()
	gutterBox := tview.NewBox()
	gutterBox.SetBackgroundColor(editor.theme.ScrollGutter)
	thumbBox := tview.NewBox()

	tagRowFlex := tview.NewFlex()
	tagRowFlex.SetDirection(tview.FlexColumn)
	tagRowFlex.AddItem(handleBox, 1, 0)
	tagView := NewTextView(tag, 0, 0, 0, 0, tagStyle, false, false)
	tagRowFlex.AddItem(tagView, 0, 1)

	bodyRowFlex := tview.NewFlex()
	bodyRowFlex.SetDirection(tview.FlexColumn)
	bodyRowFlex.AddItem(gutterBox, 1, 0)

	winRootFlex := tview.NewFlex()
	winRootFlex.SetDirection(tview.FlexRow)
	winRootFlex.AddItem(tagRowFlex, 1, 0)
	winRootFlex.AddItem(bodyRowFlex, 0, 1)

	win := &Window{
		tag:    tagView,
		parent: parent, editor: editor, onExec: onExec,
		handleBox:   handleBox,
		gutterBox:   gutterBox,
		thumbBox:    thumbBox,
		tagRowFlex:  tagRowFlex,
		bodyRowFlex: bodyRowFlex,
		winRootFlex: winRootFlex,
	}
	win.tag.theme = &editor.theme
	// Reflow body geometry when tag text wraps to a different number of rows.
	// Runs on the UI thread (tag edits come via HandleEvent under win.lk).
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
	term, err := NewTermView(editor, sess, 0, 0, w-1, h-1, func() {
		editor.RemoveWindow(win)
	})
	if err != nil {
		sess.Close()
		return nil, err
	}
	win.body = term
	win.bodyRowFlex.AddItem(term, 0, 1)
	if pty, ok := sess.(*ExternalPTY); ok {
		pty.onResize = func(rows, cols int) {
			win.broadcastEvent('P', 'Z', rows, cols, 0, "")
		}
	}
	return win, nil
}

func NewWindow(tag, body string, parent *Column, editor *Editor, x, y, w, h int, onExec func(*Column, *Window, string) bool) *Window {
	bodyStyle := tcell.StyleDefault.Background(editor.theme.BodyBG).Foreground(editor.theme.BodyFG)
	win := newWindow(tag, parent, editor, x, y, w, h, onExec)
	tv := NewTextView(body, 0, 0, 0, 0, bodyStyle, false, true)
	tv.theme = &editor.theme
	win.body = tv
	win.bodyRowFlex.AddItem(tv, 0, 1)
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
	if !win.hasVersion {
		return false
	}
	if buf := win.body.GetBuffer(); buf != nil {
		return buf.version != win.savedVersion
	}
	return false
}

func (win *Window) Warned() bool {
	if buf := win.body.GetBuffer(); buf != nil {
		return win.warnedVersion == buf.version
	}
	return true
}

func (win *Window) Warn() {
	if buf := win.body.GetBuffer(); buf != nil {
		win.warnedVersion = buf.version
	}
}

func (win *Window) GetFilename() string {
	if len(win.tag.buffer.lines) == 0 {
		return ""
	}
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

func (win *Window) GetRect() (int, int, int, int) { return win.winRootFlex.GetRect() }
func (win *Window) SetRect(x, y, w, h int) {
	win.winRootFlex.SetRect(x, y, w, h)
	win.reflow()
}

func (win *Window) tagHeight() int {
	h := len(win.tag.layout)
	if h < 1 {
		return 1
	}
	return h
}

// reflow sizes the tag and body views to match the window's current geometry.
func (win *Window) reflow() {
	th := win.tagHeight()
	rx, ry, _, _ := win.GetRect()
	win.handleBox.SetRect(rx, ry, 1, th)
	win.winRootFlex.ResizeItem(win.tagRowFlex, th, 0)
	win.winRootFlex.Layout()
}

func (win *Window) Draw(s tcell.Screen) {
	win.tag.underlineLast = win.editor.active == win

	handleColor := win.editor.theme.Handle
	if fn := win.GetFilename(); isSpecial(fn) {
		handleColor = win.editor.theme.HandleError
	} else if win.IsDirty() {
		handleColor = win.editor.theme.HandleDirty
	}

	// Tag section under lock.
	win.handleBox.SetBackgroundColor(handleColor)

	win.lk.Lock()
	win.winRootFlex.Layout()
	win.tag.Layout()
	win.tagRowFlex.Draw(s)
	if win.spansVersion != win.lastSpansVersion {
		win.lastSpansVersion = win.spansVersion
		win.cachedSpans = append(win.cachedSpans[:0], win.spans...)
	}
	spansSnapshot := win.cachedSpans
	win.lk.Unlock()

	if tv, ok := win.body.(*TextView); ok {
		if len(spansSnapshot) > 0 {
			tv.colorAt = win.colorAtFunc(spansSnapshot)
		} else {
			tv.colorAt = nil
		}
	}

	win.lk.Lock()
	win.body.Layout()
	win.bodyRowFlex.Draw(s)
	scroll, total, visible := win.body.GetScroll()
	if visible > 0 && total > visible {
		thumbHeight := max(1, (visible*visible)/total)
		thumbStart := min(visible-thumbHeight, (scroll*visible)/total)
		rx, ry, _, _ := win.GetRect()
		th := win.tagHeight()
		win.thumbBox.SetBackgroundColor(win.editor.theme.ScrollThumb)
		win.thumbBox.SetRect(rx, ry+th+thumbStart, 1, thumbHeight)
		win.thumbBox.Draw(s)
	}
	win.lk.Unlock()
}

func (win *Window) HandleEvent(ev tcell.Event) bool {
	me, ok := ev.(*tcell.EventMouse)
	if !ok {
		return false
	}

	mx, my := me.Position()
	win.tag.UpdateLayout()
	th := win.tagHeight()
	rx, ry, _, _ := win.GetRect()

	// Gutter column: handle + scrollbar area.
	if mx == rx {
		if win.handleBox.InRect(mx, my) {
			return false
		}
		bodyTop := ry + th
		amount := my - bodyTop + 1
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
				newScroll := ((my - bodyTop) * total) / visible
				win.body.Scroll(newScroll - scroll)
			}
		}
		return false
	}

	target := win.body
	if my < ry+th {
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
		return win.onExec != nil && win.onExec(win.parent, win, word)
	}
	return win.editor.Plumb(win, word)
}
