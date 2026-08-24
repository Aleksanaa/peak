package main

import (
	"time"

	"github.com/gdamore/tcell/v3"
)

// Region identifies the kind of surface under a mouse position. It is the
// single vocabulary every mouse gesture is routed through.
type Region int

const (
	RegionNone Region = iota
	RegionGlobalTag
	RegionColHandle // column separator/handle (starts a column drag)
	RegionColTag
	RegionWinHandle // window handle in the tag rows (starts a window drag)
	RegionWinScroll // window scroll gutter in the body rows
	RegionWinTag
	RegionWinBody
)

// mouseTarget is the result of a single hit-test: which region was hit and the
// owning column/window/view, so callers never re-derive geometry.
type mouseTarget struct {
	region Region
	col    *Column
	win    *Window
	view   View // the tag/body view for text regions; nil otherwise
}

// mouseGesture tracks the in-progress mouse gesture across events: the previous
// button mask (to detect deltas) and the view where the primary was pressed,
// which anchors a chord even if the pointer later moves away.
type mouseGesture struct {
	buttons   tcell.ButtonMask // buttons from the previous event
	anchorTV  *TextView
	anchorWin *Window
	chorded   bool
}

// resolveTarget hit-tests (mx, my) against the editor layout and returns the
// region under it. This is the one place mouse geometry lives; every routing
// and chording decision consults it.
func (e *Editor) resolveTarget(mx, my int) mouseTarget {
	if my == 0 {
		return mouseTarget{region: RegionGlobalTag, view: e.tag}
	}
	for _, col := range e.columns {
		if !col.Contains(mx, my) {
			continue
		}
		if my == col.tag.y {
			if mx == col.x {
				return mouseTarget{region: RegionColHandle, col: col}
			}
			return mouseTarget{region: RegionColTag, col: col, view: col.tag}
		}
		for _, win := range col.windows {
			if !win.Contains(mx, my) {
				continue
			}
			win.tag.UpdateLayout()
			th := win.tagHeight()
			if mx == win.x {
				if my < win.y+th {
					return mouseTarget{region: RegionWinHandle, col: col, win: win}
				}
				return mouseTarget{region: RegionWinScroll, col: col, win: win}
			}
			if my < win.y+th {
				return mouseTarget{region: RegionWinTag, col: col, win: win, view: win.tag}
			}
			return mouseTarget{region: RegionWinBody, col: col, win: win, view: win.body}
		}
		return mouseTarget{region: RegionNone, col: col}
	}
	return mouseTarget{region: RegionNone}
}

// chordTargetOf reports the TextView eligible for chording at a resolved target
// and its owning window, or (nil, nil) if the region is not a chordable text
// area (handles, scroll gutters and terminal windows are excluded).
func (e *Editor) chordTargetOf(t mouseTarget) (*TextView, *Window) {
	switch t.region {
	case RegionGlobalTag, RegionColTag, RegionWinTag, RegionWinBody:
	default:
		return nil, nil
	}
	if t.win != nil && t.win.kind == WinTerm {
		return nil, nil
	}
	if tv, ok := t.view.(*TextView); ok {
		return tv, t.win
	}
	return nil, nil
}

// chordTarget hit-tests and reports the chordable TextView at (mx, my).
func (e *Editor) chordTarget(mx, my int) (*TextView, *Window) {
	return e.chordTargetOf(e.resolveTarget(mx, my))
}

