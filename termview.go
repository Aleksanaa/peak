package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/aleksana/peak/term"
	"github.com/atotto/clipboard"
	"github.com/creack/pty"
	"github.com/gdamore/tcell/v2"
)

const maxHistory = 1000

type TermView struct {
	BaseView
	state       terminal.State
	vt          *terminal.VT
	ptyFile     *os.File
	closed      bool
	onClose     func()
	editor      *Editor
	lastMX      int
	lastMY      int
	lastButtons tcell.ButtonMask

	selection Selection
	selecting bool

	lastMode terminal.ModeFlag

	contentHeight int
}

func (tv *TermView) IsRaw() bool {
	tv.state.Lock()
	defer tv.state.Unlock()
	return tv.state.Mode(terminal.ModeAltScreen)
}

func NewTermView(editor *Editor, cmdStr, dir string, x, y, w, h int, onClose func()) (*TermView, error) {
	tv := &TermView{
		BaseView: BaseView{
			x: x, y: y, w: w, h: h,
		},
		onClose:       onClose,
		editor:        editor,
		contentHeight: h,
	}
	tv.scroll.AutoScroll = true

	var cmd *exec.Cmd
	if cmdStr == "" {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		cmd = exec.Command(shell)
	} else {
		cmd = exec.Command("/bin/sh", "-c", cmdStr)
	}
	cmd.Dir = dir

	vt, ptyFile, err := terminal.Start(&tv.state, cmd)
	if err != nil {
		return nil, err
	}
	tv.vt = vt
	tv.ptyFile = ptyFile

	// Initial resize
	tv.Resize(x, y, w, h)

	go func() {
		for {
			err := tv.vt.Parse()
			if err != nil {
				tv.state.Lock()
				tv.closed = true
				tv.state.Unlock()
				if tv.onClose != nil {
					tv.editor.Call(tv.onClose)
				}
				break
			}

			tv.state.Lock()
			tv.updateContentHeight()
			isAlt := tv.state.Mode(terminal.ModeAltScreen)
			changed := isAlt != (tv.lastMode&terminal.ModeAltScreen != 0)
			if changed {
				if isAlt {
					tv.lastMode |= terminal.ModeAltScreen
				} else {
					tv.lastMode &= ^terminal.ModeAltScreen
				}
			}
			tv.state.Unlock()

			if changed {
				tv.editor.Call(func() {
					tv.Resize(tv.x, tv.y, tv.w, tv.h)
				})
			} else {
				tv.SyncScroll()
			}
			tv.editor.screen.PostEvent(tcell.NewEventInterrupt(func() {}))
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
			char, fg, bg := ' ', terminal.DefaultFG, terminal.DefaultBG
			if screenY >= 0 && screenY < limit {
				if c, f, b := tv.state.Cell(x, screenY); c != 0 {
					char, fg, bg = c, f, b
				}
			}

			style := tcell.StyleDefault.
				Foreground(tv.toTcellColor(fg, true)).
				Background(tv.toTcellColor(bg, false))

			if tv.selection.Contains(x, screenY, true) {
				style = style.Background(tv.editor.theme.SelectionBG).
					Foreground(tv.editor.theme.SelectionFG)
			}
			s.SetContent(tv.x+x, tv.y+y, char, nil, style)
		}
	}
}

