package main

import (
	"os"
	"strings"
	"testing"

	"github.com/aleksana/peak/internal/vfs/afero"
	"github.com/gdamore/tcell/v3"
)


// ---- GetWordBoundaries backtick span detection ----

func TestGetWordBoundariesBacktickSpan(t *testing.T) {
	mk := func(s string) ([]rune, func(int) rune) {
		r := []rune(s)
		return r, func(i int) rune { return r[i] }
	}

	// Every position inside `foo bar` (len=9) returns the full span [0,9).
	t.Run("AllPositionsInSpan", func(t *testing.T) {
		line, get := mk("`foo bar`")
		for x := 0; x < len(line); x++ {
			s, e := GetWordBoundaries(x, len(line), get)
			if s != 0 || e != 9 {
				t.Errorf("x=%d: got [%d,%d), want [0,9)", x, s, e)
			}
		}
	})

	// Opening backtick of the span.
	t.Run("OpeningBacktick", func(t *testing.T) {
		line, get := mk("`foo bar`")
		s, e := GetWordBoundaries(0, len(line), get)
		if s != 0 || e != 9 {
			t.Errorf("got [%d,%d), want [0,9)", s, e)
		}
	})

	// Closing backtick of the span.
	t.Run("ClosingBacktick", func(t *testing.T) {
		line, get := mk("`foo bar`")
		s, e := GetWordBoundaries(8, len(line), get)
		if s != 0 || e != 9 {
			t.Errorf("got [%d,%d), want [0,9)", s, e)
		}
	})

	// Position outside any span falls back to normal word boundaries.
	t.Run("OutsideSpan", func(t *testing.T) {
		line, get := mk("`foo bar` baz")
		// x=10 is on 'b' in "baz"
		s, e := GetWordBoundaries(10, len(line), get)
		word := string(line[s:e])
		if word != "baz" {
			t.Errorf("got %q [%d,%d), want \"baz\"", word, s, e)
		}
	})

	// Double backtick is a literal, not a span delimiter; falls back to normal boundaries.
	t.Run("DoubleBacktickIsLiteral", func(t *testing.T) {
		line, get := mk("foo ``bar")
		// x=4 is on the first backtick of ``
		s, e := GetWordBoundaries(4, len(line), get)
		word := string(line[s:e])
		// Normal scan: space at 3 stops the left scan; all non-space chars to the right are included.
		if !strings.HasPrefix(word, "``") {
			t.Errorf("expected word starting with `` (literal backtick pair), got %q", word)
		}
	})

	// Second of two spans on the same line.
	t.Run("SecondSpanOnSameLine", func(t *testing.T) {
		// "`a b` `c d`": second span starts at x=6
		line, get := mk("`a b` `c d`")
		s, e := GetWordBoundaries(7, len(line), get)
		word := string(line[s:e])
		// Should return `c d` span: [6, 11)
		if word != "`c d`" {
			t.Errorf("got %q, want \"`c d`\"", word)
		}
	})

	// Unclosed span: no closing backtick, no span match; normal boundaries.
	t.Run("UnclosedSpan", func(t *testing.T) {
		line, get := mk("`foo bar")
		s, e := GetWordBoundaries(1, len(line), get)
		word := string(line[s:e])
		// Normal scan from x=1: backtick at 0 is a word char, extends left; no span closes.
		// The exact word depends on IsWordChar; backtick and letters all qualify.
		if word == "" {
			t.Errorf("expected a non-empty word for unclosed span, got empty")
		}
	})
}

// ---- GetFilename with backtick-quoted name in tag ----

