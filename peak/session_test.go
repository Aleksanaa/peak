package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func newTestEditorWithColumn(t *testing.T) (*Editor, *Column) {
	t.Helper()
	e, _ := setupTest(t, 200, 50)
	col := NewColumn(0, 1, 200, 49, e)
	col.explicitWidth = 200
	e.columns = append(e.columns, col)
	e.resize()
	e.syncChildren()
	return e, col
}

func addFileWindow(t *testing.T, col *Column, tag, body string) *Window {
	t.Helper()
	win := col.AddWindow(tag, body)
	win.kind = WinFile
	win.writable = true
	win.savedVersion = win.bodyTextView().buffer.version
	col.Resize(col.x, col.y, col.w, col.h)
	return win
}

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "peak-session-test-*")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(content)
	f.Close()
	return f.Name()
}

// ── defaultSessionFile ───────────────────────────────────────────────────────

func TestDefaultSessionFile(t *testing.T) {
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".peak", "peak.dump")
	if got := defaultSessionFile(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ── Column.saveState ─────────────────────────────────────────────────────────

func TestColumnSaveState(t *testing.T) {
	e, col := newTestEditorWithColumn(t)
	col.tag.buffer.SetText(" New Zerox Delcol ")

	cs := col.saveState(e.w)

	wantPct := 100 * col.explicitWidth / e.w
	if cs.WidthPct != wantPct {
		t.Errorf("WidthPct = %v, want %v", cs.WidthPct, wantPct)
	}
	if cs.Tag != " New Zerox Delcol " {
		t.Errorf("Tag = %q", cs.Tag)
	}
}

// ── Window.saveState ─────────────────────────────────────────────────────────

func TestWindowSaveStateCleanFile(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	win := addFileWindow(t, col, " /tmp/foo.go Get Put Del ", "line1\nline2\nline3")
	tv := win.bodyTextView()
	tv.scroll.Pos = 1
	tv.buffer.cursor = Cursor{3, 2}
	tv.tabWidth = 8
	win.explicitHeight = 20

	ws := win.saveState(col.h)

	if ws.Kind != "file" {
		t.Errorf("Kind = %q, want file", ws.Kind)
	}
	if ws.Dirty || ws.Body != "" {
		t.Error("clean file should not set Dirty or Body")
	}
	if ws.Scroll != 1 {
		t.Errorf("Scroll = %d, want 1", ws.Scroll)
	}
	if ws.CursorLine != 2 || ws.CursorCol != 3 {
		t.Errorf("cursor = (%d,%d), want (2,3)", ws.CursorLine, ws.CursorCol)
	}
	if ws.TabWidth != 8 {
		t.Errorf("TabWidth = %d, want 8", ws.TabWidth)
	}
	wantPct := 100 * win.explicitHeight / col.h
	if ws.HeightPct != wantPct {
		t.Errorf("HeightPct = %v, want %v", ws.HeightPct, wantPct)
	}
}

func TestWindowSaveStateDirtyFile(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	win := col.AddWindow(" /tmp/unsaved.go Get Put Del ", "")
	win.kind = WinFile
	win.writable = true
	// savedVersion stays 0; mutate buffer to make it dirty
	win.bodyTextView().buffer.SetText("unsaved content")

	ws := win.saveState(col.h)

	if ws.Kind != "file" {
		t.Errorf("Kind = %q, want file", ws.Kind)
	}
	if !ws.Dirty {
		t.Error("expected Dirty=true")
	}
	if ws.Body != "unsaved content" {
		t.Errorf("Body = %q", ws.Body)
	}
}

func TestWindowSaveStateDir(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	win := col.AddWindow(" /home/user/ Get Del ", "")
	win.kind = WinDir
	tv := win.bodyTextView()
	tv.scroll.Pos = 5
	tv.buffer.cursor = Cursor{0, 3}

	ws := win.saveState(col.h)

	if ws.Kind != "dir" {
		t.Errorf("Kind = %q, want dir", ws.Kind)
	}
	if ws.Scroll != 5 {
		t.Errorf("Scroll = %d, want 5", ws.Scroll)
	}
	if ws.CursorLine != 3 || ws.CursorCol != 0 {
		t.Errorf("cursor = (%d,%d), want (3,0)", ws.CursorLine, ws.CursorCol)
	}
	if ws.Dirty || ws.Body != "" {
		t.Error("dir should not set Dirty or Body")
	}
}

func TestWindowSaveStateTerm(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	win := col.AddWindow(" /home/user/-bash Del ", "")
	win.kind = WinTerm
	win.explicitHeight = 15

	ws := win.saveState(col.h)

	if ws.Kind != "term" {
		t.Errorf("Kind = %q, want term", ws.Kind)
	}
	// TermCmd comes from TermView.cmd; empty here since body is a stub TextView.
	if ws.TermDir != win.GetDir() {
		t.Errorf("TermDir = %q, want %q", ws.TermDir, win.GetDir())
	}
}

func TestWindowSaveStateTag(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	win := col.AddWindow(" /tmp/x.go Get Put Del ", "")
	win.kind = WinFile
	win.savedVersion = win.bodyTextView().buffer.version

	ws := win.saveState(col.h)

	if ws.Tag != " /tmp/x.go Get Put Del " {
		t.Errorf("Tag = %q", ws.Tag)
	}
}

// ── Window.applyPreset ───────────────────────────────────────────────────────

func TestWindowApplyPresetDirty(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	win := col.AddWindow(" /tmp/new.go Get Put Del ", "")

	ws := &WindowSession{
		Tag:   " /tmp/new.go Get Put Del ",
		Kind:  "file",
		Dirty: true,
		Body:  "dirty content\nline2",
	}
	win.applyPreset(ws)

	if win.kind != WinFile {
		t.Errorf("kind = %v, want WinFile", win.kind)
	}
	if !win.writable {
		t.Error("expected writable=true")
	}
	if win.IsDirty() == false {
		t.Error("expected window to be dirty")
	}
	if got := win.bodyTextView().buffer.GetText(); got != "dirty content\nline2" {
		t.Errorf("body = %q", got)
	}
}

func TestWindowApplyPresetDirtyTabWidth(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	win := col.AddWindow(" /tmp/f.go Get Put Del ", "")
	ws := &WindowSession{
		Tag: " /tmp/f.go Get Put Del ", Kind: "file",
		Dirty: true, Body: "x", TabWidth: 8,
	}
	win.applyPreset(ws)
	if win.bodyTextView().tabWidth != 8 {
		t.Errorf("tabWidth = %d, want 8", win.bodyTextView().tabWidth)
	}
}

func TestWindowApplyPresetCleanFile(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	path := writeTempFile(t, "hello from disk\n")
	win := col.AddWindow(" "+path+" Get Put Del ", "")

	ws := &WindowSession{Tag: " " + path + " Get Put Del ", Kind: "file"}
	win.applyPreset(ws)

	if win.kind != WinFile {
		t.Errorf("kind = %v, want WinFile", win.kind)
	}
	if win.IsDirty() {
		t.Error("expected clean file to not be dirty")
	}
	body := win.bodyTextView().buffer.GetText()
	if !strings.Contains(body, "hello from disk") {
		t.Errorf("body = %q, expected file content", body)
	}
}

func TestWindowApplyPresetCleanDir(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	dir := t.TempDir()
	win := col.AddWindow(" "+dir+"/ Get Del ", "")

	ws := &WindowSession{Tag: " " + dir + "/ Get Del ", Kind: "dir"}
	win.applyPreset(ws)

	if win.kind != WinDir {
		t.Errorf("kind = %v, want WinDir", win.kind)
	}
}

func TestWindowApplyPresetFileNotFound(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	win := col.AddWindow(" /nonexistent/path/file.go Get Put Del ", "")

	ws := &WindowSession{Tag: " /nonexistent/path/file.go Get Put Del ", Kind: "file"}
	win.applyPreset(ws) // must not panic

	// window stays in default state: empty body, not modified
	if win.IsDirty() {
		t.Error("missing file should leave window clean")
	}
}

func TestWindowApplyPresetTabWidthZeroNoOverride(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	win := col.AddWindow(" /tmp/f.go Get Put Del ", "")
	win.bodyTextView().tabWidth = 2
	ws := &WindowSession{
		Tag: " /tmp/f.go Get Put Del ", Kind: "file",
		Dirty: true, Body: "x", TabWidth: 0,
	}
	win.applyPreset(ws)
	if win.bodyTextView().tabWidth != 2 {
		t.Errorf("TabWidth=0 should not override existing tabWidth, got %d", win.bodyTextView().tabWidth)
	}
}

// ── Window.restoreViewState ──────────────────────────────────────────────────

func makeTextWindow(t *testing.T, col *Column, body string) *Window {
	t.Helper()
	win := col.AddWindow(" /tmp/f.go Get Put Del ", body)
	win.kind = WinFile
	win.savedVersion = win.bodyTextView().buffer.version
	col.Resize(col.x, col.y, col.w, col.h) // computes layout
	return win
}

func TestWindowRestoreViewState(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	lines := strings.Repeat("line of text\n", 30)
	win := makeTextWindow(t, col, lines)

	win.restoreViewState(WindowSession{Scroll: 5, CursorLine: 10, CursorCol: 3})

	tv := win.bodyTextView()
	if tv.scroll.Pos != 5 {
		t.Errorf("Scroll = %d, want 5", tv.scroll.Pos)
	}
	if tv.buffer.cursor.y != 10 || tv.buffer.cursor.x != 3 {
		t.Errorf("cursor = (%d,%d), want (10,3)", tv.buffer.cursor.y, tv.buffer.cursor.x)
	}
}

func TestWindowRestoreViewStateCursorAtOrigin(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	win := makeTextWindow(t, col, strings.Repeat("x\n", 20))
	// put cursor somewhere else first
	win.bodyTextView().buffer.cursor = Cursor{5, 5}

	win.restoreViewState(WindowSession{CursorLine: 0, CursorCol: 0})

	c := win.bodyTextView().buffer.cursor
	if c.x != 0 || c.y != 0 {
		t.Errorf("cursor = (%d,%d), want (0,0)", c.x, c.y)
	}
}

func TestWindowRestoreViewStateCursorClamped(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	win := makeTextWindow(t, col, "only one line")

	win.restoreViewState(WindowSession{CursorLine: 999, CursorCol: 999})

	c := win.bodyTextView().buffer.cursor
	tv := win.bodyTextView()
	maxLine := len(tv.buffer.lines) - 1
	if c.y != maxLine {
		t.Errorf("line clamped to %d, want %d", c.y, maxLine)
	}
	maxCol := len(tv.buffer.lines[c.y])
	if c.x != maxCol {
		t.Errorf("col clamped to %d, want %d", c.x, maxCol)
	}
}

func TestWindowRestoreViewStateScrollClamped(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	win := makeTextWindow(t, col, "short\n")

	win.restoreViewState(WindowSession{Scroll: 99999})

	tv := win.bodyTextView()
	if tv.scroll.Pos > max(0, len(tv.layout)-1) {
		t.Errorf("Scroll not clamped: pos=%d, layout=%d", tv.scroll.Pos, len(tv.layout))
	}
}

// ── Editor.Dump ───────────────────────────────────────────────────────────────

func TestEditorDumpJSON(t *testing.T) {
	e, col := newTestEditorWithColumn(t)
	e.tag.buffer.SetText(" NewCol Help Dump Exit ")
	col.tag.buffer.SetText(" New Zerox Delcol ")
	addFileWindow(t, col, " /tmp/a.go Get Put Del ", "")

	dest := filepath.Join(t.TempDir(), "session.json")
	if err := e.Dump(dest); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(dest)
	s, err2 := decode(data)
	if err2 != nil {
		t.Fatalf("decode: %v", err2)
	}
	if s.Version != sessionVersion {
		t.Errorf("Version = %d, want %d", s.Version, sessionVersion)
	}
	if s.GlobalTag != " NewCol Help Dump Exit " {
		t.Errorf("GlobalTag = %q", s.GlobalTag)
	}
	if len(s.Columns) != 1 {
		t.Fatalf("Columns = %d, want 1", len(s.Columns))
	}
	if s.Columns[0].Tag != " New Zerox Delcol " {
		t.Errorf("Column tag = %q", s.Columns[0].Tag)
	}
	if len(s.Columns[0].Windows) != 1 {
		t.Errorf("Windows = %d, want 1", len(s.Columns[0].Windows))
	}
}

func TestEditorDumpSkipsWinOut(t *testing.T) {
	e, col := newTestEditorWithColumn(t)
	win := col.AddWindow(" /tmp/+Errors Get Del ", "some output")
	win.kind = WinOut

	dest := filepath.Join(t.TempDir(), "session.json")
	e.Dump(dest)

	data, _ := os.ReadFile(dest)
	s, _ := decode(data)
	for _, cs := range s.Columns {
		if len(cs.Windows) != 0 {
			t.Errorf("WinOut should be skipped, got %d windows in column", len(cs.Windows))
		}
	}
}

func TestEditorDumpColumnIndex(t *testing.T) {
	e, _ := setupTest(t, 200, 50)
	col0 := NewColumn(0, 1, 100, 49, e)
	col0.explicitWidth = 100
	col1 := NewColumn(100, 1, 100, 49, e)
	col1.explicitWidth = 100
	e.columns = append(e.columns, col0, col1)
	e.resize()
	e.syncChildren()

	addFileWindow(t, col1, " /tmp/b.go Get Put Del ", "")

	dest := filepath.Join(t.TempDir(), "session.json")
	e.Dump(dest)

	data, _ := os.ReadFile(dest)
	s, _ := decode(data)
	if len(s.Columns[0].Windows) != 0 {
		t.Errorf("col0 should have no windows, got %d", len(s.Columns[0].Windows))
	}
	if len(s.Columns[1].Windows) != 1 {
		t.Errorf("col1 should have 1 window, got %d", len(s.Columns[1].Windows))
	}
}

func TestEditorDumpWidthPct(t *testing.T) {
	e, col := newTestEditorWithColumn(t)
	dest := filepath.Join(t.TempDir(), "session.json")
	e.Dump(dest)

	data, _ := os.ReadFile(dest)
	s, _ := decode(data)

	want := 100 * col.explicitWidth / e.w
	if s.Columns[0].WidthPct != want {
		t.Errorf("WidthPct = %v, want %v", s.Columns[0].WidthPct, want)
	}
}

// ── Editor.Load ───────────────────────────────────────────────────────────────

func TestEditorLoadFileNotFound(t *testing.T) {
	e, _ := newTestEditorWithColumn(t)
	origCols := len(e.columns)

	err := e.Load("/nonexistent/session.json")

	if err == nil {
		t.Error("expected error for missing file")
	}
	if len(e.columns) != origCols {
		t.Error("state should be unchanged on error")
	}
}

func TestEditorLoadInvalidJSON(t *testing.T) {
	e, _ := newTestEditorWithColumn(t)
	f := writeTempFile(t, "not valid json{{{")

	err := e.Load(f)

	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestEditorLoadVersionMismatch(t *testing.T) {
	e, _ := newTestEditorWithColumn(t)
	f := writeTempFile(t, "peak-session-v99\n/tmp\ntag\n")

	err := e.Load(f)

	if err == nil {
		t.Error("expected error for version mismatch")
	}
}

func TestEditorLoadClearsExistingColumns(t *testing.T) {
	e, _ := newTestEditorWithColumn(t)
	// dump the single-column state
	dest := filepath.Join(t.TempDir(), "session.json")
	e.Dump(dest)
	// add another column to make state diverge
	extra := NewColumn(0, 1, 50, 49, e)
	extra.explicitWidth = 50
	e.columns = append(e.columns, extra)

	e.Load(dest)

	if len(e.columns) != 1 {
		t.Errorf("after load: %d columns, want 1", len(e.columns))
	}
}

func TestEditorLoadFallbackColumn(t *testing.T) {
	e, _ := setupTest(t, 200, 50)
	f := writeTempFile(t, string(encode(Session{Version: sessionVersion, CurrentDir: "/tmp", GlobalTag: " tag "})))

	e.Load(f)

	if len(e.columns) != 1 {
		t.Errorf("expected fallback column, got %d columns", len(e.columns))
	}
}

func TestEditorLoadGlobalTag(t *testing.T) {
	e, _ := newTestEditorWithColumn(t)
	e.tag.buffer.SetText(" NewCol Dump Load Exit ")
	dest := filepath.Join(t.TempDir(), "session.json")
	e.Dump(dest)
	e.tag.buffer.SetText(" other ")

	e.Load(dest)

	if got := e.tag.buffer.GetText(); got != " NewCol Dump Load Exit " {
		t.Errorf("GlobalTag = %q", got)
	}
}

// ── round-trip ────────────────────────────────────────────────────────────────

func TestEditorRoundTripCleanFile(t *testing.T) {
	path := writeTempFile(t, "round trip content\n")
	e, col := newTestEditorWithColumn(t)
	col.tag.buffer.SetText(" Custom Col Tag ")
	win := addFileWindow(t, col, " "+path+" Get Put Del ", "")
	win.bodyTextView().buffer.cursor = Cursor{3, 0}

	dest := filepath.Join(t.TempDir(), "session.json")
	if err := e.Dump(dest); err != nil {
		t.Fatal(err)
	}

	e2, _ := setupTest(t, 200, 50)
	if err := e2.Load(dest); err != nil {
		t.Fatal(err)
	}

	if len(e2.columns) != 1 {
		t.Fatalf("columns = %d, want 1", len(e2.columns))
	}
	if e2.columns[0].tag.buffer.GetText() != " Custom Col Tag " {
		t.Errorf("column tag not restored: %q", e2.columns[0].tag.buffer.GetText())
	}
	if len(e2.columns[0].windows) != 1 {
		t.Fatalf("windows = %d, want 1", len(e2.columns[0].windows))
	}
	w2 := e2.columns[0].windows[0]
	if w2.kind != WinFile {
		t.Errorf("kind = %v, want WinFile", w2.kind)
	}
	if !strings.Contains(w2.bodyTextView().buffer.GetText(), "round trip content") {
		t.Error("file content not restored")
	}
	if w2.IsDirty() {
		t.Error("restored clean file should not be dirty")
	}
}

func TestEditorRoundTripDirtyFile(t *testing.T) {
	e, col := newTestEditorWithColumn(t)
	win := col.AddWindow(" /tmp/unsaved.go Get Put Del ", "")
	win.kind = WinFile
	win.writable = true
	win.bodyTextView().buffer.SetText("my dirty edits\nline2")

	dest := filepath.Join(t.TempDir(), "session.json")
	e.Dump(dest)

	e2, _ := setupTest(t, 200, 50)
	e2.Load(dest)

	w2 := e2.columns[0].windows[0]
	if !w2.IsDirty() {
		t.Error("restored dirty window should be dirty")
	}
	if got := w2.bodyTextView().buffer.GetText(); got != "my dirty edits\nline2" {
		t.Errorf("dirty body = %q", got)
	}
}

func TestEditorRoundTripMultipleColumns(t *testing.T) {
	e, _ := setupTest(t, 200, 50)
	col0 := NewColumn(0, 1, 100, 49, e)
	col0.explicitWidth = 100
	col1 := NewColumn(100, 1, 100, 49, e)
	col1.explicitWidth = 100
	e.columns = append(e.columns, col0, col1)
	e.resize()
	e.syncChildren()

	addFileWindow(t, col0, " /tmp/a.go Get Put Del ", "")
	addFileWindow(t, col1, " /tmp/b.go Get Put Del ", "")

	dest := filepath.Join(t.TempDir(), "session.json")
	e.Dump(dest)

	e2, _ := setupTest(t, 200, 50)
	e2.Load(dest)

	if len(e2.columns) != 2 {
		t.Fatalf("columns = %d, want 2", len(e2.columns))
	}
	if len(e2.columns[0].windows) != 1 || len(e2.columns[1].windows) != 1 {
		t.Errorf("windows per column: %d, %d; want 1, 1",
			len(e2.columns[0].windows), len(e2.columns[1].windows))
	}
}

func TestEditorRoundTripWindowTag(t *testing.T) {
	e, col := newTestEditorWithColumn(t)
	addFileWindow(t, col, " /tmp/f.go Get Put Undo Redo Snarf Del ", "")

	dest := filepath.Join(t.TempDir(), "session.json")
	e.Dump(dest)

	e2, _ := setupTest(t, 200, 50)
	e2.Load(dest)

	got := e2.columns[0].windows[0].tag.buffer.GetText()
	if got != " /tmp/f.go Get Put Undo Redo Snarf Del " {
		t.Errorf("tag = %q", got)
	}
}
