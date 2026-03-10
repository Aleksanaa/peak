package main

import (
	"log"

	"github.com/gdamore/tcell/v2"
)

type Editor struct {
	screen  tcell.Screen
	tag     *TextView
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

	// Catppuccin Macchiato Crust: #181926, Sky: #91d7e3
	tagStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x181926)).Foreground(tcell.NewHexColor(0x91d7e3))
	e.tag = NewTextView(" NewCol Exit ", 0, 0, e.width, 1, tagStyle, true)

	// Initial Column
	col := NewColumn(0, 1, e.width, e.height-1, e.Execute)
	e.columns = append(e.columns, col)

	// Add initial window
	win := col.AddWindow(" /home/user/peak/main.go Get Put Del ",
		"Welcome to Peak\nRefactored with unified TextView.\nSelection, scrolling and editing now work the same everywhere.")
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
	e.tag.Draw(e.screen)
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

		if my == 0 {
			if buttons == tcell.Button3 {
				word := e.tag.buffer.GetSelectedText()
				if word == "" {
					word = e.tag.buffer.GetWordAt(mx, 0)
				}
				return e.Execute(nil, nil, word)
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
			// Focus logic
			for _, win := range clickedCol.windows {
				if mx >= win.x && mx < win.x+win.w && my >= win.y && my < win.y+win.h {
					if buttons == tcell.Button1 {
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

func (e *Editor) Resize() {
	if len(e.columns) == 0 {
		return
	}
	e.tag.Resize(0, 0, e.width, 1)
	colW := e.width / len(e.columns)
	xOffset := 0
	for i, col := range e.columns {
		actualW := colW
		if i == len(e.columns)-1 {
			actualW = e.width - xOffset
		}
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
