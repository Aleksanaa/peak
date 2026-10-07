package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aleksana/peak/internal/quote"
	"github.com/aleksana/peak/internal/session"
)

const sessionVersion = 1

type Session struct {
	Version    int
	CurrentDir string
	GlobalTag  string
	Columns    []ColumnSession
}

type ColumnSession struct {
	WidthPct int
	Tag      string
	Windows  []WindowSession
}

type WindowSession struct {
	HeightPct  int
	Kind       string // "file", "dir", "term"
	Tag        string
	Body       string // only for dirty WinFile
	Dirty      bool
	Scroll     int
	CursorLine int
	CursorCol  int
	TabWidth   int
	TermCmd    string
	TermDir    string
}

func defaultSessionFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".peak", "peak.dump")
}

// encode serialises s into a line-based byte slice.
//
// Format per record type — integers on the type line, strings on following lines:
//
//	peak-session-v<N> / <currentDir> / <globalTag>
//	c <widthPct>         → <colTag>
//	f <h> <sc> <cl> <cc> <tw>          → <winTag>
//	u <h> <sc> <cl> <cc> <tw> <blen>   → <winTag> → <body bytes>
//	r <h> <sc> <cl> <cc>               → <winTag>
//	t <h>                              → <termCmd> → <termDir> → <winTag>
func encode(s Session) []byte {
	b := fmt.Appendf(nil, "peak-session-v%d\n%s\n%s\n", s.Version, s.CurrentDir, s.GlobalTag)
	for _, cs := range s.Columns {
		b = fmt.Appendf(b, "c %d\n%s\n", cs.WidthPct, cs.Tag)
		for _, ws := range cs.Windows {
			switch ws.Kind {
			case "file":
				if ws.Dirty {
					b = fmt.Appendf(b, "u %d %d %d %d %d %d\n%s\n", ws.HeightPct, ws.Scroll, ws.CursorLine, ws.CursorCol, ws.TabWidth, len(ws.Body), ws.Tag)
					b = append(b, ws.Body...)
				} else {
					b = fmt.Appendf(b, "f %d %d %d %d %d\n%s\n", ws.HeightPct, ws.Scroll, ws.CursorLine, ws.CursorCol, ws.TabWidth, ws.Tag)
				}
			case "dir":
				b = fmt.Appendf(b, "r %d %d %d %d\n%s\n", ws.HeightPct, ws.Scroll, ws.CursorLine, ws.CursorCol, ws.Tag)
			case "term":
				b = fmt.Appendf(b, "t %d\n%s\n%s\n%s\n", ws.HeightPct, ws.TermCmd, ws.TermDir, ws.Tag)
			}
		}
	}
	return b
}

func decode(data []byte) (Session, error) {
	pos := 0

	line := func() string {
		start := pos
		for pos < len(data) && data[pos] != '\n' {
			pos++
		}
		s := strings.TrimRight(string(data[start:pos]), "\r")
		if pos < len(data) {
			pos++
		}
		return s
	}
	raw := func(n int) string {
		if pos+n > len(data) {
			n = len(data) - pos
		}
		s := string(data[pos : pos+n])
		pos += n
		return s
	}
	nums := func(s string) []int {
		var out []int
		for _, f := range strings.Fields(s) {
			if n, err := strconv.Atoi(f); err == nil {
				out = append(out, n)
			}
		}
		return out
	}

	var s Session
	if magic := line(); magic != fmt.Sprintf("peak-session-v%d", sessionVersion) {
		return s, fmt.Errorf("unknown session format %q", magic)
	}
	s.Version = sessionVersion
	s.CurrentDir = line()
	s.GlobalTag = line()

	var curCol *ColumnSession
	for pos < len(data) {
		l := line()
		if l == "" {
			break
		}
		n := nums(l[1:])
		switch l[0] {
		case 'c':
			if len(n) < 1 {
				continue
			}
			s.Columns = append(s.Columns, ColumnSession{WidthPct: n[0], Tag: line()})
			curCol = &s.Columns[len(s.Columns)-1]
		case 'f':
			if curCol == nil || len(n) < 5 {
				continue
			}
			curCol.Windows = append(curCol.Windows, WindowSession{
				Kind: "file", HeightPct: n[0], Scroll: n[1], CursorLine: n[2], CursorCol: n[3], TabWidth: n[4],
				Tag: line(),
			})
		case 'u':
			if curCol == nil || len(n) < 6 {
				continue
			}
			tag := line()
			curCol.Windows = append(curCol.Windows, WindowSession{
				Kind: "file", HeightPct: n[0], Scroll: n[1], CursorLine: n[2], CursorCol: n[3], TabWidth: n[4],
				Dirty: true, Body: raw(n[5]), Tag: tag,
			})
		case 'r':
			if curCol == nil || len(n) < 4 {
				continue
			}
			curCol.Windows = append(curCol.Windows, WindowSession{
				Kind: "dir", HeightPct: n[0], Scroll: n[1], CursorLine: n[2], CursorCol: n[3],
				Tag: line(),
			})
		case 't':
			if curCol == nil || len(n) < 1 {
				continue
			}
			cmd, dir, tag := line(), line(), line()
			curCol.Windows = append(curCol.Windows, WindowSession{
				Kind: "term", HeightPct: n[0], TermCmd: cmd, TermDir: dir, Tag: tag,
			})
		}
	}
	return s, nil
}

