package main

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

func TestMouseButtonChangeReportsChangedButton(t *testing.T) {
	const (
		b1 = tcell.ButtonPrimary
		b2 = tcell.ButtonMiddle
		b3 = tcell.ButtonSecondary
	)

	tests := []struct {
		name      string
		prev, cur tcell.ButtonMask
		code      int
		release   bool
		changed   bool
	}{
		{"no change", b1, b1, 0, false, false},
		{"press primary", tcell.ButtonNone, b1, 0, false, true},
		{"press middle", tcell.ButtonNone, b2, 1, false, true},
		{"press secondary", tcell.ButtonNone, b3, 2, false, true},
		// The chord cases: a second button joins a held primary and must be the
		// button reported, not the primary that is still down.
		{"chord add middle", b1, b1 | b2, 1, false, true},
		{"chord add secondary", b1, b1 | b3, 2, false, true},
		// Releasing one button of several reports that button's release.
		{"release middle, keep primary", b1 | b2, b1, 1, true, true},
		{"release primary fully", b1, tcell.ButtonNone, 0, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, release, changed := mouseButtonChange(tt.prev, tt.cur)
			if code != tt.code || release != tt.release || changed != tt.changed {
				t.Fatalf("mouseButtonChange(%v, %v) = (%d, %v, %v), want (%d, %v, %v)",
					tt.prev, tt.cur, code, release, changed, tt.code, tt.release, tt.changed)
			}
		})
	}
}
