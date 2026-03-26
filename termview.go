package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/gdamore/tcell/v2"
	"github.com/micro-editor/terminal"
)

const scrollbackMax = 1000

type TermView struct {
	x, y, w, h  int
	state       terminal.State
	vt          *terminal.VT
	closed      bool
	onClose     func()
	editor      *Editor
	lastMX      int
	lastMY      int
	lastButtons tcell.ButtonMask

	selectionStart struct{ x, y int }
	selectionEnd   struct{ x, y int }
	hasSelection   bool
	selecting      bool

	scroll     int
	scrollable bool
	lastMode   terminal.ModeFlag
}

func NewTermView(editor *Editor, cmdStr string, x, y, w, h int, onClose func()) (*TermView, error) {
	tv := &TermView{
		x:          x,
		y:          y,
		w:          w,
		h:          h,
		onClose:    onClose,
		editor:     editor,
		scrollable: true,
	}

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

	vt, _, err := terminal.Start(&tv.state, cmd)
	if err != nil {
		return nil, err
	}
	tv.vt = vt

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

			// Detect mode change (e.g. entering/exiting AltScreen)
			tv.state.Lock()
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

	isAlt := tv.state.Mode(terminal.ModeAltScreen)
	totalH := tv.h
	if !isAlt {
		totalH = scrollbackMax
	}

	for y := 0; y < tv.h; y++ {
		screenY := tv.scroll + y
		if screenY >= totalH {
			break
		}
		for x := 0; x < tv.w; x++ {
			char, fg, bg := tv.state.Cell(x, screenY)

			style := tcell.StyleDefault.
				Foreground(tv.toTcellColor(fg, true)).
				Background(tv.toTcellColor(bg, false))

			if tv.hasSelection && tv.isSelected(x, screenY) {
				style = style.Background(tv.editor.theme.SelectionBG).
					Foreground(tv.editor.theme.SelectionFG)
			}

			s.SetContent(tv.x+x, tv.y+y, char, nil, style)
		}
	}
}

func (tv *TermView) isSelected(x, y int) bool {
	start, end := tv.selectionStart, tv.selectionEnd
	if start.y > end.y || (start.y == end.y && start.x > end.x) {
		start, end = end, start
	}

	if y < start.y || y > end.y {
		return false
	}
	if y == start.y && y == end.y {
		return x >= start.x && x <= end.x
	}
	if y == start.y {
		return x >= start.x
	}
	if y == end.y {
		return x <= end.x
	}
	return true
}

func (tv *TermView) toTcellColor(c terminal.Color, isFG bool) tcell.Color {
	if c == terminal.DefaultFG || c == terminal.DefaultBG {
		if isFG {
			return tv.editor.theme.BodyFG
		}
		return tv.editor.theme.BodyBG
	}
	if c < 256 {
		return tcell.PaletteColor(int(c))
	}
	return tcell.Color(c)
}

func (tv *TermView) ShowCursor(s tcell.Screen) {
	tv.state.Lock()
	defer tv.state.Unlock()
	if tv.state.CursorVisible() {
		cx, cy := tv.state.Cursor()
		// Relative to view
		ry := cy - tv.scroll
		if cx >= 0 && cx < tv.w && ry >= 0 && ry < tv.h {
			s.ShowCursor(tv.x+cx, tv.y+ry)
		} else {
			s.HideCursor()
		}
	} else {
		s.HideCursor()
	}
}

func (tv *TermView) getContentHeight() int {
	// Must be called with lock
	maxH := tv.h
	_, cy := tv.state.Cursor()
	if cy+1 > maxH {
		maxH = cy + 1
	}

	for y := scrollbackMax - 1; y >= maxH; y-- {
		for x := 0; x < tv.w; x++ {
			char, _, _ := tv.state.Cell(x, y)
			if char != ' ' && char != 0 {
				return y + 1
			}
		}
	}
	return maxH
}

