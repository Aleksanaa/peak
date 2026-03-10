package main

import (
	"os"
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

func (e *Editor) Execute(win *Window, cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	logDebug("Execute: %s", cmd)

	switch cmd {
	case "Exit":
		return true
	case "Get":
		if win != nil {
			filename := win.GetFilename()
			logDebug("Get filename: %s", filename)
			if filename != "" {
				content, err := os.ReadFile(filename)
				if err == nil {
					win.body.buffer.SetText(string(content))
				} else {
					logDebug("Get error: %v", err)
				}
			}
		}
	case "Put":
		if win != nil {
			filename := win.GetFilename()
			logDebug("Put filename: %s", filename)
			if filename != "" {
				content := win.body.buffer.GetText()
				err := os.WriteFile(filename, []byte(content), 0644)
				if err != nil {
					logDebug("Put error: %v", err)
				}
			}
		}
	case "Del":
		// Find window and remove it from its column
		target := win
		if target == nil {
			target = e.active
		}
		for _, col := range e.columns {
			for i, w := range col.windows {
				if w == target {
					col.windows = append(col.windows[:i], col.windows[i+1:]...)
					if len(col.windows) == 0 {
						e.RemoveColumn(col)
					} else {
						col.Resize(col.x, col.y, col.w, col.h) // Re-tile
						if e.active == target {
							e.active = col.windows[0]
						}
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
		e.active = newCol.AddWindow(" [No Name] Get Put Del Exit ", "")
		e.Resize() // Trigger layout
	case "New":
		// Add window to active column or first column
		parent := win
		if parent == nil {
			parent = e.active
		}

		if parent != nil {
			// Find column of parent window
			for _, col := range e.columns {
				for _, w := range col.windows {
					if w == parent {
						e.active = col.AddWindow(" [No Name] Get Put Del Exit ", "")
						col.Resize(col.x, col.y, col.w, col.h)
						return false
					}
				}
			}
		} else if len(e.columns) > 0 {
			e.active = e.columns[0].AddWindow(" [No Name] Get Put Del Exit ", "")
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
