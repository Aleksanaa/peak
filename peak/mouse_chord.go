package main

import (
	"github.com/gdamore/tcell/v3"
)

// handleMouseChord implements acme-style mouse chording.
//
// tcell v3 reports the full set of pressed buttons in every EventMouse:
// its input parser accumulates button state in btnsDown and hands back
// the combined mask (see the 'M' press case in tcell's input.go), so a
// chord is simply the primary button held at the same time as the middle
// or secondary button. The initial primary press is left for normal
// dispatch so it can anchor a drag selection; the chord fires the moment
// a second button joins. Each chord fires exactly once -- later events
// are consumed until all buttons are released.
//
// Returns true if the event is consumed (suppressed); false to continue
// normal mouse dispatch.
func (e *Editor) handleMouseChord(mx, my int, buttons tcell.ButtonMask) bool {
	if buttons&(tcell.WheelUp|tcell.WheelDown|tcell.WheelLeft|tcell.WheelRight) != 0 {
		return false
	}

	// A full release ends any in-progress chord.
	if buttons == tcell.ButtonNone {
		e.chordActive = false
		return false
	}

	// Once a chord has fired, swallow everything until the buttons release
	// so the underlying drag/selection logic never sees stray events.
	if e.chordActive {
		return true
	}

	// A chord requires the primary button plus a second button.
	if buttons&tcell.ButtonPrimary == 0 {
		return false
	}
	if buttons&(tcell.ButtonMiddle|tcell.ButtonSecondary) == 0 {
		// Primary alone: let normal dispatch anchor the selection.
		return false
	}

	tv, win := e.findChordTarget(mx, my)
	if tv == nil {
		return false
	}

	if buttons&tcell.ButtonMiddle != 0 {
		e.fireCutChord(tv, win)
	} else {
		e.firePasteChord(tv, win)
	}
	e.finishChordDrag(tv, win)
	e.chordActive = true
	return true
}

// findChordTarget returns the eligible TextView and its owning Window at
// (mx, my), or (nil, nil) if the position is not eligible for chording.
func (e *Editor) findChordTarget(mx, my int) (*TextView, *Window) {
	if my == 0 {
		return nil, nil
	}
	for _, col := range e.columns {
		if !col.Contains(mx, my) {
			continue
		}
		if my == col.y {
			return nil, nil
		}
		for _, win := range col.windows {
			if !win.Contains(mx, my) {
				continue
			}
			if mx == win.x {
				return nil, nil
			}
			win.tag.UpdateLayout()
			th := win.tagHeight()
			if win.kind == WinTerm {
				return nil, nil
			}
			if my < win.y+th {
				return win.tag, win
			}
			if mx == win.bodyView.scroll.x {
				return nil, nil
			}
			if tv, ok := win.body.(*TextView); ok {
				return tv, win
			}
			return nil, nil
		}
	}
	return nil, nil
}

// finishChordDrag tears down the drag state the primary press armed, so
// later released events do not resume selecting.
func (e *Editor) finishChordDrag(tv *TextView, win *Window) {
	tv.drag = false
	if e.dragView == tv {
		e.dragView = nil
	}
	if e.scrollWin == win {
		e.scrollWin = nil
	}
}

func (e *Editor) fireCutChord(tv *TextView, win *Window) {
	if win != nil {
		win.lk.Lock()
		defer win.lk.Unlock()
	}
	tv.typingStart = nil
	tv.buffer.Cut()
}

func (e *Editor) firePasteChord(tv *TextView, win *Window) {
	if win != nil {
		win.lk.Lock()
		defer win.lk.Unlock()
	}
	tv.prepareTyping()
	tv.buffer.Paste()
}
