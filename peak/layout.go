package main

import "github.com/gdamore/tcell/v3"

// A rect is an area in its parent's coordinates.
type rect struct{ x, y, w, h int }

func (r rect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

// A canvas is an area of the screen with its own origin. Drawing outside it
// is clipped.
type canvas struct {
	s          tcell.Screen
	x, y, w, h int
}

// sub returns the area r of cv, as a canvas of its own.
func (cv canvas) sub(r rect) canvas {
	return canvas{cv.s, cv.x + r.x, cv.y + r.y, max(0, min(r.w, cv.w-r.x)), max(0, min(r.h, cv.h-r.y))}
}

func (cv canvas) put(x, y int, str string, st tcell.Style) {
	if (rect{0, 0, cv.w, cv.h}).contains(x, y) {
		cv.s.Put(cv.x+x, cv.y+y, str, st)
	}
}

// fill paints the area r of cv blank in style st.
func (cv canvas) fill(r rect, st tcell.Style) {
	for y := r.y; y < r.y+r.h; y++ {
		for x := r.x; x < r.x+r.w; x++ {
			cv.put(x, y, " ", st)
		}
	}
}

func (cv canvas) showCursor(x, y int) {
	if (rect{0, 0, cv.w, cv.h}).contains(x, y) {
		cv.s.ShowCursor(cv.x+x, cv.y+y)
	}
}

type sizer interface {
	PreferredSize() int
	MinSize() int
}

// distribute splits total among children by their preferred sizes, scaled by
// how total changed since lastTotal; children without one share the rest.
func distribute[T sizer](children []T, total int, lastTotal int) []int {
	heights := make([]int, len(children))
	totalExplicit, numAuto := 0, 0

	ratio := 1.0
	if lastTotal > 0 && lastTotal != total {
		ratio = float64(total) / float64(lastTotal)
	}

	for i, c := range children {
		if p := c.PreferredSize(); p > 0 {
			heights[i] = int(float64(p) * ratio)
			totalExplicit += heights[i]
		} else {
			numAuto++
		}
	}

	if numAuto > 0 && totalExplicit >= total {
		targetAuto := max(5*numAuto, (total*numAuto)/len(children))
		if totalExplicit > 0 {
			scale := float64(total-targetAuto) / float64(totalExplicit)
			totalExplicit = 0
			for i, c := range children {
				if c.PreferredSize() > 0 {
					heights[i] = max(c.MinSize(), int(float64(heights[i])*scale))
					totalExplicit += heights[i]
				}
			}
		}
	}

	autoSpace := 0
	if numAuto > 0 {
		autoSpace = max(5, (total-totalExplicit)/numAuto)
	}

	actualTotal := 0
	for i, c := range children {
		if heights[i] <= 0 {
			heights[i] = autoSpace
		}
		heights[i] = max(heights[i], c.MinSize())
		actualTotal += heights[i]
	}

	if n := len(children); n > 0 {
		heights[n-1] = max(heights[n-1]+total-actualTotal, children[n-1].MinSize())
	}

	return heights
}