func (tv *TermView) toTcellColor(c terminal.Color, isFG bool) tcell.Color {
	if c == terminal.DefaultFG || c == terminal.DefaultBG {
		if isFG {
			return tv.editor.theme.BodyFG
		}
		return tv.editor.theme.BodyBG
	}
	if c.IsRGB() {
		r, g, b := c.RGBComponents()
		return tcell.NewRGBColor(int32(r), int32(g), int32(b))
	}
	return tcell.PaletteColor(int(c))
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

func (tv *TermView) getContentHeight() int {
	// Must be called with lock
	return tv.contentHeight
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
			c, _, _ := tv.state.Cell(x, y)
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
	if tv.vt != nil {
		// Always keep emulator at maxHistory to avoid losing Primary buffer data
		// when switching screens or resizing.
		tv.vt.Resize(w, max(maxHistory, h))

		// Tell the process the visible size. This must be called AFTER tv.vt.Resize
		// to override any PTY size changes the emulator might have made.
		pty.Setsize(tv.ptyFile, &pty.Winsize{
			Rows: uint16(h),
			Cols: uint16(w),
		})
	}
	tv.state.Lock()
	tv.updateContentHeight()
	tv.state.Unlock()
	tv.SyncScroll()
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

	eh := tv.getContentHeight()
	_, cy := tv.state.Cursor()
	tv.state.Unlock()
	tv.scroll.Sync(cy, eh, tv.h)
}

func (tv *TermView) Scroll(n int) {
	_, total, visible := tv.GetScroll()
	tv.scroll.Scroll(n, total, visible)
}

func (tv *TermView) GetScroll() (scroll, total, visible int) {
	tv.state.Lock()
	defer tv.state.Unlock()

	totalH := tv.h
	if !tv.state.Mode(terminal.ModeAltScreen) {
		totalH = tv.getContentHeight()
	}
	return tv.scroll.Pos, max(0, totalH), tv.h
}

func (tv *TermView) LineCount() int {
	tv.state.Lock()
	defer tv.state.Unlock()
	return tv.getContentHeight()
}

func (tv *TermView) GetLine(y int) string {
	tv.state.Lock()
	defer tv.state.Unlock()
	return tv.getLine(y)
}

func (tv *TermView) getLine(y int) string {
	// Must be called with lock
	limit := max(maxHistory, tv.h)
	if y < 0 || y >= limit {
		return ""
	}
	var sb strings.Builder
	for x := 0; x < tv.w; x++ {
		c, _, _ := tv.state.Cell(x, y)
		sb.WriteRune(c)
	}
	return sb.String()
}

func (tv *TermView) Search(word string) int {
	start := Cursor{0, 0}
	if tv.selection.Active {
		start = tv.selection.End
	}
	line, sel, ok := Search(tv, word, start)
	if ok {
		tv.selection = sel
		return line
	}
	return -1
}

func (tv *TermView) ShowLineAt(lineNum int, vrow int) {
	tv.scroll.Pos = lineNum - vrow
	_, total, visible := tv.GetScroll()
	tv.scroll.Clamp(total, visible)
}

func (tv *TermView) GetClickWord(mx, my int) string {
	tv.state.Lock()
	defer tv.state.Unlock()

	rx, ry := mx-tv.x, my-tv.y
	realRY := ry + tv.scroll.Pos

	if tv.selection.Contains(rx, realRY, true) {
		return tv.GetSelectedText()
	}

	limit := max(maxHistory, tv.h)
	if realRY < 0 || realRY >= limit {
		return ""
	}

	start, end := GetWordBoundaries(rx, tv.w, func(x int) rune {
		c, _, _ := tv.state.Cell(x, realRY)
		return c
	})

	var sb strings.Builder
	for x := start; x < end; x++ {
		c, _, _ := tv.state.Cell(x, realRY)
		if c != 0 {
			sb.WriteRune(c)
		}
	}
	return strings.TrimSpace(sb.String())
}

func (tv *TermView) GetBuffer() *Buffer {
	return nil
}

type termLineProvider struct {
	tv *TermView
}

func (p termLineProvider) LineCount() int       { return p.tv.getContentHeight() }
func (p termLineProvider) GetLine(y int) string { return p.tv.getLine(y) }

func (tv *TermView) GetSelectedText() string {
	tv.state.Lock()
	defer tv.state.Unlock()
	return GetTextInSelection(termLineProvider{tv}, tv.selection, true)
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
			key, r := e.Key(), e.Rune()
			isC := key == tcell.KeyCtrlC || (key == tcell.KeyRune && (r == 'c' || r == 'C') && mod&tcell.ModCtrl != 0)
			isX := key == tcell.KeyCtrlX || (key == tcell.KeyRune && (r == 'x' || r == 'X') && mod&tcell.ModCtrl != 0)
			isV := key == tcell.KeyCtrlV || (key == tcell.KeyRune && (r == 'v' || r == 'V') && mod&tcell.ModCtrl != 0)
			isF := key == tcell.KeyCtrlF || (key == tcell.KeyRune && (r == 'f' || r == 'F') && mod&tcell.ModCtrl != 0)

			if isC || isX {
				tv.Snarf()
				return false
			}
			if isV {
				tv.Paste()
				return false
			}
			if isF {
				if tv.GetSelectedText() != "" {
					tv.editor.Execute(nil, nil, "Look")
				}
				return false
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
		if tv.vt != nil && tv.vt.File() != nil {
			tv.vt.File().Write([]byte(keyToEscSeq(e)))
		}
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
				tv.vt.File().Write([]byte(seq))
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

		if tv.vt != nil && tv.vt.File() != nil && isMouseMode && !ctrlPressed && !tv.selecting {
			motion := mx != tv.lastMX || my != tv.lastMY
			handled := false
			isMotion, isRelease := false, false
			btnReport := 0

			if buttons != tv.lastButtons {
				handled = true
				if buttons == tcell.ButtonNone {
					isRelease = true
					switch {
					case tv.lastButtons&tcell.Button1 != 0:
						btnReport = 0
					case tv.lastButtons&tcell.Button3 != 0:
						btnReport = 1
					case tv.lastButtons&tcell.Button2 != 0:
						btnReport = 2
					}
				} else {
					switch {
					case buttons&tcell.Button1 != 0:
						btnReport = 0
					case buttons&tcell.Button3 != 0:
						btnReport = 1
					case buttons&tcell.Button2 != 0:
						btnReport = 2
					}
				}
			} else if motion {
				tv.state.Lock()
				motionMode := tv.state.Mode(terminal.ModeMouseMotion | terminal.ModeMouseMany)
				manyMode := tv.state.Mode(terminal.ModeMouseMany)
				tv.state.Unlock()

				if buttons != tcell.ButtonNone && motionMode {
					switch {
					case buttons&tcell.Button1 != 0:
						btnReport = 0
					case buttons&tcell.Button3 != 0:
						btnReport = 1
					case buttons&tcell.Button2 != 0:
						btnReport = 2
					}
					isMotion, handled = true, true
				} else if manyMode {
					btnReport, isMotion, handled = 3, true, true
				}
			}

			if handled && sgrMode && rx >= 0 && rx < tv.w && ry >= 0 && ry < tv.h {
				esc := tv.encodeSGR(btnReport, rx, ry, isMotion, isRelease, mod)
				tv.vt.File().Write([]byte(esc))
			}

			if buttons&tcell.Button1 != 0 {
				tv.selection.Active = false
			}
		} else {
			if buttons&tcell.Button1 != 0 {
				if !tv.selecting {
					tv.selecting = true
					tv.selection = Selection{Start: Cursor{rx, realRY}, Active: true}
				}
				tv.selection.End = Cursor{rx, realRY}
			} else if tv.selecting {
				tv.selecting = false
				if tv.selection.Start == tv.selection.End {
					tv.selection.Active = false
				}
			}
		}

		tv.lastMX, tv.lastMY, tv.lastButtons = mx, my, buttons
	}
	return false
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
	if tv.vt != nil {
		tv.vt.Close()
	}
}

func (tv *TermView) Snarf() {
	if text := tv.GetSelectedText(); text != "" {
		go clipboard.WriteAll(text)
	}
}

func (tv *TermView) Paste() {
	text, _ := clipboard.ReadAll()
	if text != "" && tv.vt != nil && tv.vt.File() != nil {
		tv.vt.File().Write([]byte(text))
	}
}

func keyToEscSeq(e *tcell.EventKey) string {
	if e.Key() == tcell.KeyRune {
		return string(e.Rune())
	}

	switch e.Key() {
	case tcell.KeyEnter:
		return "\r"
	case tcell.KeyTab:
		return "\t"
	case tcell.KeyEsc:
		return "\x1b"
	case tcell.KeyBackspace, tcell.KeyBackspace2:
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
	case tcell.KeyCtrlA:
		return "\x01"
	case tcell.KeyCtrlB:
		return "\x02"
	case tcell.KeyCtrlC:
		return "\x03"
	case tcell.KeyCtrlD:
		return "\x04"
	case tcell.KeyCtrlE:
		return "\x05"
	case tcell.KeyCtrlF:
		return "\x06"
	case tcell.KeyCtrlG:
		return "\x07"
	case tcell.KeyCtrlH:
		return "\x08"
	case tcell.KeyCtrlI:
		return "\x09"
	case tcell.KeyCtrlJ:
		return "\x0a"
	case tcell.KeyCtrlK:
		return "\x0b"
	case tcell.KeyCtrlL:
		return "\x0c"
	case tcell.KeyCtrlM:
		return "\x0d"
	case tcell.KeyCtrlN:
		return "\x0e"
	case tcell.KeyCtrlO:
		return "\x0f"
	case tcell.KeyCtrlP:
		return "\x10"
	case tcell.KeyCtrlQ:
		return "\x11"
	case tcell.KeyCtrlR:
		return "\x12"
	case tcell.KeyCtrlS:
		return "\x13"
	case tcell.KeyCtrlT:
		return "\x14"
	case tcell.KeyCtrlU:
		return "\x15"
	case tcell.KeyCtrlV:
		return "\x16"
	case tcell.KeyCtrlW:
		return "\x17"
	case tcell.KeyCtrlX:
		return "\x18"
	case tcell.KeyCtrlY:
		return "\x19"
	case tcell.KeyCtrlZ:
		return "\x1a"
	}
	return ""
}
