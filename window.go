package main

import (
	"strings"

	"github.com/atotto/clipboard"
	"github.com/gdamore/tcell/v2"
)

type TextView struct {
	buffer     *Buffer
	x, y, w, h int
	style      tcell.Style
	scroll     int
	drag       bool
	singleLine bool
}

func NewTextView(text string, x, y, w, h int, style tcell.Style, singleLine bool) *TextView {
	return &TextView{
		buffer:     NewBuffer(text),
		x:          x,
		y:          y,
		w:          w,
		h:          h,
		style:      style,
		singleLine: singleLine,
	}
}

func (tv *TextView) Draw(s tcell.Screen) {
	selStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x6e738d)).Foreground(tcell.ColorWhite)
	for row := 0; row < tv.h; row++ {
		bufferRow := row + tv.scroll
		var line []rune
		if bufferRow < len(tv.buffer.lines) {
			line = tv.buffer.lines[bufferRow]
		}
		for col := 0; col < tv.w; col++ {
			char := ' '
			style := tv.style
			if tv.buffer.IsSelected(col, bufferRow) {
				style = selStyle
			}
			if col < len(line) {
				char = line[col]
			}
			s.SetContent(tv.x+col, tv.y+row, char, nil, style)
		}
	}
}

func (tv *TextView) Resize(x, y, w, h int) {
	tv.x, tv.y, tv.w, tv.h = x, y, w, h
}

func (tv *TextView) HandleEvent(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *tcell.EventKey:
		logDebug("Key Event: key=%v rune=%v mod=%v", ev.Key(), ev.Rune(), ev.Modifiers())
		switch ev.Key() {
		case tcell.KeyCtrlC:
			text := tv.buffer.GetSelectedText()
			if text != "" {
				clipboard.WriteAll(text)
			}
			return false
		case tcell.KeyCtrlX:
			text := tv.buffer.GetSelectedText()
			if text != "" {
				clipboard.WriteAll(text)
				tv.buffer.DeleteSelection()
			}
			return false
		case tcell.KeyCtrlV:
			text, err := clipboard.ReadAll()
			logDebug("Paste: len=%d err=%v", len(text), err)
			if err == nil {
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
			return false
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
		// Adjust scroll
		if tv.buffer.cursor.y < tv.scroll {
			tv.scroll = tv.buffer.cursor.y
		} else if tv.buffer.cursor.y >= tv.scroll+tv.h {
			tv.scroll = tv.buffer.cursor.y - tv.h + 1
		}
		return false
	case *tcell.EventMouse:
		buttons := ev.Buttons()
		if !tv.singleLine {
			if buttons&tcell.WheelUp != 0 {
				if tv.scroll > 0 {
					tv.scroll--
				}
				return false
			}
			if buttons&tcell.WheelDown != 0 {
				if tv.scroll < len(tv.buffer.lines)-1 {
					tv.scroll++
				}
				return false
			}
		}

		mx, my := ev.Position()
		bx := mx - tv.x
		by := my - tv.y + tv.scroll

		// Clamp coordinates to TextView boundaries for internal logic
		if bx < 0 { bx = 0 }
		if bx >= tv.w { bx = tv.w - 1 }
		if by < 0 { by = 0 }
		if by >= len(tv.buffer.lines) { by = len(tv.buffer.lines) - 1 }

		if buttons == tcell.Button1 {
			if !tv.drag {
				tv.drag = true
				tv.buffer.cursor.y = by
				tv.buffer.cursor.x = bx
				// Further clamp to actual line length
				if tv.buffer.cursor.x > len(tv.buffer.lines[tv.buffer.cursor.y]) {
					tv.buffer.cursor.x = len(tv.buffer.lines[tv.buffer.cursor.y])
				}
				tv.buffer.SetSelection(tv.buffer.cursor, tv.buffer.cursor)
			} else {
				tv.buffer.cursor.y = by
				tv.buffer.cursor.x = bx
				if tv.buffer.cursor.x > len(tv.buffer.lines[tv.buffer.cursor.y]) {
					tv.buffer.cursor.x = len(tv.buffer.lines[tv.buffer.cursor.y])
				}
				tv.buffer.selectionEnd = &Cursor{tv.buffer.cursor.x, tv.buffer.cursor.y}
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

type Window struct {
	tag      *TextView
	body     *TextView
	parent   *Column
	x, y     int
	w, h     int
	onExec   func(*Column, *Window, string) bool
	focusTag bool
}

func NewWindow(tagText, bodyText string, parent *Column, x, y, w, h int, onExec func(*Column, *Window, string) bool) *Window {
	tagStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x1e2030)).Foreground(tcell.NewHexColor(0x91d7e3))
	bodyStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x24273a)).Foreground(tcell.NewHexColor(0xcad3f5))

	tag := NewTextView(tagText, x+1, y, w-1, 1, tagStyle, true)
	body := NewTextView(bodyText, x+1, y+1, w-1, h-1, bodyStyle, false)

	return &Window{
		tag:    tag,
		body:   body,
		parent: parent,
		x:      x,
		y:      y,
		w:      w,
		h:      h,
		onExec: onExec,
	}
}

func (win *Window) GetFilename() string {
	if len(win.tag.buffer.lines) == 0 {
		return ""
	}
	line := win.tag.buffer.lines[0]
	start := 0
	for start < len(line) && (line[start] == ' ' || line[start] == '\t') {
		start++
	}
	if start >= len(line) {
		return ""
	}
	end := start
	for end < len(line) && isWordChar(line[end]) {
		end++
	}
	return string(line[start:end])
}

func (win *Window) Draw(s tcell.Screen) {
	handleStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0xb7bdf8)).Foreground(tcell.ColorBlack)
	if win.focusTag {
		handleStyle = tcell.StyleDefault.Background(tcell.NewHexColor(0x91d7e3)).Foreground(tcell.ColorBlack)
	}
	s.SetContent(win.x, win.tag.y, ' ', nil, handleStyle)

	win.tag.Draw(s)
	win.body.Draw(s)

	if win.focusTag {
		s.ShowCursor(win.tag.x+win.tag.buffer.cursor.x, win.tag.y)
	} else {
		if win.body.buffer.cursor.y >= win.body.scroll && win.body.buffer.cursor.y < win.body.scroll+win.body.h {
			cx := win.body.x + win.body.buffer.cursor.x
			cy := win.body.y + win.body.buffer.cursor.y - win.body.scroll
			s.ShowCursor(cx, cy)
		}
	}
}

