package main

import (
	"os"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/gdamore/tcell/v2"
)

const tabWidth = 4

type VisualLine struct {
	BufferLine int
	Start, End int
}

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
		visualPos, start := 0, 0
		for idx, r := range line {
			width := 1
			if r == '\t' {
				width = tabWidth - (visualPos % tabWidth)
			}
			if visualPos+width > tv.w && visualPos > 0 {
				tv.layout = append(tv.layout, VisualLine{i, start, idx})
				start, visualPos = idx, 0
				if r == '\t' {
					width = tabWidth
				}
			}
			visualPos += width
		}
		tv.layout = append(tv.layout, VisualLine{i, start, len(line)})
	}
}

// bufferToVisual translates a buffer position to visual coordinates (vx, vrow).
func (tv *TextView) bufferToVisual(bx, by int) (int, int) {
	for lidx, vl := range tv.layout {
		if vl.BufferLine == by && bx >= vl.Start && bx <= vl.End {
			vx := 0
			line := tv.buffer.lines[by]
			for i := vl.Start; i < bx; i++ {
				if line[i] == '\t' {
					vx += tabWidth - (vx % tabWidth)
				} else {
					vx++
				}
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
		w := 1
		if line[i] == '\t' {
			w = tabWidth - (currVX % tabWidth)
		}
		if currVX+w/2 > vx {
			break
		}
		currVX += w
		bx = i + 1
	}
	return bx, vl.BufferLine
}

func (tv *TextView) Draw(s tcell.Screen) {
	tv.UpdateLayout()
	if !tv.scrollable {
		tv.scroll = 0
	}
	selStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x6e738d)).Foreground(tcell.ColorWhite)
	vrow := 0
	for lidx := tv.scroll; lidx < len(tv.layout) && vrow < tv.h; lidx++ {
		vl := tv.layout[lidx]
		line := tv.buffer.lines[vl.BufferLine]
		vcol := 0
		for idx := vl.Start; idx < vl.End && vcol < tv.w; idx++ {
			r, style := line[idx], tv.style
			if tv.buffer.IsSelected(idx, vl.BufferLine) {
				style = selStyle
			}
			if r == '\t' {
				tw := tabWidth - (vcol % tabWidth)
				for k := 0; k < tw && vcol < tv.w; k++ {
					s.SetContent(tv.x+vcol, tv.y+vrow, ' ', nil, style)
					vcol++
				}
			} else {
				s.SetContent(tv.x+vcol, tv.y+vrow, r, nil, style)
				vcol++
			}
		}
		for ; vcol < tv.w; vcol++ {
			s.SetContent(tv.x+vcol, tv.y+vrow, ' ', nil, tv.style)
		}
		vrow++
	}
	for ; vrow < tv.h; vrow++ {
		for col := 0; col < tv.w; col++ {
			s.SetContent(tv.x+col, tv.y+vrow, ' ', nil, tv.style)
		}
	}
}

