package main

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

func setupMouseChordWindow(t *testing.T) (*Editor, *Window, *TextView) {
	t.Helper()

	e, _ := setupTest(t, 80, 24)
	col := NewColumn(0, 1, e.w, e.h-1, e, e.Execute)
	e.columns = append(e.columns, col)
	win := col.AddWindow(" /tmp/chord.txt Get Put Del ", "alpha beta")
	col.Resize(col.x, col.y, col.w, col.h)
	win.tag.UpdateLayout()
	tv := win.bodyTextView()
	if tv == nil {
		t.Fatal("window body is not a TextView")
	}
	return e, win, tv
}

func press(e *Editor, x, y int, buttons tcell.ButtonMask) {
	e.HandleEvent(tcell.NewEventMouse(x, y, buttons, 0))
}

func TestMouseChordPrimaryMiddleCutsSelectedBodyText(t *testing.T) {
	e, _, tv := setupMouseChordWindow(t)
	tv.buffer.SetSelection(Cursor{6, 0}, Cursor{10, 0})

	press(e, tv.x, tv.y, tcell.ButtonPrimary)
	press(e, tv.x, tv.y, tcell.ButtonPrimary|tcell.ButtonMiddle)

	if !e.gesture.chorded {
		t.Fatal("chord should be marked fired after cutting")
	}
	if got, want := tv.buffer.GetText(), "alpha "; got != want {
		t.Fatalf("body text after cut chord = %q, want %q", got, want)
	}
}

func TestMouseChordPrimarySecondaryPastes(t *testing.T) {
	oldRead := readClipboard
	readClipboard = func() (string, error) { return "XYZ", nil }
	defer func() { readClipboard = oldRead }()

	e, _, tv := setupMouseChordWindow(t)

	press(e, tv.x, tv.y, tcell.ButtonPrimary)
	press(e, tv.x, tv.y, tcell.ButtonPrimary|tcell.ButtonSecondary)

	if got, want := tv.buffer.GetText(), "XYZalpha beta"; got != want {
		t.Fatalf("body text after paste chord = %q, want %q", got, want)
	}
}

func TestMouseChordRequiresPrimaryHeld(t *testing.T) {
	e, _, tv := setupMouseChordWindow(t)
	tv.buffer.SetSelection(Cursor{0, 0}, Cursor{5, 0})

	// Middle or secondary alone (no primary) must not arm or fire a chord:
	// they are normal execute/plumb clicks. Click on the space at column 5 so
	// no word is under the pointer and no command runs.
	for _, b := range []tcell.ButtonMask{tcell.ButtonMiddle, tcell.ButtonSecondary} {
		press(e, tv.x+5, tv.y, b)
		if e.gesture.chorded {
			t.Fatalf("button %v alone must not fire a chord", b)
		}
		if e.gesture.anchorTV != nil {
			t.Fatalf("button %v alone must not arm a chord", b)
		}
	}
}

func TestMouseChordFiresOncePerGesture(t *testing.T) {
	e, _, tv := setupMouseChordWindow(t)
	tv.buffer.SetSelection(Cursor{6, 0}, Cursor{10, 0})

	press(e, tv.x, tv.y, tcell.ButtonPrimary)
	press(e, tv.x, tv.y, tcell.ButtonPrimary|tcell.ButtonMiddle)
	want := tv.buffer.GetText()

	// Repeated combined-mask events while held must be swallowed, not re-cut.
	press(e, tv.x, tv.y, tcell.ButtonPrimary|tcell.ButtonMiddle)
	if got := tv.buffer.GetText(); got != want {
		t.Fatalf("chord fired more than once: text = %q, want %q", got, want)
	}

	// A full release resets the gesture so the next chord can fire.
	press(e, tv.x, tv.y, tcell.ButtonNone)
	if e.gesture.chorded || e.gesture.anchorTV != nil {
		t.Fatal("release should reset chord state")
	}
}

