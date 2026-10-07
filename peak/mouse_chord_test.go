package main

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
)

func setupMouseChordWindow(t *testing.T) (*Editor, *Window, *TextView) {
	t.Helper()

	e, _ := setupTest(t, 80, 24)
	col := NewColumn(0, 1, e.w, e.h-1, e)
	e.columns = append(e.columns, col)
	win := col.AddWindow(" /tmp/chord.txt Get Put Del ", "alpha beta")
	col.Resize(col.rect)
	tv := win.bodyTextView()
	if tv == nil {
		t.Fatal("window body is not a TextView")
	}
	return e, win, tv
}

func press(e *Editor, x, y int, buttons tcell.ButtonMask) {
	e.HandleEvent(tcell.NewEventMouse(x, y, buttons, 0))
}

// chordTarget hit-tests the top-left corner of v and reports the view a chord
// there would act on, and its window.
func chordTarget(e *Editor, v any) (View, *Window) {
	p := screenAt(e, v)
	t := e.resolveTarget(p.X, p.Y)
	return t.view, t.win
}

func TestMouseChordSweepMiddleCutsBodyText(t *testing.T) {
	e, _, tv := setupMouseChordWindow(t)
	p := screenAt(e, tv)

	// Sweep-select "beta" (columns 6..10), then chord Button2 to cut it.
	press(e, p.X+6, p.Y, tcell.ButtonPrimary)
	press(e, p.X+10, p.Y, tcell.ButtonPrimary)
	press(e, p.X+10, p.Y, tcell.ButtonPrimary|tcell.ButtonMiddle)

	if !e.gesture.chorded {
		t.Fatal("chord should be marked fired after cutting")
	}
	if got, want := tv.buffer.GetText(), "alpha "; got != want {
		t.Fatalf("body text after cut chord = %q, want %q", got, want)
	}
}

func TestMouseChordClickOnSelectionDoesNothing(t *testing.T) {
	e, _, tv := setupMouseChordWindow(t)
	p := screenAt(e, tv)
	tv.buffer.SetSelection(Cursor{6, 0}, Cursor{10, 0})

	// A plain click on a standing selection deselects it; a chord that follows
	// finds nothing selected and must not cut.
	press(e, p.X+7, p.Y, tcell.ButtonPrimary)
	press(e, p.X+7, p.Y, tcell.ButtonPrimary|tcell.ButtonMiddle)

	if got := tv.buffer.GetText(); got != "alpha beta" {
		t.Fatalf("body text after click-then-chord = %q, want unchanged", got)
	}
	if tv.buffer.GetSelectedText() != "" {
		t.Fatal("click on a selection should have deselected it")
	}
}

func TestMouseChordPrimarySecondaryPastes(t *testing.T) {
	oldRead := readClipboard
	readClipboard = func() (string, error) { return "XYZ", nil }
	defer func() { readClipboard = oldRead }()

	e, _, tv := setupMouseChordWindow(t)
	p := screenAt(e, tv)

	press(e, p.X, p.Y, tcell.ButtonPrimary)
	press(e, p.X, p.Y, tcell.ButtonPrimary|tcell.ButtonSecondary)

	if got, want := tv.buffer.GetText(), "XYZalpha beta"; got != want {
		t.Fatalf("body text after paste chord = %q, want %q", got, want)
	}
}

func TestMouseChordCutThenPasteWithoutReleasing(t *testing.T) {
	oldRead := readClipboard
	readClipboard = func() (string, error) { return "XX", nil }
	defer func() { readClipboard = oldRead }()

	e, _, tv := setupMouseChordWindow(t)
	p := screenAt(e, tv)

	// Sweep-select "beta", cut it, then — without releasing Button1 — release
	// Button2 and press Button3 to paste in its place.
	press(e, p.X+6, p.Y, tcell.ButtonPrimary)
	press(e, p.X+10, p.Y, tcell.ButtonPrimary)
	press(e, p.X+10, p.Y, tcell.ButtonPrimary|tcell.ButtonMiddle)
	press(e, p.X+10, p.Y, tcell.ButtonPrimary)
	press(e, p.X+10, p.Y, tcell.ButtonPrimary|tcell.ButtonSecondary)

	if got, want := tv.buffer.GetText(), "alpha XX"; got != want {
		t.Fatalf("body text after cut-then-paste chord = %q, want %q", got, want)
	}
	if tv.buffer.GetSelectedText() != "XX" {
		t.Fatalf("pasted text should be selected, got %q", tv.buffer.GetSelectedText())
	}
}

