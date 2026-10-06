package main

import (
	"path/filepath"
	"strings"

	"unicode"

	"github.com/aleksana/peak/internal/quote"
	"github.com/aleksana/peak/internal/session"
	"github.com/aleksana/peak/internal/wevent"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
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

// A View is a tag or body. It knows only its own size: positions it is given,
// in mouse events and PosAt, are in its own coordinates, and it draws
// on a canvas of its own.
type View interface {
	// Layout computes visual-line wrapping and synchronises scroll position.
	// Must be called once per frame before Draw, and after every Resize.
	// Must not paint anything.
	Layout()
	Draw(canvas)
	ShowCursor(canvas)
	Resize(w, h int)
	HandleEvent(tcell.Event)
	PosAt(x, y int) Cursor // the buffer position shown at (x, y)
	GetBuffer() *Buffer
	Scroll(n int)
	// AdvanceDragCursor moves the end of a sweep under way by one line in
	// direction dir, as the view scrolls under it.
	AdvanceDragCursor(dir int)
	GetScroll() (scroll, total, visible int)
	Search(word string) int
	ShowLineAt(lineNum int)
	IsRaw() bool
}

type TextView struct {
	w, h          int
	scroll        ScrollState
	buffer        *Buffer
	drag          bool
	singleLine    bool
	scrollable    bool
	underlineLast bool
	layout        []VisualLine
	lastWidth     int
	lastVersion   int
	theme         *Theme
	colors        *colorPair // points into theme, so theme changes apply
	tabWidth      int
	typingStart   *Cursor
	typingEnd     *Cursor
	// colorAt, when non-nil, returns a foreground color override for a rune offset.
	colorAt func(runeOff int) (tcell.Color, bool)
}

func (tv *TextView) IsRaw() bool {
	return false
}

func NewTextView(text string, w, h int, theme *Theme, colors *colorPair, singleLine, scrollable bool) *TextView {
	tv := &TextView{
		w: w, h: h,
		buffer:      NewBuffer(text),
		theme:       theme,
		colors:      colors,
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

func (tv *TextView) Draw(cv canvas) {
	selStyle := tv.theme.Selection.style()
	vrow := 0
	for lidx := tv.scroll.Pos; lidx < len(tv.layout) && vrow < tv.h; lidx++ {
		vl, vcol := tv.layout[lidx], 0
		line := tv.buffer.lines[vl.BufferLine]
		lineStyle := tv.colors.style()
		if tv.underlineLast && lidx == len(tv.layout)-1 {
			lineStyle = lineStyle.Underline(true)
		}
		var lineRuneBase int
		if tv.colorAt != nil {
			lineRuneBase = tv.buffer.RuneOffsetOfPos(vl.BufferLine, vl.Start)
		}
		for idx := vl.Start; idx < vl.End; idx++ {
			r, style := line[idx], lineStyle
			if tv.buffer.selection.Contains(idx, vl.BufferLine, false) {
				style = selStyle
			} else if tv.colorAt != nil {
				if c, ok := tv.colorAt(lineRuneBase + idx - vl.Start); ok {
					style = style.Foreground(c)
				}
			}

			width := tv.runeWidth(r, vcol)
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
		if tv.buffer.selection.Contains(vl.End, vl.BufferLine, false) {
			eolStyle = selStyle
		}
		cv.fill(rect{vcol, vrow, tv.w - vcol, 1}, eolStyle)
		vrow++
	}
	cv.fill(rect{0, vrow, tv.w, tv.h - vrow}, tv.colors.style())
}

func (tv *TextView) PosAt(x, y int) Cursor {
	bx, by := tv.visualToBuffer(x, y+tv.scroll.Pos)
	return Cursor{bx, by}
}

func (tv *TextView) ShowCursor(cv canvas) {
	vx, vrow := tv.bufferToVisual(tv.buffer.cursor.x, tv.buffer.cursor.y)
	cv.showCursor(max(0, min(vx, tv.w-1)), vrow-tv.scroll.Pos)
}

// fit gives tv width w and the height its text needs at that width.
func (tv *TextView) fit(w int) {
	tv.w = w
	tv.UpdateLayout()
	tv.h = max(1, len(tv.layout))
}

func (tv *TextView) Resize(w, h int) {
	if tv.w == w && tv.h == h {
		return
	}
	tv.w, tv.h = w, h
	tv.UpdateLayout()
}

func (tv *TextView) GetBuffer() *Buffer {
	return tv.buffer
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

func (tv *TextView) HandleEvent(ev tcell.Event) {
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
		case tcell.KeyCtrlH, tcell.KeyBackspace:
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
			for _, r := range ev.Str() {
				tv.buffer.Insert(r)
			}
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
	case *tcell.EventMouse:
		buttons := ev.Buttons()
		if buttons != tcell.ButtonNone {
			tv.typingStart = nil
		}
		if tv.scrollable {
			if buttons&tcell.WheelUp != 0 {
				tv.Scroll(-1)
				return
			}
			if buttons&tcell.WheelDown != 0 {
				tv.Scroll(1)
				return
			}
		}
		mx, my := ev.Position()
		if buttons != tcell.ButtonNone {
			p := tv.PosAt(mx, my)
			if buttons == tcell.ButtonPrimary && !tv.drag {
				tv.buffer.ClearSelection()
			}
			if buttons == tcell.ButtonPrimary {
				if !tv.drag {
					tv.drag, tv.buffer.cursor = true, p
					tv.buffer.SetSelection(p, p)
				} else {
					tv.buffer.cursor, tv.buffer.selection.End = p, p
				}
			} else if !tv.buffer.selection.Active {
				tv.buffer.cursor = p
			}
		} else {
			tv.drag = false
			if tv.buffer.selection.Active && tv.buffer.selection.Start == tv.buffer.selection.End {
				tv.buffer.ClearSelection()
			}
		}
	}
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

type Window struct {
	rect           // in the column
	ID             int
	tag            *TextView
	body           View
	parent         *Column
	editor         *Editor
	explicitHeight int

	kind          WinKind
	writable      bool
	savedVersion  int
	warnedVersion int

	eventSubs []*eventSub

	addrQ0, addrQ1 int

	spans []colorSpan
}

func (w *Window) PreferredSize() int { return w.explicitHeight }
func (w *Window) MinSize() int       { return w.tag.h }

// The handle and the scroll bar share the window's first column, beside the
// tag and the body.
func (w *Window) tagRect() rect  { return rect{1, 0, w.w - 1, w.tag.h} }
func (w *Window) bodyRect() rect { return rect{1, w.tag.h, w.w - 1, max(0, w.h-w.tag.h)} }

func (w *Window) Draw(cv canvas) {
	theme := &w.editor.theme
	handle := theme.Handle
	switch w.kind {
	case WinOut, WinTerm:
		handle = theme.HandleError
	case WinFile:
		if w.IsDirty() {
			handle = theme.HandleDirty
		} else if w.writable {
			handle = theme.HandleWritable
		} else {
			handle = theme.HandleUnwritable
		}
	}
	cv.fill(rect{0, 0, 1, w.tag.h}, tcell.StyleDefault.Background(handle).Foreground(color.Black))

	w.tag.underlineLast = w.editor.active == w
	w.editor.drawView(w.tag, cv.sub(w.tagRect()))

	if tv, ok := w.body.(*TextView); ok {
		tv.colorAt = nil
		if len(w.spans) > 0 {
			tv.colorAt = w.colorAtFunc()
		}
	}
	w.editor.drawView(w.body, cv.sub(w.bodyRect()))

	if scroll, total, visible := w.body.GetScroll(); visible > 0 && total > visible {
		thumb := max(1, visible*visible/total)
		at := min(visible-thumb, scroll*visible/total)
		cv.fill(rect{0, w.tag.h + at, 1, thumb}, tcell.StyleDefault.Background(theme.ScrollThumb))
	}
}

// broadcastEvent delivers a counted event record to all open event file subscribers.
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
// mutation [q0, q1Old) → [q0, q1New).
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

// colorAtFunc returns a closure that looks up a rune offset in the window's spans.
func (win *Window) colorAtFunc() func(int) (tcell.Color, bool) {
	spans := win.spans
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

// newWindow returns a window of width w, to be placed by its column.
func newWindow(tag string, parent *Column, editor *Editor, w int) *Window {
	win := &Window{
		rect:   rect{w: w},
		tag:    NewTextView(tag, w-1, 1, &editor.theme, &editor.theme.Tag, false, false),
		parent: parent, editor: editor,
	}
	win.tag.fit(w - 1)
	win.tag.buffer.onMutate = func(_, _, _ int, _ string) {
		h := win.tag.h
		win.tag.fit(win.w - 1)
		if win.tag.h != h {
			win.reflow()
		}
	}
	return win
}

func NewTermWindow(tag string, parent *Column, editor *Editor, w int, cmd, dir string) (*Window, error) {
	sess, err := session.NewLocal(cmd, dir)
	if err != nil {
		return nil, err
	}
	win, err := newTermWindowFromSession(tag, sess, parent, editor, w)
	if err != nil {
		return nil, err
	}
	win.body.(*TermView).cmd = cmd
	return win, nil
}

func newTermWindowFromSession(tag string, sess session.Session, parent *Column, editor *Editor, w int) (*Window, error) {
	win := newWindow(tag, parent, editor, w)
	term, err := NewTermView(editor, sess, func() {
		editor.RemoveWindow(win)
	})
	if err != nil {
		sess.Close()
		return nil, err
	}
	win.kind = WinTerm
	win.body = term
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
			if _, err := ns.Stat(newPath); err == nil {
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

func NewWindow(tag, body string, parent *Column, editor *Editor, w int) *Window {
	win := newWindow(tag, parent, editor, w)
	tv := NewTextView(body, w-1, 0, &editor.theme, &editor.theme.Body, false, true)
	win.body = tv
	tv.buffer.onMutate = func(q0, q1Old, q1New int, text string) {
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

// Close releases resources owned by the window's body. For a terminal it
// hangs up the session, which ends the child process and the parse goroutine.
func (win *Window) Close() {
	if tv, ok := win.body.(*TermView); ok {
		tv.Close()
	}
}

func (win *Window) bodyTextView() *TextView {
	if tv, ok := win.body.(*TextView); ok {
		return tv
	}
	return nil
}

// loaded records that the body now holds exactly what is on disk.
func (win *Window) loaded(isDir, writable bool) {
	win.kind = WinFile
	if isDir {
		win.kind = WinDir
	}
	win.writable = writable
	win.markSaved(win.body.GetBuffer().version)
}

// markSaved records that the body at version matches the file on disk; -1
// means no version does. Saving also settles any unsaved-changes warning.
func (win *Window) markSaved(version int) {
	win.savedVersion, win.warnedVersion = version, version
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

// tagText is the tag of a window named name, followed by commands. The name
// is the tag's first field, quoted if it contains spaces.
func tagText(name, commands string) string {
	return " " + quote.Quote(name) + " " + commands + " "
}

func (win *Window) GetFilename() string {
	name, _ := quote.Cut(string(win.tag.buffer.lines[0]))
	return name
}

func (win *Window) GetDir() string {
	return getPathDir(win.GetFilename())
}

func (win *Window) SetName(name string) {
	tag := win.tag.buffer.GetText()
	if strings.TrimSpace(tag) == "" {
		win.tag.buffer.SetText(tagText(name, "Get Put Del"))
		return
	}
	_, rest := quote.Cut(tag) // the rest of the tag is kept as typed
	win.tag.buffer.SetText(" " + quote.Quote(name) + rest)
}

func (win *Window) reflow() {
	win.tag.fit(win.w - 1)
	r := win.bodyRect()
	win.body.Resize(r.w, r.h)
}

// Resize places the window at r in its column.
func (win *Window) Resize(r rect) {
	win.rect = r
	win.reflow()
}
