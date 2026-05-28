package tview

import "github.com/gdamore/tcell/v2"

const (
	FlexRow    = 0
	FlexColumn = 1
)

type flexItem struct {
	Item       Primitive
	FixedSize  int
	Proportion int
}

type Flex struct {
	*Box

	items     []*flexItem
	direction int
}

func NewFlex() *Flex {
	f := &Flex{
		direction: FlexColumn,
	}
	f.Box = NewBox()
	return f
}

func (f *Flex) SetDirection(direction int) *Flex {
	f.direction = direction
	return f
}

func (f *Flex) AddItem(item Primitive, fixedSize, proportion int) *Flex {
	f.items = append(f.items, &flexItem{Item: item, FixedSize: fixedSize, Proportion: proportion})
	return f
}

func (f *Flex) RemoveItem(p Primitive) *Flex {
	for index := len(f.items) - 1; index >= 0; index-- {
		if f.items[index].Item == p {
			f.items = append(f.items[:index], f.items[index+1:]...)
		}
	}
	return f
}

func (f *Flex) ItemCount() int {
	return len(f.items)
}

func (f *Flex) Clear() *Flex {
	f.items = nil
	return f
}

func (f *Flex) Draw(screen tcell.Screen) {
	f.Box.draw(screen, f)

	x, y, width, height := f.GetInnerRect()
	var proportionSum int
	distSize := width
	if f.direction == FlexRow {
		distSize = height
	}
	for _, item := range f.items {
		if item.FixedSize > 0 {
			distSize -= item.FixedSize
		} else {
			proportionSum += item.Proportion
		}
	}

	pos := x
	if f.direction == FlexRow {
		pos = y
	}
	for _, item := range f.items {
		size := item.FixedSize
		if size <= 0 {
			if proportionSum > 0 {
				size = distSize * item.Proportion / proportionSum
				distSize -= size
				proportionSum -= item.Proportion
			} else {
				size = 0
			}
		}
		if item.Item != nil {
			if f.direction == FlexColumn {
				item.Item.SetRect(pos, y, size, height)
			} else {
				item.Item.SetRect(x, pos, width, size)
			}
		}
		pos += size

		if item.Item != nil {
			item.Item.Draw(screen)
		}
	}
}
