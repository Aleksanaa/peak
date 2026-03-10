package main

import (
	"log"
	"os"
	"time"

	"github.com/gdamore/tcell/v2"
)

// Editor is the main application state.
type Editor struct {
	screen      tcell.Screen
	tag         *TextView
	columns     []*Column
	active      *Window
	width       int
	height      int
	dragView    *TextView
	dragWin     *Window
	dragCol     *Column
	focusedView *TextView

	scrollWin       *Window
	scrollAmount    int
	scrollDir       int
	scrollStartTime time.Time
	lastWidth       int
	lastClickY      int
}

// Init sets up the initial editor state with two columns.
func (e *Editor) Init() {
	s, err := tcell.NewScreen()
	if err != nil {
		log.Fatalf("%+v", err)
	}
	if err := s.Init(); err != nil {
		log.Fatalf("%+v", err)
	}

	e.screen = s
	e.screen.EnableMouse()
	e.width, e.height = e.screen.Size()

	// Top menu: #11111b, menu Text: #89dceb
	tagStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x11111b)).Foreground(tcell.NewHexColor(0x89dceb))
	e.tag = NewTextView(" NewCol Exit ", 0, 0, e.width, 1, tagStyle, true, false)
	e.focusedView = e.tag

	// Start with two columns
	colLeft := NewColumn(0, 1, e.width/2, e.height-1, e, e.Execute)
	e.columns = append(e.columns, colLeft)

	colRight := NewColumn(e.width/2, 1, e.width-e.width/2, e.height-1, e, e.Execute)
	e.columns = append(e.columns, colRight)

	dir, _ := os.Getwd()
	win := colRight.AddWindow(" "+dir+" Get Put Undo Redo Snarf Zerox Del ", "")
	e.ActivateWindow(win)

	// Initial directory listing
	e.Execute(colRight, win, "Get")
	e.Resize()
}

// Run enters the main event loop.
func (e *Editor) Run() {
	events := make(chan tcell.Event)
	go func() {
		for {
			events <- e.screen.PollEvent()
		}
	}()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		e.Draw()
		select {
		case ev := <-events:
			if ev == nil {
				return
			}
			switch ev := ev.(type) {
			case *tcell.EventInterrupt:
				if f, ok := ev.Data().(func()); ok {
					f()
				}
			default:
				if e.HandleEvent(ev) {
					return
				}
			}
		case <-ticker.C:
			if e.scrollWin != nil && time.Since(e.scrollStartTime) > 200*time.Millisecond {
				e.scrollWin.body.Scroll(e.scrollDir * e.scrollAmount)
			}
		}
	}
}

func (e *Editor) Draw() {
	e.screen.Clear()
	e.tag.Draw(e.screen)
	for _, col := range e.columns {
		col.Draw(e.screen)
	}
	if e.focusedView != nil {
		e.focusedView.ShowCursor(e.screen)
	}
	e.screen.Show()
}

func (e *Editor) HandleEvent(ev tcell.Event) bool {
	if me, ok := ev.(*tcell.EventMouse); ok {
		if me.Buttons() != tcell.ButtonNone {
			_, my := me.Position()
			e.lastClickY = my
		}
	}

	switch ev := ev.(type) {
	case *tcell.EventKey:
		if ev.Key() == tcell.KeyCtrlF {
			if e.focusedView != nil && e.focusedView.buffer.GetSelectedText() != "" {
				return e.Execute(nil, nil, "Look")
			}
		}
		if e.focusedView != nil {
			return e.focusedView.HandleEvent(ev)
		}
	case *tcell.EventMouse:
		mx, my := ev.Position()
		buttons := ev.Buttons()

		if buttons == tcell.ButtonNone {
			e.scrollWin = nil
		}

		if e.dragCol != nil {
			if buttons&tcell.Button1 != 0 {
				e.moveColumnTo(e.dragCol, mx)
				return false
			}
			e.dragCol = nil
			return false
		}
		if e.dragWin != nil {
			if buttons&tcell.Button1 != 0 {
				e.moveWindowTo(e.dragWin, mx, my)
				return false
			}
			e.dragWin = nil
			return false
		}
		if e.dragView != nil {
			e.dragView.HandleEvent(ev)
			if buttons == tcell.ButtonNone {
				e.dragView = nil
			}
			return false
		}

		// Global Tag clicks
		if my == 0 {
			word := e.tag.GetClickWord(mx, my)
			if word != "" {
				if buttons == tcell.Button3 { // Middle-click
					return e.Execute(nil, nil, word)
				}
				if buttons == tcell.Button2 { // Right-click
					return e.Plumb(nil, word)
				}
			}
			if buttons == tcell.Button1 {
				e.dragView, e.focusedView = e.tag, e.tag
			}
			return e.tag.HandleEvent(ev)
		}

		for _, col := range e.columns {
			if col.Contains(mx, my) {
				if my == col.tag.y {
					if mx == col.x && buttons == tcell.Button1 {
						e.dragCol = col
						return false
					}
					if mx > col.x && buttons == tcell.Button1 {
						e.dragView, e.focusedView = col.tag, col.tag
					}
					return col.HandleEvent(ev)
				}
				for _, win := range col.windows {
					if win.Contains(mx, my) {
						if buttons == tcell.Button1 {
							if mx == win.x && my >= win.y && my < win.y+win.tagHeight() {
								e.dragWin = win
								e.ActivateWindow(win)
								e.focusedView = win.tag
								return false
							}
							e.ActivateWindow(win)
							if my < win.y+win.tagHeight() {
								e.focusedView = win.tag
							}
							e.dragView = e.focusedView
						}
						return win.HandleEvent(ev)
					}
				}
				return col.HandleEvent(ev)
			}
		}
	case *tcell.EventResize:
		e.width, e.height = e.screen.Size()
		e.Resize()
		e.screen.Sync()
	}
	return false
}

