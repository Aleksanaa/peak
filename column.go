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
	lastHeight    int
}

func NewColumn(x, y, w, h int, editor *Editor, onExec func(*Column, *Window, string) bool) *Column {
	tagStyle := tcell.StyleDefault.Background(editor.theme.ColTagBG).Foreground(editor.theme.ColTagFG)
	tag := NewTextView(" New Zerox Delcol ", x+1, y, w-1, 1, tagStyle, true, false)
	tag.theme = &editor.theme

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
	return newWin
}

func (c *Column) Draw(s tcell.Screen) {
	sepStyle := tcell.StyleDefault.Background(c.editor.theme.ScrollGutter).Foreground(c.editor.theme.Corner)
	cornerStyle := tcell.StyleDefault.Background(c.editor.theme.Corner).Foreground(tcell.ColorBlack)

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

	availableH := h - 1
	heights := distributeSpace(availableH, len(c.windows), func(i int) int {
		return c.windows[i].explicitHeight
	}, func(i int) int {
		return c.windows[i].tagHeight() + 1
	}, c.lastHeight, h)
	c.lastHeight = h

	yOffset := y + 1
	for i, win := range c.windows {
		winH := heights[i]
		win.explicitHeight = winH
		win.Resize(x, yOffset, w, winH)
		yOffset += winH
	}
}

func (c *Column) Contains(x, y int) bool {
	return x >= c.x && x < c.x+c.w && y >= c.y && y < c.y+c.h
}

func (c *Column) HandleEvent(ev tcell.Event) bool {
	if me, ok := ev.(*tcell.EventMouse); ok {
		mx, my := me.Position()
		buttons := me.Buttons()

		if my == c.tag.y {
			if mx == c.x && buttons == tcell.Button1 {
				c.editor.dragCol = c
				return false
			}
			if mx > c.x {
				word := c.tag.GetClickWord(mx, my)
				if word != "" {
					if buttons == tcell.Button3 { // Middle-click
						return c.onExec(c, nil, word)
					}
					if buttons == tcell.Button2 { // Right-click
						return c.editor.Plumb(nil, word)
					}
				}
				if buttons == tcell.Button1 {
					c.editor.dragView, c.editor.focusedView = c.tag, c.tag
				}
				return c.tag.HandleEvent(ev)
			}
		}

		for _, win := range c.windows {
			if win.Contains(mx, my) {
				if buttons == tcell.Button1 {
					if mx == win.x && my >= win.y && my < win.y+win.tagHeight() {
						c.editor.dragWin = win
						c.editor.ActivateWindow(win)
						c.editor.focusedView = win.tag
						return false
					}
					c.editor.ActivateWindow(win)
					if my < win.y+win.tagHeight() {
						c.editor.focusedView = win.tag
					}
					c.editor.dragView = c.editor.focusedView
				}
				return win.HandleEvent(ev)
			}
		}
	}
	return false
}
