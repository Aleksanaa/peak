package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aleksana/peak/internal/session"
)

const sessionVersion = 2

type Session struct {
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
	HeightPct int
	Kind      WinKind // WinFile, WinDir or WinTerm
	Tag       string
	Body      string // only for dirty WinFile
	Dirty     bool
	Org       int // the rune offset the view starts at
	Q0, Q1    int // dot
	TabWidth  int
	TermCmd   string
	TermDir   string
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
//	f <h> <org> <q0> <q1> <tw>          → <winTag>
//	u <h> <org> <q0> <q1> <tw> <blen>   → <winTag> → <body bytes>
//	r <h> <org> <q0> <q1>               → <winTag>
//	t <h>                               → <termCmd> → <termDir> → <winTag>
//
// Positions are rune offsets, which hold at any width.
func encode(s Session) []byte {
	b := fmt.Appendf(nil, "peak-session-v%d\n%s\n%s\n", sessionVersion, s.CurrentDir, s.GlobalTag)
	for _, cs := range s.Columns {
		b = fmt.Appendf(b, "c %d\n%s\n", cs.WidthPct, cs.Tag)
		for _, ws := range cs.Windows {
			switch ws.Kind {
			case WinFile:
				if ws.Dirty {
					b = fmt.Appendf(b, "u %d %d %d %d %d %d\n%s\n", ws.HeightPct, ws.Org, ws.Q0, ws.Q1, ws.TabWidth, len(ws.Body), ws.Tag)
					b = append(b, ws.Body...)
				} else {
					b = fmt.Appendf(b, "f %d %d %d %d %d\n%s\n", ws.HeightPct, ws.Org, ws.Q0, ws.Q1, ws.TabWidth, ws.Tag)
				}
			case WinDir:
				b = fmt.Appendf(b, "r %d %d %d %d\n%s\n", ws.HeightPct, ws.Org, ws.Q0, ws.Q1, ws.Tag)
			case WinTerm:
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
				Kind: WinFile, HeightPct: n[0], Org: n[1], Q0: n[2], Q1: n[3], TabWidth: n[4],
				Tag: line(),
			})
		case 'u':
			if curCol == nil || len(n) < 6 {
				continue
			}
			tag := line()
			curCol.Windows = append(curCol.Windows, WindowSession{
				Kind: WinFile, HeightPct: n[0], Org: n[1], Q0: n[2], Q1: n[3], TabWidth: n[4],
				Dirty: true, Body: raw(n[5]), Tag: tag,
			})
		case 'r':
			if curCol == nil || len(n) < 4 {
				continue
			}
			curCol.Windows = append(curCol.Windows, WindowSession{
				Kind: WinDir, HeightPct: n[0], Org: n[1], Q0: n[2], Q1: n[3],
				Tag: line(),
			})
		case 't':
			if curCol == nil || len(n) < 1 {
				continue
			}
			cmd, dir, tag := line(), line(), line()
			curCol.Windows = append(curCol.Windows, WindowSession{
				Kind: WinTerm, HeightPct: n[0], TermCmd: cmd, TermDir: dir, Tag: tag,
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
		Kind:      w.kind,
		Tag:       w.tag.buffer.GetText(),
	}
	switch w.kind {
	case WinFile:
		tv := w.bodyTextView()
		ws.Org, ws.Q0, ws.Q1 = tv.org, tv.buffer.q0, tv.buffer.q1
		ws.TabWidth = tv.tabWidth
		if w.IsDirty() {
			ws.Dirty = true
			ws.Body = tv.buffer.GetText()
		}
	case WinDir:
		tv := w.bodyTextView()
		ws.Org, ws.Q0, ws.Q1 = tv.org, tv.buffer.q0, tv.buffer.q1
	case WinTerm:
		ws.TermCmd = w.body.(*TermView).cmd
		ws.TermDir = w.GetDir()
	}
	return ws
}

// restore loads w's text as ws says, the saved body or the file its tag
// names, and puts the view and dot back where they were, as far as the text
// still reaches. A file since gone leaves the window empty.
func (w *Window) restore(ws *WindowSession) {
	tv := w.bodyTextView()
	if ws.TabWidth > 0 {
		tv.tabWidth = ws.TabWidth
	}
	place := func(error) {
		n := tv.buffer.Len()
		tv.org = min(ws.Org, n)
		tv.buffer.SetDot(min(ws.Q0, n), min(ws.Q1, n))
	}
	w.kind = ws.Kind
	switch {
	case ws.Dirty:
		tv.buffer.SetText(ws.Body)
		w.writable = true
		w.markSaved(-1)
		place(nil)
	case w.fileName() == "": // a new window, whose file is not named yet
		w.loaded(false, true)
	default:
		w.editor.get(w, w.GetFilename(), place)
	}
}

func (e *Editor) Dump(file string) error {
	if file == "" {
		file = defaultSessionFile()
	}
	s := Session{
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

	for _, win := range e.allWindows() {
		e.RemoveWindow(win)
	}
	e.columns, e.active, e.focusedView = nil, nil, e.tag

	if s.CurrentDir != "" {
		os.Chdir(s.CurrentDir)
	}
	e.tag.buffer.SetText(s.GlobalTag)

	// A column is as high as the editor whatever its width, so its windows
	// take their heights at once; resize then lays everything out.
	for _, cs := range s.Columns {
		w := max(5, cs.WidthPct*e.w/100)
		col := NewColumn(0, 1, w, e.h-1, e)
		col.explicitWidth = w
		col.tag.buffer.SetText(cs.Tag)
		e.columns = append(e.columns, col)
		for _, ws := range cs.Windows {
			var win *Window
			if ws.Kind == WinTerm {
				sess, err := session.NewLocal(ws.TermCmd, ws.TermDir)
				if err == nil {
					win, err = col.AddTermWindow(ws.Tag, ws.TermCmd, sess)
				}
				if err != nil {
					continue
				}
			} else {
				win = col.AddWindow(ws.Tag, "")
				win.restore(&ws)
			}
			if ws.HeightPct > 0 {
				win.explicitHeight = max(win.MinSize(), ws.HeightPct*col.h/100)
			}
		}
	}
	if len(e.columns) == 0 {
		col := NewColumn(0, 1, e.w, e.h-1, e)
		col.explicitWidth = e.w
		e.columns = append(e.columns, col)
	}
	e.resize()
	if first := e.columns[0]; len(first.windows) > 0 {
		e.ActivateWindow(first.windows[0])
	}
	return nil
}