func (tv *TextView) ShowCursor(s tcell.Screen) {
	vx, vrow := tv.bufferToVisual(tv.buffer.cursor.x, tv.buffer.cursor.y)
	if vrow >= tv.scroll && vrow < tv.scroll+tv.h {
		s.ShowCursor(tv.x+vx, tv.y+(vrow-tv.scroll))
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
		case tcell.KeyCtrlZ:
			if ev.Modifiers()&tcell.ModShift != 0 {
				tv.buffer.Redo()
			} else {
				tv.buffer.Undo()
			}
		case tcell.KeyCtrlY:
			tv.buffer.Redo()
		case tcell.KeyCtrlC:
			if txt := tv.buffer.GetSelectedText(); txt != "" {
				clipboard.WriteAll(txt)
			}
		case tcell.KeyCtrlX:
			if txt := tv.buffer.GetSelectedText(); txt != "" {
				clipboard.WriteAll(txt)
				tv.buffer.DeleteSelection()
			}
		case tcell.KeyCtrlV:
			if txt, err := clipboard.ReadAll(); err == nil {
				if tv.buffer.selectionStart != nil {
					tv.buffer.DeleteSelection()
				}
				for _, r := range txt {
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
			tv.buffer.Delete()
		case tcell.KeyPgUp:
			tv.buffer.ClearSelection()
			tv.scroll -= tv.h
			if tv.scroll < 0 {
				tv.scroll = 0
			}
			_, vrow := tv.bufferToVisual(tv.buffer.cursor.x, tv.buffer.cursor.y)
			if vrow >= tv.scroll+tv.h {
				bx, by := tv.visualToBuffer(0, tv.scroll)
				tv.buffer.cursor = Cursor{bx, by}
			}
		case tcell.KeyPgDn:
			tv.buffer.ClearSelection()
			tv.scroll += tv.h
			if tv.scroll >= len(tv.layout) {
				tv.scroll = len(tv.layout) - 1
			}
			if tv.scroll < 0 {
				tv.scroll = 0
			}
			_, vrow := tv.bufferToVisual(tv.buffer.cursor.x, tv.buffer.cursor.y)
			if vrow < tv.scroll {
				bx, by := tv.visualToBuffer(0, tv.scroll)
				tv.buffer.cursor = Cursor{bx, by}
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
			if ev.Modifiers()&tcell.ModCtrl != 0 {
				tv.buffer.MoveWordLeft()
			} else {
				tv.buffer.MoveLeft()
			}
		case tcell.KeyRight:
			tv.buffer.ClearSelection()
			if ev.Modifiers()&tcell.ModCtrl != 0 {
				tv.buffer.MoveWordRight()
			} else {
				tv.buffer.MoveRight()
			}
		case tcell.KeyHome:
			tv.buffer.ClearSelection()
			tv.buffer.MoveHome()
		case tcell.KeyEnd:
			tv.buffer.ClearSelection()
			tv.buffer.MoveEnd()
		case tcell.KeyEnter:
			tv.buffer.ClearSelection()
			if !tv.singleLine {
				tv.buffer.NewLine()
			}
		case tcell.KeyTab:
			if tv.buffer.selectionStart != nil {
				tv.buffer.DeleteSelection()
			}
			tv.buffer.Insert('\t')
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
			bx, by := tv.visualToBuffer(mx-tv.x, my-tv.y+tv.scroll)
			if buttons == tcell.Button1 && !tv.drag && !tv.buffer.IsSelected(bx, by) {
				tv.buffer.ClearSelection()
			}
			if buttons == tcell.Button1 {
				if !tv.drag {
					tv.drag = true
					if !tv.buffer.IsSelected(bx, by) {
						tv.buffer.cursor = Cursor{bx, by}
						tv.buffer.SetSelection(tv.buffer.cursor, tv.buffer.cursor)
					}
				} else {
					tv.buffer.cursor = Cursor{bx, by}
					tv.buffer.selectionEnd = &Cursor{bx, by}
				}
			} else if tv.buffer.selectionStart == nil {
				tv.buffer.cursor = Cursor{bx, by}
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
	_, vrow := tv.bufferToVisual(tv.buffer.cursor.x, tv.buffer.cursor.y)
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
	sx, sy := tv.buffer.cursor.x+1, tv.buffer.cursor.y
	for y := sy; y < len(tv.buffer.lines); y++ {
		line := string(tv.buffer.lines[y])
		if sx >= len(line) {
			sx = 0
			continue
		}
		if x := strings.Index(line[sx:], word); x != -1 {
			tv.buffer.cursor = Cursor{sx + x, y}
			tv.buffer.ClearSelection()
			return
		}
		sx = 0
	}
}

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
	tagStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x1e1e2e)).Foreground(tcell.NewHexColor(0x89dceb))
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

func (win *Window) Contains(x, y int) bool {
	return x >= win.x && x < win.x+win.w && y >= win.y && y < win.y+win.h
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
