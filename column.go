package main

import (
	"github.com/gdamore/tcell/v2"
)

type Column struct {
	tag     *TextView
	windows []*Window
	editor  *Editor
	x, y    int
	w, h    int
	onExec  func(*Column, *Window, string) bool
}

func NewColumn(x, y, w, h int, editor *Editor, onExec func(*Column, *Window, string) bool) *Column {
	// Catppuccin Macchiato Mantle: #1e2030, Sky: #91d7e3
	tagStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x1e2030)).Foreground(tcell.NewHexColor(0x91d7e3))
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
	logDebug("NewColumn: col=%p, x=%d", c, x)
	return c
}

func (c *Column) AddWindow(tagText, bodyText string) *Window {
	if tagText == "" {
		tagText = " [No Name] Get Put Snarf Zerox Del "
	}
	// Simple vertical tiling for now
	h := (c.h - 1)
	if len(c.windows) > 0 {
		h /= (len(c.windows) + 1)
		// Resize existing windows
		yOffset := c.y + 1
		for _, win := range c.windows {
			win.Resize(c.x, yOffset, c.w, h)
			yOffset += h
		}
	}

	newWin := NewWindow(tagText, bodyText, c, c.editor, c.x, c.y+c.h-h, c.w, h, c.onExec)
	c.windows = append(c.windows, newWin)
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
	c.tag.Resize(x+1, y, w-1, 1) // Offset by 1 for separator
	if len(c.windows) > 0 {
		winH := (h - 1) / len(c.windows)
		yOffset := y + 1
		for i, win := range c.windows {
			actualH := winH
			if i == len(c.windows)-1 {
				actualH = (y + h) - yOffset
			}
			win.Resize(x, yOffset, w, actualH)
			yOffset += actualH
		}
	}
}

func (c *Column) HandleEvent(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *tcell.EventMouse:
		mx, my := ev.Position()
		if mx == c.x {
			return false
		}
		if my == c.tag.y {
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
			if mx >= win.x && mx < win.x+win.w && my >= win.y && my < win.y+win.h {
				return win.HandleEvent(ev)
			}
		}
	case *tcell.EventKey:
		// Forward to active win if any, or just first one
		if len(c.windows) > 0 {
			return c.windows[0].HandleEvent(ev)
		}
	}
	return false
}
