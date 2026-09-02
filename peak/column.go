package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	
	"github.com/aleksana/peak/internal/quote"
	"github.com/aleksana/peak/internal/session"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

type Gutter struct {
	BaseView
	theme *Theme
}

func (g *Gutter) Layout() {}
func (g *Gutter) Draw(s tcell.Screen) {
	sepStyle := tcell.StyleDefault.Background(g.theme.ScrollGutter).Foreground(g.theme.HandleColumn)
	handleStyle := tcell.StyleDefault.Background(g.theme.HandleColumn).Foreground(color.Black)
	for y := g.y; y < g.y+g.h; y++ {
		style := sepStyle
		if y == g.y {
			style = handleStyle
		}
		s.Put(g.x, y, " ", style)
	}
}
func (g *Gutter) Resize(x, y, w, h int) { g.x, g.y, g.w, g.h = x, y, w, h }

type Column struct {
	TreeNode
	tag           *TextView
	windows       []*Window
	editor        *Editor
	gutter        *Gutter
	onExec        func(*Column, *Window, string) bool
	explicitWidth int
	winCache      []DrawNode
	maximized     *Window
}

func (c *Column) Layout() {}

func (c *Column) PreferredSize() int { return c.explicitWidth }
func (c *Column) MinSize() int       { return 5 }

func (c *Column) WalkLayout() {
	c.syncChildren()
	c.TreeNode.WalkLayout()
}

func (c *Column) WalkDraw(s tcell.Screen) {
	c.TreeNode.WalkDraw(s)
}

func (c *Column) Draw(s tcell.Screen) {}

func (c *Column) syncChildren() {
	c.children = []DrawNode{c.gutter, c.tag}
	for _, w := range c.windows {
		c.children = append(c.children, w)
	}
}

func NewColumn(x, y, w, h int, editor *Editor, onExec func(*Column, *Window, string) bool) *Column {
	tagStyle := tcell.StyleDefault.Background(editor.theme.ColTagBG).Foreground(editor.theme.ColTagFG)
	tag := NewTextView(" New Zerox Win Delcol ", x+1, y, w-1, 1, tagStyle, true, false)
	tag.style = func() tcell.Style {
		return tcell.StyleDefault.Background(editor.theme.ColTagBG).Foreground(editor.theme.ColTagFG)
	}
	tag.theme = &editor.theme

	gutter := &Gutter{
		BaseView: BaseView{x: x, y: y, w: 1, h: h},
		theme:    &editor.theme,
	}

	c := &Column{
		TreeNode: TreeNode{BaseView: BaseView{x: x, y: y, w: w, h: h}},
		tag:      tag,
		editor:   editor,
		gutter:   gutter,
		onExec:   onExec,
	}
	return c
}

// contentInsertPos scans windows first-to-last and returns the index at which
// a new window should be inserted and how many rows of empty space are available
// there. Empty space is body rows with no content. Returns (len(windows), 0) if
// no window has spare space.
func (c *Column) contentInsertPos() (idx int, emptyH int) {
	for i, win := range c.windows {
		scroll, total, bodyH := win.body.GetScroll()
		if empty := bodyH - max(0, total-scroll); empty >= win.MinSize() {
			return i + 1, empty
		}
	}
	return len(c.windows), 0
}

// AddWindow creates a file/dir window. With a preset the content is restored
// atomically and the window is appended in session order; without one the
// normal smart-insertion with space-stealing is used.
func (c *Column) AddWindow(tagText, bodyText string, preset ...*WindowSession) *Window {
	if tagText == "" {
		tagText = " ./untitled.txt Get Put Undo Redo Snarf Zerox Del "
	}
	c.maximized = nil
	newWin := NewWindow(tagText, bodyText, c, c.editor, c.x, c.y, c.w, 0, c.onExec)
	newWin.ID = c.editor.nextWinID
	c.editor.nextWinID++

	var ws *WindowSession
	if len(preset) > 0 {
		ws = preset[0]
	}
	if ws != nil {
		newWin.applyPreset(ws)
		if ws.HeightPct > 0 && c.h > 0 {
			newWin.explicitHeight = max(newWin.MinSize(), ws.HeightPct*c.h/100)
		}
		c.windows = append(c.windows, newWin)
	} else {
		insertIdx, emptyH := c.contentInsertPos()
		if emptyH > 0 {
			src := c.windows[insertIdx-1]
			src.explicitHeight = src.h - emptyH
			newWin.explicitHeight = emptyH
		}
		c.windows = slices.Insert(c.windows, insertIdx, newWin)
	}
	c.editor.ninep.MountWindow(newWin)
	return newWin
}

