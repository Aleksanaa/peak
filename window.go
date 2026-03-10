package main

import (
	"os"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/gdamore/tcell/v2"
)

// VisualLine maps a screen row to a segment of a buffer line.
type VisualLine struct {
	BufferLine int
	Start, End int
}

// TextView provides a rendered, scrollable, and editable view of a Buffer.
type TextView struct {
	buffer     *Buffer
	x, y, w, h int
	style      tcell.Style
	scroll     int
	drag       bool
	singleLine bool
	scrollable bool
	layout     []VisualLine
}

func NewTextView(text string, x, y, w, h int, style tcell.Style, singleLine, scrollable bool) *TextView {
	tv := &TextView{
		buffer: NewBuffer(text),
		x:      x, y: y, w: w, h: h,
		style:      style,
		singleLine: singleLine,
		scrollable: scrollable,
	}
	tv.UpdateLayout()
	return tv
}

// UpdateLayout recalculates the visual lines based on current width and buffer content.
func (tv *TextView) UpdateLayout() {
	if tv.w <= 0 {
		return
	}
	tv.layout = nil
	for i, line := range tv.buffer.lines {
		if len(line) == 0 {
			tv.layout = append(tv.layout, VisualLine{i, 0, 0})
			continue
		}
		for start := 0; start < len(line); start += tv.w {
			end := start + tv.w
			if end > len(line) {
				end = len(line)
			}
			tv.layout = append(tv.layout, VisualLine{i, start, end})
		}
	}
}

func (tv *TextView) Draw(s tcell.Screen) {
	tv.UpdateLayout()
	if !tv.scrollable {
		tv.scroll = 0
	}

	selStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x6e738d)).Foreground(tcell.ColorWhite)
	vrow := 0

	for layoutIdx := tv.scroll; layoutIdx < len(tv.layout) && vrow < tv.h; layoutIdx++ {
		vl := tv.layout[layoutIdx]
		line := tv.buffer.lines[vl.BufferLine]

		for col := 0; col < tv.w; col++ {
			char := ' '
			style := tv.style
			bx := vl.Start + col

			if tv.buffer.IsSelected(bx, vl.BufferLine) {
				style = selStyle
			}
			if bx < vl.End {
				char = line[bx]
			}
			s.SetContent(tv.x+col, tv.y+vrow, char, nil, style)
		}
		vrow++
	}
	// Clear remaining space
	for ; vrow < tv.h; vrow++ {
		for col := 0; col < tv.w; col++ {
			s.SetContent(tv.x+col, tv.y+vrow, ' ', nil, tv.style)
		}
	}
}

func (tv *TextView) ShowCursor(s tcell.Screen) {
	vrow := 0
	for lidx, vl := range tv.layout {
		if vl.BufferLine == tv.buffer.cursor.y && tv.buffer.cursor.x >= vl.Start && tv.buffer.cursor.x <= vl.End {
			// Special case: cursor at end of a full line should appear on next visual line if available
			if tv.buffer.cursor.x == vl.End && vl.End-vl.Start == tv.w && lidx+1 < len(tv.layout) && tv.layout[lidx+1].BufferLine == vl.BufferLine {
				vrow++
				continue
			}

			if lidx >= tv.scroll && lidx < tv.scroll+tv.h {
				cx := tv.x + (tv.buffer.cursor.x - vl.Start)
				cy := tv.y + (lidx - tv.scroll)
				s.ShowCursor(cx, cy)
			}
			return
		}
		vrow++
	}
}

func (tv *TextView) Resize(x, y, w, h int) {
	tv.x, tv.y, tv.w, tv.h = x, y, w, h
	tv.UpdateLayout()
}

