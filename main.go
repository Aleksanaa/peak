package main

import (
	"log"
	"os"

	"github.com/gdamore/tcell/v2"
)

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
}

func (e *Editor) Init() {
	initDebug()
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

	// Catppuccin Macchiato Crust: #181926, Sky: #91d7e3
	tagStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x181926)).Foreground(tcell.NewHexColor(0x91d7e3))
	e.tag = NewTextView(" NewCol Exit ", 0, 0, e.width, 1, tagStyle, true, false)
	e.focusedView = e.tag

	// Left Column: Empty
	colLeft := NewColumn(0, 1, e.width/2, e.height-1, e, e.Execute)
	e.columns = append(e.columns, colLeft)

	// Right Column: pwd listing
	colRight := NewColumn(e.width/2, 1, e.width-e.width/2, e.height-1, e, e.Execute)
	e.columns = append(e.columns, colRight)

	// Add window to right column
	dir, _ := os.Getwd()
	win := colRight.AddWindow(dir+" Get Put Snarf Zerox Del ", "")
	e.active = win
	e.focusedView = win.body
	
	// Trigger internal "Get" to perform the ls
	e.Execute(colRight, win, "Get")
	
	e.Resize()
}

func (e *Editor) Run() {
	for {
		e.Draw()
		ev := e.screen.PollEvent()
		if ev == nil {
			continue
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
	switch ev := ev.(type) {
	case *tcell.EventKey:
		if e.focusedView != nil {
			return e.focusedView.HandleEvent(ev)
		}
	case *tcell.EventMouse:
		mx, my := ev.Position()
		buttons := ev.Buttons()

		if e.dragCol != nil {
			if buttons&tcell.Button1 != 0 {
				e.moveColumnTo(e.dragCol, mx)
				return false
			} else {
				e.dragCol = nil
				return false
			}
		}

		if e.dragWin != nil {
			if buttons&tcell.Button1 != 0 {
				e.moveWindowTo(e.dragWin, mx, my)
				return false
			} else {
				e.dragWin = nil
				return false
			}
		}

		if e.dragView != nil {
			e.dragView.HandleEvent(ev)
			if buttons == tcell.ButtonNone {
				e.dragView = nil
			}
			return false
		}

		if my == 0 {
			if buttons == tcell.Button3 {
				word := e.tag.buffer.GetSelectedText()
				if word == "" {
					word = e.tag.buffer.GetWordAt(mx, 0)
				}
				return e.Execute(nil, nil, word)
			}
			if buttons == tcell.Button1 {
				e.dragView = e.tag
				e.focusedView = e.tag
			}
			return e.tag.HandleEvent(ev)
		}

		var clickedCol *Column
		for _, col := range e.columns {
			if mx >= col.x && mx < col.x+col.w && my >= col.y && my < col.y+col.h {
				clickedCol = col
				break
			}
		}

		if clickedCol != nil {
			if my == clickedCol.tag.y {
				if mx == clickedCol.x && buttons == tcell.Button1 {
					e.dragCol = clickedCol
					e.active = nil
					e.focusedView = clickedCol.tag
					return false
				} else if mx > clickedCol.x {
					if buttons == tcell.Button1 {
						e.dragView = clickedCol.tag
						e.focusedView = clickedCol.tag
					}
					return clickedCol.HandleEvent(ev)
				}
			}

			for _, win := range clickedCol.windows {
				if mx >= win.x && mx < win.x+win.w && my >= win.y && my < win.y+win.h {
					if buttons == tcell.Button1 {
						th := win.tagHeight()
						if mx == win.x && my >= win.y && my < win.y+th {
							e.dragWin = win
							e.active = win
							e.focusedView = win.tag
							return false
						}
						
						e.active = win
						if my >= win.y && my < win.y+th {
							e.dragView = win.tag
							e.focusedView = win.tag
						} else {
							e.dragView = win.body
							e.focusedView = win.body
						}
					}
					return clickedCol.HandleEvent(ev)
				}
			}
			return clickedCol.HandleEvent(ev)
		}
	case *tcell.EventResize:
		e.width, e.height = e.screen.Size()
		e.Resize()
		e.screen.Sync()
	}
	return false
}

func (e *Editor) moveColumnTo(col *Column, mx int) {
	idx := -1
	for i, c := range e.columns {
		if c == col { idx = i; break }
	}
	if idx == -1 { return }

	if idx == 0 {
		if len(e.columns) > 1 && mx > e.columns[1].x + e.columns[1].w/2 {
			e.columns[0], e.columns[1] = e.columns[1], e.columns[0]
			e.columns[0].explicitWidth = 0
			e.columns[1].explicitWidth = 0
		}
	} else {
		prevCol := e.columns[idx-1]
		if mx < prevCol.x + 2 {
			e.columns[idx], e.columns[idx-1] = e.columns[idx-1], e.columns[idx]
			e.columns[idx].explicitWidth = 0
			e.columns[idx-1].explicitWidth = 0
		} else {
			newPrevW := mx - prevCol.x
			if newPrevW < 5 { newPrevW = 5 }
			prevCol.explicitWidth = newPrevW
		}
	}
	e.Resize()
}

func (e *Editor) moveWindowTo(win *Window, mx, my int) {
	var targetCol *Column
	for _, col := range e.columns {
		if mx >= col.x && mx < col.x+col.w {
			targetCol = col
			break
		}
	}
	if targetCol == nil { return }

	if win.parent != targetCol {
		oldCol := win.parent
		for i, w := range oldCol.windows {
			if w == win {
				oldCol.windows = append(oldCol.windows[:i], oldCol.windows[i+1:]...)
				oldCol.Resize(oldCol.x, oldCol.y, oldCol.w, oldCol.h)
				break
			}
		}
		win.parent = targetCol
		win.explicitHeight = 0
		newIdx := 0
		for _, w := range targetCol.windows {
			if my < w.y + w.h/2 { break }
			newIdx++
		}
		targetCol.windows = append(targetCol.windows[:newIdx], append([]*Window{win}, targetCol.windows[newIdx:]...)...)
		targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
		return
	}

	idx := -1
	for i, w := range targetCol.windows {
		if w == win { idx = i; break }
	}
	if idx == -1 { return }

	if idx == 0 {
		if len(targetCol.windows) > 1 && my > targetCol.windows[1].y + targetCol.windows[1].tagHeight() {
			targetCol.windows[0], targetCol.windows[1] = targetCol.windows[1], targetCol.windows[0]
			targetCol.windows[0].explicitHeight = 0
			targetCol.windows[1].explicitHeight = 0
		}
	} else {
		prevWin := targetCol.windows[idx-1]
		if my < prevWin.y + prevWin.tagHeight() {
			targetCol.windows[idx], targetCol.windows[idx-1] = targetCol.windows[idx-1], targetCol.windows[idx]
			targetCol.windows[idx].explicitHeight = 0
			targetCol.windows[idx-1].explicitHeight = 0
		} else {
			newPrevH := my - prevWin.y
			if newPrevH < prevWin.tagHeight() + 1 { newPrevH = prevWin.tagHeight() + 1 }
			prevWin.explicitHeight = newPrevH
		}
	}
	targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
}

func (e *Editor) Resize() {
	if len(e.columns) == 0 { return }
	e.tag.Resize(0, 0, e.width, 1)
	
	xOffset := 0
	availableW := e.width
	
	totalExplicit := 0
	numAuto := 0
	for _, col := range e.columns {
		if col.explicitWidth > 0 { totalExplicit += col.explicitWidth } else { numAuto++ }
	}

	autoW := 0
	if numAuto > 0 {
		autoW = (availableW - totalExplicit) / numAuto
		if autoW < 5 { autoW = 5 }
	}

	for i, col := range e.columns {
		colW := col.explicitWidth
		if colW <= 0 { colW = autoW }
		if i == len(e.columns)-1 { colW = e.width - xOffset }
		if colW < 1 { colW = 1 }
		col.Resize(xOffset, 1, colW, e.height-1)
		xOffset += colW
	}
}

func main() {
	editor := &Editor{}
	editor.Init()
	defer editor.screen.Fini()
	editor.Run()
}
