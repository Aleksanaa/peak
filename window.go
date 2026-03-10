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
	scrollable bool
}

func NewTextView(text string, x, y, w, h int, style tcell.Style, singleLine bool, scrollable bool) *TextView {
	return &TextView{
		buffer:     NewBuffer(text),
		x:          x,
		y:          y,
		w:          w,
		h:          h,
		style:      style,
		singleLine: singleLine,
		scrollable: scrollable,
	}
}

func (tv *TextView) Draw(s tcell.Screen) {
	selStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x6e738d)).Foreground(tcell.ColorWhite)
	
	if !tv.scrollable {
		tv.scroll = 0
	}

	vrow := 0
	skip := tv.scroll
	
	for i, line := range tv.buffer.lines {
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
				goto done
			}

			start := vl * tv.w
			end := start + tv.w
			if end > len(line) { end = len(line) }

			for col := 0; col < tv.w; col++ {
				char := ' '
				style := tv.style
				bx := start + col
				
				if tv.buffer.IsSelected(bx, i) {
					style = selStyle
				}

				if bx < end {
					char = line[bx]
				}
				s.SetContent(tv.x+col, tv.y+vrow, char, nil, style)
			}
			vrow++
		}
	}
done:
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
		
		if tv.scrollable {
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
		} else {
			tv.scroll = 0
		}
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
		
		if buttons != tcell.ButtonNone {
			targetVRow := my - tv.y + tv.scroll
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
					
					// If clicking outside existing selection, clear it
					if buttons == tcell.Button1 && !tv.drag && !tv.buffer.IsSelected(bx, i) {
						tv.buffer.ClearSelection()
					}

					if buttons == tcell.Button1 {
						if !tv.drag {
							tv.drag = true
							if !tv.buffer.IsSelected(bx, i) {
								tv.buffer.cursor.y = i
								tv.buffer.cursor.x = bx
								tv.buffer.SetSelection(tv.buffer.cursor, tv.buffer.cursor)
							}
						} else {
							tv.buffer.cursor.y = i
							tv.buffer.cursor.x = bx
							tv.buffer.selectionEnd = &Cursor{bx, i}
						}
					} else {
						if tv.buffer.selectionStart == nil {
							tv.buffer.cursor.y = i
							tv.buffer.cursor.x = bx
						}
					}
					found = true
					break
				}
				currVRow += vl
			}
			if !found && targetVRow >= currVRow {
				lastLine := len(tv.buffer.lines) - 1
				if lastLine < 0 { lastLine = 0 }
				bx := len(tv.buffer.lines[lastLine])
				
				if buttons == tcell.Button1 && !tv.drag && !tv.buffer.IsSelected(bx, lastLine) {
					tv.buffer.ClearSelection()
				}

				if buttons == tcell.Button1 {
					if !tv.drag {
						tv.drag = true
						if !tv.buffer.IsSelected(bx, lastLine) {
							tv.buffer.cursor.y = lastLine
							tv.buffer.cursor.x = bx
							tv.buffer.SetSelection(tv.buffer.cursor, tv.buffer.cursor)
						}
					} else {
						tv.buffer.cursor.y = lastLine
						tv.buffer.cursor.x = bx
						tv.buffer.selectionEnd = &Cursor{bx, lastLine}
					}
				} else {
					if tv.buffer.selectionStart == nil {
						tv.buffer.cursor.y = lastLine
						tv.buffer.cursor.x = bx
					}
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

	tag := NewTextView(tagText, x+1, y, w-1, 1, tagStyle, false, false)
	body := NewTextView(bodyText, x+1, y+1, w-1, h-1, bodyStyle, false, true)

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
	line := string(win.tag.buffer.lines[0])
	fields := strings.Fields(line)
	if len(fields) > 0 {
		return fields[0]
	}
	return ""
}

func (win *Window) tagHeight() int {
	totalVRows := 0
	for _, line := range win.tag.buffer.lines {
		if win.tag.w > 0 && len(line) > 0 {
			totalVRows += (len(line) + win.tag.w - 1) / win.tag.w
		} else {
			totalVRows++
		}
	}
	if totalVRows < 1 { return 1 }
	return totalVRows
}

func (win *Window) Draw(s tcell.Screen) {
	th := win.tagHeight()
	win.tag.h = th
	win.body.y = win.y + th
	win.body.h = win.h - th
	if win.body.h < 0 {
		win.body.h = 0
	}

	handleStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0xb7bdf8)).Foreground(tcell.ColorBlack)
	for i := 0; i < th; i++ {
		s.SetContent(win.x, win.y+i, ' ', nil, handleStyle)
	}
	win.tag.Draw(s)
	win.body.Draw(s)
}

func (win *Window) Resize(x, y, w, h int) {
	win.x, win.y, win.w, win.h = x, y, w, h
	th := win.tagHeight()
	win.tag.Resize(x+1, y, w-1, th)
	win.body.Resize(x+1, y+th, w-1, h-th)
}

func (win *Window) HandleEvent(ev tcell.Event) bool {
	th := win.tagHeight()
	switch ev := ev.(type) {
	case *tcell.EventMouse:
		_, my := ev.Position()
		if my >= win.y && my < win.y+th {
			win.tag.HandleEvent(ev) // Always move cursor on click
			if ev.Buttons() == tcell.Button3 {
				word := win.tag.buffer.GetSelectedText()
				if word == "" {
					word = win.tag.buffer.GetWordAt(win.tag.buffer.cursor.x, win.tag.buffer.cursor.y)
				}
				if win.onExec != nil {
					return win.onExec(win.parent, win, word)
				}
			} else if ev.Buttons() == tcell.Button2 {
				word := win.tag.buffer.GetSelectedText()
				if word == "" {
					word = win.tag.buffer.GetWordAt(win.tag.buffer.cursor.x, win.tag.buffer.cursor.y)
				}
				win.body.Search(word)
				return false
			}
			return false
		} else if my >= win.y+th && my < win.y+win.h {
			win.body.HandleEvent(ev) // Always move cursor on click
			if ev.Buttons() == tcell.Button3 {
				word := win.body.buffer.GetSelectedText()
				if word == "" {
					word = win.body.buffer.GetWordAt(win.body.buffer.cursor.x, win.body.buffer.cursor.y)
				}
				if win.onExec != nil {
					return win.onExec(win.parent, win, word)
				}
			} else if ev.Buttons() == tcell.Button2 {
				word := win.body.buffer.GetSelectedText()
				if word == "" {
					word = win.body.buffer.GetWordAt(win.body.buffer.cursor.x, win.body.buffer.cursor.y)
				}
				win.body.Search(word)
				return false
			}
			return false
		}
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
