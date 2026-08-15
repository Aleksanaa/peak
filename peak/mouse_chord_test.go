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

func TestMouseChordPrimaryMiddleCutsSelectedBodyText(t *testing.T) {
	e, _, tv := setupMouseChordWindow(t)
	tv.buffer.SetSelection(Cursor{6, 0}, Cursor{10, 0})

	// The primary press anchors the selection via normal dispatch and is
	// therefore not consumed.
	if e.handleMouseChord(tv.x, tv.y, tcell.ButtonPrimary) {
		t.Fatal("primary-only press must fall through to normal dispatch")
	}
	// tcell v3 reports the combined mask once the second button joins.
	if !e.handleMouseChord(tv.x, tv.y, tcell.ButtonPrimary|tcell.ButtonMiddle) {
		t.Fatal("primary+middle chord was not consumed")
	}
	if !e.mouseChord.fired {
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

	if e.handleMouseChord(tv.x, tv.y, tcell.ButtonPrimary) {
		t.Fatal("primary-only press must fall through to normal dispatch")
	}
	if !e.handleMouseChord(tv.x, tv.y, tcell.ButtonPrimary|tcell.ButtonSecondary) {
		t.Fatal("primary+secondary chord was not consumed")
	}
	if got, want := tv.buffer.GetText(), "XYZalpha beta"; got != want {
		t.Fatalf("body text after paste chord = %q, want %q", got, want)
	}
}

func TestMouseChordRequiresPrimaryHeld(t *testing.T) {
	e, _, tv := setupMouseChordWindow(t)
	tv.buffer.SetSelection(Cursor{0, 0}, Cursor{5, 0})

	// Middle or secondary alone (no primary) must fall through: they are
	// normal execute/plumb clicks, not chords.
	for _, b := range []tcell.ButtonMask{tcell.ButtonMiddle, tcell.ButtonSecondary} {
		if e.handleMouseChord(tv.x, tv.y, b) {
			t.Fatalf("button %v alone must not be consumed by the chord handler", b)
		}
	}
	if e.mouseChord.armed || e.mouseChord.fired {
		t.Fatal("chord must not arm without the primary button held")
	}
}

func TestMouseChordFiresOncePerGesture(t *testing.T) {
	e, _, tv := setupMouseChordWindow(t)
	tv.buffer.SetSelection(Cursor{6, 0}, Cursor{10, 0})

	e.handleMouseChord(tv.x, tv.y, tcell.ButtonPrimary)
	if !e.handleMouseChord(tv.x, tv.y, tcell.ButtonPrimary|tcell.ButtonMiddle) {
		t.Fatal("first chord was not consumed")
	}
	want := tv.buffer.GetText()

	// Repeated combined-mask events while held must be consumed, not re-cut.
	if !e.handleMouseChord(tv.x, tv.y, tcell.ButtonPrimary|tcell.ButtonMiddle) {
		t.Fatal("subsequent chord event should be consumed while buttons held")
	}
	if got := tv.buffer.GetText(); got != want {
		t.Fatalf("chord fired more than once: text = %q, want %q", got, want)
	}

	// A full release resets the gesture so the next chord can fire.
	if e.handleMouseChord(tv.x, tv.y, tcell.ButtonNone) {
		t.Fatal("release should not be consumed")
	}
	if e.mouseChord.armed || e.mouseChord.fired {
		t.Fatal("release should reset chord state")
	}
}

func TestMouseChordUsesInitialTargetAfterPointerMoves(t *testing.T) {
	e, _, tv := setupMouseChordWindow(t)
	tv.buffer.SetSelection(Cursor{6, 0}, Cursor{10, 0})

	if e.handleMouseChord(tv.x, tv.y, tcell.ButtonPrimary) {
		t.Fatal("primary press should arm without being consumed")
	}
	// Move before pressing the second button; the chord must still target the
	// view where Button 1 was originally pressed, not the global tag.
	e.handleMouseChord(e.tag.x, e.tag.y, tcell.ButtonPrimary)
	if !e.handleMouseChord(e.tag.x, e.tag.y, tcell.ButtonPrimary|tcell.ButtonMiddle) {
		t.Fatal("chord after moving pointer was not consumed")
	}
	if got, want := tv.buffer.GetText(), "alpha "; got != want {
		t.Fatalf("body text after anchored cut chord = %q, want %q", got, want)
	}
}

func TestMouseChordEndToEndExistingSelectionCuts(t *testing.T) {
	e, _, tv := setupMouseChordWindow(t)
	tv.buffer.SetSelection(Cursor{6, 0}, Cursor{10, 0})

	e.HandleEvent(tcell.NewEventMouse(tv.x, tv.y, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(tv.x, tv.y, tcell.ButtonPrimary|tcell.ButtonMiddle, 0))

	if got, want := tv.buffer.GetText(), "alpha "; got != want {
		t.Fatalf("body text after end-to-end cut chord = %q, want %q", got, want)
	}
}

func TestMouseChordEndToEndDragSelectionCuts(t *testing.T) {
	e, _, tv := setupMouseChordWindow(t)

	e.HandleEvent(tcell.NewEventMouse(tv.x, tv.y, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(tv.x+10, tv.y, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(tv.x+10, tv.y, tcell.ButtonPrimary|tcell.ButtonMiddle, 0))

	if got := tv.buffer.GetText(); got != "" {
		t.Fatalf("body text after drag-selection cut chord = %q, want empty", got)
	}
}

func TestMouseChordGlobalTagCut(t *testing.T) {
	e, _, _ := setupMouseChordWindow(t)
	e.tag.buffer.SetSelection(Cursor{1, 0}, Cursor{8, 0})

	e.HandleEvent(tcell.NewEventMouse(e.tag.x+1, e.tag.y, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(e.tag.x+1, e.tag.y, tcell.ButtonPrimary|tcell.ButtonMiddle, 0))

	if got, want := e.tag.buffer.GetText(), " Help Exit "; got != want {
		t.Fatalf("global tag text after cut chord = %q, want %q", got, want)
	}
}

func TestMouseChordColumnTagCut(t *testing.T) {
	e, win, _ := setupMouseChordWindow(t)
	colTag := win.parent.tag
	colTag.buffer.SetSelection(Cursor{1, 0}, Cursor{11, 0})

	e.HandleEvent(tcell.NewEventMouse(colTag.x+1, colTag.y, tcell.ButtonPrimary, 0))
	e.HandleEvent(tcell.NewEventMouse(colTag.x+1, colTag.y, tcell.ButtonPrimary|tcell.ButtonMiddle, 0))

	if got, want := colTag.buffer.GetText(), " Win Delcol "; got != want {
		t.Fatalf("column tag text after cut chord = %q, want %q", got, want)
	}
}

func TestFindChordTargetRejectsNonTextAreas(t *testing.T) {
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
			gotTV, gotWin := e.findChordTarget(tt.x, tt.y)
			if gotTV != nil || gotWin != nil {
				t.Fatalf("findChordTarget(%d, %d) = (%p, %p), want (nil, nil)", tt.x, tt.y, gotTV, gotWin)
			}
		})
	}
}

func TestFindChordTargetRejectsTerminalWindow(t *testing.T) {
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
			gotTV, gotWin := e.findChordTarget(tt.x, tt.y)
			if gotTV != nil || gotWin != nil {
				t.Fatalf("terminal %s target = (%p, %p), want (nil, nil)", tt.name, gotTV, gotWin)
			}
		})
	}
}