func (e *Editor) ActivateWindow(win *Window) {
	if win == nil {
		return
	}
	e.active = win
	e.focusedView = win.body
}

func (e *Editor) moveColumnTo(col *Column, mx int) {
	idx := -1
	for i, c := range e.columns {
		if c == col {
			idx = i
			break
		}
	}
	if idx == -1 {
		return
	}

	if idx == 0 {
		if len(e.columns) > 1 && mx > e.columns[1].x+e.columns[1].w/2 {
			e.columns[0], e.columns[1] = e.columns[1], e.columns[0]
			e.columns[0].explicitWidth, e.columns[1].explicitWidth = 0, 0
		}
	} else {
		prev := e.columns[idx-1]
		if mx < prev.x+2 {
			e.columns[idx], e.columns[idx-1] = e.columns[idx-1], e.columns[idx]
			e.columns[idx].explicitWidth, e.columns[idx-1].explicitWidth = 0, 0
		} else {
			newPrevW := mx - prev.x
			if newPrevW < 5 {
				newPrevW = 5
			}
			combinedW := prev.w + col.w
			prev.explicitWidth = newPrevW
			col.explicitWidth = combinedW - newPrevW
			if col.explicitWidth < 5 {
				col.explicitWidth = 5
			}
		}
	}
	e.Resize()
}

func (e *Editor) moveWindowTo(win *Window, mx, my int) {
	var target *Column
	for _, col := range e.columns {
		if mx >= col.x && mx < col.x+col.w {
			target = col
			break
		}
	}
	if target == nil {
		return
	}

	if win.parent != target {
		old := win.parent
		for i, w := range old.windows {
			if w == win {
				old.windows = append(old.windows[:i], old.windows[i+1:]...)
				old.Resize(old.x, old.y, old.w, old.h)
				break
			}
		}
		win.parent, win.explicitHeight = target, 0
		newIdx := 0
		for _, w := range target.windows {
			if my < w.y+w.h/2 {
				break
			}
			newIdx++
		}
		target.windows = append(target.windows[:newIdx], append([]*Window{win}, target.windows[newIdx:]...)...)
		target.Resize(target.x, target.y, target.w, target.h)
		return
	}

	idx := -1
	for i, w := range target.windows {
		if w == win {
			idx = i
			break
		}
	}
	if idx == -1 {
		return
	}

	if idx == 0 {
		if len(target.windows) > 1 && my > target.windows[1].y+target.windows[1].tagHeight() {
			target.windows[0], target.windows[1] = target.windows[1], target.windows[0]
			target.windows[0].explicitHeight, target.windows[1].explicitHeight = 0, 0
		}
	} else {
		prev := target.windows[idx-1]
		if my < prev.y+prev.tagHeight() {
			target.windows[idx], target.windows[idx-1] = target.windows[idx-1], target.windows[idx]
			target.windows[idx].explicitHeight, target.windows[idx-1].explicitHeight = 0, 0
		} else {
			newPrevH := my - prev.y
			if newPrevH < prev.tagHeight()+1 {
				newPrevH = prev.tagHeight() + 1
			}
			combinedH := prev.h + win.h
			prev.explicitHeight = newPrevH
			win.explicitHeight = combinedH - newPrevH
			if win.explicitHeight < win.tagHeight()+1 {
				win.explicitHeight = win.tagHeight() + 1
			}
		}
	}
	target.Resize(target.x, target.y, target.w, target.h)
}

func (e *Editor) Resize() {
	if len(e.columns) == 0 {
		return
	}
	e.tag.Resize(0, 0, e.width, 1)

	// 1. Proportional scaling for existing columns
	if e.lastWidth > 0 && e.lastWidth != e.width {
		ratio := float64(e.width) / float64(e.lastWidth)
		for _, col := range e.columns {
			if col.explicitWidth > 0 {
				col.explicitWidth = int(float64(col.explicitWidth) * ratio)
			}
		}
	}
	e.lastWidth = e.width

	// 2. Count explicit vs automatic columns
	totalExplicit, numAuto := 0, 0
	for _, col := range e.columns {
		if col.explicitWidth > 0 {
			totalExplicit += col.explicitWidth
		} else {
			numAuto++
		}
	}

	// 3. Redistribute if adding new columns to a full editor
	availableW := e.width
	if numAuto > 0 && totalExplicit >= availableW {
		// New columns should get a fair share (1/N total columns)
		targetTotalAuto := (availableW * numAuto) / len(e.columns)
		if targetTotalAuto < 5*numAuto {
			targetTotalAuto = 5 * numAuto
		}
		scale := float64(availableW-targetTotalAuto) / float64(totalExplicit)
		totalExplicit = 0
		for _, col := range e.columns {
			if col.explicitWidth > 0 {
				col.explicitWidth = int(float64(col.explicitWidth) * scale)
				totalExplicit += col.explicitWidth
			}
		}
	}

	// 4. Final layout
	autoW := 0
	if numAuto > 0 {
		autoW = (availableW - totalExplicit) / numAuto
		if autoW < 5 {
			autoW = 5
		}
	}

	xOffset := 0
	for i, col := range e.columns {
		cw := col.explicitWidth
		if cw <= 0 {
			cw = autoW
		}
		if i == len(e.columns)-1 {
			cw = e.width - xOffset
		}
		if cw < 1 {
			cw = 1
		}
		col.explicitWidth = cw
		col.Resize(xOffset, 1, cw, e.height-1)
		xOffset += cw
	}
}

func main() {
	editor := &Editor{}
	editor.Init()
	defer editor.screen.Fini()
	editor.Run()
}
