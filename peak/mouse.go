package main

import (
	"image"
	"time"

	"github.com/gdamore/tcell/v3"
)

// anyButton masks the three primary mouse buttons — every button except the
// wheel — i.e. the buttons that begin and sustain a drag or chord.
const anyButton = tcell.ButtonPrimary | tcell.ButtonSecondary | tcell.ButtonMiddle

// mouseTarget is the result of a single hit-test: the column, window and
// content view under a position, mirroring the layout tree (col -> win ->
// tag/body). A content hit (any tag or body) sets view; a chrome hit (column
// handle, window handle, scroll gutter) sets col/win and leaves view nil, and
// dispatch tells those apart by position. A zero mouseTarget means nothing
// actionable was hit.
type mouseTarget struct {
	col  *Column
	win  *Window
	view View // the tag/body content view; nil for handles and the scroll gutter
	x, y int  // the position in the innermost of view, win and col that was hit
}

// mouseGesture tracks the in-progress mouse gesture across events: the previous
// button mask (to detect deltas) and the view where the primary was pressed,
// which anchors a chord even if the pointer later moves away.
type mouseGesture struct {
	buttons    tcell.ButtonMask // buttons from the previous event
	anchorView View             // the tag/body/terminal view the primary was pressed on
	anchorWin  *Window
	chorded    bool
}

// resolveTarget hit-tests the screen position (x, y) against the editor
// layout and returns the column/window/content view under it. Every routing
// and chording decision starts here; dispatch makes the two chrome
// sub-distinctions (handle vs. scroll gutter, column handle vs. miss) from
// the pointers and position it returns. Each container hit-tests its
// children in its own coordinates.
func (e *Editor) resolveTarget(x, y int) mouseTarget {
	if y == 0 {
		return mouseTarget{view: e.tag, x: x, y: y}
	}
	for _, col := range e.columns {
		if col.contains(x, y) {
			return col.hit(x-col.x, y-col.y)
		}
	}
	return mouseTarget{}
}

func (c *Column) hit(x, y int) mouseTarget {
	if t := c.tagRect(); t.contains(x, y) {
		return mouseTarget{col: c, view: c.tag, x: x - t.x, y: y - t.y}
	}
	for _, win := range c.windows {
		if win.contains(x, y) {
			t := win.hit(x-win.x, y-win.y)
			t.col = c
			return t
		}
	}
	return mouseTarget{col: c, x: x, y: y} // the column handle, or off every window
}

func (w *Window) hit(x, y int) mouseTarget {
	if t := w.tagRect(); t.contains(x, y) {
		return mouseTarget{win: w, view: w.tag, x: x - t.x, y: y - t.y}
	}
	if b := w.bodyRect(); b.contains(x, y) {
		return mouseTarget{win: w, view: w.body, x: x - b.x, y: y - b.y}
	}
	return mouseTarget{win: w, x: x, y: y} // handle rows / scroll gutter
}

// chordTargetOf reports the view eligible for chording at a resolved target and
// its owning window, or (nil, nil) if the hit is not a chordable content area.
// Any content view chords: a text tag/body cuts and pastes its buffer, a
// terminal body copies (Snarf) and pastes to the pty. Handles and the scroll
// gutter have no view and are not chordable.
func (e *Editor) chordTargetOf(t mouseTarget) (View, *Window) {
	switch t.view.(type) {
	case *TextView, *TermView:
		return t.view, t.win
	}
	return nil, nil
}

// chordAllowed reports whether a chord may operate locally on the pressed view.
// A full-screen terminal app forwards its buttons to the child program, so we
// suppress local chording there — letting the child run its own chord — unless
// the user holds Ctrl to force a local action. This mirrors the word-execution
// gate in clickWindow; ordinary text views are never raw and always chord.
func (e *Editor) chordAllowed(v View, ev *tcell.EventMouse) bool {
	return ev.Modifiers()&tcell.ModCtrl != 0 || !v.IsRaw()
}