func (c *Column) saveState(totalW int) ColumnSession {
	cs := ColumnSession{
		WidthPct: 100 * c.explicitWidth / totalW,
		Tag:      c.tag.buffer.GetText(),
	}
	for _, win := range c.windows {
		if win.kind != WinOut {
			cs.Windows = append(cs.Windows, win.saveState(c.h))
		}
	}
	return cs
}

func (w *Window) saveState(colH int) WindowSession {
	ws := WindowSession{
		HeightPct: 100 * w.explicitHeight / colH,
		Tag:       w.tag.buffer.GetText(),
	}
	switch w.kind {
	case WinFile:
		ws.Kind = "file"
		if tv := w.bodyTextView(); tv != nil {
			ws.Scroll = tv.top()
			ws.CursorLine = tv.buffer.cursor.y
			ws.CursorCol = tv.buffer.cursor.x
			ws.TabWidth = tv.tabWidth
			if w.IsDirty() {
				ws.Dirty = true
				ws.Body = tv.buffer.GetText()
			}
		}
	case WinDir:
		ws.Kind = "dir"
		if tv := w.bodyTextView(); tv != nil {
			ws.Scroll = tv.top()
			ws.CursorLine = tv.buffer.cursor.y
			ws.CursorCol = tv.buffer.cursor.x
		}
	case WinTerm:
		ws.Kind = "term"
		if tv, ok := w.body.(*TermView); ok {
			ws.TermCmd = tv.cmd
		}
		ws.TermDir = w.GetDir()
	}
	return ws
}

// applyPreset loads content into w from ws.
func (w *Window) applyPreset(ws *WindowSession) {
	filename, _ := quote.Cut(ws.Tag)
	tv := w.bodyTextView()
	if ws.TabWidth > 0 {
		tv.tabWidth = ws.TabWidth
	}
	if ws.Dirty {
		tv.buffer.SetText(ws.Body)
		w.kind, w.writable = WinFile, true
		w.markSaved(-1)
	} else if filename != "" {
		if content, isDir, writable, err := readFileOrDir(filename); err == nil {
			tv.buffer.SetText(content)
			w.loaded(isDir, writable)
		}
	}
}

// restoreViewState sets scroll and cursor once Resize has set the final width.
func (w *Window) restoreViewState(ws WindowSession) {
	tv := w.bodyTextView()
	if tv == nil {
		return
	}
	if ws.Scroll > 0 {
		tv.setTop(ws.Scroll)
	}
	line := max(0, min(ws.CursorLine, len(tv.buffer.lines)-1))
	col := max(0, min(ws.CursorCol, len(tv.buffer.lines[line])))
	tv.buffer.cursor = Cursor{col, line}
}

func (e *Editor) Dump(file string) error {
	if file == "" {
		file = defaultSessionFile()
	}
	s := Session{
		Version:    sessionVersion,
		CurrentDir: getwd(),
		GlobalTag:  e.tag.buffer.GetText(),
	}
	for _, col := range e.columns {
		s.Columns = append(s.Columns, col.saveState(e.w))
	}
	return os.WriteFile(file, encode(s), 0600)
}

func (e *Editor) Load(file string) error {
	if file == "" {
		file = defaultSessionFile()
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	s, err := decode(data)
	if err != nil {
		return err
	}

	for len(e.columns) > 0 {
		col := e.columns[0]
		for len(col.windows) > 0 {
			e.ninep.UmountWindow(col.windows[0])
			col.windows[0].Close()
			col.windows = col.windows[1:]
		}
		e.columns = e.columns[1:]
	}
	e.active = nil
	e.focusedView = e.tag

	if s.CurrentDir != "" {
		os.Chdir(s.CurrentDir)
	}
	e.tag.buffer.SetText(s.GlobalTag)

	// Pass 1: create columns so e.resize() can compute their dimensions.
	for _, cs := range s.Columns {
		w := max(5, cs.WidthPct*e.w/100)
		col := NewColumn(0, 1, w, e.h-1, e)
		col.explicitWidth = w
		col.tag.buffer.SetText(cs.Tag)
		e.columns = append(e.columns, col)
	}
	if len(e.columns) == 0 {
		col := NewColumn(0, 1, e.w, e.h-1, e)
		col.explicitWidth = e.w
		e.columns = append(e.columns, col)
	}
	e.resize()

	// Pass 2: add windows now that col.h is set for height calculation.
	type pending struct {
		win *Window
		ws  WindowSession
	}
	var deferred []pending
	for i, cs := range s.Columns {
		col := e.columns[i]
		for _, ws := range cs.Windows {
			var win *Window
			if ws.Kind == "term" {
				sess, err := session.NewLocal(ws.TermCmd, ws.TermDir)
				if err == nil {
					win, err = col.AddTermWindow(ws.Tag, ws.TermCmd, sess)
				}
				if err != nil {
					continue
				}
			} else {
				win = col.AddWindow(ws.Tag, "")
				win.applyPreset(&ws)
			}
			if ws.HeightPct > 0 {
				win.explicitHeight = max(win.MinSize(), ws.HeightPct*col.h/100)
			}
			deferred = append(deferred, pending{win, ws})
		}
	}

	for _, col := range e.columns {
		col.Resize(col.rect)
	}
	// Restore scroll and cursor after Resize: the scroll saved is a visual
	// line, which depends on the window's final width.
	for _, p := range deferred {
		p.win.restoreViewState(p.ws)
	}
	if len(e.columns) > 0 && len(e.columns[0].windows) > 0 {
		e.ActivateWindow(e.columns[0].windows[0])
	}
	return nil
}