func TestMouseChordPasteThenCutWithoutReleasing(t *testing.T) {
	oldRead := readClipboard
	readClipboard = func() (string, error) { return "XX", nil }
	defer func() { readClipboard = oldRead }()

	e, _, tv := setupMouseChordWindow(t)
	p := screenAt(e, tv)

	// Click (no selection), paste "XX" — which selects it — then, without
	// releasing Button1, release Button3 and press Button2 to cut it back out.
	press(e, p.X, p.Y, tcell.ButtonPrimary)
	press(e, p.X, p.Y, tcell.ButtonPrimary|tcell.ButtonSecondary)
	press(e, p.X, p.Y, tcell.ButtonPrimary)
	press(e, p.X, p.Y, tcell.ButtonPrimary|tcell.ButtonMiddle)

	if got, want := tv.buffer.GetText(), "alpha beta"; got != want {
		t.Fatalf("body text after paste-then-cut chord = %q, want %q", got, want)
	}
}

func TestMouseChordRequiresPrimaryHeld(t *testing.T) {
	e, _, tv := setupMouseChordWindow(t)
	p := screenAt(e, tv)
	tv.buffer.SetSelection(Cursor{0, 0}, Cursor{5, 0})

	// Middle or secondary alone (no primary) must not arm or fire a chord:
	// they are normal execute/plumb clicks. Click on the space at column 5 so
	// no word is under the pointer and no command runs.
	for _, b := range []tcell.ButtonMask{tcell.ButtonMiddle, tcell.ButtonSecondary} {
		press(e, p.X+5, p.Y, b)
		if e.gesture.chorded {
			t.Fatalf("button %v alone must not fire a chord", b)
		}
		if e.gesture.anchorView != nil {
			t.Fatalf("button %v alone must not arm a chord", b)
		}
	}
}

func TestMouseChordFiresOncePerGesture(t *testing.T) {
	e, _, tv := setupMouseChordWindow(t)
	p := screenAt(e, tv)

	// Sweep-select "beta", then cut it with a chord.
	press(e, p.X+6, p.Y, tcell.ButtonPrimary)
	press(e, p.X+10, p.Y, tcell.ButtonPrimary)
	press(e, p.X+10, p.Y, tcell.ButtonPrimary|tcell.ButtonMiddle)
	want := tv.buffer.GetText()

	// Repeated combined-mask events while held must be swallowed, not re-cut.
	press(e, p.X+10, p.Y, tcell.ButtonPrimary|tcell.ButtonMiddle)
	if got := tv.buffer.GetText(); got != want {
		t.Fatalf("chord fired more than once: text = %q, want %q", got, want)
	}

	// A full release resets the gesture so the next chord can fire.
	press(e, p.X+10, p.Y, tcell.ButtonNone)
	if e.gesture.chorded || e.gesture.anchorView != nil {
		t.Fatal("release should reset chord state")
	}
}

func TestMouseChordAnchorsToInitialView(t *testing.T) {
	e, _, tv := setupMouseChordWindow(t)
	p := screenAt(e, tv)
	tagBefore := e.tag.buffer.GetText()

	// Press primary in the body, drag the pointer up onto the global tag, then
	// chord: the cut must target the anchored body, not the tag under the
	// pointer when the second button was pressed.
	press(e, p.X+6, p.Y, tcell.ButtonPrimary)
	press(e, p.X+10, p.Y, tcell.ButtonPrimary)
	press(e, 0, 0, tcell.ButtonPrimary)
	press(e, 0, 0, tcell.ButtonPrimary|tcell.ButtonMiddle)

	if e.tag.buffer.GetText() != tagBefore {
		t.Fatal("global tag was modified; chord should target the anchored body")
	}
	if tv.buffer.GetText() == "alpha beta" {
		t.Fatal("body was not cut; chord should target the anchored body")
	}
}

func TestMouseChordEndToEndDragSelectionCuts(t *testing.T) {
	e, _, tv := setupMouseChordWindow(t)
	p := screenAt(e, tv)

	press(e, p.X, p.Y, tcell.ButtonPrimary)
	press(e, p.X+10, p.Y, tcell.ButtonPrimary)
	press(e, p.X+10, p.Y, tcell.ButtonPrimary|tcell.ButtonMiddle)

	if got := tv.buffer.GetText(); got != "" {
		t.Fatalf("body text after drag-selection cut chord = %q, want empty", got)
	}
}

func TestMouseChordGlobalTagCut(t *testing.T) {
	e, _, _ := setupMouseChordWindow(t)

	// Sweep-select "NewCol " in the global tag, then chord-cut it.
	press(e, 1, 0, tcell.ButtonPrimary)
	press(e, 8, 0, tcell.ButtonPrimary)
	press(e, 8, 0, tcell.ButtonPrimary|tcell.ButtonMiddle)

	if got, want := e.tag.buffer.GetText(), " Help Exit "; got != want {
		t.Fatalf("global tag text after cut chord = %q, want %q", got, want)
	}
}

