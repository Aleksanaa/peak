package main

import (
	"sort"
	"unicode/utf8"

	"github.com/odvcencio/gotreesitter"
)

type byteRange struct {
	lo, hi int
}

func runeToByteOffset(body []byte, runeOff int) (int, bool) {
	if runeOff < 0 {
		return 0, false
	}
	runes := 0
	for i := 0; i < len(body); {
		if runes == runeOff {
			return i, true
		}
		_, size := utf8.DecodeRune(body[i:])
		i += size
		runes++
	}
	if runes == runeOff {
		return len(body), true
	}
	return 0, false
}

func pointAtByte(body []byte, byteOff int) (gotreesitter.Point, bool) {
	if byteOff < 0 || byteOff > len(body) {
		return gotreesitter.Point{}, false
	}
	var row, col uint32
	for _, b := range body[:byteOff] {
		if b == '\n' {
			row++
			col = 0
			continue
		}
		col++
	}
	return gotreesitter.Point{Row: row, Column: col}, true
}

func advancePoint(start gotreesitter.Point, text []byte) gotreesitter.Point {
	point := start
	for _, b := range text {
		if b == '\n' {
			point.Row++
			point.Column = 0
			continue
		}
		point.Column++
	}
	return point
}

func fitsUint32(v int) bool {
	return v >= 0 && uint64(v) <= uint64(^uint32(0))
}

func mergeByteRanges(ranges []byteRange) []byteRange {
	if len(ranges) == 0 {
		return nil
	}
	sorted := make([]byteRange, len(ranges))
	copy(sorted, ranges)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].lo != sorted[j].lo {
			return sorted[i].lo < sorted[j].lo
		}
		return sorted[i].hi < sorted[j].hi
	})
	merged := []byteRange{sorted[0]}
	for _, r := range sorted[1:] {
		last := &merged[len(merged)-1]
		if r.lo <= last.hi {
			if r.hi > last.hi {
				last.hi = r.hi
			}
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}

func expandToLineBoundaries(body []byte, r byteRange) byteRange {
	lo := r.lo
	for lo > 0 && body[lo-1] != '\n' {
		lo--
	}
	hi := r.hi
	for hi < len(body) && body[hi] != '\n' {
		hi++
	}
	return byteRange{lo, hi}
}

func tsRangesToByteRanges(ranges []gotreesitter.Range) []byteRange {
	if len(ranges) == 0 {
		return nil
	}
	out := make([]byteRange, len(ranges))
	for i, r := range ranges {
		out[i] = byteRange{lo: int(r.StartByte), hi: int(r.EndByte)}
	}
	return out
}

func rangeByteToRune(body []byte, r byteRange) (lo, hi int, ok bool) {
	if r.lo < 0 || r.hi > len(body) || r.lo > r.hi {
		return 0, 0, false
	}
	if !isRuneBoundary(body, r.lo) || !isRuneBoundary(body, r.hi) {
		return 0, 0, false
	}
	runes := 0
	for i := 0; i < r.lo; {
		_, size := utf8.DecodeRune(body[i:])
		if size == 0 {
			return 0, 0, false
		}
		i += size
		runes++
	}
	lo = runes
	hi = lo
	for i := r.lo; i < r.hi; {
		_, size := utf8.DecodeRune(body[i:])
		i += size
		hi++
	}
	return lo, hi, true
}

func isRuneBoundary(body []byte, off int) bool {
	if off < 0 || off > len(body) {
		return false
	}
	if off == 0 || off == len(body) {
		return true
	}
	return utf8.RuneStart(body[off])
}

// buildByteToRune builds a slice where index i holds the rune offset
// corresponding to byte offset i in src. Index len(src) is the past-the-end sentinel.
func buildByteToRune(src []byte) []int {
	out := make([]int, len(src)+1)
	runeOff := 0
	for i := 0; i < len(src); {
		_, size := utf8.DecodeRune(src[i:])
		for j := range size {
			out[i+j] = runeOff
		}
		i += size
		runeOff++
	}
	out[len(src)] = runeOff
	return out
}
