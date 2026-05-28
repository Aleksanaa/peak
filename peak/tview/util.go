package tview

import (
	"math"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

const (
	AlignLeft   = iota
	AlignCenter
	AlignRight
)

func Print(screen tcell.Screen, text string, x, y, maxWidth, align int, style tcell.Style) (int, int) {
	if maxWidth <= 0 || len(text) == 0 {
		return 0, 0
	}

	runes := []rune(text)
	textWidth := uniseg.StringWidth(text)

	// Apply alignment
	start := x
	if align == AlignRight {
		if textWidth > maxWidth {
			// Trim from left
			trim := uniseg.StringWidth(string(runes))
			for trim > maxWidth && len(runes) > 0 {
				rw := uniseg.StringWidth(string(runes[0]))
				runes = runes[1:]
				trim -= rw
			}
			textWidth = trim
		}
		start = x + maxWidth - textWidth
	} else if align == AlignCenter {
		if textWidth > maxWidth {
			// Trim from both sides
			excess := textWidth - maxWidth
			remove := excess / 2
			for remove > 0 && len(runes) > 0 {
				remove -= uniseg.StringWidth(string(runes[0]))
				runes = runes[1:]
			}
			excess = textWidth - maxWidth
			remove = excess - (excess / 2)
			for remove > 0 && len(runes) > 0 {
				remove -= uniseg.StringWidth(string(runes[len(runes)-1]))
				runes = runes[:len(runes)-1]
			}
			textWidth = maxWidth
		}
		start = x + (maxWidth-textWidth)/2
	}

	// Draw text
	col := 0
	printed := 0
	for _, r := range runes {
		rw := uniseg.StringWidth(string(r))
		if rw == 0 {
			continue
		}
		if col+rw > maxWidth {
			// Draw ellipsis
			if maxWidth >= 1 {
				screen.SetContent(start+col, y, SemigraphicsHorizontalEllipsis, nil, style)
			}
			break
		}
		screen.SetContent(start+col, y, r, nil, style)
		for k := 1; k < rw; k++ {
			screen.SetContent(start+col+k, y, ' ', nil, style)
		}
		col += rw
		printed++
	}

	return printed, col
}

// PrintSimple is a convenience function that prints text without alignment or width constraints.
func PrintSimple(screen tcell.Screen, text string, x, y int, style tcell.Style) {
	Print(screen, text, x, y, math.MaxInt32, AlignLeft, style)
}
