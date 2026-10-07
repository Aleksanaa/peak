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
	PosAt(x, y int) int // the rune offset shown at (x, y)
	GetBuffer() *Buffer
	Scroll(n int)
	// AdvanceSweep moves the end of a sweep under way by one line in
	// direction dir, as the view scrolls under it.
	AdvanceSweep(dir int)
	GetScroll() (scroll, total, visible int)
	Show(q int) // scroll to show rune offset q
	IsRaw() bool
}

// A TextView is a frame whose text is typed and edited: a tag or a body.
type TextView struct {
	frame
	singleLine bool
	// The text typed since the cursor last moved starts at typedFrom and,
	// once Esc ends the typing, ends at typedTo; Esc selects it. Either is
	// -1 until then.
	typedFrom, typedTo int
}

func (tv *TextView) IsRaw() bool {
	return false
}

func NewTextView(text string, w, h int, theme *Theme, colors *colorPair, singleLine, scrollable bool) *TextView {
	return &TextView{
		frame:      newFrame(NewBuffer(text), w, h, theme, colors, scrollable),
		singleLine: singleLine,
		typedFrom:  -1,
		typedTo:    -1,
	}
}

// Layout scrolls to follow the cursor, which is shown where dot is empty.
func (tv *TextView) Layout() {
	if !tv.scrollable {
		tv.org = 0
	}
	tv.SyncScroll()
	tv.cursor, tv.cursorHidden = tv.buffer.q0, tv.buffer.q0 != tv.buffer.q1
}

// GotoLineCol puts the cursor at column col of line, or selects the line if
// col is negative, and shows it.
func (tv *TextView) GotoLineCol(line, col int) {
	b := tv.buffer
	line = max(0, min(line, len(b.lines)-1))
	if col < 0 {
		b.SetDot(b.Offset(line, 0), b.Offset(line, len(b.lines[line])))
	} else {
		b.moveTo(b.Offset(line, min(col, len(b.lines[line]))))
	}
	tv.Show(b.q0)
}

func (tv *TextView) Resize(w, h int) {
	tv.w, tv.h = w, h
}

func (tv *TextView) GetBuffer() *Buffer {
	return tv.buffer
}

// startTyping notes where the text about to be typed over dot starts.
func (tv *TextView) startTyping() {
	tv.typedTo = -1
	if tv.buffer.q0 < tv.buffer.q1 || tv.typedFrom < 0 {
		tv.typedFrom = tv.buffer.q0
	}
}

// selectTyped selects the text typed and shows it.
func (tv *TextView) selectTyped() {
	tv.buffer.SetDot(tv.typedFrom, tv.typedTo)
	tv.Show(tv.typedFrom)
}

// page scrolls to show visual line top first, keeping the cursor at q if
// that is still shown and moving it to the first line otherwise.
func (tv *TextView) page(top, q int) {
	tv.setTop(top)
	top = tv.top()
	if _, vrow := tv.visualOf(q); vrow < top || vrow >= top+tv.h {
		q = tv.PosAt(0, 0)
	}
	tv.buffer.moveTo(q)
}

