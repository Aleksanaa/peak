package main

import (
	"strings"

	"github.com/gdamore/tcell/v2"
)

type Window struct {
	tag      *Tag
	body     *Body
	parent   *Column
	x, y     int
	w, h     int
	onExec   func(*Column, *Window, string) bool
	focusTag bool
}

func NewWindow(tagText, bodyText string, parent *Column, x, y, w, h int, onExec func(*Column, *Window, string) bool) *Window {
	tagStyle := tcell.StyleDefault.Background(tcell.ColorPaleTurquoise).Foreground(tcell.ColorBlack)
	bodyStyle := tcell.StyleDefault.Background(tcell.ColorNavajoWhite).Foreground(tcell.ColorBlack)

	tag := &Tag{
		buffer: NewBuffer(tagText),
		x:      x + 1,
		y:      y,
		w:      w - 1,
		h:      1,
		style:  tagStyle,
	}

	body := &Body{
		buffer: NewBuffer(bodyText),
		x:      x + 1,
		y:      y + 1,
		w:      w - 1,
		h:      h - 1,
		style:  bodyStyle,
	}

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
	// Skip leading spaces
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
	// Draw window handle square on the vertical separator line
	handleStyle := tcell.StyleDefault.Background(tcell.ColorSteelBlue).Foreground(tcell.ColorBlack)
	if win.focusTag {
		handleStyle = tcell.StyleDefault.Background(tcell.ColorRoyalBlue).Foreground(tcell.ColorBlack)
	}
	s.SetContent(win.x, win.tag.y, ' ', nil, handleStyle)

	win.tag.Draw(s)
	win.body.Draw(s)
	// Cursor management
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
	win.tag.Resize(x+1, y, w-1, 1) // Offset by 1 for handle on vertical line
	win.body.Resize(x+1, y+1, w-1, h-1) // Offset body too
}

func (win *Window) HandleEvent(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *tcell.EventMouse:
		mx, my := ev.Position()
		if my == win.tag.y {
			if ev.Buttons() == tcell.Button1 {
				win.focusTag = true
			}
			if ev.Buttons() == tcell.Button3 { // Middle-click (100) -> Execute
				word := win.tag.buffer.GetSelectedText()
				if word == "" {
					word = win.tag.buffer.GetWordAt(mx-win.tag.x, 0)
				}
				if win.onExec != nil {
					return win.onExec(win.parent, win, word)
				}
			} else if ev.Buttons() == tcell.Button2 { // Right-click (10) -> Search
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
			if ev.Buttons() == tcell.Button3 { // Middle-click (100) -> Execute
				word := win.body.buffer.GetSelectedText()
				if word == "" {
					word = win.body.buffer.GetWordAt(mx-win.body.x, my-win.body.y+win.body.scroll)
				}
				if win.onExec != nil {
					return win.onExec(win.parent, win, word)
				}
			} else if ev.Buttons() == tcell.Button2 { // Right-click (10) -> Search
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

type Tag struct {
	buffer *Buffer
	x, y   int
	w, h   int
	style  tcell.Style
	drag   bool
}

func (t *Tag) Draw(s tcell.Screen) {
	selStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x6e738d)).Foreground(tcell.ColorWhite)
	for i := 0; i < t.w; i++ {
		char := ' '
		style := t.style
		if t.buffer.IsSelected(i, 0) {
			style = selStyle
		}
		s.SetContent(t.x+i, t.y, char, nil, style)
	}
	if len(t.buffer.lines) > 0 {
		line := t.buffer.lines[0]
		for i, r := range line {
			if i < t.w {
				style := t.style
				if t.buffer.IsSelected(i, 0) {
					style = selStyle
				}
				s.SetContent(t.x+i, t.y, r, nil, style)
			}
		}
	}
}

func (t *Tag) Resize(x, y, w, h int) {
	t.x, t.y, t.w, t.h = x, y, w, h
}

func (t *Tag) HandleEvent(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *tcell.EventKey:
		t.buffer.ClearSelection()
		switch ev.Key() {
		case tcell.KeyLeft:
			t.buffer.MoveLeft()
		case tcell.KeyRight:
			t.buffer.MoveRight()
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			t.buffer.Backspace()
		case tcell.KeyRune:
			t.buffer.Insert(ev.Rune())
		}
	case *tcell.EventMouse:
		mx, _ := ev.Position()
		bx := mx - t.x
		if ev.Buttons() == tcell.Button1 {
			if !t.drag {
				t.drag = true
				t.buffer.cursor.x = bx
				t.buffer.SetSelection(Cursor{bx, 0}, Cursor{bx, 0})
			} else {
				t.buffer.selectionEnd = &Cursor{bx, 0}
				t.buffer.cursor.x = bx
			}
		} else {
			t.drag = false
			// If selection is just a point, clear it
			if t.buffer.selectionStart != nil && t.buffer.selectionEnd != nil {
				if *t.buffer.selectionStart == *t.buffer.selectionEnd {
					t.buffer.ClearSelection()
				}
			}
		}
	}
	return false
}

type Body struct {
	buffer *Buffer
	x, y   int
	w, h   int
	style  tcell.Style
	scroll int
	drag   bool
}

func (b *Body) Search(word string) {
	if word == "" {
		return
	}
	// Simple search from current cursor down
	startX := b.buffer.cursor.x + 1
	startY := b.buffer.cursor.y

	for y := startY; y < len(b.buffer.lines); y++ {
		line := string(b.buffer.lines[y])
		x := strings.Index(line[startX:], word)
		if x != -1 {
			b.buffer.cursor.y = y
			b.buffer.cursor.x = startX + x
			b.buffer.ClearSelection() // Clear selection on search result
			return
		}
		startX = 0
	}
}

func (b *Body) Draw(s tcell.Screen) {
	selStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x6e738d)).Foreground(tcell.ColorWhite)
	for row := 0; row < b.h; row++ {
		bufferRow := row + b.scroll
		var line []rune
		if bufferRow < len(b.buffer.lines) {
			line = b.buffer.lines[bufferRow]
		}
		for col := 0; col < b.w; col++ {
			char := ' '
			style := b.style
			if b.buffer.IsSelected(col, bufferRow) {
				style = selStyle
			}
			if col < len(line) {
				char = line[col]
			}
			s.SetContent(b.x+col, b.y+row, char, nil, style)
		}
	}
	if b.buffer.cursor.y >= b.scroll && b.buffer.cursor.y < b.scroll+b.h {
		cx := b.x + b.buffer.cursor.x
		cy := b.y + b.buffer.cursor.y - b.scroll
		s.ShowCursor(cx, cy)
	}
}

