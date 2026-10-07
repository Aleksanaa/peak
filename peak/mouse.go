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

// mouseGesture tracks the chord layer of the gesture across events: the previous
// button mask (to detect deltas) and the view where the primary was pressed,
// which anchors a chord even if the pointer later moves away.
type mouseGesture struct {
	buttons    tcell.ButtonMask // buttons from the previous event
	anchorView View             // the tag/body/terminal view the primary was pressed on
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

	// A gesture under way owns the mouse until it ends; releasing the buttons
	// stops any auto-scroll.
	if buttons == tcell.ButtonNone {
		e.repeat = nil
	}
	if e.capture != nil {
		if !e.capture(ev) {
			e.capture = nil
		}
		return false
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
	// later second button operates on it even if the pointer has moved. Any
	// view chords: a text tag/body cuts and pastes its buffer, a terminal body
	// copies (Snarf) and pastes to the pty. Handles and the scroll gutter have
	// no view and do not chord.
	if buttons&tcell.ButtonPrimary != 0 && buttons&(tcell.ButtonMiddle|tcell.ButtonSecondary) == 0 {
		if t.view != nil && e.chordAllowed(t.view, ev) {
			e.gesture.anchorView = t.view
		}
	}

	held := buttons&anyButton != 0

	switch {
	case t.view != nil: // a content region: global/column/window tag or window body
		// The view sees the event in its own coordinates, as it does the rest
		// of a sweep the press starts.
		origin := image.Pt(mx-t.x, my-t.y)
		if buttons == tcell.ButtonPrimary {
			e.sweep(t.view, t.win, origin)
		}
		ev := relative(ev, origin)
		if t.win != nil {
			return e.clickWindow(ev, t, buttons)
		}
		return e.clickTag(ev, t.view, t.col, buttons)

	case t.win != nil: // window chrome: handle in the tag rows, scroll gutter below
		win := t.win
		if t.y < win.tag.h {
			if held {
				e.dragWindow(win, buttons)
			}
		} else if held {
			e.scrollBar(win, ev, my-(t.y-win.tag.h), buttons)
		}

	case t.col != nil && t.x == 0 && t.y == 0: // column handle
		if held {
			e.dragColumn(t.col)
		}
	}
	return false
}

// sweep makes v, pressed at origin on screen, follow the pointer in its own
// coordinates until release. A sweep in a window's body that reaches its top
// or bottom row scrolls it, extending the selection.
func (e *Editor) sweep(v View, win *Window, origin image.Point) {
	e.capture = func(ev *tcell.EventMouse) bool {
		ev = relative(ev, origin)
		v.HandleEvent(ev)
		if ev.Buttons() == tcell.ButtonNone {
			return false
		}
		if ev.Buttons()&tcell.ButtonPrimary != 0 && win != nil && v == win.body {
			_, y := ev.Position()
			_, _, h := v.GetScroll()
			e.repeat = nil
			if dir := edge(y, h); dir != 0 {
				e.repeat = func() { scrollStep(win, dir, 1) }
			}
		}
		return true
	}
}

// edge reports whether row y of a view h rows high is at its bottom (1), its
// top (-1) or neither (0).
func edge(y, h int) int {
	switch {
	case y >= h-1:
		return 1
	case y <= 0:
		return -1
	}
	return 0
}

// scrollStep scrolls win's body n rows in direction dir, unless it is already
// at the end, and extends a sweep under way along with it.
func scrollStep(win *Window, dir, n int) {
	if scroll, total, visible := win.body.GetScroll(); dir > 0 && scroll+visible >= total {
		return
	}
	win.body.Scroll(dir * n)
	win.body.AdvanceSweep(dir)
}

// dragWindow moves win by its handle with the pointer, within or across
// columns, until release. Released where it began, the window grows instead:
// Button1 moderately, Button2 to the full column, Button3 to the only one
// shown.
func (e *Editor) dragWindow(win *Window, button tcell.ButtonMask) {
	e.ActivateWindow(win)
	e.focusedView = win.tag
	origH, startY := win.explicitHeight, win.y
	e.capture = func(ev *tcell.EventMouse) bool {
		if ev.Buttons()&anyButton != 0 {
			x, y := ev.Position()
			if e.moveWindowTo(win, x, y, origH) {
				origH, startY = win.explicitHeight, -1 // no growing in another column
			}
			return true
		}
		if win.y == startY {
			switch button {
			case tcell.ButtonPrimary:
				win.parent.GrowModerate(win)
			case tcell.ButtonSecondary:
				win.parent.Maximize(win)
			case tcell.ButtonMiddle:
				win.parent.GrowFull(win)
			}
		}
		return false
	}
}

// dragColumn moves col by its handle with the pointer until release.
func (e *Editor) dragColumn(col *Column) {
	origW := col.explicitWidth
	e.capture = func(ev *tcell.EventMouse) bool {
		if ev.Buttons()&anyButton == 0 {
			return false
		}
		x, _ := ev.Position()
		e.moveColumnTo(col, x, origW)
		return true
	}
}

// clickTag handles a primary/middle/secondary click on the global tag (col nil)
// or a column tag. It executes/plumbs a clicked word or starts a tag selection.
func (e *Editor) clickTag(ev *tcell.EventMouse, tag View, col *Column, buttons tcell.ButtonMask) bool {
	_, _, word := clickRange(tag.GetBuffer(), tag.PosAt(ev.Position()))
	if word != "" {
		if buttons == tcell.ButtonMiddle {
			return e.Execute(col, nil, word)
		}
		if buttons == tcell.ButtonSecondary {
			return e.Plumb(nil, word)
		}
	}
	if buttons == tcell.ButtonPrimary {
		e.focusedView = tag
	}
	tag.HandleEvent(ev)
	return false
}

// scrollBar scrolls win's body with its scroll bar until release, top being
// the screen row of the body's first line. Button1 scrolls up and Button3
// down by as many rows as the pointer is from top, repeating while held;
// Button2 jumps to the place in proportion to the pointer, following it.
func (e *Editor) scrollBar(win *Window, ev *tcell.EventMouse, top int, buttons tcell.ButtonMask) {
	dir := 0
	switch {
	case buttons&tcell.ButtonPrimary != 0:
		dir = -1
	case buttons&tcell.ButtonSecondary != 0:
		dir = 1
	}
	amount := 0
	follow := func(ev *tcell.EventMouse) {
		_, y := ev.Position()
		scroll, total, visible := win.body.GetScroll()
		row := max(0, min(y-top, visible-1))
		if dir != 0 {
			amount = row + 1
		} else if visible > 0 && total > 0 {
			win.body.Scroll(row*total/visible - scroll)
		}
	}

	follow(ev)
	if dir != 0 {
		win.body.Scroll(dir * amount)
		start := time.Now()
		e.repeat = func() {
			if time.Since(start) > 200*time.Millisecond {
				scrollStep(win, dir, amount)
			}
		}
	}
	e.capture = func(ev *tcell.EventMouse) bool {
		if ev.Buttons()&anyButton != 0 {
			follow(ev)
		}
		return ev.Buttons() != tcell.ButtonNone
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
	}

	target.HandleEvent(ev)
	var word string
	if buttons&(tcell.ButtonMiddle|tcell.ButtonSecondary) != 0 && (!target.IsRaw() || ev.Modifiers()&tcell.ModCtrl != 0) {
		var q0, q1 int
		if q0, q1, word = clickRange(target.GetBuffer(), target.PosAt(t.x, t.y)); word != "" {
			if buttons&tcell.ButtonMiddle != 0 {
				win.broadcastEvent('M', 'x', q0, q1, word)
			} else {
				win.broadcastEvent('M', 'l', q0, q1, word)
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
			v.typedFrom = -1
			v.buffer.Cut()
		} else {
			v.startTyping()
			v.buffer.Paste()
		}
	}

	// The chord ends the sweep, and with it any auto-scroll at the view edge.
	e.repeat = nil
}
