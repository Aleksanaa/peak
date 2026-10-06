package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aleksana/peak/internal/session"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

type Column struct {
	rect          // in the editor
	tag           *TextView
	windows       []*Window
	editor        *Editor
	explicitWidth int
	lastSize      int
	maximized     *Window
}

func (c *Column) PreferredSize() int { return c.explicitWidth }
func (c *Column) MinSize() int       { return 5 }

// The gutter is the column's first column, beside the tag; windows span the
// column's width below the tag.
func (c *Column) tagRect() rect { return rect{1, 0, c.w - 1, 1} }

func (c *Column) Draw(cv canvas) {
	theme := &c.editor.theme
	cv.fill(rect{0, 0, 1, 1}, tcell.StyleDefault.Background(theme.HandleColumn).Foreground(color.Black))
	cv.fill(rect{0, 1, 1, c.h - 1}, tcell.StyleDefault.Background(theme.ScrollGutter).Foreground(theme.HandleColumn))
	c.editor.drawView(c.tag, cv.sub(c.tagRect()))
	for _, w := range c.windows {
		w.Draw(cv.sub(w.rect))
	}
}

func NewColumn(x, y, w, h int, editor *Editor) *Column {
	return &Column{
		rect:   rect{x, y, w, h},
		tag:    NewTextView(" New Zerox Win Delcol ", w-1, 1, &editor.theme, &editor.theme.ColTag, true, false),
		editor: editor,
	}
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
	newWin := NewWindow(tagText, bodyText, c, c.editor, c.w)
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
func (c *Column) AddTermWindow(tag, cmd, dir string, preset ...*WindowSession) (*Window, error) {
	if tag == "" {
		var name string
		if cmd == "" {
			if name, _ = os.Hostname(); name == "" {
				name = "term"
			}
		} else {
			name = filepath.Base(strings.Fields(cmd)[0])
		}
		tag = tagText(filepath.Join(dir, "-"+name), "Zerox Del")
	}

	c.maximized = nil
	newWin, err := NewTermWindow(tag, c, c.editor, c.w, cmd, dir)
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
	newWin, err := newTermWindowFromSession(tagText(title, "Zerox Del"), sess, c, c.editor, c.w)
	if err != nil {
		return nil, err
	}
	newWin.ID = c.editor.nextWinID
	c.editor.nextWinID++
	c.windows = append(c.windows, newWin)
	c.editor.ninep.MountWindow(newWin)
	return newWin, nil
}

// Resize places the column at r in the editor and lays out its windows.
func (c *Column) Resize(r rect) {
	c.rect = r
	t := c.tagRect()
	c.tag.Resize(t.w, t.h)
	if len(c.windows) == 0 {
		return
	}

	if c.maximized != nil {
		// Maximized window fills the column; all others are pushed off-screen below.
		c.maximized.explicitHeight = c.h - 1
		c.maximized.Resize(rect{0, 1, c.w, c.h - 1})
		y := c.h
		for _, win := range c.windows {
			if win != c.maximized {
				wh := win.MinSize()
				win.explicitHeight = wh
				win.Resize(rect{0, y, c.w, wh})
				y += wh
			}
		}
		return
	}

	availableH := c.h - 1
	sizes := distribute(c.windows, availableH, c.lastSize)
	c.lastSize = availableH

	y := 1
	for i, win := range c.windows {
		win.explicitHeight = sizes[i]
		win.Resize(rect{0, y, c.w, sizes[i]})
		y += sizes[i]
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
	c.Resize(c.rect)
}

func (c *Column) Maximize(win *Window) {
	idx := slices.Index(c.windows, win)
	if idx > 0 {
		c.windows = slices.Delete(c.windows, idx, idx+1)
		c.windows = slices.Insert(c.windows, 0, win)
	}
	c.maximized = win
	c.Resize(c.rect)
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
	c.Resize(c.rect)
}

// remove takes win out of the column and lays out the rest.
func (c *Column) remove(win *Window) {
	i := slices.Index(c.windows, win)
	c.windows = slices.Delete(c.windows, i, i+1)
	if c.maximized == win {
		c.maximized = nil
	}
	c.Resize(c.rect)
}

// insert adds win, from another column, at row y of the column: before the
// first window whose middle is below y.
func (c *Column) insert(win *Window, y int) {
	i := 0
	for i < len(c.windows) && y >= c.windows[i].y+c.windows[i].h/2 {
		i++
	}
	win.parent, win.explicitHeight = c, 0
	c.windows = slices.Insert(c.windows, i, win)
	c.Resize(c.rect)
}

// moveWindow drags win, origH high when the drag began, to row y of the
// column. Past the top of the next window, win swaps with it; above the tag
// of the previous one, win swaps with that; otherwise win's top follows y.
func (c *Column) moveWindow(win *Window, y, origH int) {
	wins := c.windows
	idx := slices.Index(wins, win)
	if idx < len(wins)-1 && y > wins[idx+1].y {
		delta := origH - win.explicitHeight
		wins[idx], wins[idx+1] = wins[idx+1], wins[idx]
		wins[idx+1].explicitHeight = origH
		if idx > 0 {
			wins[idx-1].explicitHeight -= delta
		}
		c.Resize(c.rect)
		return
	}
	if idx == 0 {
		return
	}
	prev := wins[idx-1]
	combinedH := prev.h + win.h
	if y < prev.y+prev.tagHeight() {
		wins[idx], wins[idx-1] = wins[idx-1], wins[idx]
		wins[idx-1].explicitHeight = origH
		wins[idx].explicitHeight = combinedH - origH
	} else {
		newH := max(prev.tagHeight(), min(combinedH-win.tagHeight(), y-prev.y))
		if newH == prev.explicitHeight {
			return
		}
		win.explicitHeight += prev.explicitHeight - newH
		prev.explicitHeight = newH
	}
	c.Resize(c.rect)
}