func TestMouseChordColumnTagCut(t *testing.T) {
	e, win, _ := setupMouseChordWindow(t)
	colTag := win.parent.tag
	c := screenAt(e, colTag)

	// Sweep-select "New Zerox " in the column tag, then chord-cut it.
	press(e, c.X+1, c.Y, tcell.ButtonPrimary)
	press(e, c.X+11, c.Y, tcell.ButtonPrimary)
	press(e, c.X+11, c.Y, tcell.ButtonPrimary|tcell.ButtonMiddle)

	if got, want := colTag.buffer.GetText(), " Win Delcol "; got != want {
		t.Fatalf("column tag text after cut chord = %q, want %q", got, want)
	}
}

func TestChordTargetRejectsNonTextAreas(t *testing.T) {
	e, win, tv := setupMouseChordWindow(t)
	p := screenAt(e, tv)
	w := screenAt(e, win)
	tests := []struct {
		name string
		x, y int
	}{
		{name: "window handle", x: w.X, y: w.Y},
		{name: "column handle", x: win.parent.x, y: win.parent.y},
		{name: "scroll gutter", x: w.X, y: p.Y},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if v := e.resolveTarget(tt.x, tt.y).view; v != nil {
				t.Fatalf("view at (%d, %d) = %p, want none to chord on", tt.x, tt.y, v)
			}
		})
	}
}

func TestChordTargetsTerminalWindow(t *testing.T) {
	e, _, _ := setupMouseChordWindow(t)
	col := e.columns[0]

	termWin := addTerm(t, col, " /tmp/-sh Zerox Del ", "sh")
	col.Resize(col.rect)

	term, ok := termWin.body.(*TermView)
	if !ok {
		t.Fatalf("terminal body is %T, want *TermView", termWin.body)
	}

	// The terminal body is chordable and resolves to the TermView itself, so a
	// middle chord there copies (Snarf) rather than cutting a text buffer.
	if gotView, gotWin := chordTarget(e, term); gotView != term || gotWin != termWin {
		t.Fatalf("terminal body chord target = (%v, %v), want (%v, %v)", gotView, gotWin, term, termWin)
	}
	// The terminal's tag is an ordinary text tag and chords as a TextView.
	gotTag, _ := chordTarget(e, termWin.tag)

	if _, ok := gotTag.(*TextView); !ok {
		t.Fatalf("terminal tag chord target = %T, want *TextView", gotTag)
	}
}

func TestChordAllowedInTerminal(t *testing.T) {
	e, _, tv := setupMouseChordWindow(t)
	noMods := tcell.NewEventMouse(0, 0, tcell.ButtonPrimary, 0)

	// A text view always chords.
	if !e.chordAllowed(tv, noMods) {
		t.Fatal("text view should always be chordable")
	}

	termWin := addTerm(t, e.columns[0], " /tmp/-sh Zerox Del ", "sh")
	term := termWin.body.(*TermView)

	// A plain shell isn't tracking the mouse, so the terminal chords locally,
	// with or without Ctrl.
	if !e.chordAllowed(term, noMods) {
		t.Fatal("terminal without mouse tracking should chord locally")
	}
	ctrl := tcell.NewEventMouse(0, 0, tcell.ButtonPrimary, tcell.ModCtrl)
	if !e.chordAllowed(term, ctrl) {
		t.Fatal("Ctrl should force local chording in a terminal")
	}
}

func TestChordSuppressedInFullScreenTerminal(t *testing.T) {
	e, _ := setupTest(t, 80, 24)
	col := NewColumn(0, 1, e.w, e.h-1, e)
	e.columns = append(e.columns, col)

	// A child that switches to the alternate screen (DECSET 1049) and stays
	// alive, i.e. a full-screen app that owns the mouse (e.g. a nested peak).
	termWin := addTerm(t, col, " /tmp/-sh Zerox Del ", "printf '\\033[?1049h'; sleep 30")
	col.Resize(col.rect)
	term := termWin.body.(*TermView)

	deadline := time.Now().Add(3 * time.Second)
	for !term.IsRaw() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !term.IsRaw() {
		t.Skip("terminal did not enter full-screen mode in time")
	}

	// A bare press must not arm a chord — the buttons belong to the child app.
	noMods := tcell.NewEventMouse(0, 0, tcell.ButtonPrimary, 0)
	if e.chordAllowed(term, noMods) {
		t.Fatal("chording should be suppressed in a full-screen terminal app")
	}
	// Ctrl forces a local action, so chording is allowed.
	ctrl := tcell.NewEventMouse(0, 0, tcell.ButtonPrimary, tcell.ModCtrl)
	if !e.chordAllowed(term, ctrl) {
		t.Fatal("Ctrl should override and allow local chording")
	}
}