// handleMouse is the single entry point for EventMouse. It layers acme-style
// chording over the drag/scroll state machine and dispatches fresh presses by
// region. Returns true to quit the editor.
func (e *Editor) handleMouse(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()
	buttons := ev.Buttons()
	g := &e.gesture

	// Chord layer, driven by button-mask deltas. Wheel events carry no chord
	// meaning and must not disturb the gesture bookkeeping.
	if buttons&(tcell.WheelUp|tcell.WheelDown|tcell.WheelLeft|tcell.WheelRight) == 0 {
		prev := g.buttons
		g.buttons = buttons
		if buttons == tcell.ButtonNone {
			*g = mouseGesture{}
		} else if g.chorded {
			// Swallow everything until the buttons release.
			return false
		} else if buttons&tcell.ButtonPrimary != 0 &&
			buttons&(tcell.ButtonMiddle|tcell.ButtonSecondary) != 0 &&
			prev&(tcell.ButtonMiddle|tcell.ButtonSecondary) == 0 &&
			g.anchorTV != nil {
			// A second button joined a held, armed primary: fire the chord.
			e.fireChord(buttons&tcell.ButtonMiddle != 0)
			return false
		}
	}

	// Drag/scroll state machine. An active drag owns the gesture until release.
	if buttons == tcell.ButtonNone {
		e.scrollWin = nil
	}
	if e.dragCol != nil {
		if buttons&(tcell.ButtonPrimary|tcell.ButtonSecondary|tcell.ButtonMiddle) != 0 {
			e.moveColumnTo(e.dragCol, mx)
			return false
		}
		e.dragCol = nil
		return false
	}
	if e.dragWin != nil {
		if buttons&(tcell.ButtonPrimary|tcell.ButtonSecondary|tcell.ButtonMiddle) != 0 {
			e.moveWindowTo(e.dragWin, mx, my)
			return false
		}
		if e.dragWin.y == e.dragWinStartY {
			switch e.dragWinButton {
			case tcell.ButtonPrimary:
				e.dragWin.parent.GrowModerate(e.dragWin)
			case tcell.ButtonSecondary:
				e.dragWin.parent.Maximize(e.dragWin)
			case tcell.ButtonMiddle:
				e.dragWin.parent.GrowFull(e.dragWin)
			}
		}
		e.dragWin = nil
		return false
	}
	if e.dragView != nil {
		quit := e.dragView.HandleEvent(ev)
		if buttons == tcell.ButtonNone {
			e.dragView = nil
		} else if buttons&tcell.ButtonPrimary != 0 {
			e.trackDragScroll(e.dragView, my)
		}
		return quit
	}

	return e.dispatchPress(ev, mx, my, buttons)
}

// dispatchPress handles a fresh press: it arms chording on a primary-only press
// over a text area, then routes the event to the region under it.
func (e *Editor) dispatchPress(ev *tcell.EventMouse, mx, my int, buttons tcell.ButtonMask) bool {
	t := e.resolveTarget(mx, my)

	// Anchor a potential chord to the view under a primary-only press, so a
	// later second button operates on it even if the pointer has moved.
	if buttons&tcell.ButtonPrimary != 0 && buttons&(tcell.ButtonMiddle|tcell.ButtonSecondary) == 0 {
		if tv, win := e.chordTargetOf(t); tv != nil {
			e.gesture.anchorTV, e.gesture.anchorWin = tv, win
		}
	}

	switch t.region {
	case RegionGlobalTag:
		return e.clickTag(ev, e.tag, nil, mx, my, buttons)
	case RegionColHandle:
		if buttons&(tcell.ButtonPrimary|tcell.ButtonSecondary|tcell.ButtonMiddle) != 0 {
			e.dragCol = t.col
			e.dragColOrigW = t.col.explicitWidth
		}
		return false
	case RegionColTag:
		return e.clickTag(ev, t.col.tag, t.col, mx, my, buttons)
	case RegionWinHandle:
		if buttons&(tcell.ButtonPrimary|tcell.ButtonSecondary|tcell.ButtonMiddle) != 0 {
			win := t.win
			e.dragWin = win
			e.dragWinOrigH = win.explicitHeight
			e.dragWinButton = buttons
			e.dragWinStartY = win.y
			e.ActivateWindow(win)
			e.focusedView = win.tag
		}
		return false
	case RegionWinScroll:
		e.scrollWindow(t.win, my, buttons)
		return false
	case RegionWinTag, RegionWinBody:
		return e.clickWindow(ev, t, mx, my, buttons)
	}
	return false
}

