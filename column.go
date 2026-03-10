package main

import (
	"github.com/gdamore/tcell/v2"
)

type Column struct {
	tag           *TextView
	windows       []*Window
	editor        *Editor
	x, y          int
	w, h          int
	onExec        func(*Column, *Window, string) bool
	explicitWidth int
}

func NewColumn(x, y, w, h int, editor *Editor, onExec func(*Column, *Window, string) bool) *Column {
	// Column menu: #181825, menu Text: #89dceb
	tagStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x181825)).Foreground(tcell.NewHexColor(0x89dceb))
	tag := NewTextView(" New Zerox Delcol ", x+1, y, w-1, 1, tagStyle, true, false)

	c := &Column{
		tag:    tag,
		editor: editor,
		x:      x,
		y:      y,
		w:      w,
		h:      h,
		onExec: onExec,
	}
	return c
}

func (c *Column) AddWindow(tagText, bodyText string) *Window {
	if tagText == "" {
		tagText = " ./untitled.txt Get Put Undo Redo Snarf Zerox Del "
	}

	newWin := NewWindow(tagText, bodyText, c, c.editor, c.x, c.y, c.w, 0, c.onExec)
	c.windows = append(c.windows, newWin)
	// After adding, we rely on Resize to set heights
	return newWin
}

func (c *Column) Draw(s tcell.Screen) {
	// Catppuccin Macchiato Crust: #181926, Blue: #8aadf4
	sepStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x181926)).Foreground(tcell.NewHexColor(0x8aadf4))
	cornerStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x8aadf4)).Foreground(tcell.ColorBlack)

	// Draw vertical separator
	for y := c.y; y < c.y+c.h; y++ {
		style := sepStyle
		if y == c.y {
			style = cornerStyle
		}
		s.SetContent(c.x, y, ' ', nil, style)
	}

	c.tag.Draw(s)
	for _, win := range c.windows {
		win.Draw(s)
	}
}

func (c *Column) Resize(x, y, w, h int) {
	c.x, c.y, c.w, c.h = x, y, w, h
	c.tag.Resize(x+1, y, w-1, 1)
	if len(c.windows) == 0 {
		return
	}

	yOffset := y + 1
	availableH := h - 1

	totalExplicit := 0
	numAuto := 0
	for _, win := range c.windows {
		if win.explicitHeight > 0 {
			totalExplicit += win.explicitHeight
		} else {
			numAuto++
		}
	}

	autoH := 0
	if numAuto > 0 {
		autoH = (availableH - totalExplicit) / numAuto
		if autoH < 2 {
			autoH = 2
		}
	}

	for i, win := range c.windows {
		winH := win.explicitHeight
		if winH <= 0 {
			winH = autoH
		}

		if i == len(c.windows)-1 {
			winH = (y + h) - yOffset
		}

		if winH < 1 {
			winH = 1
		}
		win.Resize(x, yOffset, w, winH)
		yOffset += winH
	}
}

func (c *Column) Contains(x, y int) bool {
	return x >= c.x && x < c.x+c.w && y >= c.y && y < c.y+c.h
}

func (c *Column) HandleEvent(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *tcell.EventMouse:
		mx, my := ev.Position()
		if my == c.tag.y {
			if mx == c.x {
				return false
			}
			if ev.Buttons() == tcell.Button3 { // Middle-click
				word := c.tag.buffer.GetSelectedText()
				if word == "" {
					word = c.tag.buffer.GetWordAt(mx-c.tag.x, 0)
				}
				return c.onExec(c, nil, word)
			}
			return c.tag.HandleEvent(ev)
		}
		for _, win := range c.windows {
			if win.Contains(mx, my) {
				return win.HandleEvent(ev)
			}
		}
	case *tcell.EventKey:
		if len(c.windows) > 0 {
			return c.windows[0].HandleEvent(ev)
		}
	}
	return false
}
