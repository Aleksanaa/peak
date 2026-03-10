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
	// After adding, we rely on Resize to set heights
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

	// 1. Proportional scaling for windows
	if c.lastHeight > 0 && c.lastHeight != h {
		ratio := float64(h) / float64(c.lastHeight)
		for _, win := range c.windows {
			if win.explicitHeight > 0 {
				win.explicitHeight = int(float64(win.explicitHeight) * ratio)
			}
		}
	}
	c.lastHeight = h

	// 2. Count explicit vs automatic windows
	totalExplicit, numAuto := 0, 0
	for _, win := range c.windows {
		if win.explicitHeight > 0 {
			totalExplicit += win.explicitHeight
		} else {
			numAuto++
		}
	}

	// 3. Redistribute if adding new windows to a full column
	availableH := h - 1
	if numAuto > 0 && totalExplicit >= availableH {
		// New windows should get a fair share (1/N total windows)
		targetTotalAuto := (availableH * numAuto) / len(c.windows)
		if targetTotalAuto < 2*numAuto {
			targetTotalAuto = 2 * numAuto
		}
		scale := float64(availableH-targetTotalAuto) / float64(totalExplicit)
		totalExplicit = 0
		for _, win := range c.windows {
			if win.explicitHeight > 0 {
				win.explicitHeight = int(float64(win.explicitHeight) * scale)
				totalExplicit += win.explicitHeight
			}
		}
	}

	// 4. Final layout
	autoH := 0
	if numAuto > 0 {
		autoH = (availableH - totalExplicit) / numAuto
		if autoH < 2 {
			autoH = 2
		}
	}

	yOffset := y + 1
	for i, win := range c.windows {
		winH := win.explicitHeight
		if winH <= 0 {
			winH = autoH
		}

		neededRemaining := 0
		for j := i + 1; j < len(c.windows); j++ {
			neededRemaining += c.windows[j].tagHeight() + 1
		}

		maxWinH := (y + h - yOffset) - neededRemaining
		if winH > maxWinH {
			winH = maxWinH
		}
		minWinH := win.tagHeight() + 1
		if winH < minWinH {
			winH = minWinH
		}

		if i == len(c.windows)-1 {
			winH = (y + h) - yOffset
		}
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
		if my != c.tag.y || mx == c.x {
			return false
		}
		word := c.tag.GetClickWord(mx, my)
		if word != "" {
			if me.Buttons() == tcell.Button3 { // Middle-click
				return c.onExec(c, nil, word)
			}
			if me.Buttons() == tcell.Button2 { // Right-click
				return c.editor.Plumb(nil, word)
			}
		}
		return c.tag.HandleEvent(ev)
	}
	return false
}