func (tv *TermView) Resize(x, y, w, h int) {
	tv.x, tv.y, tv.w, tv.h = x, y, w, h
	if tv.vt != nil {
		tv.state.Lock()
		isAlt := tv.state.Mode(terminal.ModeAltScreen)
		tv.state.Unlock()

		ew, eh := w, h
		if !isAlt {
			eh = scrollbackMax
		}
		tv.vt.Resize(ew, eh)
	}
	tv.SyncScroll()
}

func (tv *TermView) SyncScroll() {
	tv.state.Lock()
	defer tv.state.Unlock()

	isAlt := tv.state.Mode(terminal.ModeAltScreen)
	if isAlt {
		tv.scroll = 0
		return
	}

	_, cy := tv.state.Cursor()
	eh := tv.getContentHeight()

	// Ensure cursor is visible
	if cy < tv.scroll {
		tv.scroll = cy
	} else if cy >= tv.scroll+tv.h {
		tv.scroll = cy - tv.h + 1
	}

	// Bounds check
	if tv.scroll < 0 {
		tv.scroll = 0
	}
	if tv.scroll > eh-tv.h {
		tv.scroll = eh - tv.h
	}
	if eh <= tv.h {
		tv.scroll = 0
	}
}

func (tv *TermView) Scroll(n int) {
	tv.state.Lock()
	isAlt := tv.state.Mode(terminal.ModeAltScreen)
	eh := tv.h
	if !isAlt {
		eh = tv.getContentHeight()
	}
	tv.state.Unlock()

	if isAlt {
		// Fullscreen apps usually handle their own scrolling
		// If we reach here via scrollbar, we don't scroll the AltScreen buffer
		return
	}

	tv.scroll += n
	if tv.scroll < 0 {
		tv.scroll = 0
	}
	if tv.scroll > eh-tv.h {
		tv.scroll = eh - tv.h
	}
	if eh <= tv.h {
		tv.scroll = 0
	}
}

func (tv *TermView) GetScroll() (scroll, total, visible int) {
	tv.state.Lock()
	defer tv.state.Unlock()

	isAlt := tv.state.Mode(terminal.ModeAltScreen)
	totalH := tv.h
	if !isAlt {
		totalH = tv.getContentHeight()
	}
	return tv.scroll, totalH, tv.h
}

func (tv *TermView) GetPos() (x, y, w, h int) {
	return tv.x, tv.y, tv.w, tv.h
}

func (tv *TermView) SetPos(x, y, w, h int) {
	tv.x, tv.y, tv.w, tv.h = x, y, w, h
}

func (tv *TermView) GetClickWord(mx, my int) string {
	return ""
}

func (tv *TermView) GetBuffer() *Buffer {
	return nil
}

