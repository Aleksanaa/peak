package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/atotto/clipboard"
	"github.com/gdamore/tcell/v2"
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
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '/' || r == '.' || r == '-' || r == '~'
}

func resolvePath(path string) string {
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			if path == "~" {
				return home
			}
			return filepath.Join(home, path[2:])
		}
	}
	return path
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
			filename := resolvePath(win.GetFilename())
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
			filename := resolvePath(win.GetFilename())
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
		if win != nil {
			logDebug("Action: Del window, parentCol=%p", win.parent)
			targetCol := win.parent
			if targetCol != nil {
				for i, w := range targetCol.windows {
					if w == win {
						targetCol.windows = append(targetCol.windows[:i], targetCol.windows[i+1:]...)
						targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
						if len(targetCol.windows) > 0 {
							if e.active == win {
								e.active = targetCol.windows[0]
								e.focusedView = e.active.body
							}
						} else {
							if e.active == win {
								e.active = nil
								e.focusedView = targetCol.tag
							}
						}
						return false
					}
				}
			}
		}

	case "Delcol":
		targetCol := col
		if targetCol == nil && win != nil {
			targetCol = win.parent
		}
		if targetCol != nil {
			logDebug("Action: Delcol, col=%p", targetCol)
			e.RemoveColumn(targetCol)
			return false
		}

	case "NewCol":
		logDebug("Action: NewCol")
		newCol := NewColumn(e.width, 1, 0, e.height-1, e.Execute)
		e.columns = append(e.columns, newCol)
		e.active = newCol.AddWindow("", "")
		e.focusedView = e.active.body
		e.Resize()

	case "New":
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
			e.focusedView = e.active.body
			targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
		}

	case "Zerox":
		targetWin := win
		if targetWin == nil {
			targetWin = e.active
		}
		if targetWin != nil {
			targetCol := targetWin.parent
			if targetCol != nil {
				logDebug("Action: Zerox window %p", targetWin)
				tagText := targetWin.tag.buffer.GetText()
				bodyText := targetWin.body.buffer.GetText()
				newWin := targetCol.AddWindow(tagText, bodyText)
				newWin.body.scroll = targetWin.body.scroll
				newWin.body.buffer.cursor = targetWin.body.buffer.cursor
				e.active = newWin
				e.focusedView = newWin.body
				targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
			}
		}

	case "Snarf":
		var text string
		if e.focusedView != nil {
			text = e.focusedView.buffer.GetSelectedText()
		}
		if text != "" {
			logDebug("Action: Snarf, text len=%d", len(text))
			clipboard.WriteAll(text)
		}

	default:
		// External command execution
		logDebug("Action: External command '%s'", cmd)
		
		dir := "."
		if win != nil {
			f := resolvePath(win.GetFilename())
			if f != "" {
				if info, err := os.Stat(f); err == nil && info.IsDir() {
					dir = f
				} else {
					dir = filepath.Dir(f)
				}
			}
		} else if e.active != nil {
			f := resolvePath(e.active.GetFilename())
			if f != "" {
				dir = filepath.Dir(f)
			}
		}

		go func() {
			logDebug("Go: exec 'sh -c %s' in dir '%s'", cmd, dir)
			c := exec.Command("sh", "-c", cmd)
			c.Dir = dir
			output, err := c.CombinedOutput()
			logDebug("Go: finished '%s', err=%v, output_len=%d, output='%s'", cmd, err, len(output), string(output))
			
			if err == nil && len(output) > 0 {
				e.screen.PostEvent(tcell.NewEventInterrupt(func() {
					// Check if we can reuse an existing +Errors window
					var reuseWin *Window
					if win != nil {
						fn := win.GetFilename()
						logDebug("Checking win for reuse: '%s'", fn)
						if strings.HasSuffix(fn, "+Errors") {
							reuseWin = win
						}
					}
					if reuseWin == nil && e.active != nil {
						fn := e.active.GetFilename()
						logDebug("Checking active for reuse: '%s'", fn)
						if strings.HasSuffix(fn, "+Errors") {
							reuseWin = e.active
						}
					}

					if reuseWin != nil {
						logDebug("Reusing +Errors window %p", reuseWin)
						reuseWin.body.buffer.SetText(string(output))
						e.focusedView = reuseWin.body
						return
					}

					targetCol := col
					if targetCol == nil {
						if e.active != nil {
							targetCol = e.active.parent
						} else if len(e.columns) > 0 {
							targetCol = e.columns[0]
						}
					}
					
					if targetCol != nil {
						title := filepath.Join(dir, "+Errors")
						newWin := targetCol.AddWindow(title + " Get Put Del ", string(output))
						e.active = newWin
						e.focusedView = newWin.body
						targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
					}
				}))
			} else {
				logDebug("Command finished: err=%v, output_len=%d", err, len(output))
			}
		}()
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
					e.focusedView = e.active.body
				} else {
					e.active = nil
					e.focusedView = e.columns[0].tag
				}
			} else {
				e.active = nil
				e.focusedView = e.tag
			}
			break
		}
	}
}
