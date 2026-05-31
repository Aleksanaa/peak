package main

import (
	"testing"

	"github.com/aleksana/peak/internal/coords"
	"github.com/odvcencio/gotreesitter"
)

func TestPointAtByte(t *testing.T) {
	body := []byte("ab\ncé\n")
	tests := []struct {
		name    string
		byteOff int
		want    gotreesitter.Point
	}{
		{name: "start", byteOff: 0, want: gotreesitter.Point{Row: 0, Column: 0}},
		{name: "middle first line", byteOff: 2, want: gotreesitter.Point{Row: 0, Column: 2}},
		{name: "after newline", byteOff: 3, want: gotreesitter.Point{Row: 1, Column: 0}},
		{name: "after multibyte", byteOff: 6, want: gotreesitter.Point{Row: 1, Column: 3}},
		{name: "after trailing newline", byteOff: 7, want: gotreesitter.Point{Row: 2, Column: 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := pointAtByte(body, tt.byteOff)
			if !ok || got != tt.want {
				t.Fatalf("pointAtByte(%d) = %#v, %v; want %#v, true", tt.byteOff, got, ok, tt.want)
			}
		})
	}
}

func TestPointAtByteRejectsInvalidOffsets(t *testing.T) {
	body := []byte("abc")
	for _, byteOff := range []int{-1, 4} {
		if got, ok := pointAtByte(body, byteOff); ok {
			t.Fatalf("pointAtByte(%d) = %#v, true; want false", byteOff, got)
		}
	}
}

func TestAdvancePoint(t *testing.T) {
	start := gotreesitter.Point{Row: 3, Column: 4}
	got := advancePoint(start, []byte("ab\nçd\n"))
	want := gotreesitter.Point{Row: 5, Column: 0}
	if got != want {
		t.Fatalf("advancePoint() = %#v, want %#v", got, want)
	}
}

func TestAdvancePointEmpty(t *testing.T) {
	start := gotreesitter.Point{Row: 1, Column: 2}
	got := advancePoint(start, []byte{})
	if got != start {
		t.Fatalf("advancePoint(empty) = %#v, want %#v", got, start)
	}
}

func TestTsRangesToByteRanges(t *testing.T) {
	if got := tsRangesToByteRanges(nil); got != nil {
		t.Fatalf("tsRangesToByteRanges(nil) = %v, want nil", got)
	}
	in := []gotreesitter.Range{{StartByte: 1, EndByte: 5}, {StartByte: 10, EndByte: 20}}
	got := tsRangesToByteRanges(in)
	want := []coords.ByteRange{{Lo: 1, Hi: 5}, {Lo: 10, Hi: 20}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("tsRangesToByteRanges = %v, want %v", got, want)
	}
}
