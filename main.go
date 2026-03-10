package main

import (
	"log"

	"github.com/gdamore/tcell/v2"
)

type Editor struct {
	screen  tcell.Screen
	columns []*Column
	active  *Window
	width   int
	height  int
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

	// Initial Column
	col := NewColumn(0, 0, e.width, e.height, e.Execute)
	e.columns = append(e.columns, col)

	// Add initial window
	win := col.AddWindow(" /home/user/peak/main.go New Get Put Del Exit ",
		"Welcome to Peak\nYou can now edit the tag line above!\nClick the tag line to focus it, then type. \nMiddle-click 'Exit' or any other word in the tag to execute.")
	e.active = win
	e.Resize()
}

func (e *Editor) Run() {
	for {
		e.Draw()
		ev := e.screen.PollEvent()
		if e.HandleEvent(ev) {
			break
		}
	}
}

func (e *Editor) Draw() {
	e.screen.Clear()
	for _, col := range e.columns {
		col.Draw(e.screen)
	}
	e.screen.Show()
}

func (e *Editor) HandleEvent(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *tcell.EventKey:
		if ev.Key() == tcell.KeyCtrlC {
			return true
		}
		if e.active != nil {
			return e.active.HandleEvent(ev)
		}
	case *tcell.EventMouse:
		mx, my := ev.Position()
		buttons := ev.Buttons()
		logDebug("Mouse Event: x=%d y=%d buttons=%b", mx, my, buttons)
		var clickedCol *Column
		for _, col := range e.columns {
			if mx >= col.x && mx < col.x+col.w && my >= col.y && my < col.y+col.h {
				clickedCol = col
				break
			}
		}

		if clickedCol != nil {
			// Focus logic
			for _, win := range clickedCol.windows {
				if mx >= win.x && mx < win.x+win.w && my >= win.y && my < win.y+win.h {
					if ev.Buttons() == tcell.Button1 {
						e.active = win
					}
					break
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

func main() {
	editor := &Editor{}
	editor.Init()
	defer editor.screen.Fini()
	editor.Run()
}
