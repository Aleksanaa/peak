package coords

import (
	"sort"
	"unicode/utf8"
)

type ByteRange struct {
	Lo, Hi int
}

func RuneToByteOffset(body []byte, runeOff int) (int, bool) {
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

func FitsUint32(v int) bool {
	return v >= 0 && uint64(v) <= uint64(^uint32(0))
}

func MergeByteRanges(ranges []ByteRange) []ByteRange {
	if len(ranges) == 0 {
		return nil
	}
	sorted := make([]ByteRange, len(ranges))
	copy(sorted, ranges)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Lo != sorted[j].Lo {
			return sorted[i].Lo < sorted[j].Lo
		}
		return sorted[i].Hi < sorted[j].Hi
	})
	merged := []ByteRange{sorted[0]}
	for _, r := range sorted[1:] {
		last := &merged[len(merged)-1]
		if r.Lo <= last.Hi {
			if r.Hi > last.Hi {
				last.Hi = r.Hi
			}
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}

func ExpandToLineBoundaries(body []byte, r ByteRange) ByteRange {
	lo := r.Lo
	for lo > 0 && body[lo-1] != '\n' {
		lo--
	}
	hi := r.Hi
	for hi < len(body) && body[hi] != '\n' {
		hi++
	}
	return ByteRange{Lo: lo, Hi: hi}
}

func RangeByteToRune(body []byte, r ByteRange) (lo, hi int, ok bool) {
	if r.Lo < 0 || r.Hi > len(body) || r.Lo > r.Hi {
		return 0, 0, false
	}
	if !isRuneBoundary(body, r.Lo) || !isRuneBoundary(body, r.Hi) {
		return 0, 0, false
	}
	runes := 0
	for i := 0; i < r.Lo; {
		_, size := utf8.DecodeRune(body[i:])
		if size == 0 {
			return 0, 0, false
		}
		i += size
		runes++
	}
	lo = runes
	hi = lo
	for i := r.Lo; i < r.Hi; {
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

// BuildByteToRune builds a slice where index i holds the rune offset
// corresponding to byte offset i in src. Index len(src) is the past-the-end sentinel.
func BuildByteToRune(src []byte) []int {
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
