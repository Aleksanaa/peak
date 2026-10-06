package main

import "testing"

func TestDeleteLine(t *testing.T) {
	type mutation struct {
		q0, q1Old, q1New int
		text             string
	}
	tests := []struct {
		name       string
		text       string
		cursor     Cursor
		wantText   string
		wantCursor Cursor
		want       mutation
	}{
		{"middle line", "ab\ncd\nef", Cursor{1, 1}, "ab\nef", Cursor{0, 1}, mutation{3, 6, 3, ""}},
		{"first line", "ab\ncd", Cursor{2, 0}, "cd", Cursor{0, 0}, mutation{0, 3, 0, ""}},
		{"last line", "ab\ncd", Cursor{1, 1}, "ab", Cursor{0, 0}, mutation{2, 5, 2, ""}},
		{"only line", "abc", Cursor{2, 0}, "", Cursor{0, 0}, mutation{0, 3, 0, ""}},
		{"only line, empty", "", Cursor{0, 0}, "", Cursor{0, 0}, mutation{0, 0, 0, ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewBuffer(tt.text)
			b.cursor = tt.cursor
			var got []mutation
			b.onMutate = func(q0, q1Old, q1New int, text string) {
				got = append(got, mutation{q0, q1Old, q1New, text})
			}
			version := b.version

			b.DeleteLine()

			if text := b.GetText(); text != tt.wantText {
				t.Errorf("text = %q, want %q", text, tt.wantText)
			}
			if b.cursor != tt.wantCursor {
				t.Errorf("cursor = %v, want %v", b.cursor, tt.wantCursor)
			}
			if b.version == version {
				t.Error("version not bumped")
			}
			// Spans, addr and event subscribers all hang off onMutate.
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("onMutate calls = %v, want [%v]", got, tt.want)
			}

			b.Undo()
			if text := b.GetText(); text != tt.text {
				t.Errorf("after Undo text = %q, want %q", text, tt.text)
			}
		})
	}
}
