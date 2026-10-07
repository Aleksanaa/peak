package main

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/aleksana/peak/internal/session"
	terminal "github.com/aleksana/peak/peak/term"
	"github.com/atotto/clipboard"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

// historyLines is how many lines of history a terminal keeps.
const historyLines = 1000

// termColors are a terminal's own colors: the host terminal's defaults.
var termColors = colorPair{BG: color.Default, FG: color.Default}

// A TermView is a terminal: a frame showing what a program writes. Its last
// lines are the screen, which the emulator keeps and the program rewrites at
// will; the lines above are history, rows that have scrolled off the screen,
// which only grows. Showing, scrolling, selecting and searching it are the
// frame's.
type TermView struct {
	frame
	state       terminal.State
	vt          *terminal.VT
	session     session.Session
	closed      bool
	onClose     func()
	editor      *Editor
	cancel      context.CancelFunc
	lastMX      int
	lastMY      int
	lastButtons tcell.ButtonMask

	hist      []termLine      // the history: lines that left the screen
	screenTop int             // the rune offset the screen starts at
	styles    [][]tcell.Style // the style of each rune of each line
	scrolled  []termLine      // rows that left the screen since sync; under the state lock
	changed   atomic.Bool     // the emulator changed since sync; set by the parse goroutine

	cmd          string       // command used to start the terminal, for session save
	OnCWD        func(string) // called on main goroutine with decoded absolute path; set by owner
	onCWDStarted bool         // true once OnCWD has been called at least once
}

// termLine is a row of the screen, or a line of history, as text: a rune for
// each character, with its style.
type termLine struct {
	text    []rune
	styles  []tcell.Style
	wrapped bool // it goes on in the next row
}

func (tv *TermView) IsRaw() bool {
	tv.state.Lock()
	defer tv.state.Unlock()
	return tv.state.Mode(terminal.ModeAltScreen)
}

// NewTermView returns a terminal running sess, sized by its first Resize.
func NewTermView(editor *Editor, sess session.Session, onClose func()) (*TermView, error) {
	ctx, cancel := context.WithCancel(context.Background())
	tv := &TermView{
		frame:   newFrame(NewBuffer(""), 0, 0, &editor.theme, &termColors, true),
		session: sess,
		onClose: onClose,
		editor:  editor,
		cancel:  cancel,
	}
	tv.autoScroll = true
	tv.styleAt = func(line, col int, _ tcell.Style) tcell.Style { return tv.styles[line][col] }
	tv.changed.Store(true)

	vt, err := terminal.Create(&tv.state, sess)
	if err != nil {
		cancel()
		return nil, err
	}
	tv.state.ResponseWriter = sess
	if h := editor.theme.Body.FG.Hex(); h >= 0 {
		tv.state.FGColor = terminal.RGB(uint8(h>>16), uint8(h>>8), uint8(h))
	}
	if h := editor.theme.Body.BG.Hex(); h >= 0 {
		tv.state.BGColor = terminal.RGB(uint8(h>>16), uint8(h>>8), uint8(h))
	}
	tv.vt = vt
	tv.state.OnScrollOut = func(y int) {
		l, _ := tv.readRow(y, -1)
		tv.scrolled = append(tv.scrolled, l)
	}
	tv.state.OnCWD = func(uri string) {
		var path string
		switch {
		case strings.HasPrefix(uri, "file://"):
			u, err := url.Parse(uri)
			if err != nil || u.Path == "" {
				return
			}
			path = u.Path
		case strings.HasPrefix(uri, "/"):
			var err error
			if path, err = url.PathUnescape(uri); err != nil {
				return
			}
		default:
			return
		}
		select {
		case tv.editor.callCh <- func() {
			if tv.OnCWD != nil {
				tv.onCWDStarted = true
				tv.OnCWD(path)
			}
		}:
		default:
		}
	}

	go func() {
		defer cancel()
		for {
			err := tv.vt.Parse()
			if err != nil {
				// Only call onClose if we weren't explicitly closed via Close().
				// If ctx is already done, Close() was called first — the window
				// is already being removed, so onClose would double-delete it.
				select {
				case <-ctx.Done():
					return
				default:
				}
				tv.state.Lock()
				tv.closed = true
				tv.state.Unlock()
				tv.editor.Call(tv.onClose)
				return
			}
			tv.changed.Store(true)
			tv.editor.Redraw()
		}
	}()

	return tv, nil
}

