package coords

import (
	"testing"
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
			got, ok := RuneToByteOffset(body, tt.runeOff)
			if !ok || got != tt.want {
				t.Fatalf("RuneToByteOffset(%d) = %d, %v; want %d, true", tt.runeOff, got, ok, tt.want)
			}
		})
	}
}

func TestRuneToByteOffsetRejectsInvalidOffsets(t *testing.T) {
	body := []byte("abc")
	for _, runeOff := range []int{-1, 4} {
		if got, ok := RuneToByteOffset(body, runeOff); ok {
			t.Fatalf("RuneToByteOffset(%d) = %d, true; want false", runeOff, got)
		}
	}
}

func TestMergeByteRangesEmpty(t *testing.T) {
	if got := MergeByteRanges(nil); got != nil {
		t.Fatalf("MergeByteRanges(nil) = %v, want nil", got)
	}
}

func TestMergeByteRangesOverlappingAdjacentAndUnsorted(t *testing.T) {
	got := MergeByteRanges([]ByteRange{{Lo: 10, Hi: 15}, {Lo: 1, Hi: 5}, {Lo: 5, Hi: 8}, {Lo: 3, Hi: 12}, {Lo: 20, Hi: 22}})
	want := []ByteRange{{Lo: 1, Hi: 15}, {Lo: 20, Hi: 22}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("MergeByteRanges = %v, want %v", got, want)
	}
}

func TestExpandToLineBoundaries(t *testing.T) {
	body := []byte("aa\nbb\ncc\n")
	tests := []struct {
		name string
		r    ByteRange
		want ByteRange
	}{
		{name: "within line", r: ByteRange{Lo: 4, Hi: 5}, want: ByteRange{Lo: 3, Hi: 5}},
		{name: "across lines", r: ByteRange{Lo: 4, Hi: 6}, want: ByteRange{Lo: 3, Hi: 8}},
		{name: "already at line start", r: ByteRange{Lo: 3, Hi: 6}, want: ByteRange{Lo: 3, Hi: 8}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExpandToLineBoundaries(body, tt.r); got != tt.want {
				t.Fatalf("ExpandToLineBoundaries(%v) = %v, want %v", tt.r, got, tt.want)
			}
		})
	}
}

func TestRangeByteToRune(t *testing.T) {
	body := []byte("aé\n日b")
	tests := []struct {
		name string
		r    ByteRange
		lo   int
		hi   int
	}{
		{name: "ascii start", r: ByteRange{Lo: 0, Hi: 1}, lo: 0, hi: 1},
		{name: "multibyte", r: ByteRange{Lo: 1, Hi: 3}, lo: 1, hi: 2},
		{name: "across newline", r: ByteRange{Lo: 0, Hi: 4}, lo: 0, hi: 3},
		{name: "full body", r: ByteRange{Lo: 0, Hi: 8}, lo: 0, hi: 5},
		{name: "empty range", r: ByteRange{Lo: 3, Hi: 3}, lo: 2, hi: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lo, hi, ok := RangeByteToRune(body, tt.r)
			if !ok || lo != tt.lo || hi != tt.hi {
				t.Fatalf("RangeByteToRune(%v) = %d, %d, %v; want %d, %d, true", tt.r, lo, hi, ok, tt.lo, tt.hi)
			}
		})
	}
}

func TestRangeByteToRuneRejectsInvalid(t *testing.T) {
	body := []byte("aé\n日b")
	tests := []ByteRange{{Lo: -1, Hi: 2}, {Lo: 0, Hi: 10}, {Lo: 3, Hi: 2}, {Lo: 2, Hi: 3}, {Lo: 5, Hi: 6}}
	for _, r := range tests {
		if _, _, ok := RangeByteToRune(body, r); ok {
			t.Fatalf("RangeByteToRune(%v) = true; want false", r)
		}
	}
}

func TestBuildByteToRune(t *testing.T) {
	src := []byte("aé\nb")
	got := BuildByteToRune(src)
	want := []int{0, 1, 1, 2, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("len(BuildByteToRune) = %d, want %d", len(got), len(want))
	}
	for i, v := range want {
		if got[i] != v {
			t.Fatalf("BuildByteToRune[%d] = %d, want %d", i, got[i], v)
		}
	}
}