func (win *Window) Resize(x, y, w, h int) {
	win.x, win.y, win.w, win.h = x, y, w, h
	win.tag.Resize(x+1, y, w-1, 1)
	win.body.Resize(x+1, y+1, w-1, h-1)
}

func (win *Window) HandleEvent(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *tcell.EventMouse:
		mx, my := ev.Position()
		if my == win.tag.y {
			if ev.Buttons() == tcell.Button1 {
				win.focusTag = true
			}
			if ev.Buttons() == tcell.Button3 {
				word := win.tag.buffer.GetSelectedText()
				if word == "" {
					word = win.tag.buffer.GetWordAt(mx-win.tag.x, 0)
				}
				if win.onExec != nil {
					return win.onExec(win.parent, win, word)
				}
			} else if ev.Buttons() == tcell.Button2 {
				word := win.tag.buffer.GetSelectedText()
				if word == "" {
					word = win.tag.buffer.GetWordAt(mx-win.tag.x, 0)
				}
				win.body.Search(word)
				return false
			}
			return win.tag.HandleEvent(ev)
		} else if my >= win.body.y && my < win.body.y+win.body.h {
			if ev.Buttons() == tcell.Button1 {
				win.focusTag = false
			}
			if ev.Buttons() == tcell.Button3 {
				word := win.body.buffer.GetSelectedText()
				if word == "" {
					word = win.body.buffer.GetWordAt(mx-win.body.x, my-win.body.y+win.body.scroll)
				}
				if win.onExec != nil {
					return win.onExec(win.parent, win, word)
				}
			} else if ev.Buttons() == tcell.Button2 {
				word := win.body.buffer.GetSelectedText()
				if word == "" {
					word = win.body.buffer.GetWordAt(mx-win.body.x, my-win.body.y+win.body.scroll)
				}
				win.body.Search(word)
				return false
			}
			return win.body.HandleEvent(ev)
		}
	case *tcell.EventKey:
		if win.focusTag {
			return win.tag.HandleEvent(ev)
		}
		return win.body.HandleEvent(ev)
	}
	return false
}

func (tv *TextView) Search(word string) {
	if word == "" {
		return
	}
	startX := tv.buffer.cursor.x + 1
	startY := tv.buffer.cursor.y
	for y := startY; y < len(tv.buffer.lines); y++ {
		line := string(tv.buffer.lines[y])
		x := strings.Index(line[startX:], word)
		if x != -1 {
			tv.buffer.cursor.y = y
			tv.buffer.cursor.x = startX + x
			tv.buffer.ClearSelection()
			return
		}
		startX = 0
	}
}