func TestGetFilenameQuoted(t *testing.T) {
	e, _ := setupTest(t, 80, 24)
	col := NewColumn(0, 1, e.w, e.h-1, e, e.Execute)
	e.columns = append(e.columns, col)

	tests := []struct {
		tag  string
		want string
	}{
		{" /foo/bar Get Put ", "/foo/bar"},
		{" `/foo/bar baz` Get Put ", "/foo/bar baz"},
		{" `/a/b c/d e.txt` Get Put Del ", "/a/b c/d e.txt"},
		// double-backtick in filename field → literal backtick in name
		{" `` Get Put ", "`"},
	}
	for _, tt := range tests {
		win := col.AddWindow(tt.tag, "")
		if got := win.GetFilename(); got != tt.want {
			t.Errorf("GetFilename tag=%q: got %q, want %q", tt.tag, got, tt.want)
		}
	}
}

// ---- SetName → GetFilename round trip ----

func TestSetNameQuotes(t *testing.T) {
	e, _ := setupTest(t, 80, 24)
	col := NewColumn(0, 1, e.w, e.h-1, e, e.Execute)
	e.columns = append(e.columns, col)
	win := col.AddWindow(" /old Get Put Del ", "")

	// Name without space: stored plain, no backticks.
	win.SetName("/new/path")
	if got := win.GetFilename(); got != "/new/path" {
		t.Errorf("SetName plain: GetFilename=%q, want /new/path", got)
	}
	if strings.Contains(win.tag.buffer.GetText(), "`") {
		t.Errorf("no backticks expected for spaceless name, tag=%q", win.tag.buffer.GetText())
	}

	// Name with space: stored with backticks; GetFilename still returns plain name.
	win.SetName("/my documents/report.txt")
	if got := win.GetFilename(); got != "/my documents/report.txt" {
		t.Errorf("SetName spaced: GetFilename=%q, want /my documents/report.txt", got)
	}
	tag := win.tag.buffer.GetText()
	if !strings.Contains(tag, "`/my documents/report.txt`") {
		t.Errorf("expected backtick-quoted name in tag, got %q", tag)
	}

	// Other tag fields are preserved.
	for _, field := range []string{"Get", "Put", "Del"} {
		if !strings.Contains(tag, field) {
			t.Errorf("field %q missing from tag after SetName, tag=%q", field, tag)
		}
	}

	// Changing back to a plain name removes the backticks from the name field.
	win.SetName("/simple")
	tag = win.tag.buffer.GetText()
	if win.GetFilename() != "/simple" {
		t.Errorf("GetFilename after reset=%q, want /simple", win.GetFilename())
	}

	// SetName must re-quote any other tag fields that contain spaces.
	// Simulate a TermView tag where the user added a spaced command.
	win2 := col.AddWindow(" /dir/-term `ls -al` Zerox Del ", "")
	win2.SetName("/newdir/-term")
	tag2 := win2.tag.buffer.GetText()
	if !strings.Contains(tag2, "`ls -al`") {
		t.Errorf("SetName must preserve backtick-quoted fields; tag=%q", tag2)
	}
	if win2.GetFilename() != "/newdir/-term" {
		t.Errorf("GetFilename after SetName=%q, want /newdir/-term", win2.GetFilename())
	}
}

// ---- listDir wraps spaced entries in backticks ----

func TestListDirQuotesSpacedNames(t *testing.T) {
	e, _ := setupTest(t, 80, 24)
	_ = e

	base := "/peak/mirage/testquotedir"
	getVFS().MkdirAll(base+"/spaced dir", 0755)
	writeFile(base+"/plain.txt", []byte(""))
	writeFile(base+"/with spaces.txt", []byte(""))

	listing, err := listDir(base + "/")
	if err != nil {
		t.Fatal(err)
	}

	lineSet := make(map[string]bool)
	for _, l := range strings.Split(strings.TrimSpace(listing), "\n") {
		lineSet[l] = true
	}

	if !lineSet["plain.txt"] {
		t.Errorf("plain.txt should appear unquoted; lines: %v", lineSet)
	}
	if !lineSet["`with spaces.txt`"] {
		t.Errorf("`with spaces.txt` should appear backtick-quoted; lines: %v", lineSet)
	}
	if !lineSet["`spaced dir/`"] {
		t.Errorf("`spaced dir/` should appear backtick-quoted with trailing slash; lines: %v", lineSet)
	}
	// Unquoted entries must not accidentally appear for spaced names.
	if lineSet["with spaces.txt"] {
		t.Errorf("unquoted 'with spaces.txt' must not appear in listing")
	}
	if lineSet["spaced dir/"] {
		t.Errorf("unquoted 'spaced dir/' must not appear in listing")
	}
}

