package tview

import "github.com/gdamore/tcell/v2"

type Box struct {
	x, y, width, height int
	backgroundColor      tcell.Color
	dontClear            bool
}

func NewBox() *Box {
	return &Box{}
}

func (b *Box) SetRect(x, y, width, height int) {
	b.x, b.y, b.width, b.height = x, y, width, height
}

func (b *Box) GetRect() (int, int, int, int) {
	return b.x, b.y, b.width, b.height
}

func (b *Box) GetInnerRect() (int, int, int, int) {
	return b.x, b.y, b.width, b.height
}

func (b *Box) SetBackgroundColor(color tcell.Color) *Box {
	b.backgroundColor = color
	return b
}

func (b *Box) SetDontClear(v bool) *Box {
	b.dontClear = v
	return b
}

func (b *Box) InRect(x, y int) bool {
	return x >= b.x && x < b.x+b.width && y >= b.y && y < b.y+b.height
}

func (b *Box) Draw(screen tcell.Screen) {
	if b.width <= 0 || b.height <= 0 {
		return
	}

	if !b.dontClear {
		background := tcell.StyleDefault.Background(b.backgroundColor)
		for row := b.y; row < b.y+b.height; row++ {
			for col := b.x; col < b.x+b.width; col++ {
				screen.SetContent(col, row, ' ', nil, background)
			}
		}
	}
}