// AddTermWindow creates a terminal window. An optional preset sets the initial
// height from the saved session.
func (c *Column) AddTermWindow(tagText, cmd, dir string, preset ...*WindowSession) (*Window, error) {
	if tagText == "" {
		var name string
		if cmd == "" {
			if name, _ = os.Hostname(); name == "" {
				name = "term"
			}
		} else {
			name = filepath.Base(strings.Fields(cmd)[0])
		}
		tagText = " " + quote.Quote(filepath.Join(dir, "-"+name)) + " Zerox Del "
	}

	c.maximized = nil
	newWin, err := NewTermWindow(tagText, c, c.editor, c.x, c.y, c.w, 0, cmd, dir, c.onExec)
	if err != nil {
		return nil, err
	}
	newWin.ID = c.editor.nextWinID
	c.editor.nextWinID++
	if len(preset) > 0 && preset[0] != nil && preset[0].HeightPct > 0 && c.h > 0 {
		newWin.explicitHeight = max(newWin.MinSize(), preset[0].HeightPct*c.h/100)
	}
	c.windows = append(c.windows, newWin)
	c.editor.ninep.MountWindow(newWin)
	return newWin, nil
}

func (c *Column) AddSessionTermWindow(title string, sess session.Session) (*Window, error) {
	c.maximized = nil
	newWin, err := newTermWindowFromSession(" "+quote.Quote(title)+" Zerox Del ", sess, c, c.editor, c.x, c.y, c.w, 0, c.onExec)
	if err != nil {
		return nil, err
	}
	newWin.ID = c.editor.nextWinID
	c.editor.nextWinID++
	c.windows = append(c.windows, newWin)
	c.editor.ninep.MountWindow(newWin)
	return newWin, nil
}

func (c *Column) Resize(x, y, w, h int) {
	c.x, c.y, c.w, c.h = x, y, w, h
	c.gutter.Resize(x, y, 1, h)
	c.tag.Resize(x+1, y, w-1, 1)
	if len(c.windows) == 0 {
		return
	}

	if c.maximized != nil {
		// Maximized window fills the column; all others are pushed off-screen below.
		c.maximized.explicitHeight = h - 1
		c.maximized.Resize(x, y+1, w, h-1)
		yOffset := y + h
		for _, win := range c.windows {
			if win != c.maximized {
				wh := win.MinSize()
				win.explicitHeight = wh
				win.Resize(x, yOffset, w, wh)
				yOffset += wh
			}
		}
		return
	}

	availableH := h - 1
	sizes := distribute(c.winNodes(), availableH, c.lastSize)
	c.lastSize = availableH

	yOffset := y + 1
	for i, win := range c.windows {
		win.explicitHeight = sizes[i]
		win.Resize(x, yOffset, w, sizes[i])
		yOffset += sizes[i]
	}
}

// GrowModerate grows win by max(5, half its current height), stealing rows
// from the nearest neighbours outward, never below their minimum. Exits
// maximize mode (matching acme's Button1 handle-click behaviour).
func (c *Column) GrowModerate(win *Window) {
	if len(c.windows) <= 1 {
		return
	}
	idx := slices.Index(c.windows, win)
	bodyH := win.h - win.tagHeight()
	target := min(win.h+max(min(5, win.h), bodyH/2), c.h-1)
	needed := target - win.h
	if needed <= 0 {
		return
	}
	win.explicitHeight = win.h
	for k := 1; k < len(c.windows) && needed > 0; k++ {
		for _, j := range []int{idx + k, idx - k} {
			if j < 0 || j >= len(c.windows) || needed <= 0 {
				continue
			}
			nb := c.windows[j]
			give := min(needed, max(1, nb.h/2), nb.h-nb.MinSize())
			if give <= 0 {
				continue
			}
			nb.explicitHeight = nb.h - give
			win.explicitHeight += give
			needed -= give
		}
	}
	c.maximized = nil
	c.Resize(c.x, c.y, c.w, c.h)
}

func (c *Column) Maximize(win *Window) {
	idx := slices.Index(c.windows, win)
	if idx > 0 {
		c.windows = slices.Delete(c.windows, idx, idx+1)
		c.windows = slices.Insert(c.windows, 0, win)
	}
	c.maximized = win
	c.Resize(c.x, c.y, c.w, c.h)
}

// GrowFull expands win to use all remaining column space while keeping every
// other window visible at its minimum tag height. Exits the maximized state.
func (c *Column) GrowFull(win *Window) {
	c.maximized = nil
	avail := c.h - 1
	for _, w := range c.windows {
		if w != win {
			avail -= w.MinSize()
		}
	}
	for _, w := range c.windows {
		if w != win {
			w.explicitHeight = w.MinSize()
		} else {
			w.explicitHeight = max(w.MinSize(), avail)
		}
	}
	c.Resize(c.x, c.y, c.w, c.h)
}

func (c *Column) winNodes() []DrawNode {
	if cap(c.winCache) < len(c.windows) {
		c.winCache = make([]DrawNode, len(c.windows))
	}
	c.winCache = c.winCache[:len(c.windows)]
	for i, w := range c.windows {
		c.winCache[i] = w
	}
	return c.winCache
}

func (c *Column) Contains(x, y int) bool {
	return x >= c.x && x < c.x+c.w && y >= c.y && y < c.y+c.h
}
