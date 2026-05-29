package main

import "github.com/gdamore/tcell/v2"

type DrawNode interface {
	Layout()
	Draw(tcell.Screen)
	Resize(x, y, w, h int)
	ShowCursor(tcell.Screen)
	GetBounds() (x, y, w, h int)
}

type Sizer interface {
	PreferredSize() int
	MinSize() int
	SetExplicit(int)
}

type TreeNode struct {
	BaseView
	children []DrawNode
	lastSize int
}

func (n *TreeNode) Children() []DrawNode { return n.children }

func (n *TreeNode) AddChild(c ...DrawNode) { n.children = append(n.children, c...) }

func (n *TreeNode) ClearChildren() { n.children = n.children[:0] }

func (n *TreeNode) WalkLayout() {
	for _, c := range n.children {
		if p, ok := c.(interface{ WalkLayout() }); ok {
			p.WalkLayout()
		}
		c.Layout()
	}
}

func (n *TreeNode) WalkDraw(s tcell.Screen) {
	for _, c := range n.children {
		c.Draw(s)
		if p, ok := c.(interface{ WalkDraw(tcell.Screen) }); ok {
			p.WalkDraw(s)
		}
	}
}

func (n *TreeNode) Walk(fn func(DrawNode)) {
	for _, c := range n.children {
		fn(c)
		if p, ok := c.(interface{ Walk(func(DrawNode)) }); ok {
			p.Walk(fn)
		}
	}
}

func distribute(children []DrawNode, total int, lastTotal int) []int {
	heights := make([]int, len(children))
	totalExplicit, numAuto := 0, 0

	ratio := 1.0
	if lastTotal > 0 && lastTotal != total {
		ratio = float64(total) / float64(lastTotal)
	}

	for i, c := range children {
		if s, ok := c.(Sizer); ok && s.PreferredSize() > 0 {
			heights[i] = int(float64(s.PreferredSize()) * ratio)
			totalExplicit += heights[i]
		} else {
			numAuto++
		}
	}

	if numAuto > 0 && totalExplicit >= total {
		targetAuto := (total * numAuto) / (len(children) + 1)
		if targetAuto < 5*numAuto {
			targetAuto = 5 * numAuto
		}
		if totalExplicit > 0 {
			scale := float64(total-targetAuto) / float64(totalExplicit)
			totalExplicit = 0
			for i, c := range children {
				if s, ok := c.(Sizer); ok && s.PreferredSize() > 0 {
					heights[i] = int(float64(heights[i]) * scale)
					totalExplicit += heights[i]
				}
			}
		}
	}

	autoSpace := 0
	if numAuto > 0 {
		autoSpace = (total - totalExplicit) / numAuto
		if autoSpace < 5 {
			autoSpace = 5
		}
	}

	actualTotal := 0
	for i, c := range children {
		h := heights[i]
		if h <= 0 {
			h = autoSpace
		}
		if s, ok := c.(Sizer); ok {
			if h < s.MinSize() {
				h = s.MinSize()
			}
		} else if h < 1 {
			h = 1
		}
		heights[i] = h
		actualTotal += h
	}

	if len(children) > 0 {
		heights[len(children)-1] += total - actualTotal
		if s, ok := children[len(children)-1].(Sizer); ok {
			if heights[len(children)-1] < s.MinSize() {
				heights[len(children)-1] = s.MinSize()
			}
		}
	}

	return heights
}
