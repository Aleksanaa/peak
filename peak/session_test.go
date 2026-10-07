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
	return e, col
}

func addFileWindow(t *testing.T, col *Column, tag, body string) *Window {
	t.Helper()
	win := col.AddWindow(tag, body)
	win.kind = WinFile
	win.writable = true
	win.savedVersion = win.bodyTextView().buffer.version
	col.Resize(col.rect)
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
	tv.setTop(1)
	tv.buffer.moveTo(tv.buffer.Offset(2, 3))
	tv.tabWidth = 8
	win.explicitHeight = 20

	ws := win.saveState(col.h)

	if ws.Kind != "file" {
		t.Errorf("Kind = %q, want file", ws.Kind)
	}
	if ws.Dirty || ws.Body != "" {
		t.Error("clean file should not set Dirty or Body")
	}
	if ws.Org != 6 || ws.Q0 != 15 || ws.Q1 != 15 {
		t.Errorf("org, dot = %d, [%d, %d), want 6, [15, 15)", ws.Org, ws.Q0, ws.Q1)
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
	win := col.AddWindow(" /home/user/ Get Del ", strings.Repeat("entry/\n", 10))
	win.kind = WinDir
	tv := win.bodyTextView()
	tv.setTop(5)
	tv.buffer.moveTo(tv.buffer.Offset(3, 0))

	ws := win.saveState(col.h)

	if ws.Kind != "dir" {
		t.Errorf("Kind = %q, want dir", ws.Kind)
	}
	if ws.Org != 35 || ws.Q0 != 21 || ws.Q1 != 21 {
		t.Errorf("org, dot = %d, [%d, %d), want 35, [21, 21)", ws.Org, ws.Q0, ws.Q1)
	}
	if ws.Dirty || ws.Body != "" {
		t.Error("dir should not set Dirty or Body")
	}
}

func TestWindowSaveStateTerm(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	win := addTerm(t, col, " /tmp/-sh Del ", "sh")

	ws := win.saveState(col.h)

	if ws.Kind != "term" || ws.TermCmd != "sh" {
		t.Errorf("Kind, TermCmd = %q, %q, want term, sh", ws.Kind, ws.TermCmd)
	}
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

// ── Window.restore ───────────────────────────────────────────────────────

func TestWindowRestoreDirty(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	win := col.AddWindow(" /tmp/new.go Get Put Del ", "")

	ws := &WindowSession{
		Tag:   " /tmp/new.go Get Put Del ",
		Kind:  "file",
		Dirty: true,
		Body:  "dirty content\nline2",
	}
	win.restore(ws)

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

func TestWindowRestoreDirtyTabWidth(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	win := col.AddWindow(" /tmp/f.go Get Put Del ", "")
	ws := &WindowSession{
		Tag: " /tmp/f.go Get Put Del ", Kind: "file",
		Dirty: true, Body: "x", TabWidth: 8,
	}
	win.restore(ws)
	if win.bodyTextView().tabWidth != 8 {
		t.Errorf("tabWidth = %d, want 8", win.bodyTextView().tabWidth)
	}
}

func TestWindowRestoreCleanFile(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	path := writeTempFile(t, "hello from disk\n")
	win := col.AddWindow(" "+path+" Get Put Del ", "")

	ws := &WindowSession{Tag: " " + path + " Get Put Del ", Kind: "file"}
	win.restore(ws)

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

func TestWindowRestoreCleanDir(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	dir := t.TempDir()
	win := col.AddWindow(" "+dir+"/ Get Del ", "")

	ws := &WindowSession{Tag: " " + dir + "/ Get Del ", Kind: "dir"}
	win.restore(ws)

	if win.kind != WinDir {
		t.Errorf("kind = %v, want WinDir", win.kind)
	}
}

func TestWindowRestoreFileNotFound(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	win := col.AddWindow(" /nonexistent/path/file.go Get Put Del ", "")

	ws := &WindowSession{Tag: " /nonexistent/path/file.go Get Put Del ", Kind: "file"}
	win.restore(ws) // must not panic

	// window stays in default state: empty body, not modified
	if win.IsDirty() {
		t.Error("missing file should leave window clean")
	}
}

func TestWindowRestoreTabWidthZeroNoOverride(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	win := col.AddWindow(" /tmp/f.go Get Put Del ", "")
	win.bodyTextView().tabWidth = 2
	ws := &WindowSession{
		Tag: " /tmp/f.go Get Put Del ", Kind: "file",
		Dirty: true, Body: "x", TabWidth: 0,
	}
	win.restore(ws)
	if win.bodyTextView().tabWidth != 2 {
		t.Errorf("TabWidth=0 should not override existing tabWidth, got %d", win.bodyTextView().tabWidth)
	}
}

// restore puts the view and dot back as rune offsets, which hold at any
// width; where the text has since shrunk, at its end.
func TestWindowRestorePlacesViewAndDot(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	body := strings.Repeat("line of text\n", 30)
	for _, tt := range []struct{ org, q0, q1, wantOrg, wantQ0, wantQ1 int }{
		{65, 133, 140, 65, 133, 140},
		{9999, 9999, 9999, len(body), len(body), len(body)},
	} {
		win := col.AddWindow(" /tmp/f.go Get Put Del ", "")
		win.restore(&WindowSession{Kind: "file", Dirty: true, Body: body, Org: tt.org, Q0: tt.q0, Q1: tt.q1})
		tv := win.bodyTextView()
		if tv.org != tt.wantOrg || tv.buffer.q0 != tt.wantQ0 || tv.buffer.q1 != tt.wantQ1 {
			t.Errorf("restored org, dot = %d, [%d, %d), want %d, [%d, %d)",
				tv.org, tv.buffer.q0, tv.buffer.q1, tt.wantOrg, tt.wantQ0, tt.wantQ1)
		}
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
	win.bodyTextView().buffer.SetDot(6, 10)

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
	if b := w2.bodyTextView().buffer; b.q0 != 6 || b.q1 != 10 {
		t.Errorf("dot = [%d, %d), want [6, 10)", b.q0, b.q1)
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