// readRow returns screen row y as text, and the column in it of cell x. A
// row that wraps onto the next is text up to its last cell written; any
// other loses the blank cells that end it. Either keeps the cells before x.
// Called with the state locked.
func (tv *TermView) readRow(y, x int) (l termLine, col int) {
	cols, _ := tv.state.Size()
	written, shown, cont := 0, 0, false
	for i := range cols {
		c, fg, bg, mode := tv.state.Cell(i, y)
		if i == x {
			col = len(l.text)
		}
		if c == 0 && cont { // the second cell of a wide character
			cont = false
			continue
		}
		cont = wide(c)
		l.wrapped = mode&terminal.AttrWrap != 0 // the row's last cell says
		r := c
		if r == 0 { // never written
			r = ' '
		}
		l.text = append(l.text, r)
		l.styles = append(l.styles, cellStyle(fg, bg, mode))
		if c != 0 || i < x {
			written = len(l.text)
		}
		if (c != 0 && c != ' ') || bg != terminal.DefaultBG || i < x {
			shown = len(l.text)
		}
	}
	if x >= cols {
		col = len(l.text)
	}
	keep := shown
	if l.wrapped {
		keep = written
	}
	l.text, l.styles = l.text[:keep], l.styles[:keep]
	return l, col
}

func cellStyle(fg, bg terminal.Color, mode int16) tcell.Style {
	style := tcell.StyleDefault.Foreground(termColor(fg)).Background(termColor(bg))
	if mode&terminal.AttrUnderline != 0 {
		style = style.Underline(true)
	}
	if mode&terminal.AttrBold != 0 {
		style = style.Bold(true)
	}
	if mode&terminal.AttrItalic != 0 {
		style = style.Italic(true)
	}
	if mode&terminal.AttrBlink != 0 {
		style = style.Blink(true)
	}
	return style
}

func termColor(c terminal.Color) tcell.Color {
	if c == terminal.DefaultFG || c == terminal.DefaultBG {
		return color.Default
	}
	if c.IsRGB() {
		r, g, b := c.RGBComponents()
		return color.NewRGBColor(int32(r), int32(g), int32(b))
	}
	return color.PaletteColor(int(c))
}

// sync brings the text up to date with the emulator: rows that left the
// screen join the history, the screen's rows follow it, and the cursor is
// the terminal's. A line keeps its number as it scrolls into history, so
// what refers to it stays put.
func (tv *TermView) sync() {
	if !tv.changed.Swap(false) {
		return
	}
	tv.state.Lock()
	scrolled := tv.scrolled
	tv.scrolled = nil
	_, rows := tv.state.Size()
	cx, cy := tv.state.Cursor()
	screen := make([]termLine, rows)
	col := 0
	for y := range screen {
		x := -1
		if y == cy {
			x = cx
		}
		var c int
		if screen[y], c = tv.readRow(y, x); y == cy {
			col = c
		}
	}
	tv.cursorHidden = !tv.state.CursorVisible()
	tv.state.Unlock()

	// A row that wraps goes on in the next: a line is all of its rows.
	for _, r := range scrolled {
		if n := len(tv.hist); n > 0 && tv.hist[n-1].wrapped {
			last := &tv.hist[n-1]
			last.text = append(last.text, r.text...)
			last.styles = append(last.styles, r.styles...)
			last.wrapped = r.wrapped
		} else {
			tv.hist = append(tv.hist, r)
		}
	}
	if n := len(tv.hist) - historyLines; n > 0 {
		tv.forget(n)
	}

	b := tv.buffer
	lines, styles := b.lines[:0], tv.styles[:0]
	for _, l := range tv.hist {
		lines, styles = append(lines, l.text), append(styles, l.styles)
	}
	join := len(tv.hist) > 0 && tv.hist[len(tv.hist)-1].wrapped
	type at struct{ line, col int }
	starts := make([]at, len(screen)) // where each row starts in the text
	for y, l := range screen {
		if join {
			i := len(lines) - 1
			starts[y] = at{i, len(lines[i])}
			lines[i] = slices.Concat(lines[i], l.text)
			styles[i] = slices.Concat(styles[i], l.styles)
		} else {
			starts[y] = at{len(lines), 0}
			lines, styles = append(lines, l.text), append(styles, l.styles)
		}
		join = l.wrapped
	}
	b.lines, tv.styles = lines, styles
	b.bumpVersion()
	tv.screenTop = b.Offset(starts[0].line, starts[0].col)
	tv.cursor = b.Offset(starts[cy].line, starts[cy].col) + col
}

