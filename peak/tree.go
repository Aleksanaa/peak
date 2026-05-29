package main

import "github.com/gdamore/tcell/v2"

type DrawNode interface {
	Layout()
	Draw(tcell.Screen)
	Resize(x, y, w, h int)
	ShowCursor(tcell.Screen)
	GetBounds() (x, y, w, h int)
}

type TreeNode struct {
	BaseView
	children []DrawNode
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
