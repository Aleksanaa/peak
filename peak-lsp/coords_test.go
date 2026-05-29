package main

import (
	"testing"

	"github.com/odvcencio/gotreesitter"
)

func TestRuneToByteOffset(t *testing.T) {
	body := []byte("aé\n日b")
	tests := []struct {
		name    string
		runeOff int
		want    int
	}{
		{name: "start", runeOff: 0, want: 0},
		{name: "ascii", runeOff: 1, want: 1},
		{name: "after multibyte", runeOff: 2, want: 3},
		{name: "after newline", runeOff: 3, want: 4},
		{name: "after cjk", runeOff: 4, want: 7},
		{name: "end", runeOff: 5, want: 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := runeToByteOffset(body, tt.runeOff)
			if !ok || got != tt.want {
				t.Fatalf("runeToByteOffset(%d) = %d, %v; want %d, true", tt.runeOff, got, ok, tt.want)
			}
		})
	}
}

func TestRuneToByteOffsetRejectsInvalidOffsets(t *testing.T) {
	body := []byte("abc")
	for _, runeOff := range []int{-1, 4} {
		if got, ok := runeToByteOffset(body, runeOff); ok {
			t.Fatalf("runeToByteOffset(%d) = %d, true; want false", runeOff, got)
		}
	}
}

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

func TestMergeByteRangesEmpty(t *testing.T) {
	if got := mergeByteRanges(nil); got != nil {
		t.Fatalf("mergeByteRanges(nil) = %v, want nil", got)
	}
}

func TestMergeByteRangesOverlappingAdjacentAndUnsorted(t *testing.T) {
	got := mergeByteRanges([]byteRange{{lo: 10, hi: 15}, {lo: 1, hi: 5}, {lo: 5, hi: 8}, {lo: 3, hi: 12}, {lo: 20, hi: 22}})
	want := []byteRange{{lo: 1, hi: 15}, {lo: 20, hi: 22}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("mergeByteRanges = %v, want %v", got, want)
	}
}

func TestExpandToLineBoundaries(t *testing.T) {
	body := []byte("aa\nbb\ncc\n")
	tests := []struct {
		name string
		r    byteRange
		want byteRange
	}{
		{name: "within line", r: byteRange{lo: 4, hi: 5}, want: byteRange{lo: 3, hi: 5}},
		{name: "across lines", r: byteRange{lo: 4, hi: 6}, want: byteRange{lo: 3, hi: 8}},
		{name: "already at line start", r: byteRange{lo: 3, hi: 6}, want: byteRange{lo: 3, hi: 8}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expandToLineBoundaries(body, tt.r); got != tt.want {
				t.Fatalf("expandToLineBoundaries(%v) = %v, want %v", tt.r, got, tt.want)
			}
		})
	}
}

func TestTsRangesToByteRanges(t *testing.T) {
	if got := tsRangesToByteRanges(nil); got != nil {
		t.Fatalf("tsRangesToByteRanges(nil) = %v, want nil", got)
	}
	in := []gotreesitter.Range{{StartByte: 1, EndByte: 5}, {StartByte: 10, EndByte: 20}}
	got := tsRangesToByteRanges(in)
	want := []byteRange{{lo: 1, hi: 5}, {lo: 10, hi: 20}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("tsRangesToByteRanges = %v, want %v", got, want)
	}
}

func TestRangeByteToRune(t *testing.T) {
	body := []byte("aé\n日b")
	tests := []struct {
		name string
		r    byteRange
		lo   int
		hi   int
	}{
		{name: "ascii start", r: byteRange{0, 1}, lo: 0, hi: 1},
		{name: "multibyte", r: byteRange{1, 3}, lo: 1, hi: 2},
		{name: "across newline", r: byteRange{0, 4}, lo: 0, hi: 3},
		{name: "full body", r: byteRange{0, 8}, lo: 0, hi: 5},
		{name: "empty range", r: byteRange{3, 3}, lo: 2, hi: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lo, hi, ok := rangeByteToRune(body, tt.r)
			if !ok || lo != tt.lo || hi != tt.hi {
				t.Fatalf("rangeByteToRune(%v) = %d, %d, %v; want %d, %d, true", tt.r, lo, hi, ok, tt.lo, tt.hi)
			}
		})
	}
}

func TestRangeByteToRuneRejectsInvalid(t *testing.T) {
	body := []byte("aé\n日b")
	tests := []byteRange{{lo: -1, hi: 2}, {lo: 0, hi: 10}, {lo: 3, hi: 2}, {lo: 2, hi: 3}, {lo: 5, hi: 6}}
	for _, r := range tests {
		if _, _, ok := rangeByteToRune(body, r); ok {
			t.Fatalf("rangeByteToRune(%v) = true; want false", r)
		}
	}
}

func TestBuildByteToRune(t *testing.T) {
	src := []byte("aé\nb")
	got := buildByteToRune(src)
	want := []int{0, 1, 1, 2, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("len(buildByteToRune) = %d, want %d", len(got), len(want))
	}
	for i, v := range want {
		if got[i] != v {
			t.Fatalf("buildByteToRune[%d] = %d, want %d", i, got[i], v)
		}
	}
}
