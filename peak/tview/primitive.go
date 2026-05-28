package tview

import "github.com/gdamore/tcell/v2"

type Primitive interface {
	Draw(screen tcell.Screen)
	GetRect() (int, int, int, int)
	SetRect(x, y, width, height int)
}