// ---- GetClickWord backtick span detection ----

func TestGetClickWordBacktickSpan(t *testing.T) {
	// Single-line: "`foo bar` baz" at position (0,0) to (12,0).
	tv := NewTextView("`foo bar` baz", 0, 0, 80, 1, tcell.StyleDefault, true, false)
	tv.UpdateLayout()
	tv.theme = &Theme{}

	t.Run("OpeningBacktick", func(t *testing.T) {
		if got := tv.GetClickWord(0, 0); got != "foo bar" {
			t.Errorf("got %q, want \"foo bar\"", got)
		}
	})
	t.Run("InsideSpan", func(t *testing.T) {
		if got := tv.GetClickWord(4, 0); got != "foo bar" {
			t.Errorf("got %q, want \"foo bar\"", got)
		}
	})
	t.Run("ClosingBacktick", func(t *testing.T) {
		if got := tv.GetClickWord(8, 0); got != "foo bar" {
			t.Errorf("got %q, want \"foo bar\"", got)
		}
	})
	t.Run("OutsideSpan", func(t *testing.T) {
		if got := tv.GetClickWord(10, 0); got != "baz" {
			t.Errorf("got %q, want \"baz\"", got)
		}
	})

	// Multi-line: backtick span on line 1.
	tv2 := NewTextView("first line\n`spaced path` rest", 0, 0, 80, 5, tcell.StyleDefault, false, true)
	tv2.UpdateLayout()
	tv2.theme = &Theme{}

	t.Run("MultiLine_InsideSpan", func(t *testing.T) {
		// y=1 on screen → buffer line 1; x=5 is inside "`spaced path`".
		if got := tv2.GetClickWord(5, 1); got != "spaced path" {
			t.Errorf("got %q, want \"spaced path\"", got)
		}
	})
	t.Run("MultiLine_OutsideSpan", func(t *testing.T) {
		// x=14 is on 'r' in "rest".
		if got := tv2.GetClickWord(14, 1); got != "rest" {
			t.Errorf("got %q, want \"rest\"", got)
		}
	})
	t.Run("MultiLine_Line0Unaffected", func(t *testing.T) {
		// Line 0 has no backtick span; normal word extraction.
		if got := tv2.GetClickWord(0, 0); got != "first" {
			t.Errorf("got %q, want \"first\"", got)
		}
	})
}

// ---- Plumb unwraps backtick-quoted words ----

func TestPlumbUnwrapsBacktickWord(t *testing.T) {
	e, s := setupTest(t, 100, 30)
	col := NewColumn(0, 1, e.w, e.h-1, e, e.Execute)
	e.columns = append(e.columns, col)
	e.resize()
	e.Draw()
	s.Show()

	path := "/peak/mirage/my plumb file.txt"
	getVFS().MkdirAll("/peak/mirage", 0755)
	writeFile(path, []byte("plumb content"))

	// Plumb a backtick-quoted path (as if user selected "`/peak/mirage/my plumb file.txt`").
	e.Plumb(nil, "`"+path+"`")

	waitFor(t, e, s, func() bool {
		for _, c := range e.columns {
			for _, w := range c.windows {
				if w.GetFilename() == path {
					return true
				}
			}
		}
		return false
	})

	// Verify the opened window has the correct name and backtick-quoted tag.
	var win *Window
	for _, c := range e.columns {
		for _, w := range c.windows {
			if w.GetFilename() == path {
				win = w
			}
		}
	}
	if win == nil {
		t.Fatal("window for spaced-name file not found")
	}
	tag := win.tag.buffer.GetText()
	if !strings.Contains(tag, "`"+path+"`") {
		t.Errorf("expected backtick-quoted name in tag, got %q", tag)
	}
}

