package tview

import "github.com/gdamore/tcell/v2"

type Box struct {
	x, y, width, height int

	backgroundColor tcell.Color
	border          bool
	borderStyle     tcell.Style

	title      string
	titleColor tcell.Color
	titleAlign int

	paddingTop, paddingBottom, paddingLeft, paddingRight int

	dontClear bool
}

func NewBox() *Box {
	return &Box{
		titleAlign: AlignCenter,
	}
}

func (b *Box) SetDontClear(v bool) *Box {
	b.dontClear = v
	return b
}

func (b *Box) SetRect(x, y, width, height int) {
	b.x, b.y, b.width, b.height = x, y, width, height
}

func (b *Box) GetRect() (int, int, int, int) {
	return b.x, b.y, b.width, b.height
}

func (b *Box) GetInnerRect() (int, int, int, int) {
	x, y := b.x, b.y
	w, h := b.width, b.height
	if b.border {
		x++
		y++
		w -= 2
		h -= 2
	}
	x += b.paddingLeft
	y += b.paddingTop
	w -= b.paddingLeft + b.paddingRight
	h -= b.paddingTop + b.paddingBottom
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	return x, y, w, h
}

func (b *Box) SetBackgroundColor(color tcell.Color) *Box {
	b.backgroundColor = color
	return b
}

func (b *Box) SetBorder(show bool) *Box {
	b.border = show
	return b
}

func (b *Box) SetBorderStyle(style tcell.Style) *Box {
	b.borderStyle = style
	return b
}

func (b *Box) SetBorderColor(color tcell.Color) *Box {
	b.borderStyle = b.borderStyle.Foreground(color)
	return b
}

func (b *Box) SetBorderPadding(top, bottom, left, right int) *Box {
	b.paddingTop, b.paddingBottom, b.paddingLeft, b.paddingRight = top, bottom, left, right
	return b
}

func (b *Box) SetTitle(title string) *Box {
	b.title = title
	return b
}

func (b *Box) SetTitleColor(color tcell.Color) *Box {
	b.titleColor = color
	return b
}

func (b *Box) SetTitleAlign(align int) *Box {
	b.titleAlign = align
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

	if b.border && b.width >= 2 && b.height >= 2 {
		b.drawBorder(screen)
	}
}

func (b *Box) drawBorder(screen tcell.Screen) {
	horizontal := Borders.Horizontal
	vertical := Borders.Vertical
	topLeft := Borders.TopLeft
	topRight := Borders.TopRight
	bottomLeft := Borders.BottomLeft
	bottomRight := Borders.BottomRight

	for col := b.x + 1; col < b.x+b.width-1; col++ {
		screen.SetContent(col, b.y, horizontal, nil, b.borderStyle)
		screen.SetContent(col, b.y+b.height-1, horizontal, nil, b.borderStyle)
	}
	for row := b.y + 1; row < b.y+b.height-1; row++ {
		screen.SetContent(b.x, row, vertical, nil, b.borderStyle)
		screen.SetContent(b.x+b.width-1, row, vertical, nil, b.borderStyle)
	}
	screen.SetContent(b.x, b.y, topLeft, nil, b.borderStyle)
	screen.SetContent(b.x+b.width-1, b.y, topRight, nil, b.borderStyle)
	screen.SetContent(b.x, b.y+b.height-1, bottomLeft, nil, b.borderStyle)
	screen.SetContent(b.x+b.width-1, b.y+b.height-1, bottomRight, nil, b.borderStyle)

	if b.title != "" && b.width >= 4 {
		titleStyle := b.borderStyle.Foreground(b.titleColor)
		printed, _ := Print(screen, b.title, b.x+1, b.y, b.width-2, b.titleAlign, titleStyle)
		titleRunes := []rune(b.title)
		if len(titleRunes)-printed > 0 && printed > 0 {
			xEllipsis := b.x + b.width - 2
			if b.titleAlign == AlignRight {
				xEllipsis = b.x + 1
			}
			_, _, existingStyle, _ := screen.GetContent(xEllipsis, b.y)
			fg, _, _ := existingStyle.Decompose()
			Print(screen, string(SemigraphicsHorizontalEllipsis), xEllipsis, b.y, 1, AlignLeft, tcell.StyleDefault.Foreground(fg))
		}
	}
}