func (tv *TermView) GetSelectedText() string {
	tv.state.Lock()
	defer tv.state.Unlock()

	if !tv.hasSelection {
		return ""
	}

	start, end := tv.selectionStart, tv.selectionEnd
	if start.y > end.y || (start.y == end.y && start.x > end.x) {
		start, end = end, start
	}

	var sb strings.Builder
	for y := start.y; y <= end.y; y++ {
		x1, x2 := 0, tv.w-1
		if y == start.y {
			x1 = start.x
		}
		if y == end.y {
			x2 = end.x
		}

		line := ""
		for x := x1; x <= x2; x++ {
			char, _, _ := tv.state.Cell(x, y)
			line += string(char)
		}
		sb.WriteString(strings.TrimRight(line, " "))
		if y < end.y {
			sb.WriteRune('\n')
		}
	}
	return sb.String()
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
		if tv.vt != nil && tv.vt.File() != nil {
			tv.vt.File().Write([]byte(keyToEscSeq(e)))
		}
		return false
	case *tcell.EventMouse:
		mx, my := e.Position()
		rx, ry := mx-tv.x, my-tv.y
		// Mouse coordinates for terminal should be relative to visible area
		// but vt10x expects them relative to its internal screen rows.
		// So we add scroll offset.
		realRY := ry + tv.scroll

		buttons := e.Buttons()
		mod := e.Modifiers()
		ctrlPressed := mod&tcell.ModCtrl != 0

		// Wheel handling
		if buttons&(tcell.WheelUp|tcell.WheelDown) != 0 {
			if isAlt {
				if isMouseMode && sgrMode {
					btn := 64
					if buttons&tcell.WheelDown != 0 {
						btn = 65
					}
					esc := tv.encodeSGR(btn, rx, ry, false, false, mod)
					tv.vt.File().Write([]byte(esc))
				} else {
					// In AltScreen but no mouse mode, send arrow keys
					seq := "\x1b[A"
					if buttons&tcell.WheelDown != 0 {
						seq = "\x1b[B"
					}
					// Send 3 times for faster scrolling
					tv.vt.File().Write([]byte(seq + seq + seq))
				}
			} else {
				if buttons&tcell.WheelUp != 0 {
					tv.Scroll(-1)
				} else {
					tv.Scroll(1)
				}
			}
			tv.lastMX, tv.lastMY = mx, my
			tv.lastButtons = buttons
			return false
		}

		if tv.vt != nil && tv.vt.File() != nil && isMouseMode && !ctrlPressed && !tv.selecting {
			motion := mx != tv.lastMX || my != tv.lastMY

			handled := false
			isMotion := false
			isRelease := false
			btnReport := 0

			if buttons != tv.lastButtons {
				if buttons == tcell.ButtonNone {
					isRelease = true
					// Report release of the button that was down
					if tv.lastButtons&tcell.Button1 != 0 {
						btnReport = 0
					} else if tv.lastButtons&tcell.Button3 != 0 {
						btnReport = 1
					} else if tv.lastButtons&tcell.Button2 != 0 {
						btnReport = 2
					}
				} else {
					// Report press of the button that just went down
					if buttons&tcell.Button1 != 0 {
						btnReport = 0
					} else if buttons&tcell.Button3 != 0 {
						btnReport = 1
					} else if buttons&tcell.Button2 != 0 {
						btnReport = 2
					}
					// Wheel already handled above
				}
				handled = true
			} else if motion {
				tv.state.Lock()
				motionMode := tv.state.Mode(terminal.ModeMouseMotion | terminal.ModeMouseMany)
				manyMode := tv.state.Mode(terminal.ModeMouseMany)
				tv.state.Unlock()

				if buttons != tcell.ButtonNone {
					if motionMode {
						if buttons&tcell.Button1 != 0 {
							btnReport = 0
						} else if buttons&tcell.Button3 != 0 {
							btnReport = 1
						} else if buttons&tcell.Button2 != 0 {
							btnReport = 2
						}
						isMotion = true
						handled = true
					}
				} else if manyMode {
					btnReport = 3 // Standard button code for "no button"
					isMotion = true
					handled = true
				}
			}

			if handled && sgrMode {
				if rx >= 0 && rx < tv.w && ry >= 0 && ry < tv.h {
					esc := tv.encodeSGR(btnReport, rx, ry, isMotion, isRelease, mod)
					tv.vt.File().Write([]byte(esc))
				}
			}

			if buttons&tcell.Button1 != 0 {
				tv.hasSelection = false
			}
		} else {
			// Local selection
			if buttons&tcell.Button1 != 0 {
				if !tv.selecting {
					tv.selecting = true
					tv.hasSelection = true
					tv.selectionStart = struct{ x, y int }{rx, realRY}
				}
				tv.selectionEnd = struct{ x, y int }{rx, realRY}
			} else {
				if tv.selecting {
					tv.selecting = false
					if tv.selectionStart == tv.selectionEnd {
						tv.hasSelection = false
					}
				}
			}
		}

		tv.lastMX, tv.lastMY = mx, my
		tv.lastButtons = buttons
		return false
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