// forget drops the oldest n lines of history. What refers to the lines left
// moves with them.
func (tv *TermView) forget(n int) {
	b := tv.buffer
	off := 0
	for _, l := range tv.hist[:n] {
		off += len(l.text) + 1
	}
	tv.hist = slices.Delete(tv.hist, 0, n)
	tv.org = max(0, tv.org-off)
	tv.anchor = max(0, tv.anchor-off)
	b.q0, b.q1 = max(0, b.q0-off), max(0, b.q1-off)
}

// Layout follows the screen, unless the view was scrolled away or a sweep
// is under way.
func (tv *TermView) Layout() {
	tv.sync()
	if tv.autoScroll && !tv.drag {
		_, top := tv.visualOf(tv.screenTop)
		tv.setTop(top)
	}
}

// ShowLineAt shows line n, and stops following the screen if that moved the
// view, so that it stays on n.
func (tv *TermView) ShowLineAt(n int) {
	if tv.showLine(n) {
		tv.autoScroll = false
	}
}

func (tv *TermView) GetBuffer() *Buffer {
	tv.sync()
	return tv.buffer
}

func (tv *TermView) Resize(w, h int) {
	if tv.w == w && tv.h == h {
		return
	}
	tv.w, tv.h = w, h
	tv.vt.Resize(w, h)
	// Tell the process the visible size.
	tv.session.Resize(h, w)
	tv.changed.Store(true)
}

func (tv *TermView) HandleEvent(ev tcell.Event) {
	tv.state.Lock()
	closed := tv.closed
	isMouseMode := tv.state.Mode(terminal.ModeMouseMask)
	sgrMode := tv.state.Mode(terminal.ModeMouseSgr)
	isAlt := tv.state.Mode(terminal.ModeAltScreen)
	tv.state.Unlock()

	if closed {
		return
	}

	switch e := ev.(type) {
	case *tcell.EventKey:
		tv.autoScroll = true
		mod := e.Modifiers()
		if mod&(tcell.ModAlt|tcell.ModMeta) != 0 {
			key := e.Key()
			// tcell v3 reports Ctrl+<letter> as KeyRune plus ModCtrl (the
			// legacy KeyCtrlX constants are only emitted when Ctrl is the
			// sole modifier), so match the letter here instead.
			if mod&tcell.ModCtrl != 0 && key == tcell.KeyRune {
				switch e.Str() {
				case "c", "C", "x", "X":
					tv.Snarf()
					return
				case "v", "V":
					tv.Paste()
					return
				case "f", "F":
					if tv.GetBuffer().GetSelectedText() != "" {
						tv.editor.Execute(nil, nil, "Look")
					}
					return
				}
			}

			switch key {
			case tcell.KeyEsc:
				tv.buffer.moveTo(tv.buffer.q0)
				return
			case tcell.KeyPgUp:
				tv.Scroll(-tv.h)
				return
			case tcell.KeyPgDn:
				tv.Scroll(tv.h)
				return
			}
		}
		if !tv.onCWDStarted {
			tv.OnCWD = nil
		}
		tv.session.Write([]byte(keyToEscSeq(e)))
	case *tcell.EventMouse:
		rx, ry := e.Position()
		buttons := e.Buttons()
		mod := e.Modifiers()
		defer func() { tv.lastMX, tv.lastMY, tv.lastButtons = rx, ry, buttons }()

		if buttons&(tcell.WheelUp|tcell.WheelDown) != 0 {
			if !isAlt {
				tv.mouse(e)
				return
			}
			seq := "\x1b[A"
			if buttons&tcell.WheelDown != 0 {
				seq = "\x1b[B"
			}
			if isMouseMode && sgrMode {
				btn := 64
				if buttons&tcell.WheelDown != 0 {
					btn = 65
				}
				seq = tv.encodeSGR(btn, rx, ry, false, false, mod)
			} else {
				seq = seq + seq + seq
			}
			tv.session.Write([]byte(seq))
			return
		}

		// A program tracking the mouse gets it, unless Ctrl is held or a
		// selection is under way; otherwise the mouse selects text.
		if !isMouseMode || mod&tcell.ModCtrl != 0 || tv.drag {
			tv.mouse(e)
			return
		}
		motion := rx != tv.lastMX || ry != tv.lastMY
		handled := false
		isMotion, isRelease := false, false
		btnReport := 0

		if code, release, changed := mouseButtonChange(tv.lastButtons, buttons); changed {
			handled = true
			btnReport, isRelease = code, release
		} else if motion {
			tv.state.Lock()
			motionMode := tv.state.Mode(terminal.ModeMouseMotion | terminal.ModeMouseMany)
			manyMode := tv.state.Mode(terminal.ModeMouseMany)
			tv.state.Unlock()

			if buttons != tcell.ButtonNone && motionMode {
				btnReport = sgrButton(buttons)
				isMotion, handled = true, true
			} else if manyMode {
				btnReport, isMotion, handled = 3, true, true
			}
		}

		if handled && sgrMode && rx >= 0 && rx < tv.w && ry >= 0 && ry < tv.h {
			esc := tv.encodeSGR(btnReport, rx, ry, isMotion, isRelease, mod)
			tv.session.Write([]byte(esc))
		}

		if buttons&tcell.ButtonPrimary != 0 {
			tv.buffer.moveTo(tv.buffer.q0)
		}
	}
}

