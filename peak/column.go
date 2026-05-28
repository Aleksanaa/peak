package main

import (
	"github.com/aleksana/peak/internal/session"
	"github.com/aleksana/peak/peak/tview"
	"github.com/gdamore/tcell/v2"
)

type bodyWithGutter struct {
	bg     *tview.Box
	handle *tview.Box
	flex   *tview.Flex
	rect   [4]int
}

func (b *bodyWithGutter) Draw(s tcell.Screen) {
	b.bg.Draw(s)
	b.handle.Draw(s)
	b.flex.Draw(s)
}

func (b *bodyWithGutter) GetRect() (int, int, int, int) {
	return b.rect[0], b.rect[1], b.rect[2], b.rect[3]
}

func (b *bodyWithGutter) SetRect(x, y, w, h int) {
	b.rect[0], b.rect[1], b.rect[2], b.rect[3] = x, y, w, h
	b.bg.SetRect(x, y, w, h)
	b.handle.SetRect(x, y, 1, h)
	b.flex.SetRect(x, y, w, h)
}

type Column struct {
	tag        *TextView
	windows    []*Window
	editor     *Editor
	onExec     func(*Column, *Window, string) bool
	lastHeight int

	tagRowFlex  *tview.Flex
	tagHandle   *tview.Box
	bodyArea    *bodyWithGutter
	winFlex     *tview.Flex
	colRootFlex *tview.Flex
}

func (c *Column) GetRect() (int, int, int, int) { return c.colRootFlex.GetRect() }
func (c *Column) SetRect(x, y, w, h int) {
	c.colRootFlex.SetRect(x, y, w, h)

	scaleRatio := 1.0
	if c.lastHeight > 0 && c.lastHeight != h {
		scaleRatio = float64(h) / float64(c.lastHeight)
	}
	c.lastHeight = h

	sizes := make(map[*Window]int)
	if c.winFlex.ItemCount() == len(c.windows) {
		for _, win := range c.windows {
			if fixed, _ := c.winFlex.GetItemSize(win); fixed > 0 {
				sizes[win] = fixed
			}
		}
	}

	c.winFlex.Clear()
	for _, win := range c.windows {
		fixedSize := sizes[win]
		if fixedSize > 0 && scaleRatio != 1.0 {
			fixedSize = int(float64(fixedSize)*scaleRatio + 0.5)
			fixedSize = max(fixedSize, win.tagHeight()+1)
		}
		if fixedSize > 0 {
			c.winFlex.AddItem(win, fixedSize, 0)
		} else {
			c.winFlex.AddItem(win, 0, 1)
			c.winFlex.SetMinSize(win, win.tagHeight()+1)
		}
	}
	c.colRootFlex.Layout()
	c.winFlex.Layout()
}
func (c *Column) InRect(x, y int) bool {
	rx, ry, rw, rh := c.GetRect()
	return x >= rx && x < rx+rw && y >= ry && y < ry+rh
}
func (c *Column) Reflow() {
	rx, ry, rw, rh := c.GetRect()
	c.SetRect(rx, ry, rw, rh)
}

