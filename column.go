package main

import (
	"github.com/gdamore/tcell/v2"
)

type Column struct {
	tag     *Tag
	windows []*Window
	x, y    int
	w, h    int
	onExec  func(*Window, string) bool
}

func NewColumn(x, y, w, h int, onExec func(*Window, string) bool) *Column {
	tagStyle := tcell.StyleDefault.Background(tcell.ColorPaleTurquoise).Foreground(tcell.ColorBlack)
	tag := &Tag{
		buffer: NewBuffer(" NewCol | Exit "),
		x:      x,
		y:      y,
		w:      w,
		h:      1,
		style:  tagStyle,
	}

	return &Column{
		tag:    tag,
		x:      x,
		y:      y,
		w:      w,
		h:      h,
		onExec: onExec,
	}
}

func (c *Column) AddWindow(tagText, bodyText string) *Window {
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

	newWin := NewWindow(tagText, bodyText, c.x, c.y+c.h-h, c.w, h, c.onExec)
	c.windows = append(c.windows, newWin)
	return newWin
}

func (c *Column) Draw(s tcell.Screen) {
	c.tag.Draw(s)
	for _, win := range c.windows {
		win.Draw(s)
	}
}

func (c *Column) Resize(x, y, w, h int) {
	c.x, c.y, c.w, c.h = x, y, w, h
	c.tag.Resize(x, y, w, 1)
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
		if my == c.tag.y {
			if ev.Buttons() == tcell.Button3 { // Middle-click
				word := c.tag.buffer.GetWordAt(mx-c.x, 0)
				return c.onExec(nil, word)
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