func (b *Body) Resize(x, y, w, h int) {
	b.x, b.y, b.w, b.h = x, y, w, h
}

func (b *Body) HandleEvent(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *tcell.EventKey:
		b.buffer.ClearSelection()
		switch ev.Key() {
		case tcell.KeyUp:
			b.buffer.MoveUp()
		case tcell.KeyDown:
			b.buffer.MoveDown()
		case tcell.KeyLeft:
			b.buffer.MoveLeft()
		case tcell.KeyRight:
			b.buffer.MoveRight()
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			b.buffer.Backspace()
		case tcell.KeyEnter:
			b.buffer.NewLine()
		case tcell.KeyRune:
			b.buffer.Insert(ev.Rune())
		}
		if b.buffer.cursor.y < b.scroll {
			b.scroll = b.buffer.cursor.y
		} else if b.buffer.cursor.y >= b.scroll+b.h {
			b.scroll = b.buffer.cursor.y - b.h + 1
		}
	case *tcell.EventMouse:
		buttons := ev.Buttons()
		if buttons&tcell.WheelUp != 0 {
			if b.scroll > 0 {
				b.scroll--
			}
			return false
		}
		if buttons&tcell.WheelDown != 0 {
			if b.scroll < len(b.buffer.lines)-1 {
				b.scroll++
			}
			return false
		}
		
		mx, my := ev.Position()
		bx := mx - b.x
		by := my - b.y + b.scroll

		if buttons == tcell.Button1 {
			if !b.drag {
				b.drag = true
				b.buffer.cursor.y = by
				b.buffer.cursor.x = bx
				if b.buffer.cursor.y >= len(b.buffer.lines) {
					b.buffer.cursor.y = len(b.buffer.lines) - 1
				}
				if b.buffer.cursor.y < 0 {
					b.buffer.cursor.y = 0
				}
				if b.buffer.cursor.x > len(b.buffer.lines[b.buffer.cursor.y]) {
					b.buffer.cursor.x = len(b.buffer.lines[b.buffer.cursor.y])
				}
				if b.buffer.cursor.x < 0 {
					b.buffer.cursor.x = 0
				}
				b.buffer.SetSelection(b.buffer.cursor, b.buffer.cursor)
			} else {
				b.buffer.cursor.y = by
				b.buffer.cursor.x = bx
				if b.buffer.cursor.y >= len(b.buffer.lines) {
					b.buffer.cursor.y = len(b.buffer.lines) - 1
				}
				if b.buffer.cursor.y < 0 {
					b.buffer.cursor.y = 0
				}
				if b.buffer.cursor.x > len(b.buffer.lines[b.buffer.cursor.y]) {
					b.buffer.cursor.x = len(b.buffer.lines[b.buffer.cursor.y])
				}
				if b.buffer.cursor.x < 0 {
					b.buffer.cursor.x = 0
				}
				b.buffer.selectionEnd = &Cursor{b.buffer.cursor.x, b.buffer.cursor.y}
			}
		} else {
			b.drag = false
			if b.buffer.selectionStart != nil && b.buffer.selectionEnd != nil {
				if *b.buffer.selectionStart == *b.buffer.selectionEnd {
					b.buffer.ClearSelection()
				}
			}
		}
	}
	return false
}