func (tv *TextView) HandleEvent(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *tcell.EventKey:
		switch ev.Key() {
		case tcell.KeyCtrlC:
			if text := tv.buffer.GetSelectedText(); text != "" {
				clipboard.WriteAll(text)
			}
		case tcell.KeyCtrlX:
			if text := tv.buffer.GetSelectedText(); text != "" {
				clipboard.WriteAll(text)
				tv.buffer.DeleteSelection()
			}
		case tcell.KeyCtrlV:
			if text, err := clipboard.ReadAll(); err == nil {
				if tv.buffer.selectionStart != nil {
					tv.buffer.DeleteSelection()
				}
				for _, r := range text {
					if r == '\n' {
						if !tv.singleLine {
							tv.buffer.NewLine()
						}
					} else {
						tv.buffer.Insert(r)
					}
				}
			}
		case tcell.KeyCtrlU:
			tv.buffer.ClearSelection()
			tv.buffer.DeleteLine()
		case tcell.KeyCtrlW:
			tv.buffer.ClearSelection()
			tv.buffer.DeleteWordBefore()
		case tcell.KeyCtrlH, tcell.KeyBackspace, tcell.KeyBackspace2:
			tv.buffer.Backspace()
		case tcell.KeyDelete:
			if tv.buffer.selectionStart != nil {
				tv.buffer.DeleteSelection()
			}
		case tcell.KeyUp:
			tv.buffer.ClearSelection()
			if !tv.singleLine {
				tv.buffer.MoveUp()
			}
		case tcell.KeyDown:
			tv.buffer.ClearSelection()
			if !tv.singleLine {
				tv.buffer.MoveDown()
			}
		case tcell.KeyLeft:
			tv.buffer.ClearSelection()
			tv.buffer.MoveLeft()
		case tcell.KeyRight:
			tv.buffer.ClearSelection()
			tv.buffer.MoveRight()
		case tcell.KeyEnter:
			tv.buffer.ClearSelection()
			if !tv.singleLine {
				tv.buffer.NewLine()
			}
		case tcell.KeyRune:
			if tv.buffer.selectionStart != nil {
				tv.buffer.DeleteSelection()
			}
			tv.buffer.Insert(ev.Rune())
		}
		tv.UpdateLayout()
		tv.SyncScroll()
		return false

	case *tcell.EventMouse:
		buttons := ev.Buttons()
		if tv.scrollable {
			if buttons&tcell.WheelUp != 0 {
				if tv.scroll > 0 {
					tv.scroll--
				}
				return false
			}
			if buttons&tcell.WheelDown != 0 {
				if tv.scroll < len(tv.layout)-1 {
					tv.scroll++
				}
				return false
			}
		}

		mx, my := ev.Position()
		if buttons != tcell.ButtonNone {
			vidx := my - tv.y + tv.scroll
			if vidx < 0 {
				vidx = 0
			}
			if vidx >= len(tv.layout) {
				vidx = len(tv.layout) - 1
			}

			vl := tv.layout[vidx]
			bx := mx - tv.x + vl.Start
			if bx < vl.Start {
				bx = vl.Start
			}
			if bx > vl.End {
				bx = vl.End
			}

			if buttons == tcell.Button1 && !tv.drag && !tv.buffer.IsSelected(bx, vl.BufferLine) {
				tv.buffer.ClearSelection()
			}

			if buttons == tcell.Button1 {
				if !tv.drag {
					tv.drag = true
					if !tv.buffer.IsSelected(bx, vl.BufferLine) {
						tv.buffer.cursor = Cursor{bx, vl.BufferLine}
						tv.buffer.SetSelection(tv.buffer.cursor, tv.buffer.cursor)
					}
				} else {
					tv.buffer.cursor = Cursor{bx, vl.BufferLine}
					tv.buffer.selectionEnd = &Cursor{bx, vl.BufferLine}
				}
			} else {
				if tv.buffer.selectionStart == nil {
					tv.buffer.cursor = Cursor{bx, vl.BufferLine}
				}
			}
		} else {
			tv.drag = false
			if tv.buffer.selectionStart != nil && tv.buffer.selectionEnd != nil {
				if *tv.buffer.selectionStart == *tv.buffer.selectionEnd {
					tv.buffer.ClearSelection()
				}
			}
		}
	}
	return false
}