// ---- Execute parses backtick-quoted arguments ----

func TestExecuteBacktickArg(t *testing.T) {
	e, s := setupTest(t, 100, 30)
	col := NewColumn(0, 1, e.w, e.h-1, e, e.Execute)
	e.columns = append(e.columns, col)
	e.resize()
	e.Draw()
	s.Show()

	path := "/peak/mirage/exec spaced.txt"
	getVFS().MkdirAll("/peak/mirage", 0755)
	writeFile(path, []byte("exec content"))

	// "New `path`" should open the file at path.
	e.Execute(nil, nil, "New `"+path+"`")

	waitFor(t, e, s, func() bool {
		for _, c := range e.columns {
			for _, w := range c.windows {
				if w.GetFilename() == path {
					return true
				}
			}
		}
		return false
	})
}

// ---- End-to-end: right-click a backtick-quoted listing entry opens the file ----

func TestPlumbSpacedFileFromDirListing(t *testing.T) {
	e, s := setupTest(t, 100, 30)
	col := NewColumn(0, 1, e.w, e.h-1, e, e.Execute)
	e.columns = append(e.columns, col)
	e.resize()
	e.Draw()
	s.Show()

	base := "/peak/mirage/spacedlist"
	filePath := base + "/hello world.txt"
	getVFS().MkdirAll(base, 0755)
	writeFile(filePath, []byte("hello from spaced file"))

	// Open the directory.
	e.Open(nil, base+"/")
	var dirWin *Window
	waitFor(t, e, s, func() bool {
		for _, c := range e.columns {
			for _, w := range c.windows {
				if strings.Contains(w.GetFilename(), base) {
					dirWin = w
					return true
				}
			}
		}
		return false
	})

	// The listing should contain the backtick-quoted entry.
	listing := dirWin.body.GetBuffer().GetText()
	if !strings.Contains(listing, "`hello world.txt`") {
		t.Fatalf("expected '`hello world.txt`' in listing, got:\n%s", listing)
	}

	// Find it on screen and right-click it.
	e.Draw()
	s.Show()
	bodyY := dirWin.bodyTextView().y
	// The backtick is part of the displayed text; search for the inner word.
	fx, fy, found := GetWordCoordinate(s, "hello world.txt", 0, bodyY)
	if !found {
		t.Fatal("could not find 'hello world.txt' on screen")
	}
	e.HandleEvent(tcell.NewEventMouse(fx, fy, tcell.ButtonSecondary, 0))

	// The file should open.
	waitFor(t, e, s, func() bool {
		for _, c := range e.columns {
			for _, w := range c.windows {
				if w.GetFilename() == filePath {
					return true
				}
			}
		}
		return false
	})
}

func TestIsBinary(t *testing.T) {
	if err := isBinary(nil); err != nil {
		t.Errorf("nil should not be binary: %v", err)
	}
	if err := isBinary([]byte("hello")); err != nil {
		t.Errorf("text should not be binary: %v", err)
	}
	if err := isBinary([]byte{0, 1, 2}); err == nil {
		t.Error("zero byte should be detected as binary")
	}
}