// handleMouse is the single entry point for EventMouse. It layers acme-style
// chording over the drag/scroll state machine and dispatches fresh presses to
// the surface under the pointer. Returns true to quit the editor.
func (e *Editor) handleMouse(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()
	buttons := ev.Buttons()
	g := &e.gesture

	// Chord layer, driven by button-mask deltas. Wheel events carry no chord
	// meaning and must not disturb the gesture bookkeeping.
	if buttons&(tcell.WheelUp|tcell.WheelDown|tcell.WheelLeft|tcell.WheelRight) == 0 {
		prev := g.buttons
		g.buttons = buttons
		newMiddle := buttons&tcell.ButtonMiddle != 0 && prev&tcell.ButtonMiddle == 0
		newSecondary := buttons&tcell.ButtonSecondary != 0 && prev&tcell.ButtonSecondary == 0
		switch {
		case buttons == tcell.ButtonNone:
			*g = mouseGesture{}
		case g.anchorView != nil && buttons&tcell.ButtonPrimary != 0 && (newMiddle || newSecondary):
			// A second button joined the held primary: Button2 cuts, Button3
			// pastes. Repeatable while primary stays down (e.g. cut then paste
			// without releasing, and vice versa).
			e.fireChord(newMiddle)
			return false
		case g.chorded:
			// After a chord, swallow held-primary moves so they do not start a
			// fresh drag; a further chord button still fires in the case above.
			return false
		}
	}

	// Drag/scroll state machine. An active drag owns the gesture until release.
	if buttons == tcell.ButtonNone {
		e.scrollWin = nil
	}
	if e.dragCol != nil {
		if buttons&anyButton != 0 {
			e.moveColumnTo(e.dragCol, mx)
			return false
		}
		e.dragCol = nil
		return false
	}
	if e.dragWin != nil {
		if buttons&anyButton != 0 {
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
		ev := relative(ev, e.dragOrigin)
		quit := e.dragView.HandleEvent(ev)
		if buttons == tcell.ButtonNone {
			e.dragView = nil
		} else if buttons&tcell.ButtonPrimary != 0 {
			_, y := ev.Position()
			e.trackDragScroll(e.dragView, y)
		}
		return quit
	}

	return e.dispatchPress(ev, mx, my, buttons)
}

// relative returns ev as seen from origin.
func relative(ev *tcell.EventMouse, origin image.Point) *tcell.EventMouse {
	x, y := ev.Position()
	return tcell.NewEventMouse(x-origin.X, y-origin.Y, ev.Buttons(), ev.Modifiers())
}

// dispatchPress handles a fresh press: it arms chording on a primary-only press
// over a text area, then routes the event to the surface under it.
func (e *Editor) dispatchPress(ev *tcell.EventMouse, mx, my int, buttons tcell.ButtonMask) bool {
	t := e.resolveTarget(mx, my)

	// Anchor a potential chord to the view under a primary-only press, so a
	// later second button operates on it even if the pointer has moved.
	if buttons&tcell.ButtonPrimary != 0 && buttons&(tcell.ButtonMiddle|tcell.ButtonSecondary) == 0 {
		if v, win := e.chordTargetOf(t); v != nil && e.chordAllowed(v, ev) {
			e.gesture.anchorView, e.gesture.anchorWin = v, win
		}
	}

	held := buttons&anyButton != 0

	switch {
	case t.view != nil: // a content region: global/column/window tag or window body
		// The view sees the event in its own coordinates, as it does any
		// drag the press starts.
		e.dragOrigin = image.Pt(mx-t.x, my-t.y)
		ev := relative(ev, e.dragOrigin)
		if t.win != nil {
			return e.clickWindow(ev, t, buttons)
		}
		return e.clickTag(ev, t.view, t.col, buttons)

	case t.win != nil: // window chrome: handle in the tag rows, scroll gutter below
		win := t.win
		if t.y < win.tag.h {
			if held {
				e.dragWin = win
				e.dragWinOrigH = win.explicitHeight
				e.dragWinButton = buttons
				e.dragWinStartY = win.y
				e.ActivateWindow(win)
				e.focusedView = win.tag
			}
		} else {
			e.scrollWindow(win, t.y-win.tag.h, buttons)
		}

	case t.col != nil && t.x == 0 && t.y == 0: // column handle
		if held {
			e.dragCol = t.col
			e.dragColOrigW = t.col.explicitWidth
		}
	}
	return false
}

// clickTag handles a primary/middle/secondary click on the global tag (col nil)
// or a column tag. It executes/plumbs a clicked word or starts a tag selection.
func (e *Editor) clickTag(ev *tcell.EventMouse, tag View, col *Column, buttons tcell.ButtonMask) bool {
	word := tag.GetClickWord(ev.Position())
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
// jumps to a position proportional to the click. row is the clicked row of
// the body.
func (e *Editor) scrollWindow(win *Window, row int, buttons tcell.ButtonMask) {
	amount := row + 1
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
			newScroll := (row * total) / visible
			win.body.Scroll(newScroll - scroll)
		}
	}
}

// clickWindow handles a click in a window's tag or body: it activates the
// window, starts a selection, and executes/plumbs a Button2/Button3 word.
func (e *Editor) clickWindow(ev *tcell.EventMouse, t mouseTarget, buttons tcell.ButtonMask) bool {
	win, target := t.win, t.view
	if buttons == tcell.ButtonPrimary {
		e.ActivateWindow(win)
		if t.view == win.tag {
			e.focusedView = win.tag
		}
		e.dragView = e.focusedView
	}

	target.HandleEvent(ev)
	var word string
	if buttons&(tcell.ButtonMiddle|tcell.ButtonSecondary) != 0 && (!target.IsRaw() || ev.Modifiers()&tcell.ModCtrl != 0) {
		if word = target.GetClickWord(t.x, t.y); word != "" {
			q0, q1 := win.clickWordOffsets(target, t.x, t.y, word)
			if buttons&tcell.ButtonMiddle != 0 {
				win.broadcastEvent('M', 'x', q0, q1, 0, word)
			} else {
				win.broadcastEvent('M', 'l', q0, q1, 0, word)
			}
		}
	}

	if word == "" {
		return false
	}
	if buttons&tcell.ButtonMiddle != 0 {
		return e.Execute(win.parent, win, word)
	}
	return e.Plumb(win, word)
}

// fireChord executes an acme chord on the anchored view's live selection:
// Button1+Button2 (middle) cuts, Button1+Button3 pastes. A terminal body can't
// be edited, so there middle copies (Snarf) instead of cutting and paste feeds
// the pty. A plain click clears the selection on press, so a chord that follows
// one finds nothing selected and does nothing. The interrupted drag needs no
// teardown here: g.chorded swallows the held-primary events that follow, and
// the release resets the drag state.
func (e *Editor) fireChord(middle bool) {
	g := &e.gesture
	win := g.anchorWin
	g.chorded = true

	switch v := g.anchorView.(type) {
	case *TermView:
		if middle {
			v.Snarf()
		} else {
			v.Paste()
		}
	case *TextView:
		if middle {
			v.typingStart = nil
			v.buffer.Cut()
		} else {
			v.prepareTyping()
			v.buffer.Paste()
		}
	}

	// A sweep that reached the view edge armed the auto-scroll timer; the chord
	// ends the sweep, so stop it (the timer keys off scrollWin, not gesture).
	if e.scrollWin == win {
		e.scrollWin = nil
	}
}
