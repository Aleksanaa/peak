package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/aleksana/peak/internal/session"
	"github.com/aleksana/peak/peak/term"
	"github.com/atotto/clipboard"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

const maxHistory = 1000

type TermView struct {
	BaseView
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

	selection Selection
	selecting bool

	contentHeight int
	buffer        *Buffer
	bufferDirty   bool

	cmd          string       // command used to start the terminal, for session save
	OnCWD        func(string) // called on main goroutine with decoded absolute path; set by owner
	onCWDStarted bool         // true once OnCWD has been called at least once
}

func (tv *TermView) IsRaw() bool {
	tv.state.Lock()
	defer tv.state.Unlock()
	return tv.state.Mode(terminal.ModeAltScreen)
}

func (tv *TermView) Layout() {
	tv.state.Lock()
	tv.updateContentHeight()
	tv.state.Unlock()
	tv.SyncScroll()
}

func NewTermView(editor *Editor, sess session.Session, x, y, w, h int, onClose func()) (*TermView, error) {
	ctx, cancel := context.WithCancel(context.Background())
	tv := &TermView{
		BaseView: BaseView{
			x: x, y: y, w: w, h: h,
		},
		session:       sess,
		onClose:       onClose,
		editor:        editor,
		cancel:        cancel,
		contentHeight: h,
		buffer:        NewBuffer(""),
	}
	tv.scroll.AutoScroll = true

	vt, err := terminal.Create(&tv.state, sess)
	if err != nil {
		cancel()
		return nil, err
	}
	tv.state.ResponseWriter = sess
	if h := editor.theme.BodyFG.Hex(); h >= 0 {
		tv.state.FGColor = terminal.RGB(uint8(h>>16), uint8(h>>8), uint8(h))
	}
	if h := editor.theme.BodyBG.Hex(); h >= 0 {
		tv.state.BGColor = terminal.RGB(uint8(h>>16), uint8(h>>8), uint8(h))
	}
	tv.vt = vt
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

	// Initial resize
	tv.Resize(x, y, w, h)

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
			tv.bufferDirty = true
			// Layout() (called from Window.Draw on the next frame) handles
			// contentHeight and scroll sync. Just signal a redraw.
			tv.editor.Redraw()
		}
	}()

	return tv, nil
}

func (tv *TermView) Draw(s tcell.Screen) {
	tv.state.Lock()
	defer tv.state.Unlock()

	limit := max(maxHistory, tv.h)
	for y := 0; y < tv.h; y++ {
		screenY := tv.scroll.Pos + y
		for x := 0; x < tv.w; x++ {
			char, fg, bg, mode := ' ', terminal.DefaultFG, terminal.DefaultBG, int16(0)
			if screenY >= 0 && screenY < limit {
				c, f, b, m := tv.state.Cell(x, screenY)
				if c == 0 {
					continue
				}
				char, fg, bg, mode = c, f, b, m
			}

			style := tcell.StyleDefault.
				Foreground(tv.toTcellColor(fg)).
				Background(tv.toTcellColor(bg))

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

			if tv.selection.Contains(x, screenY, false) {
				style = style.Background(tv.editor.theme.SelectionBG).
					Foreground(tv.editor.theme.SelectionFG)
			}
			s.Put(tv.x+x, tv.y+y, string(char), style)
		}
	}
}

func (tv *TermView) toTcellColor(c terminal.Color) tcell.Color {
	if c == terminal.DefaultFG || c == terminal.DefaultBG {
		return color.Default
	}
	if c.IsRGB() {
		r, g, b := c.RGBComponents()
		return color.NewRGBColor(int32(r), int32(g), int32(b))
	}
	return color.PaletteColor(int(c))
}

func (tv *TermView) ShowCursor(s tcell.Screen) {
	tv.state.Lock()
	defer tv.state.Unlock()
	if tv.state.CursorVisible() {
		cx, cy := tv.state.Cursor()
		// Relative to view
		ry := cy - tv.scroll.Pos
		if cx >= 0 && cx < tv.w && ry >= 0 && ry < tv.h {
			s.ShowCursor(tv.x+cx, tv.y+ry)
			return
		}
	}
	s.HideCursor()
}

