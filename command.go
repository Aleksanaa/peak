package main

import (
	"strings"
	"unicode"
)

func (b *Buffer) GetWordAt(x, y int) string {
	if y < 0 || y >= len(b.lines) {
		return ""
	}
	line := b.lines[y]
	if x < 0 || x >= len(line) {
		return ""
	}

	start := x
	for start > 0 && isWordChar(line[start-1]) {
		start--
	}
	end := x
	for end < len(line) && isWordChar(line[end]) {
		end++
	}

	return string(line[start:end])
}

func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '/' || r == '.' || r == '-'
}

func (e *Editor) Execute(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}

	switch cmd {
	case "Exit":
		return true
	case "Del":
		// Find window and remove it from its column
		for _, col := range e.columns {
			for i, win := range col.windows {
				if win == e.active {
					col.windows = append(col.windows[:i], col.windows[i+1:]...)
					if len(col.windows) == 0 {
						// Remove column too?
						e.RemoveColumn(col)
					} else {
						col.Resize(col.x, col.y, col.w, col.h) // Re-tile
						e.active = col.windows[0]
					}
					return false
				}
			}
		}
		if len(e.columns) == 0 {
			return true
		}
	case "NewCol":
		// Add new column
		newCol := NewColumn(e.width, 0, 0, e.height, e.Execute)
		e.columns = append(e.columns, newCol)
		e.active = newCol.AddWindow(" [No Name] | Get | Put | Del | Exit ", "")
		e.Resize() // Trigger layout
	case "New":
		// Add window to active column or first column
		if e.active != nil {
			// Find column of active window
			for _, col := range e.columns {
				for _, win := range col.windows {
					if win == e.active {
						e.active = col.AddWindow(" [No Name] | Get | Put | Del | Exit ", "")
						col.Resize(col.x, col.y, col.w, col.h)
						return false
					}
				}
			}
		} else if len(e.columns) > 0 {
			e.active = e.columns[0].AddWindow(" [No Name] | Get | Put | Del | Exit ", "")
			e.columns[0].Resize(e.columns[0].x, e.columns[0].y, e.columns[0].w, e.columns[0].h)
		}
	}
	return false
}

func (e *Editor) RemoveColumn(c *Column) {
	for i, col := range e.columns {
		if col == c {
			e.columns = append(e.columns[:i], e.columns[i+1:]...)
			e.Resize()
			if len(e.columns) > 0 {
				if len(e.columns[0].windows) > 0 {
					e.active = e.columns[0].windows[0]
				}
			}
			break
		}
	}
}

func (e *Editor) Resize() {
	if len(e.columns) == 0 {
		return
	}
	colW := e.width / len(e.columns)
	xOffset := 0
	for i, col := range e.columns {
		actualW := colW
		if i == len(e.columns)-1 {
			actualW = e.width - xOffset
		}
		col.Resize(xOffset, 0, actualW, e.height)
		xOffset += actualW
	}
}
