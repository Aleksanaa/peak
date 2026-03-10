package main

import (
	"log"
	"os"

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
}

// Init sets up the initial editor state with two columns.
func (e *Editor) Init() {
	initDebug()
	s, err := tcell.NewScreen()
	if err != nil { log.Fatalf("%+v", err) }
	if err := s.Init(); err != nil { log.Fatalf("%+v", err) }

	e.screen = s
	e.screen.EnableMouse()
	e.width, e.height = e.screen.Size()

	// Top menu: #11111b, menu Text: #89b4fa
	tagStyle := tcell.StyleDefault.Background(tcell.NewHexColor(0x11111b)).Foreground(tcell.NewHexColor(0x89b4fa))
	e.tag = NewTextView(" NewCol Exit ", 0, 0, e.width, 1, tagStyle, true, false)
	e.focusedView = e.tag

	// Start with two columns
	colLeft := NewColumn(0, 1, e.width/2, e.height-1, e, e.Execute)
	e.columns = append(e.columns, colLeft)

	colRight := NewColumn(e.width/2, 1, e.width-e.width/2, e.height-1, e, e.Execute)
	e.columns = append(e.columns, colRight)

	dir, _ := os.Getwd()
	win := colRight.AddWindow(dir+" Get Put Snarf Zerox Del ", "")
	e.active, e.focusedView = win, win.body
	
	// Initial directory listing
	e.Execute(colRight, win, "Get")
	e.Resize()
}

// Run enters the main event loop.
func (e *Editor) Run() {
	for {
		e.Draw()
		ev := e.screen.PollEvent()
		if ev == nil { continue }
		switch ev := ev.(type) {
		case *tcell.EventInterrupt:
			if f, ok := ev.Data().(func()); ok { f() }
		default:
			if e.HandleEvent(ev) { return }
		}
	}
}

func (e *Editor) Draw() {
	e.screen.Clear()
	e.tag.Draw(e.screen)
	for _, col := range e.columns { col.Draw(e.screen) }
	if e.focusedView != nil { e.focusedView.ShowCursor(e.screen) }
	e.screen.Show()
}

func (e *Editor) HandleEvent(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *tcell.EventKey:
		if e.focusedView != nil { return e.focusedView.HandleEvent(ev) }
	case *tcell.EventMouse:
		mx, my := ev.Position()
		buttons := ev.Buttons()

		if e.dragCol != nil {
			if buttons&tcell.Button1 != 0 { e.moveColumnTo(e.dragCol, mx); return false }
			e.dragCol = nil; return false
		}
		if e.dragWin != nil {
			if buttons&tcell.Button1 != 0 { e.moveWindowTo(e.dragWin, mx, my); return false }
			e.dragWin = nil; return false
		}
		if e.dragView != nil {
			e.dragView.HandleEvent(ev)
			if buttons == tcell.ButtonNone { e.dragView = nil }
			return false
		}

		// Global Tag clicks
		if my == 0 {
			if buttons == tcell.Button3 {
				word := e.tag.buffer.GetSelectedText()
				if word == "" { word = e.tag.buffer.GetWordAt(mx, 0) }
				return e.Execute(nil, nil, word)
			}
			if buttons == tcell.Button1 { e.dragView, e.focusedView = e.tag, e.tag }
			return e.tag.HandleEvent(ev)
		}

		for _, col := range e.columns {
			if mx >= col.x && mx < col.x+col.w && my >= col.y && my < col.y+col.h {
				if my == col.tag.y {
					if mx == col.x && buttons == tcell.Button1 { e.dragCol = col; return false }
					if mx > col.x && buttons == tcell.Button1 { e.dragView, e.focusedView = col.tag, col.tag }
					return col.HandleEvent(ev)
				}
				for _, win := range col.windows {
					if mx >= win.x && mx < win.x+win.w && my >= win.y && my < win.y+win.h {
						if buttons == tcell.Button1 {
							if mx == win.x && my >= win.y && my < win.y+win.tagHeight() {
								e.dragWin, e.active, e.focusedView = win, win, win.tag
								return false
							}
							e.active = win
							if my < win.y+win.tagHeight() { e.dragView, e.focusedView = win.tag, win.tag } else { e.dragView, e.focusedView = win.body, win.body }
						}
						return col.HandleEvent(ev)
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

func (e *Editor) moveColumnTo(col *Column, mx int) {
	idx := -1
	for i, c := range e.columns { if c == col { idx = i; break } }
	if idx == -1 { return }

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
			nw := mx - prev.x
			if nw < 5 { nw = 5 }; prev.explicitWidth = nw
		}
	}
	e.Resize()
}

func (e *Editor) moveWindowTo(win *Window, mx, my int) {
	var target *Column
	for _, col := range e.columns { if mx >= col.x && mx < col.x+col.w { target = col; break } }
	if target == nil { return }

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
		for _, w := range target.windows { if my < w.y+w.h/2 { break }; newIdx++ }
		target.windows = append(target.windows[:newIdx], append([]*Window{win}, target.windows[newIdx:]...)...)
		target.Resize(target.x, target.y, target.w, target.h)
		return
	}

	idx := -1
	for i, w := range target.windows { if w == win { idx = i; break } }
	if idx == -1 { return }

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
			nh := my - prev.y
			if nh < prev.tagHeight()+1 { nh = prev.tagHeight() + 1 }; prev.explicitHeight = nh
		}
	}
	target.Resize(target.x, target.y, target.w, target.h)
}

func (e *Editor) Resize() {
	if len(e.columns) == 0 { return }
	e.tag.Resize(0, 0, e.width, 1)
	
	xOffset, availableW := 0, e.width
	totalExplicit, numAuto := 0, 0
	for _, col := range e.columns { if col.explicitWidth > 0 { totalExplicit += col.explicitWidth } else { numAuto++ } }

	autoW := 0
	if numAuto > 0 {
		autoW = (availableW - totalExplicit) / numAuto
		if autoW < 5 { autoW = 5 }
	}

	for i, col := range e.columns {
		cw := col.explicitWidth
		if cw <= 0 { cw = autoW }
		if i == len(e.columns)-1 { cw = e.width - xOffset }
		if cw < 1 { cw = 1 }
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