func TestMouseChordAnchorsToInitialView(t *testing.T) {
	e, _, tv := setupMouseChordWindow(t)
	tagBefore := e.tag.buffer.GetText()

	// Press primary in the body, drag the pointer up onto the global tag, then
	// chord: the cut must target the anchored body, not the tag under the
	// pointer when the second button was pressed.
	press(e, tv.x+6, tv.y, tcell.ButtonPrimary)
	press(e, tv.x+10, tv.y, tcell.ButtonPrimary)
	press(e, e.tag.x, e.tag.y, tcell.ButtonPrimary)
	press(e, e.tag.x, e.tag.y, tcell.ButtonPrimary|tcell.ButtonMiddle)

	if e.tag.buffer.GetText() != tagBefore {
		t.Fatal("global tag was modified; chord should target the anchored body")
	}
	if tv.buffer.GetText() == "alpha beta" {
		t.Fatal("body was not cut; chord should target the anchored body")
	}
}

func TestMouseChordEndToEndExistingSelectionCuts(t *testing.T) {
	e, _, tv := setupMouseChordWindow(t)
	tv.buffer.SetSelection(Cursor{6, 0}, Cursor{10, 0})

	press(e, tv.x, tv.y, tcell.ButtonPrimary)
	press(e, tv.x, tv.y, tcell.ButtonPrimary|tcell.ButtonMiddle)

	if got, want := tv.buffer.GetText(), "alpha "; got != want {
		t.Fatalf("body text after end-to-end cut chord = %q, want %q", got, want)
	}
}

func TestMouseChordEndToEndDragSelectionCuts(t *testing.T) {
	e, _, tv := setupMouseChordWindow(t)

	press(e, tv.x, tv.y, tcell.ButtonPrimary)
	press(e, tv.x+10, tv.y, tcell.ButtonPrimary)
	press(e, tv.x+10, tv.y, tcell.ButtonPrimary|tcell.ButtonMiddle)

	if got := tv.buffer.GetText(); got != "" {
		t.Fatalf("body text after drag-selection cut chord = %q, want empty", got)
	}
}

func TestMouseChordGlobalTagCut(t *testing.T) {
	e, _, _ := setupMouseChordWindow(t)
	e.tag.buffer.SetSelection(Cursor{1, 0}, Cursor{8, 0})

	press(e, e.tag.x+1, e.tag.y, tcell.ButtonPrimary)
	press(e, e.tag.x+1, e.tag.y, tcell.ButtonPrimary|tcell.ButtonMiddle)

	if got, want := e.tag.buffer.GetText(), " Help Exit "; got != want {
		t.Fatalf("global tag text after cut chord = %q, want %q", got, want)
	}
}

func TestMouseChordColumnTagCut(t *testing.T) {
	e, win, _ := setupMouseChordWindow(t)
	colTag := win.parent.tag
	colTag.buffer.SetSelection(Cursor{1, 0}, Cursor{11, 0})

	press(e, colTag.x+1, colTag.y, tcell.ButtonPrimary)
	press(e, colTag.x+1, colTag.y, tcell.ButtonPrimary|tcell.ButtonMiddle)

	if got, want := colTag.buffer.GetText(), " Win Delcol "; got != want {
		t.Fatalf("column tag text after cut chord = %q, want %q", got, want)
	}
}

func TestChordTargetRejectsNonTextAreas(t *testing.T) {
	e, win, tv := setupMouseChordWindow(t)

	tests := []struct {
		name string
		x, y int
	}{
		{name: "window handle", x: win.x, y: win.y},
		{name: "column handle", x: win.parent.x, y: win.parent.y},
		{name: "scroll gutter", x: win.bodyView.scroll.x, y: tv.y},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTV, gotWin := e.chordTarget(tt.x, tt.y)
			if gotTV != nil || gotWin != nil {
				t.Fatalf("chordTarget(%d, %d) = (%p, %p), want (nil, nil)", tt.x, tt.y, gotTV, gotWin)
			}
		})
	}
}

func TestChordTargetRejectsTerminalWindow(t *testing.T) {
	e, win, tv := setupMouseChordWindow(t)
	win.kind = WinTerm

	tests := []struct {
		name string
		x, y int
	}{
		{name: "tag", x: win.tag.x, y: win.tag.y},
		{name: "body", x: tv.x, y: tv.y},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTV, gotWin := e.chordTarget(tt.x, tt.y)
			if gotTV != nil || gotWin != nil {
				t.Fatalf("terminal %s target = (%p, %p), want (nil, nil)", tt.name, gotTV, gotWin)
			}
		})
	}
}
