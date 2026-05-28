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
	MinSize    int
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
	f.Box.dontClear = true
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

func (f *Flex) SetMinSize(item Primitive, minSize int) {
	for _, fi := range f.items {
		if fi.Item == item {
			fi.MinSize = minSize
			return
		}
	}
}

func (f *Flex) ResizeItem(item Primitive, fixedSize, proportion int) {
	for _, fi := range f.items {
		if fi.Item == item {
			fi.FixedSize = fixedSize
			fi.Proportion = proportion
		}
	}
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

func (f *Flex) GetItemSize(item Primitive) (fixedSize, proportion int) {
	for _, fi := range f.items {
		if fi.Item == item {
			return fi.FixedSize, fi.Proportion
		}
	}
	return 0, 0
}

func (f *Flex) Layout() {
	x, y, width, height := f.GetInnerRect()
	distSize := width
	if f.direction == FlexRow {
		distSize = height
	}

	var proportionSum int
	for _, item := range f.items {
		if item.FixedSize > 0 {
			distSize -= item.FixedSize
		} else {
			distSize -= item.MinSize
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
			size = item.MinSize
			if proportionSum > 0 && distSize > 0 {
				extra := distSize * item.Proportion / proportionSum
				distSize -= extra
				proportionSum -= item.Proportion
				size += extra
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
	}
}

func (f *Flex) Draw(screen tcell.Screen) {
	f.Box.Draw(screen)
	// Layout is called here so that Draw is self-contained — callers don't
	// need a separate pre-draw Layout pass. This mirrors the original tview
	// design. The double-layout when resize() also calls Layout is pure
	// arithmetic with no allocation and is accepted as benign.
	f.Layout()
	for _, item := range f.items {
		if item.Item != nil {
			item.Item.Draw(screen)
		}
	}
}
