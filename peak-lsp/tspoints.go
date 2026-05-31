package main

import (
	"github.com/aleksana/peak/internal/coords"
	"github.com/odvcencio/gotreesitter"
)

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

func tsRangesToByteRanges(ranges []gotreesitter.Range) []coords.ByteRange {
	if len(ranges) == 0 {
		return nil
	}
	out := make([]coords.ByteRange, len(ranges))
	for i, r := range ranges {
		out[i] = coords.ByteRange{Lo: int(r.StartByte), Hi: int(r.EndByte)}
	}
	return out
}
