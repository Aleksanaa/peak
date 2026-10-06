package main

import (
	"path/filepath"
	"strings"

	"github.com/aleksana/peak/internal/quote"
	"github.com/aleksana/peak/internal/session"
	"github.com/aleksana/peak/internal/wevent"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

type WinKind int

const (
	WinFile WinKind = iota // regular file (editable, tracks dirty)
	WinDir                 // directory listing
	WinOut                 // output/error sink
	WinTerm                // terminal emulator
)

// A View is a tag or body. It knows only its own size: positions it is given,
// in mouse events and PosAt, are in its own coordinates, and it draws
// on a canvas of its own.
type View interface {
	// Layout readies the view for Draw, as by scrolling to follow the
	// cursor. It is called once per frame and must not paint anything.
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

// A TextView is a frame whose text is typed and edited: a tag or a body.
type TextView struct {
	frame
	singleLine  bool
	typingStart *Cursor
	typingEnd   *Cursor
}

func (tv *TextView) IsRaw() bool {
	return false
}

func NewTextView(text string, w, h int, theme *Theme, colors *colorPair, singleLine, scrollable bool) *TextView {
	return &TextView{
		frame:      newFrame(NewBuffer(text), w, h, theme, colors, scrollable),
		singleLine: singleLine,
	}
}

func (tv *TextView) Layout() {
	if !tv.scrollable {
		tv.org = 0
	}
	tv.SyncScroll()
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

func (tv *TextView) ShowLineAt(n int) {
	tv.showLine(n)
}

func (tv *TextView) Resize(w, h int) {
	tv.w, tv.h = w, h
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
			top := max(0, tv.top()-tv.h)
			tv.setTop(top)
			_, vrow := tv.bufferToVisual(tv.buffer.cursor.x, tv.buffer.cursor.y)
			if vrow >= top+tv.h {
				bx, by := tv.visualToBuffer(0, top)
				tv.buffer.cursor = Cursor{bx, by}
			}
		case tcell.KeyPgDn:
			tv.typingStart = nil
			tv.buffer.ClearSelection()
			top := max(0, min(len(tv.lines())-1, tv.top()+tv.h))
			tv.setTop(top)
			_, vrow := tv.bufferToVisual(tv.buffer.cursor.x, tv.buffer.cursor.y)
			if vrow < top {
				bx, by := tv.visualToBuffer(0, top)
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
		tv.autoScroll = true
		_, vrow := tv.bufferToVisual(tv.buffer.cursor.x, tv.buffer.cursor.y)
		if top := tv.top(); vrow < top {
			tv.setTop(vrow)
		} else if vrow >= top+tv.h {
			tv.setTop(vrow - tv.h + 1)
		}
	case *tcell.EventMouse:
		if ev.Buttons() != tcell.ButtonNone {
			tv.typingStart = nil
		}
		tv.mouse(ev)
	}
}

func (tv *TextView) SyncScroll() {
	if !tv.scrollable || !tv.autoScroll {
		return
	}
	_, vrow := tv.bufferToVisual(tv.buffer.cursor.x, tv.buffer.cursor.y)
	if vrow >= tv.top()+tv.h {
		tv.setTop(vrow - tv.h + 1)
	}
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
		tv.styleAt = nil
		if len(w.spans) > 0 {
			tv.styleAt = w.spanStyle(tv)
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
func (win *Window) broadcastEvent(origin, typ byte, q0, q1 int, text string) {
	record := wevent.Format(wevent.Event{Origin: origin, Type: typ, Q0: q0, Q1: q1, Text: text})
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

// spanStyle returns a styleAt for tv, the window's body, that colors the
// window's spans.
func (win *Window) spanStyle(tv *TextView) func(line, col int, s tcell.Style) tcell.Style {
	spans := win.spans
	theme := win.editor.theme
	i := 0
	lastOff := -1
	return func(line, col int, s tcell.Style) tcell.Style {
		runeOff := tv.buffer.RuneOffsetOfPos(line, col)
		if runeOff < lastOff {
			i = 0
		}
		lastOff = runeOff

		for i < len(spans) && spans[i].q1 <= runeOff {
			i++
		}
		if i < len(spans) && spans[i].q0 <= runeOff && runeOff < spans[i].q1 {
			return s.Foreground(theme.colorForAttr(spans[i].attr))
		}
		return s
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
		tv.org = adjustPoint(tv.org, q0, q1Old, q1New)
		if q1Old > q0 {
			win.broadcastEvent('K', 'D', q0, q1Old, "")
		}
		if text != "" {
			win.broadcastEvent('K', 'I', q0, q1New, text)
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
