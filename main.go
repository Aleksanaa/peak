package main

import (
	"log"

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

	tagStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x181926)).Foreground(tcell.NewHexColor(0x91d7e3))
	e.tag = NewTextView(" NewCol Exit ", 0, 0, e.width, 1, tagStyle, true, false)
	e.focusedView = e.tag

	// Initial Column
	col := NewColumn(0, 1, e.width, e.height-1, e, e.Execute)
	e.columns = append(e.columns, col)

	// Add initial window
	win := col.AddWindow(" /home/user/peak/main.go Get Put Snarf Zerox Del ",
		"Welcome to Peak\nAcme-style resizing implemented.\nDrag a window handle to adjust heights or swap windows.")
	e.active = win
	e.focusedView = win.body
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
					// Column dragging could be implemented here
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
						if mx == win.x && my >= win.y && my < win.y+win.tagHeight() {
							e.dragWin = win
							e.active = win
							e.focusedView = win.tag
							return false
						}
						
						e.active = win
						if my >= win.y && my < win.y+win.tagHeight() {
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
	colW := e.width / len(e.columns)
	xOffset := 0
	for i, col := range e.columns {
		actualW := colW
		if i == len(e.columns)-1 { actualW = e.width - xOffset }
		col.Resize(xOffset, 1, actualW, e.height-1)
		xOffset += actualW
	}
}

func main() {
	editor := &Editor{}
	editor.Init()
	defer editor.screen.Fini()
	editor.Run()
}
