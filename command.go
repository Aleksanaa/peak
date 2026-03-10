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

func (e *Editor) Execute(col *Column, win *Window, cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	logDebug("Execute: cmd='%s' hasCol=%v hasWin=%v", cmd, col != nil, win != nil)

	switch cmd {
	case "Exit":
		logDebug("Action: Exit")
		return true

	case "Get":
		if win != nil {
			filename := win.GetFilename()
			logDebug("Action: Get, file='%s'", filename)
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
			logDebug("Action: Put, file='%s'", filename)
			if filename != "" {
				content := win.body.buffer.GetText()
				err := os.WriteFile(filename, []byte(content), 0644)
				if err != nil {
					logDebug("Put error: %v", err)
				}
			}
		}

	case "Del":
		// Del is strictly for windows. It NEVER removes the column.
		if win != nil {
			logDebug("Action: Del window, parentCol=%p", win.parent)
			targetCol := win.parent
			if targetCol != nil {
				for i, w := range targetCol.windows {
					if w == win {
						targetCol.windows = append(targetCol.windows[:i], targetCol.windows[i+1:]...)
						if len(targetCol.windows) == 0 {
							logDebug("Action: Del window -> Column now empty")
							// Acme keeps the empty column. We should too.
							// But we need a way to show it's empty or just let it be.
							// For now, just don't call RemoveColumn.
						}
						targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
						if len(targetCol.windows) > 0 {
							if e.active == win {
								e.active = targetCol.windows[0]
							}
						} else {
							e.active = nil
						}
						return false
					}
				}
			}
		} else {
			logDebug("Warning: Del called without window context")
		}

		case "Delcol":
			// Delcol is strictly for columns.
			targetCol := col
			if targetCol == nil && win != nil {
				targetCol = win.parent
			}
			if targetCol != nil {
				logDebug("Action: Delcol, col=%p", targetCol)
				e.RemoveColumn(targetCol)
				// Acme doesn't exit when the last column is closed.
				// It just stays open with the top menu.
				return false
			} else {
				logDebug("Warning: Delcol called without column context")
			}
		case "NewCol":
		logDebug("Action: NewCol")
		newCol := NewColumn(e.width, 1, 0, e.height-1, e.Execute)
		e.columns = append(e.columns, newCol)
		e.active = newCol.AddWindow("", "")
		e.Resize()

	case "New":
		// New opens a window in the specified column.
		targetCol := col
		if targetCol == nil && win != nil {
			targetCol = win.parent
		}
		if targetCol == nil && e.active != nil {
			targetCol = e.active.parent
		}
		if targetCol == nil && len(e.columns) > 0 {
			targetCol = e.columns[0]
		}

		if targetCol != nil {
			logDebug("Action: New window in col=%p", targetCol)
			e.active = targetCol.AddWindow("", "")
			targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
		} else {
			logDebug("Warning: New called without column target")
		}
	}
	return false
}

func (e *Editor) RemoveColumn(c *Column) {
	logDebug("RemoveColumn: col=%p", c)
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