func (tv *TermView) updateContentHeight() {
	// Must be called with lock
	if tv.state.Mode(terminal.ModeAltScreen) {
		tv.contentHeight = tv.h
		return
	}

	_, cy := tv.state.Cursor()
	lastLine := cy + 1
	if lastLine < tv.h {
		lastLine = tv.h
	}

	limit := maxHistory
	if tv.h > limit {
		limit = tv.h
	}

	for y := limit - 1; y >= lastLine; y-- {
		empty := true
		for x := 0; x < tv.w; x++ {
			c, _, _, _ := tv.state.Cell(x, y)
			if c != 0 && c != ' ' {
				empty = false
				break
			}
		}
		if !empty {
			tv.contentHeight = y + 1
			return
		}
	}
	tv.contentHeight = lastLine
}

func (tv *TermView) Resize(x, y, w, h int) {
	if tv.x == x && tv.y == y && tv.w == w && tv.h == h {
		return
	}
	tv.x, tv.y, tv.w, tv.h = x, y, w, h
	// Always keep emulator at maxHistory to avoid losing Primary buffer data
	// when switching screens or resizing.
	tv.vt.Resize(w, max(maxHistory, h))

	// Tell the process the visible size. This must be called AFTER tv.vt.Resize
	// to override any PTY size changes the emulator might have made.
	tv.session.Resize(h, w)
	// contentHeight and scroll are recomputed by Layout() on the next draw frame.
}

func (tv *TermView) SyncScroll() {
	tv.state.Lock()
	if tv.selecting || tv.state.Mode(terminal.ModeAltScreen) {
		if tv.state.Mode(terminal.ModeAltScreen) {
			tv.scroll.Pos = 0
		}
		tv.state.Unlock()
		return
	}

	if !tv.scroll.AutoScroll {
		tv.state.Unlock()
		return
	}

	eh := tv.contentHeight
	_, cy := tv.state.Cursor()
	tv.state.Unlock()

	if cy < tv.scroll.Pos {
		tv.scroll.Pos = cy
	} else if cy >= tv.scroll.Pos+tv.h {
		tv.scroll.Pos = cy - tv.h + 1
	}
	tv.scroll.Clamp(eh, tv.h)
}

func (tv *TermView) Scroll(n int) {
	_, total, visible := tv.GetScroll()
	tv.scroll.Scroll(n, total, visible)
}

func (tv *TermView) AdvanceDragCursor(dir int) {
	if !tv.selecting {
		return
	}
	tv.selection.End.y += dir
}

func (tv *TermView) GetScroll() (scroll, total, visible int) {
	tv.state.Lock()
	defer tv.state.Unlock()

	totalH := tv.h
	if !tv.state.Mode(terminal.ModeAltScreen) {
		totalH = tv.contentHeight
	}
	return tv.scroll.Pos, max(0, totalH), tv.h
}

func (tv *TermView) Search(word string) int {
	start := Cursor{0, 0}
	if tv.selection.Active {
		start = tv.selection.End
	}
	line, sel, ok := Search(tv.GetBuffer(), word, start)
	if ok {
		tv.selection = sel
		return line
	}
	return -1
}

func (tv *TermView) ShowLineAt(lineNum int) {
	if lineNum >= tv.scroll.Pos && lineNum < tv.scroll.Pos+tv.h {
		return
	}
	tv.scroll.Pos = lineNum - tv.h/4
	_, total, visible := tv.GetScroll()
	tv.scroll.Clamp(total, visible)
}

func (tv *TermView) GetClickWord(mx, my int) string {
	rx, ry := mx-tv.x, my-tv.y
	realRY := ry + tv.scroll.Pos

	if tv.selection.Contains(rx, realRY, true) {
		return GetTextInSelection(tv.GetBuffer(), tv.selection, true)
	}

	limit := max(maxHistory, tv.h)
	if realRY < 0 || realRY >= limit {
		return ""
	}

	tv.state.Lock()
	start, end := GetWordBoundaries(rx, tv.w, func(x int) rune {
		c, _, _, _ := tv.state.Cell(x, realRY)
		return c
	})
	var sb strings.Builder
	for x := start; x < end; x++ {
		c, _, _, _ := tv.state.Cell(x, realRY)
		if c != 0 {
			sb.WriteRune(c)
		}
	}
	tv.state.Unlock()
	return strings.TrimSpace(sb.String())
}