func NewColumn(x, y, w, h int, editor *Editor, onExec func(*Column, *Window, string) bool) *Column {
	tagStyle := tcell.StyleDefault.Background(editor.theme.ColTagBG).Foreground(editor.theme.ColTagFG)
	tag := NewTextView(" New Zerox Win Delcol ", 0, 0, 0, 0, tagStyle, true, false)
	tag.theme = &editor.theme

	tagHandle := tview.NewBox()
	tagHandle.SetBackgroundColor(editor.theme.HandleColumn)

	bodyHandle := tview.NewBox()
	bodyHandle.SetBackgroundColor(editor.theme.ScrollGutter)

	bgBox := tview.NewBox()
	bgBox.SetBackgroundColor(tcell.ColorDefault)

	winFlex := tview.NewFlex()
	winFlex.SetDirection(tview.FlexRow)
	winFlex.SetDontClear(true)

	tagRowFlex := tview.NewFlex()
	tagRowFlex.SetDirection(tview.FlexColumn)
	tagRowFlex.AddItem(tagHandle, 1, 0)
	tagRowFlex.AddItem(tag, 0, 1)

	bodyArea := &bodyWithGutter{bg: bgBox, handle: bodyHandle, flex: winFlex}

	colRootFlex := tview.NewFlex()
	colRootFlex.SetDirection(tview.FlexRow)
	colRootFlex.AddItem(tagRowFlex, 1, 0)
	colRootFlex.AddItem(bodyArea, 0, 1)

	c := &Column{
		tag:         tag,
		editor:      editor,
		onExec:      onExec,
		tagRowFlex:  tagRowFlex,
		tagHandle:   tagHandle,
		bodyArea:    bodyArea,
		winFlex:     winFlex,
		colRootFlex: colRootFlex,
	}
	return c
}

func (c *Column) AddWindow(tagText, bodyText string) *Window {
	if tagText == "" {
		tagText = " ./untitled.txt Get Put Undo Redo Snarf Zerox Del "
	}

	newWin := NewWindow(tagText, bodyText, c, c.editor, 0, 0, 0, 0, c.onExec)
	newWin.ID = c.editor.nextWinID
	c.editor.nextWinID++
	c.windows = append(c.windows, newWin)
	c.editor.ninep.MountWindow(newWin)
	return newWin
}

func (c *Column) AddTermWindow(tagText, cmd, dir string) (*Window, error) {
	if tagText == "" {
		tagPath := join(dir, "+Errors")
		tagText = " " + tagPath + " Zerox Del "
	}

	newWin, err := NewTermWindow(tagText, c, c.editor, 0, 0, 0, 0, cmd, dir, c.onExec)
	if err != nil {
		return nil, err
	}
	newWin.ID = c.editor.nextWinID
	c.editor.nextWinID++
	c.windows = append(c.windows, newWin)
	c.editor.ninep.MountWindow(newWin)
	return newWin, nil
}

func (c *Column) AddSessionTermWindow(title string, sess session.Session) (*Window, error) {
	newWin, err := newTermWindowFromSession(" "+title+" Zerox Del ", sess, c, c.editor, 0, 0, 0, 0, c.onExec)
	if err != nil {
		return nil, err
	}
	newWin.ID = c.editor.nextWinID
	c.editor.nextWinID++
	c.windows = append(c.windows, newWin)
	c.editor.ninep.MountWindow(newWin)
	return newWin, nil
}

func (c *Column) Draw(s tcell.Screen) {
	c.colRootFlex.Draw(s)
}

func (c *Column) HandleEvent(ev tcell.Event) bool {
	if me, ok := ev.(*tcell.EventMouse); ok {
		mx, my := me.Position()
		buttons := me.Buttons()

		if c.tagHandle.InRect(mx, my) && buttons == tcell.Button1 {
			c.editor.dragCol = c
			return false
		}
		rx, _, _, _ := c.GetRect()
		if my == c.tag.y && mx > rx {
			word := c.tag.GetClickWord(mx, my)
			if word != "" {
				if buttons == tcell.Button3 {
					return c.onExec(c, nil, word)
				}
				if buttons == tcell.Button2 {
					return c.editor.Plumb(nil, word)
				}
			}
			if buttons == tcell.Button1 {
				c.editor.dragView, c.editor.focusedView = c.tag, c.tag
			}
			return c.tag.HandleEvent(ev)
		}

		for _, win := range c.windows {
			rx, ry, rw, rh := win.GetRect()
			if mx >= rx && mx < rx+rw && my >= ry && my < ry+rh {
				if buttons == tcell.Button1 {
					if win.handleBox.InRect(mx, my) {
						c.editor.dragWin = win
						c.editor.ActivateWindow(win)
						c.editor.focusedView = win.tag
						return false
					}
					c.editor.ActivateWindow(win)
					if my < ry+win.tagHeight() {
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
