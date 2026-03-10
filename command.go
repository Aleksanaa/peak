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

func (e *Editor) resolvePathWithContext(win *Window, path string) string {
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			if path == "~" {
				return home
			}
			return filepath.Join(home, path[2:])
		}
	}
	if filepath.IsAbs(path) {
		return path
	}

	dir, _ := os.Getwd()
	targetWin := win
	if targetWin == nil {
		targetWin = e.active
	}
	if targetWin != nil {
		f := resolvePath(targetWin.GetFilename())
		if f != "" {
			if info, err := os.Stat(f); err == nil && info.IsDir() {
				dir = f
			} else {
				dir = filepath.Dir(f)
			}
		}
	}
	return filepath.Join(dir, path)
}

func (e *Editor) Execute(col *Column, win *Window, cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	
	cmdParts := strings.Fields(cmd)
	rootCmd := cmdParts[0]
	
	logDebug("Execute: rootCmd='%s' full='%s' hasCol=%v hasWin=%v", rootCmd, cmd, col != nil, win != nil)

	switch rootCmd {
	case "Exit":
		return true

	case "Get":
		if win != nil {
			filename := e.resolvePathWithContext(win, win.GetFilename())
			if filename != "" {
				info, err := os.Stat(filename)
				if err == nil {
					if info.IsDir() {
						entries, err := os.ReadDir(filename)
						if err == nil {
							var sb strings.Builder
							for _, entry := range entries {
								sb.WriteString(entry.Name())
								if entry.IsDir() {
									sb.WriteRune('/')
								}
								sb.WriteRune('\n')
							}
							win.body.buffer.SetText(sb.String())
						}
					} else {
						content, err := os.ReadFile(filename)
						if err == nil {
							win.body.buffer.SetText(string(content))
						}
					}
				}
			}
		}

	case "Put":
		if win != nil {
			filename := e.resolvePathWithContext(win, win.GetFilename())
			if filename != "" {
				content := win.body.buffer.GetText()
				os.WriteFile(filename, []byte(content), 0644)
			}
		}

	case "Del":
		if win != nil {
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
			e.RemoveColumn(targetCol)
			return false
		}

	case "NewCol":
		newCol := NewColumn(e.width, 1, 0, e.height-1, e, e.Execute)
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
			e.active = targetCol.AddWindow("", "")
			e.focusedView = e.active.body
			targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
		}

	case "Zerox":
		if win != nil {
			targetCol := win.parent
			if targetCol != nil {
				tagText := win.tag.buffer.GetText()
				bodyText := win.body.buffer.GetText()
				newWin := targetCol.AddWindow(tagText, bodyText)
				newWin.body.scroll = win.body.scroll
				newWin.body.buffer.cursor = win.body.buffer.cursor
				e.active = newWin
				e.focusedView = newWin.body
				targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
			}
		} else if col != nil {
			newCol := NewColumn(e.width, 1, 0, e.height-1, e, e.Execute)
			e.columns = append(e.columns, newCol)
			for _, w := range col.windows {
				tagText := w.tag.buffer.GetText()
				bodyText := w.body.buffer.GetText()
				nw := newCol.AddWindow(tagText, bodyText)
				nw.body.scroll = w.body.scroll
				nw.body.buffer.cursor = w.body.buffer.cursor
			}
			e.Resize()
		}

	case "Snarf":
		if e.focusedView != nil {
			text := e.focusedView.buffer.GetSelectedText()
			if text != "" {
				clipboard.WriteAll(text)
			}
		}

	case "Look":
		path := strings.TrimPrefix(cmd, "Look")
		path = strings.TrimSpace(path)
		if path == "" { return false }
		fullPath := e.resolvePathWithContext(win, path)
		
		for _, col := range e.columns {
			for _, w := range col.windows {
				if e.resolvePathWithContext(nil, w.GetFilename()) == fullPath {
					e.active = w
					e.focusedView = w.body
					return false
				}
			}
		}

		info, err := os.Stat(fullPath)
		if err != nil { return false }

		var content string
		if info.IsDir() {
			files, err := os.ReadDir(fullPath)
			if err == nil {
				var sb strings.Builder
				for _, f := range files {
					sb.WriteString(f.Name())
					if f.IsDir() { sb.WriteRune('/') }
					sb.WriteRune('\n')
				}
				content = sb.String()
			}
		} else {
			data, err := os.ReadFile(fullPath)
			if err == nil { content = string(data) }
		}

		targetCol := col
		if targetCol == nil {
			if e.active != nil { targetCol = e.active.parent } else if len(e.columns) > 0 { targetCol = e.columns[0] }
		}
		if targetCol != nil {
			tagPath := path
			// If it was a relative path, we need to make it stable for the new window.
			// But the user wants to "inherit" the style. 
			// If 'path' is relative, let's use the resolved path but keep it relative to CWD if possible, 
			// or just use hpath if the parent was hpath.
			
			if !filepath.IsAbs(path) && !strings.HasPrefix(path, "~") {
				// It was a relative path. Let's resolve it to a stable relative path 
				// from CWD or just use the full resolved path if it's cleaner.
				// For now, let's use fullPath but check if parent window used hpath.
				if win != nil {
					parentFn := win.GetFilename()
					if strings.HasPrefix(parentFn, "~") {
						home, _ := os.UserHomeDir()
						if strings.HasPrefix(fullPath, home) {
							tagPath = "~" + fullPath[len(home):]
						}
					} else if filepath.IsAbs(parentFn) {
						tagPath = fullPath
					}
				} else {
					// Fallback to absolute if no parent context
					tagPath = fullPath
				}
			}
			
			newWin := targetCol.AddWindow(tagPath+" Get Put Snarf Zerox Del ", content)
			e.active = newWin
			e.focusedView = newWin.body
			targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
		}

	default:
		dir, _ := os.Getwd()
		if win != nil {
			f := resolvePath(win.GetFilename())
			if f != "" {
				if info, err := os.Stat(f); err == nil {
					if info.IsDir() { dir = f } else { dir = filepath.Dir(f) }
				}
			}
		}
		go func() {
			c := exec.Command("sh", "-c", cmd)
			c.Dir = dir
			output, err := c.CombinedOutput()
			if err == nil && len(output) > 0 {
				e.screen.PostEvent(tcell.NewEventInterrupt(func() {
					var reuseWin *Window
					if win != nil && strings.HasSuffix(win.GetFilename(), "+Errors") { reuseWin = win }
					if reuseWin == nil && e.active != nil && strings.HasSuffix(e.active.GetFilename(), "+Errors") { reuseWin = e.active }

					if reuseWin != nil {
						reuseWin.body.buffer.SetText(string(output))
						e.focusedView = reuseWin.body
						return
					}

					targetCol := col
					if targetCol == nil {
						if e.active != nil { targetCol = e.active.parent } else if len(e.columns) > 0 { targetCol = e.columns[0] }
					}
					if targetCol != nil {
						title := filepath.Join(dir, "+Errors")
						newWin := targetCol.AddWindow(title + " Get Put Snarf Zerox Del ", string(output))
						e.active = newWin
						e.focusedView = newWin.body
						targetCol.Resize(targetCol.x, targetCol.y, targetCol.w, targetCol.h)
					}
				}))
			}
		}()
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
