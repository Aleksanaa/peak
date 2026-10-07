package main

import (
	"slices"

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

// AddWindow adds a window holding body as text under tag.
func (c *Column) AddWindow(tag, body string) *Window {
	return c.add(newTextWindow(tag, body, c))
}

// AddTermWindow adds a terminal on sess under tag. cmd is the command sess
// runs, empty for a shell, kept to start it again in a later session.
func (c *Column) AddTermWindow(tag, cmd string, sess session.Session) (*Window, error) {
	win, err := newTermWindow(tag, cmd, sess, c)
	if err != nil {
		return nil, err
	}
	return c.add(win), nil
}

// add gives win its ID and its place in the column: below the first window
// whose body has rows to spare after its text, taking those rows, or else at
// the bottom.
func (c *Column) add(win *Window) *Window {
	win.ID = c.editor.nextWinID
	c.editor.nextWinID++
	c.maximized = nil
	i := len(c.windows)
	for j, w := range c.windows {
		scroll, total, bodyH := w.body.GetScroll()
		if spare := bodyH - max(0, total-scroll); spare >= w.MinSize() {
			w.explicitHeight = w.h - spare
			win.explicitHeight = spare
			i = j + 1
			break
		}
	}
	c.windows = slices.Insert(c.windows, i, win)
	c.editor.ninep.MountWindow(win)
	return win
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
	bodyH := win.h - win.tag.h
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
	if y < prev.y+prev.tag.h {
		wins[idx], wins[idx-1] = wins[idx-1], wins[idx]
		wins[idx-1].explicitHeight = origH
		wins[idx].explicitHeight = combinedH - origH
	} else {
		newH := max(prev.tag.h, min(combinedH-win.tag.h, y-prev.y))
		if newH == prev.explicitHeight {
			return
		}
		win.explicitHeight += prev.explicitHeight - newH
		prev.explicitHeight = newH
	}
	c.Resize(c.rect)
}
