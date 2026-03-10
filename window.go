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
	
	vrow := 0
	skip := tv.scroll
	
	for i, line := range tv.buffer.lines {
		// Calculate how many visual lines this buffer line takes
		visualLines := 1
		if tv.w > 0 && len(line) > 0 {
			visualLines = (len(line) + tv.w - 1) / tv.w
		}
		if len(line) == 0 { visualLines = 1 }

		for vl := 0; vl < visualLines; vl++ {
			if skip > 0 {
				skip--
				continue
			}
			if vrow >= tv.h {
				return
			}

			start := vl * tv.w
			end := start + tv.w
			if end > len(line) { end = len(line) }

			for col := 0; col < tv.w; col++ {
				char := ' '
				style := tv.style
				bx := start + col
				
				if bx < end {
					char = line[bx]
					if tv.buffer.IsSelected(bx, i) {
						style = selStyle
					}
				}
				s.SetContent(tv.x+col, tv.y+vrow, char, nil, style)
			}
			vrow++
		}
	}
	// Clear remaining visual lines
	for ; vrow < tv.h; vrow++ {
		for col := 0; col < tv.w; col++ {
			s.SetContent(tv.x+col, tv.y+vrow, ' ', nil, tv.style)
		}
	}
}

func (tv *TextView) ShowCursor(s tcell.Screen) {
	vrow := 0
	for i, line := range tv.buffer.lines {
		visualLines := 1
		if tv.w > 0 && len(line) > 0 {
			visualLines = (len(line) + tv.w - 1) / tv.w
		}
		if len(line) == 0 { visualLines = 1 }

		if i == tv.buffer.cursor.y {
			vl := 0
			cx := 0
			if tv.w > 0 {
				vl = tv.buffer.cursor.x / tv.w
				cx = tv.buffer.cursor.x % tv.w
			}
			
			realVRow := vrow + vl
			if realVRow >= tv.scroll && realVRow < tv.scroll+tv.h {
				s.ShowCursor(tv.x+cx, tv.y+(realVRow-tv.scroll))
			}
			return
		}
		vrow += visualLines
	}
}

func (tv *TextView) Resize(x, y, w, h int) {
	tv.x, tv.y, tv.w, tv.h = x, y, w, h
}

func (tv *TextView) HandleEvent(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *tcell.EventKey:
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
		// Basic scroll sync (could be improved)
		if !tv.singleLine {
			// Find visual row of cursor
			vrow := 0
			for i := 0; i < tv.buffer.cursor.y; i++ {
				vl := 1
				if tv.w > 0 && len(tv.buffer.lines[i]) > 0 {
					vl = (len(tv.buffer.lines[i]) + tv.w - 1) / tv.w
				}
				vrow += vl
			}
			cvl := 0
			if tv.w > 0 {
				cvl = tv.buffer.cursor.x / tv.w
			}
			vrow += cvl
			
			if vrow < tv.scroll {
				tv.scroll = vrow
			} else if vrow >= tv.scroll+tv.h {
				tv.scroll = vrow - tv.h + 1
			}
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
				// Simple check for max scroll
				totalVRows := 0
				for _, line := range tv.buffer.lines {
					vl := 1
					if tv.w > 0 && len(line) > 0 {
						vl = (len(line) + tv.w - 1) / tv.w
					}
					totalVRows += vl
				}
				if tv.scroll < totalVRows-1 {
					tv.scroll++
				}
				return false
			}
		}

		mx, my := ev.Position()
		if buttons == tcell.Button1 {
			// Translate screen Y to visual row
			targetVRow := my - tv.y + tv.scroll
			
			// Find buffer position from visual row
			currVRow := 0
			found := false
			for i, line := range tv.buffer.lines {
				vl := 1
				if tv.w > 0 && len(line) > 0 {
					vl = (len(line) + tv.w - 1) / tv.w
				}
				if targetVRow >= currVRow && targetVRow < currVRow+vl {
					offset := targetVRow - currVRow
					bx := offset * tv.w + (mx - tv.x)
					if bx > len(line) { bx = len(line) }
					if bx < 0 { bx = 0 }
					
					if !tv.drag {
						tv.drag = true
						tv.buffer.ClearSelection()
						tv.buffer.cursor.y = i
						tv.buffer.cursor.x = bx
						tv.buffer.SetSelection(tv.buffer.cursor, tv.buffer.cursor)
					} else {
						tv.buffer.cursor.y = i
						tv.buffer.cursor.x = bx
						tv.buffer.selectionEnd = &Cursor{bx, i}
					}
					found = true
					break
				}
				currVRow += vl
			}
			if !found && targetVRow >= currVRow {
				// Clicked below all text
				lastLine := len(tv.buffer.lines) - 1
				if lastLine < 0 { lastLine = 0 }
				bx := len(tv.buffer.lines[lastLine])
				if !tv.drag {
					tv.drag = true
					tv.buffer.ClearSelection()
					tv.buffer.cursor.y = lastLine
					tv.buffer.cursor.x = bx
					tv.buffer.SetSelection(tv.buffer.cursor, tv.buffer.cursor)
				} else {
					tv.buffer.cursor.y = lastLine
					tv.buffer.cursor.x = bx
					tv.buffer.selectionEnd = &Cursor{bx, lastLine}
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

type Window struct {
	tag    *TextView
	body   *TextView
	parent *Column
	x, y   int
	w, h   int
	onExec func(*Column, *Window, string) bool
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
	s.SetContent(win.x, win.tag.y, ' ', nil, handleStyle)
	win.tag.Draw(s)
	win.body.Draw(s)
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
			if ev.Buttons() == tcell.Button3 {
				word := win.body.buffer.GetSelectedText()
				if word == "" {
					// Word detection here needs to account for wrapping too...
					// For now, let's keep it simple and use raw buffer word detection
					// based on translated mouse coords.
					// HandleEvent already does translation for cursor, 
					// but GetWordAt needs physical bx, by.
					// We'll let HandleEvent set the cursor and then grab the word.
					win.body.HandleEvent(ev) // Update cursor
					word = win.body.buffer.GetWordAt(win.body.buffer.cursor.x, win.body.buffer.cursor.y)
				}
				if win.onExec != nil {
					return win.onExec(win.parent, win, word)
				}
			} else if ev.Buttons() == tcell.Button2 {
				word := win.body.buffer.GetSelectedText()
				if word == "" {
					win.body.HandleEvent(ev)
					word = win.body.buffer.GetWordAt(win.body.buffer.cursor.x, win.body.buffer.cursor.y)
				}
				win.body.Search(word)
				return false
			}
			return win.body.HandleEvent(ev)
		}
	}
	return false
}

func (tv *TextView) Search(word string) {
	if word == "" {
		return
	}
	// This search still works on buffer lines, which is correct.
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