func (tv *TermView) GetBuffer() *Buffer {
	if tv.bufferDirty {
		cursor := tv.buffer.cursor
		sel := tv.buffer.selection
		tv.buffer.SetText(tv.GetScrollback())
		tv.buffer.cursor = cursor
		tv.buffer.selection = sel
		tv.buffer.history = nil
		tv.buffer.redoStack = nil
		tv.bufferDirty = false
	}
	return tv.buffer
}

// GetScrollback returns the terminal scrollback buffer as plain text.
// Each line has trailing space stripped; lines are newline-separated.
func (tv *TermView) GetScrollback() string {
	tv.state.Lock()
	defer tv.state.Unlock()
	n := tv.contentHeight
	var sb strings.Builder
	buf := make([]rune, tv.w)
	for y := 0; y < n; y++ {
		for x := 0; x < tv.w; x++ {
			r, _, _, _ := tv.state.Cell(x, y)
			if r == 0 {
				r = ' '
			}
			buf[x] = r
		}
		end := len(buf)
		for end > 0 && buf[end-1] == ' ' {
			end--
		}
		sb.WriteString(string(buf[:end]))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func (tv *TermView) GetSelectedText() string {
	return GetTextInSelection(tv.GetBuffer(), tv.selection, true)
}

func (tv *TermView) HandleEvent(ev tcell.Event) bool {
	tv.state.Lock()
	closed := tv.closed
	isMouseMode := tv.state.Mode(terminal.ModeMouseMask)
	sgrMode := tv.state.Mode(terminal.ModeMouseSgr)
	isAlt := tv.state.Mode(terminal.ModeAltScreen)
	tv.state.Unlock()

	if closed {
		return false
	}

	switch e := ev.(type) {
	case *tcell.EventKey:
		tv.scroll.AutoScroll = true
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
					return false
				case "v", "V":
					tv.Paste()
					return false
				case "f", "F":
					if tv.GetSelectedText() != "" {
						tv.editor.Execute(nil, nil, "Look")
					}
					return false
				}
			}

			switch key {
			case tcell.KeyEsc:
				tv.state.Lock()
				tv.selection.Active = false
				tv.state.Unlock()
				return false
			case tcell.KeyPgUp:
				tv.Scroll(-tv.h)
				return false
			case tcell.KeyPgDn:
				tv.Scroll(tv.h)
				return false
			}
		}
		if !tv.onCWDStarted {
			tv.OnCWD = nil
		}
		tv.session.Write([]byte(keyToEscSeq(e)))
		return false
	case *tcell.EventMouse:
		mx, my := e.Position()
		rx, ry := mx-tv.x, my-tv.y
		realRY := ry + tv.scroll.Pos

		buttons := e.Buttons()
		mod := e.Modifiers()
		ctrlPressed := mod&tcell.ModCtrl != 0

		// Wheel handling
		if buttons&(tcell.WheelUp|tcell.WheelDown) != 0 {
			if isAlt {
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
			} else {
				dir := -1
				if buttons&tcell.WheelDown != 0 {
					dir = 1
				}
				tv.Scroll(dir)
			}
			tv.lastMX, tv.lastMY, tv.lastButtons = mx, my, buttons
			return false
		}

		if isMouseMode && !ctrlPressed && !tv.selecting {
			motion := mx != tv.lastMX || my != tv.lastMY
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
				tv.selection.Active = false
			}
		} else {
			if buttons&tcell.ButtonPrimary != 0 {
				if !tv.selecting {
					tv.selecting = true
					tv.selection = Selection{Start: Cursor{rx, realRY}, End: Cursor{rx + 1, realRY}, Active: true}
				}
				tv.selection.End = Cursor{rx + 1, realRY}
			} else if tv.selecting {
				tv.selecting = false
				if tv.selection.Start.y == tv.selection.End.y && tv.selection.End.x-tv.selection.Start.x <= 1 {
					tv.selection.Active = false
				}
			}
		}

		tv.lastMX, tv.lastMY, tv.lastButtons = mx, my, buttons
	}
	return false
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

func (tv *TermView) Close() {
	tv.cancel()
	tv.vt.Close()
}

func (tv *TermView) Snarf() {
	if text := tv.GetSelectedText(); text != "" {
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
