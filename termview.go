package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/gdamore/tcell/v2"
	"github.com/micro-editor/terminal"
)

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
}

func NewTermView(editor *Editor, cmdStr string, x, y, w, h int, onClose func()) (*TermView, error) {
	tv := &TermView{
		x:       x,
		y:       y,
		w:       w,
		h:       h,
		onClose: onClose,
		editor:  editor,
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

	// Resize terminal to initial view size
	tv.vt.Resize(w, h)

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
			tv.editor.screen.PostEvent(tcell.NewEventInterrupt(func() {}))
		}
	}()

	return tv, nil
}

func (tv *TermView) Draw(s tcell.Screen) {
	tv.state.Lock()
	defer tv.state.Unlock()

	for y := 0; y < tv.h; y++ {
		for x := 0; x < tv.w; x++ {
			char, fg, bg := tv.state.Cell(x, y)

			style := tcell.StyleDefault.
				Foreground(tv.toTcellColor(fg, true)).
				Background(tv.toTcellColor(bg, false))

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
		if cx >= 0 && cx < tv.w && cy >= 0 && cy < tv.h {
			s.ShowCursor(tv.x+cx, tv.y+cy)
		} else {
			s.HideCursor()
		}
	} else {
		s.HideCursor()
	}
}

func (tv *TermView) Resize(x, y, w, h int) {
	tv.x, tv.y, tv.w, tv.h = x, y, w, h
	if tv.vt != nil {
		tv.vt.Resize(w, h)
	}
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

func (tv *TermView) HandleEvent(ev tcell.Event) bool {
	tv.state.Lock()
	closed := tv.closed
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
		if tv.vt != nil && tv.vt.File() != nil && tv.state.Mode(terminal.ModeMouseMask) {
			mx, my := e.Position()
			buttons := e.Buttons()
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
					} else if buttons&tcell.WheelUp != 0 {
						btnReport = 64
					} else if buttons&tcell.WheelDown != 0 {
						btnReport = 65
					}
				}
				handled = true
			} else if motion {
				if buttons != tcell.ButtonNone {
					if tv.state.Mode(terminal.ModeMouseMotion | terminal.ModeMouseMany) {
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
				} else if tv.state.Mode(terminal.ModeMouseMany) {
					btnReport = 3 // Standard button code for "no button"
					isMotion = true
					handled = true
				}
			}

			tv.lastMX, tv.lastMY = mx, my
			tv.lastButtons = buttons

			if handled && tv.state.Mode(terminal.ModeMouseSgr) {
				rx, ry := mx-tv.x, my-tv.y
				if rx >= 0 && rx < tv.w && ry >= 0 && ry < tv.h {
					esc := tv.encodeSGR(btnReport, rx, ry, isMotion, isRelease, e.Modifiers())
					tv.vt.File().Write([]byte(esc))
				}
			}
		}
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