func (tv *TextView) HandleEvent(ev tcell.Event) {
	b := tv.buffer
	switch ev := ev.(type) {
	case *tcell.EventKey:
		switch ev.Key() {
		case tcell.KeyEsc:
			switch {
			case tv.typedFrom >= 0 && tv.typedTo < 0:
				tv.typedTo = b.q0
				tv.selectTyped()
			case b.q0 < b.q1:
				b.moveTo(b.q0)
			case tv.typedFrom >= 0:
				tv.selectTyped()
			}
		case tcell.KeyCtrlZ:
			tv.typedFrom = -1
			if ev.Modifiers()&tcell.ModShift != 0 {
				b.Redo()
			} else {
				b.Undo()
			}
		case tcell.KeyCtrlY:
			tv.typedFrom = -1
			b.Redo()
		case tcell.KeyCtrlA:
			tv.typedFrom = -1
			b.SetDot(0, b.Len())
		case tcell.KeyCtrlC:
			b.Snarf()
		case tcell.KeyCtrlX:
			tv.typedFrom = -1
			b.Cut()
		case tcell.KeyCtrlV:
			tv.startTyping()
			b.Paste()
		case tcell.KeyCtrlU:
			tv.typedFrom = -1
			b.DeleteLine()
		case tcell.KeyCtrlW:
			tv.typedFrom = -1
			b.DeleteWordBefore()
		case tcell.KeyCtrlH, tcell.KeyBackspace:
			tv.startTyping()
			b.Backspace()
		case tcell.KeyDelete:
			tv.startTyping()
			b.Delete()
		case tcell.KeyPgUp:
			tv.typedFrom = -1
			tv.page(max(0, tv.top()-tv.h), b.q0)
		case tcell.KeyPgDn:
			tv.typedFrom = -1
			tv.page(max(0, min(len(tv.lines())-1, tv.top()+tv.h)), b.q1)
		case tcell.KeyUp:
			tv.typedFrom = -1
			b.MoveUp()
		case tcell.KeyDown:
			tv.typedFrom = -1
			b.MoveDown()
		case tcell.KeyLeft:
			tv.typedFrom = -1
			if ev.Modifiers()&tcell.ModCtrl != 0 {
				b.MoveWordLeft()
			} else {
				b.MoveLeft()
			}
		case tcell.KeyRight:
			tv.typedFrom = -1
			if ev.Modifiers()&tcell.ModCtrl != 0 {
				b.MoveWordRight()
			} else {
				b.MoveRight()
			}
		case tcell.KeyHome:
			tv.typedFrom = -1
			b.MoveHome()
		case tcell.KeyEnd:
			tv.typedFrom = -1
			b.MoveEnd()
		case tcell.KeyEnter:
			if !tv.singleLine {
				tv.startTyping()
				b.Insert("\n")
			}
		case tcell.KeyTab:
			tv.startTyping()
			b.Insert("\t")
		case tcell.KeyRune:
			tv.startTyping()
			b.Insert(ev.Str())
		}
		tv.autoScroll = true
		_, vrow := tv.visualOf(b.q0)
		if top := tv.top(); vrow < top {
			tv.setTop(vrow)
		} else if vrow >= top+tv.h {
			tv.setTop(vrow - tv.h + 1)
		}
	case *tcell.EventMouse:
		if ev.Buttons() != tcell.ButtonNone {
			tv.typedFrom = -1
		}
		tv.mouse(ev)
	}
}

func (tv *TextView) SyncScroll() {
	if !tv.scrollable || !tv.autoScroll {
		return
	}
	_, vrow := tv.visualOf(tv.buffer.q0)
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
		runeOff := tv.buffer.Offset(line, col)
		if runeOff < lastOff {
			i = 0
		}
		lastOff = runeOff

		for i < len(spans) && spans[i].q1 <= runeOff {
			i++
		}
		if i < len(spans) && spans[i].q0 <= runeOff && runeOff < spans[i].q1 {
			return s.Foreground(theme.Syn[spans[i].attr])
		}
		return s
	}
}

// newWindow returns a window in col with tag, for its body to be set.
func newWindow(tag string, col *Column) *Window {
	theme := &col.editor.theme
	win := &Window{
		rect:   rect{w: col.w},
		tag:    NewTextView(tag, col.w-1, 1, theme, &theme.Tag, false, false),
		parent: col, editor: col.editor,
	}
	win.tag.fit(col.w - 1)
	win.tag.buffer.onMutate = func(_, _, _ int, _ string) {
		h := win.tag.h
		win.tag.fit(win.w - 1)
		if win.tag.h != h {
			win.reflow()
		}
	}
	return win
}

// newTextWindow returns a window in col holding body as text.
func newTextWindow(tag, body string, col *Column) *Window {
	win := newWindow(tag, col)
	theme := &col.editor.theme
	tv := NewTextView(body, col.w-1, 0, theme, &theme.Body, false, true)
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

// newTermWindow returns a window in col with a terminal on sess, which
// runs cmd. It closes sess if the terminal cannot start.
func newTermWindow(tag, cmd string, sess session.Session, col *Column) (*Window, error) {
	win := newWindow(tag, col)
	editor := col.editor
	term, err := NewTermView(editor, sess, func() {
		editor.RemoveWindow(win)
	})
	if err != nil {
		sess.Close()
		return nil, err
	}
	term.cmd = cmd
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