// sgrButton maps a mouse-button mask to its SGR/X10 report code (primary 0,
// middle 1, secondary 2). When several bits are set, middle and secondary win
// over primary, so a chord's second button is the one reported.
func sgrButton(mask tcell.ButtonMask) int {
	switch {
	case mask&tcell.ButtonMiddle != 0:
		return 1
	case mask&tcell.ButtonSecondary != 0:
		return 2
	default:
		return 0
	}
}

// mouseButtonChange reports how a button-mask transition should be forwarded to
// a child program: the SGR code of the button that changed, whether it was a
// release, and whether anything changed at all. It reports the newly pressed or
// released button rather than the highest-priority one still held, so a chord's
// second button (and its later release) is encoded correctly.
func mouseButtonChange(prev, cur tcell.ButtonMask) (code int, release, changed bool) {
	if prev == cur {
		return 0, false, false
	}
	if pressed := cur &^ prev; pressed != 0 {
		return sgrButton(pressed), false, true
	}
	return sgrButton(prev &^ cur), true, true
}

func (tv *TermView) encodeSGR(btn, x, y int, motion, release bool, mod tcell.ModMask) string {
	b := btn
	if motion {
		b += 32
	}
	if mod&tcell.ModShift != 0 {
		b += 4
	}
	if mod&tcell.ModAlt != 0 {
		b += 8
	}
	if mod&tcell.ModCtrl != 0 {
		b += 16
	}

	suffix := "M"
	if release {
		suffix = "m"
	}
	return fmt.Sprintf("\x1b[<%d;%d;%d%s", b, x+1, y+1, suffix)
}

// Close is called on the main goroutine. Cancelling is synchronous so the
// parse goroutine never reports a close for a window already being removed;
// closing the session is not, since for a remote session it is several
// blocking 9P round trips that must not stall the UI.
func (tv *TermView) Close() {
	tv.cancel()
	go tv.vt.Close()
}

func (tv *TermView) Snarf() {
	if text := tv.GetBuffer().GetSelectedText(); text != "" {
		go clipboard.WriteAll(text)
	}
}

func (tv *TermView) Paste() {
	text, _ := clipboard.ReadAll()
	if text != "" {
		if !tv.onCWDStarted {
			tv.OnCWD = nil
		}
		tv.session.Write([]byte(text))
	}
}

func keyToEscSeq(e *tcell.EventKey) string {
	if e.Key() == tcell.KeyRune {
		return e.Str()
	}
	if e.Key() >= tcell.KeyCtrlA && e.Key() <= tcell.KeyCtrlZ {
		return string([]byte{byte(e.Key() - tcell.KeyCtrlA + 1)})
	}
	switch e.Key() {
	case tcell.KeyEnter:
		return "\r"
	case tcell.KeyTab:
		return "\t"
	case tcell.KeyEsc:
		return "\x1b"
	case tcell.KeyBackspace:
		return "\x7f"
	case tcell.KeyUp:
		return "\x1b[A"
	case tcell.KeyDown:
		return "\x1b[B"
	case tcell.KeyRight:
		return "\x1b[C"
	case tcell.KeyLeft:
		return "\x1b[D"
	case tcell.KeyPgUp:
		return "\x1b[5~"
	case tcell.KeyPgDn:
		return "\x1b[6~"
	case tcell.KeyHome:
		return "\x1b[H"
	case tcell.KeyEnd:
		return "\x1b[F"
	case tcell.KeyDelete:
		return "\x1b[3~"
	}
	return ""
}