func (tv *TextView) SyncScroll() {
	if !tv.scrollable {
		return
	}
	vrow := -1
	for i, vl := range tv.layout {
		if vl.BufferLine == tv.buffer.cursor.y && tv.buffer.cursor.x >= vl.Start && tv.buffer.cursor.x <= vl.End {
			vrow = i
			if tv.buffer.cursor.x < vl.End || i+1 == len(tv.layout) || tv.layout[i+1].BufferLine != tv.buffer.cursor.y {
				break
			}
		}
	}
	if vrow != -1 {
		if vrow < tv.scroll {
			tv.scroll = vrow
		} else if vrow >= tv.scroll+tv.h {
			tv.scroll = vrow - tv.h + 1
		}
	}
}

func (tv *TextView) Search(word string) {
	word = strings.TrimSpace(word)
	if word == "" {
		return
	}
	startX, startY := tv.buffer.cursor.x+1, tv.buffer.cursor.y
	for y := startY; y < len(tv.buffer.lines); y++ {
		line := string(tv.buffer.lines[y])
		if startX >= len(line) {
			startX = 0
			continue
		}
		if x := strings.Index(line[startX:], word); x != -1 {
			tv.buffer.cursor = Cursor{startX + x, y}
			tv.buffer.ClearSelection()
			return
		}
		startX = 0
	}
}

// Window container logic
type Window struct {
	tag            *TextView
	body           *TextView
	parent         *Column
	editor         *Editor
	x, y, w, h     int
	onExec         func(*Column, *Window, string) bool
	explicitHeight int
}

func NewWindow(tag, body string, parent *Column, editor *Editor, x, y, w, h int, onExec func(*Column, *Window, string) bool) *Window {
	// Window menu: #1e1e2e, menu Text: #89dceb
	tagStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x1e1e2e)).Foreground(tcell.NewHexColor(0x89dceb))
	// Text background: #313244, Text: #cdd6f4
	bodyStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x313244)).Foreground(tcell.NewHexColor(0xcdd6f4))
	return &Window{
		tag:    NewTextView(tag, x+1, y, w-1, 1, tagStyle, false, false),
		body:   NewTextView(body, x+1, y+1, w-1, h-1, bodyStyle, false, true),
		parent: parent, editor: editor, x: x, y: y, w: w, h: h, onExec: onExec,
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

func (win *Window) tagHeight() int {
	h := len(win.tag.layout)
	if h < 1 {
		return 1
	}
	return h
}

func (win *Window) Draw(s tcell.Screen) {
	th := win.tagHeight()
	win.tag.h, win.body.y, win.body.h = th, win.y+th, win.h-th
	if win.body.h < 0 {
		win.body.h = 0
	}

	handleStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x89dceb)).Foreground(tcell.ColorBlack)
	for i := 0; i < th; i++ {
		s.SetContent(win.x, win.y+i, ' ', nil, handleStyle)
	}
	win.tag.Draw(s)
	win.body.Draw(s)
}

func (win *Window) Resize(x, y, w, h int) {
	win.x, win.y, win.w, win.h = x, y, w, h
	win.tag.Resize(x+1, y, w-1, win.tagHeight())
	win.body.Resize(x+1, win.body.y, w-1, win.h-win.tagHeight())
}

func (win *Window) HandleEvent(ev tcell.Event) bool {
	if me, ok := ev.(*tcell.EventMouse); ok {
		_, my := me.Position()
		th := win.tagHeight()
		target := win.body
		if my < win.y+th {
			target = win.tag
		}

		target.HandleEvent(ev)
		if me.Buttons() == tcell.Button3 || me.Buttons() == tcell.Button2 {
			word := strings.TrimSpace(target.buffer.GetSelectedText())
			if word == "" {
				word = strings.TrimSpace(target.buffer.GetWordAt(target.buffer.cursor.x, target.buffer.cursor.y))
			}
			if word == "" {
				return false
			}

			if me.Buttons() == tcell.Button3 {
				if win.onExec != nil {
					return win.onExec(win.parent, win, word)
				}
			} else {
				fullPath := ""
				if win.editor != nil {
					fullPath = win.editor.resolvePathWithContext(win, word)
				} else {
					fullPath = resolvePath(word)
				}
				if _, err := os.Stat(fullPath); err == nil {
					if win.onExec != nil {
						return win.onExec(win.parent, win, "Look "+word)
					}
				}
				win.body.Search(word)
			}
		}
	}
	return false
}