func openMemFile(t *testing.T, content string) afero.File {
	t.Helper()
	fs := afero.NewMemMapFs()
	f, err := fs.Create("test")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(content)
	f.Close()
	f, err = fs.Open("test")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestReadFileTail_PrefixNil(t *testing.T) {
	f := openMemFile(t, "hello world")
	s, err := readFileTail(f, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if s != "hello world" {
		t.Errorf("got %q, want %q", s, "hello world")
	}
}

func TestReadFileTail_PrefixPartial(t *testing.T) {
	f := openMemFile(t, "hello world")
	s, err := readFileTail(f, []byte("hello"), 5)
	if err != nil {
		t.Fatal(err)
	}
	if s != "hello world" {
		t.Errorf("got %q, want %q", s, "hello world")
	}
}

func TestReadFileTail_PrefixFullButFileLarger(t *testing.T) {
	content := strings.Repeat("x", 5000)
	f := openMemFile(t, content)
	prefix := []byte(content[:5])
	s, err := readFileTail(f, prefix, 5)
	if err != nil {
		t.Fatal(err)
	}
	if s != content {
		t.Errorf("got %d bytes, want %d", len(s), len(content))
	}
}

func TestReadFileTail_OffsetAfterStart(t *testing.T) {
	f := openMemFile(t, "0123456789")
	s, err := readFileTail(f, nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	if s != "56789" {
		t.Errorf("got %q, want %q", s, "56789")
	}
}

func TestReadFileTail_BinaryDetection(t *testing.T) {
	fs := afero.NewMemMapFs()
	f, _ := fs.Create("test")
	f.Write([]byte{0, 1, 2, 3})
	f.Close()
	f, _ = fs.Open("test")

	_, err := readFileTail(f, nil, 0)
	if err == nil {
		t.Fatal("expected binary detection error")
	}
}

func TestReadFileTail_BinaryDetectionAfterPrefix(t *testing.T) {
	// Binary detection already happened in the fast path (on prefix).
	// readFileTail must NOT re-detect on subsequent chunks.
	data := make([]byte, 4096)
	for i := range data {
		data[i] = 'A'
	}
	data[4095] = 0
	fs := afero.NewMemMapFs()
	f, _ := fs.Create("test")
	f.Write(data)
	f.Close()
	f, _ = fs.Open("test")

	prefix := make([]byte, 512)
	for i := range prefix {
		prefix[i] = 'A'
	}
	s, err := readFileTail(f, prefix, 512)
	if err != nil {
		t.Fatalf("should not re-detect binary after prefix: %v", err)
	}
	if len(s) != len(data) {
		t.Errorf("got %d bytes, want %d", len(s), len(data))
	}
}

func TestReadFileTail_EOFDuringRead(t *testing.T) {
	f := openMemFile(t, "short")
	s, err := readFileTail(f, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if s != "" {
		t.Errorf("got %q, want empty", s)
	}
}

func TestReadFileTail_EmptyFile(t *testing.T) {
	f := openMemFile(t, "")
	s, err := readFileTail(f, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if s != "" {
		t.Errorf("got %q, want empty", s)
	}
}

// --- Integration: readFileOrDir with wrong Stat().Size() ---

type wrongSizeFs struct {
	afero.Fs
}

func (fs *wrongSizeFs) Stat(name string) (os.FileInfo, error) {
	fi, err := fs.Fs.Stat(name)
	if err != nil {
		return nil, err
	}
	return &wrongSizeInfo{FileInfo: fi}, nil
}
func (fs *wrongSizeFs) Name() string { return "wrongSizeFs" }

type wrongSizeInfo struct {
	os.FileInfo
}

func (fi *wrongSizeInfo) Size() int64 { return 1 }

func TestReadFileOrDir_IgnoresWrongSize(t *testing.T) {
	e, _ := setupTest(t, 100, 24)

	mountPath := "/peak/mirage/wrongsize"
	content := "Hello World - this file is 62 bytes but Stat claims size=1"

	mem := afero.NewMemMapFs()
	afero.WriteFile(mem, "/test.txt", []byte(content), 0644)
	e.ninep.vfs.Mount(mountPath, &wrongSizeFs{Fs: mem})

	// Call readFileOrDir directly (bypasses the async Get command).
	got, isDir, _, err := readFileOrDir(mountPath + "/test.txt")
	if err != nil {
		t.Fatal(err)
	}
	if isDir {
		t.Fatal("expected file, got directory")
	}
	if got != content {
		t.Errorf("file truncated: got %q (%d chars), want %q (%d chars)",
			got, len(got), content, len(content))
	}
}