// clickTag handles a primary/middle/secondary click on the global tag (col nil)
// or a column tag. It executes/plumbs a clicked word or starts a tag selection.
func (e *Editor) clickTag(ev *tcell.EventMouse, tag *TextView, col *Column, mx, my int, buttons tcell.ButtonMask) bool {
	word := tag.GetClickWord(mx, my)
	if word != "" {
		if buttons == tcell.ButtonMiddle {
			return e.Execute(col, nil, word)
		}
		if buttons == tcell.ButtonSecondary {
			return e.Plumb(nil, word)
		}
	}
	if buttons == tcell.ButtonPrimary {
		e.dragView, e.focusedView = tag, tag
	}
	return tag.HandleEvent(ev)
}

// scrollWindow implements the window scroll gutter: Button1 scrolls up,
// Button3 scrolls down (both auto-repeat via the main-loop timer), Button2
// jumps to a position proportional to the click.
func (e *Editor) scrollWindow(win *Window, my int, buttons tcell.ButtonMask) {
	th := win.tagHeight()
	amount := my - (win.y + th) + 1
	switch {
	case buttons&tcell.ButtonPrimary != 0:
		if e.scrollWin == nil {
			win.body.Scroll(-amount)
			e.scrollStartTime = time.Now()
		}
		e.scrollWin, e.scrollAmount, e.scrollDir = win, amount, -1
	case buttons&tcell.ButtonSecondary != 0:
		if e.scrollWin == nil {
			win.body.Scroll(amount)
			e.scrollStartTime = time.Now()
		}
		e.scrollWin, e.scrollAmount, e.scrollDir = win, amount, 1
	case buttons&tcell.ButtonMiddle != 0:
		if scroll, total, visible := win.body.GetScroll(); visible > 0 && total > 0 {
			newScroll := ((my - (win.y + th)) * total) / visible
			win.body.Scroll(newScroll - scroll)
		}
	}
}

// clickWindow handles a click in a window's tag or body: it activates the
// window, starts a selection, and executes/plumbs a Button2/Button3 word.
func (e *Editor) clickWindow(ev *tcell.EventMouse, t mouseTarget, mx, my int, buttons tcell.ButtonMask) bool {
	win, target := t.win, t.view
	if buttons == tcell.ButtonPrimary {
		e.ActivateWindow(win)
		if t.region == RegionWinTag {
			e.focusedView = win.tag
		}
		e.dragView = e.focusedView
	}

	win.lk.Lock()
	target.HandleEvent(ev)
	var word string
	if buttons&(tcell.ButtonMiddle|tcell.ButtonSecondary) != 0 && (!target.IsRaw() || ev.Modifiers()&tcell.ModCtrl != 0) {
		if word = target.GetClickWord(mx, my); word != "" {
			q0, q1 := win.clickWordOffsets(target, mx, my, word)
			if buttons&tcell.ButtonMiddle != 0 {
				win.broadcastEvent('M', 'x', q0, q1, 0, word)
			} else {
				win.broadcastEvent('M', 'l', q0, q1, 0, word)
			}
		}
	}
	win.lk.Unlock()

	if word == "" {
		return false
	}
	if buttons&tcell.ButtonMiddle != 0 {
		return win.onExec(win.parent, win, word)
	}
	return e.Plumb(win, word)
}

// fireChord executes an acme chord (Button1+Button2 = Cut, Button1+Button3 =
// Paste) on the anchored view's live selection, then tears down the drag the
// primary press had started. A plain click clears the selection on press, so a
// chord that follows one finds nothing to cut and does nothing.
func (e *Editor) fireChord(cut bool) {
	g := &e.gesture
	tv, win := g.anchorTV, g.anchorWin
	g.chorded = true

	if win != nil {
		win.lk.Lock()
		defer win.lk.Unlock()
	}
	if cut {
		tv.typingStart = nil
		tv.buffer.Cut()
	} else {
		tv.prepareTyping()
		tv.buffer.Paste()
	}

	tv.drag = false
	if e.dragView == tv {
		e.dragView = nil
	}
	if e.scrollWin == win {
		e.scrollWin = nil
	}
}
