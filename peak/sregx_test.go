package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

const editContents = "This is a\nshort text\nto try addressing\n"

// editCtx returns a Context wired to a fresh Buffer. ctx.Editor/Column/Window
// are nil — sufficient for all pure text commands.
func editCtx(text string) (*Context, *Buffer, *bytes.Buffer) {
	buf := NewBuffer(text)
	var out bytes.Buffer
	ctx := &Context{Buffer: buf, Out: &out, Log: &Elog{}}
	return ctx, buf, &out
}

// runEdit compiles and executes one Edit command string, applies the elog to
// the buffer, and returns (body text, output written to ctx.Out).
func runEdit(t *testing.T, ctx *Context, dot Range, cmd string) (string, string) {
	t.Helper()
	out := ctx.Out.(*bytes.Buffer)
	out.Reset()
	ctx.Log = &Elog{}

	res, err := SregxCompile(cmd, out)
	if err != nil {
		t.Fatalf("SregxCompile(%q): %v", cmd, err)
	}
	res.Cmd.Execute(ctx, dot)
	ctx.Log.Apply(ctx.Buffer)
	return ctx.Buffer.GetText(), out.String()
}

// allOf returns Range{0, len(runes(s))}.
func allOf(s string) Range { return Range{0, len([]rune(s))} }

// ---- a (append) ----

func TestEditAppend(t *testing.T) {
	tests := []struct {
		dot  Range
		cmd  string
		want string
	}{
		// append at empty dot inserts before the current position
		{Range{0, 0}, "a/TAIL", "TAILThis is a\nshort text\nto try addressing\n"},
		// append after a non-empty dot inserts at q1
		{Range{9, 9}, "a/TAIL", "This is aTAIL\nshort text\nto try addressing\n"},
		// append after whole buffer
		{allOf(editContents), "a/TAIL", editContents + "TAIL"},
		// address /is/ matches the "is" inside "This" (pos 2–4); append after it
		{Range{0, 0}, "/is/a/TAIL", "ThisTAIL is a\nshort text\nto try addressing\n"},
	}
	for _, tc := range tests {
		ctx, _, _ := editCtx(editContents)
		got, _ := runEdit(t, ctx, tc.dot, tc.cmd)
		if got != tc.want {
			t.Errorf("a dot=%v cmd=%q\n got  %q\n want %q", tc.dot, tc.cmd, got, tc.want)
		}
	}
}

// ---- i (insert) ----

func TestEditInsert(t *testing.T) {
	tests := []struct {
		dot  Range
		cmd  string
		want string
	}{
		{Range{0, 0}, "i/HEAD", "HEADThis is a\nshort text\nto try addressing\n"},
		{Range{2, 6}, "i/HEAD", "ThHEADis is a\nshort text\nto try addressing\n"},
		{Range{0, 0}, "/text/i/HEAD", "This is a\nshort HEADtext\nto try addressing\n"},
	}
	for _, tc := range tests {
		ctx, _, _ := editCtx(editContents)
		got, _ := runEdit(t, ctx, tc.dot, tc.cmd)
		if got != tc.want {
			t.Errorf("i dot=%v cmd=%q\n got  %q\n want %q", tc.dot, tc.cmd, got, tc.want)
		}
	}
}

// ---- c (change) ----

func TestEditChange(t *testing.T) {
	tests := []struct {
		dot  Range
		cmd  string
		want string
	}{
		{Range{0, 0}, "c/NEW", "NEWThis is a\nshort text\nto try addressing\n"},
		{Range{2, 6}, "c/NEW", "ThNEWs a\nshort text\nto try addressing\n"},
		{Range{0, 0}, "/text/c/NEW", "This is a\nshort NEW\nto try addressing\n"},
	}
	for _, tc := range tests {
		ctx, _, _ := editCtx(editContents)
		got, _ := runEdit(t, ctx, tc.dot, tc.cmd)
		if got != tc.want {
			t.Errorf("c dot=%v cmd=%q\n got  %q\n want %q", tc.dot, tc.cmd, got, tc.want)
		}
	}
}

// ---- d (delete) ----

func TestEditDelete(t *testing.T) {
	tests := []struct {
		dot  Range
		cmd  string
		want string
	}{
		{Range{0, 0}, "d", editContents}, // empty dot: no-op
		{Range{2, 6}, "d", "Ths a\nshort text\nto try addressing\n"},
		{Range{0, 0}, "/text/d", "This is a\nshort \nto try addressing\n"},
	}
	for _, tc := range tests {
		ctx, _, _ := editCtx(editContents)
		got, _ := runEdit(t, ctx, tc.dot, tc.cmd)
		if got != tc.want {
			t.Errorf("d dot=%v cmd=%q\n got  %q\n want %q", tc.dot, tc.cmd, got, tc.want)
		}
	}
}

// ---- s (substitute) ----

func TestEditSubstitute(t *testing.T) {
	tests := []struct {
		dot  Range
		cmd  string
		want string
	}{
		// basic replacement
		{allOf(editContents), "s/short/long/", "This is a\nlong text\nto try addressing\n"},
		// g flag replaces all occurrences; 's' appears in "This", "is", "short", "addressing"
		{allOf(editContents), "s/s/S/g", "ThiS iS a\nShort text\nto try addreSSing\n"},
		// nth occurrence: 2nd 's' is the one in "is" (pos 6)
		{allOf(editContents), "s2/s/S/", "This iS a\nshort text\nto try addressing\n"},
		// backreference \1
		{allOf(editContents), `s/(i.)/!\1!/g`, "Th!is! !is! a\nshort text\nto try address!in!g\n"},
		// & inserts whole match; first [a-z]+ match is "his" inside "This"
		{allOf(editContents), "s/[a-z]+/[&]/", "T[his] is a\nshort text\nto try addressing\n"},
	}
	for _, tc := range tests {
		ctx, _, _ := editCtx(editContents)
		got, _ := runEdit(t, ctx, tc.dot, tc.cmd)
		if got != tc.want {
			t.Errorf("s cmd=%q\n got  %q\n want %q", tc.cmd, got, tc.want)
		}
	}
}

// ---- g/v (guard) ----

func TestEditGuard(t *testing.T) {
	tests := []struct {
		dot  Range
		cmd  string
		want string
	}{
		{Range{0, 12}, "g/This/d", "ort text\nto try addressing\n"},
		{Range{0, 0}, "g/This/d", editContents}, // empty dot has no "This"
		{Range{0, 3}, "v/This/d", "s is a\nshort text\nto try addressing\n"},
		{Range{0, 12}, "v/This/d", editContents}, // dot contains "This", v skips
	}
	for _, tc := range tests {
		ctx, _, _ := editCtx(editContents)
		got, _ := runEdit(t, ctx, tc.dot, tc.cmd)
		if got != tc.want {
			t.Errorf("g/v dot=%v cmd=%q\n got  %q\n want %q", tc.dot, tc.cmd, got, tc.want)
		}
	}
}

// ---- x (loop over matches) ----

func TestEditLoop(t *testing.T) {
	tests := []struct {
		dot  Range
		cmd  string
		want string
	}{
		// [a-z]+ matches lowercase words; appends @ after each
		{allOf(editContents), `x/[a-z]+/ a/@/`,
			"This@ is@ a@\nshort@ text@\nto@ try@ addressing@\n"},
		// $ matches end-of-line (and end-of-file); appends @ at each
		{allOf(editContents), `x/$/a/@/`,
			"This is a@\nshort text@\nto try addressing@\n@"},
		// [^\n]+ matches non-empty line content (without the \n)
		{allOf(editContents), `x/[^\n]+/ a/@/`,
			"This is a@\nshort text@\nto try addressing@\n"},
	}
	for _, tc := range tests {
		ctx, _, _ := editCtx(editContents)
		got, _ := runEdit(t, ctx, tc.dot, tc.cmd)
		if got != tc.want {
			t.Errorf("x cmd=%q\n got  %q\n want %q", tc.cmd, got, tc.want)
		}
	}
}

// ---- x without pattern (linelooper) ----

func TestEditXNoPattern(t *testing.T) {
	tests := []struct {
		cmd  string
		want string
	}{
		// x without pattern iterates over each line's content (without \n)
		{",x a/@/", "This is a@\nshort text@\nto try addressing@\n"},
		// insert before each line
		{",x i/>/", ">This is a\n>short text\n>to try addressing\n"},
		// change each line to its content wrapped in brackets
		{",x c/LINE/", "LINE\nLINE\nLINE\n"},
	}
	for _, tc := range tests {
		ctx, _, _ := editCtx(editContents)
		got, _ := runEdit(t, ctx, allOf(editContents), tc.cmd)
		if got != tc.want {
			t.Errorf("x no-pattern cmd=%q\n got  %q\n want %q", tc.cmd, got, tc.want)
		}
	}
}

// ---- y (loop over non-matches) ----

func TestEditInverseLoop(t *testing.T) {
	// Input: "Hello World\n"
	// [a-z]+ matches "ello" {1,5} and "orld" {7,11}.
	// y gives the gaps: "H" {0,1}, " W" {5,7}, "\n" {11,12}.
	// Replacing each gap with "X" gives "XelloXorldX".
	ctx, _, _ := editCtx("Hello World\n")
	got, _ := runEdit(t, ctx, allOf("Hello World\n"), `y/[a-z]+/ c/X/`)
	want := "XelloXorldX"
	if got != want {
		t.Errorf("y: got %q, want %q", got, want)
	}
}

// ---- p (print) ----

func TestEditPrint(t *testing.T) {
	tests := []struct {
		dot  Range
		want string
	}{
		// p prints exactly the selected text with no added newline
		{Range{0, 4}, "This"},
		{Range{0, 10}, "This is a\n"}, // selection already ends with \n
		{Range{10, 20}, "short text"},
	}
	for _, tc := range tests {
		ctx, _, _ := editCtx(editContents)
		_, out := runEdit(t, ctx, tc.dot, "p")
		if out != tc.want {
			t.Errorf("p dot=%v: got %q, want %q", tc.dot, out, tc.want)
		}
	}
}

// ---- m (move) / t (copy) ----

func TestEditMoveCopy(t *testing.T) {
	tests := []struct {
		dot  Range
		cmd  string
		want string
	}{
		{Range{0, 4}, "m/try", " is a\nshort text\nto tryThis addressing\n"},
		{Range{0, 3}, "t/try", "This is a\nshort text\nto tryThi addressing\n"},
		{Range{1, 3}, "m0", "hiTs is a\nshort text\nto try addressing\n"},
	}
	for _, tc := range tests {
		ctx, _, _ := editCtx(editContents)
		got, _ := runEdit(t, ctx, tc.dot, tc.cmd)
		if got != tc.want {
			t.Errorf("m/t dot=%v cmd=%q\n got  %q\n want %q", tc.dot, tc.cmd, got, tc.want)
		}
	}
}

// ---- = (print address) ----

// editContents line layout:
//   line 1: "This is a\n"   chars 0–9   (10 chars, \n at 9)
//   line 2: "short text\n"  chars 10–20 (11 chars, \n at 20)
//   line 3: "to try addressing\n" chars 21–38 (18 chars, \n at 38)

func TestEditEqualsLine(t *testing.T) {
	// bare = outputs line numbers (default, same as acme/sam)
	tests := []struct {
		dot  Range
		want string
	}{
		{Range{0, 3}, "1"},   // within line 1
		{Range{10, 19}, "2"}, // within line 2
		// Range{0,10} ends at the \n of line 1; trailing \n causes l2 to be decremented
		{Range{0, 10}, "1"},
		// Range{0,11} ends one char into line 2 → spans two lines
		{Range{0, 11}, "1,2"},
		// whole buffer (ends with \n → l2 decremented from 4 to 3)
		{allOf(editContents), "1,3"},
	}
	for _, tc := range tests {
		ctx, _, _ := editCtx(editContents)
		_, out := runEdit(t, ctx, tc.dot, "=")
		got := strings.TrimSpace(out)
		if got != tc.want {
			t.Errorf("= dot=%v: got %q, want %q", tc.dot, got, tc.want)
		}
	}
}

func TestEditEqualsChar(t *testing.T) {
	// =# outputs char offsets
	tests := []struct {
		dot  Range
		want string
	}{
		{Range{1, 3}, "#1,#3"},
		{Range{5, 5}, "#5"}, // q0==q1: no comma
		{Range{0, 4}, "#0,#4"},
	}
	for _, tc := range tests {
		ctx, _, _ := editCtx(editContents)
		_, out := runEdit(t, ctx, tc.dot, "=#")
		got := strings.TrimSpace(out)
		if got != tc.want {
			t.Errorf("=# dot=%v: got %q, want %q", tc.dot, got, tc.want)
		}
	}
}

func TestEditEqualsLineChar(t *testing.T) {
	// =+ outputs line+char format
	tests := []struct {
		dot  Range
		want string
	}{
		// q0=1 → line 1, col 1; q1=3 on same line → omit end
		{Range{1, 3}, "1+#1"},
		// q0=0 → line 1 col 0; q1=10 → line 2 col 0 (right after the \n)
		{Range{0, 10}, "1+#0,2+#0"},
		// q0=10 → line 2 col 0; q1=13 → line 2 col 3 (same line → omit end)
		{Range{10, 13}, "2+#0"},
	}
	for _, tc := range tests {
		ctx, _, _ := editCtx(editContents)
		_, out := runEdit(t, ctx, tc.dot, "=+")
		got := strings.TrimSpace(out)
		if got != tc.want {
			t.Errorf("=+ dot=%v: got %q, want %q", tc.dot, got, tc.want)
		}
	}
}

func TestEditEqualsFilename(t *testing.T) {
	// = prefixes the window's filename when one is set.
	_, _, win, _ := setupWindowTest(t)
	win.editor.Call(func() {
		win.SetName("myfile.go")
		win.body.GetBuffer().SetText(editContents)
	})
	buf := win.body.GetBuffer()
	var out bytes.Buffer
	ctx := &Context{
		Editor: win.editor, Column: win.parent, Window: win,
		Buffer: buf, Out: &out, Log: &Elog{},
	}
	for _, tc := range []struct {
		cmd  string
		want string
	}{
		{"=", "myfile.go:1\n"},
		{"=#", "myfile.go:#0\n"},
		{"=+", "myfile.go:1+#0\n"},
	} {
		var finalOut string
		win.editor.Call(func() {
			out.Reset()
			ctx.Log = &Elog{}
			res, err := SregxCompile(tc.cmd, &out)
			if err != nil {
				t.Fatalf("SregxCompile(%q): %v", tc.cmd, err)
			}
			res.Cmd.Execute(ctx, Range{0, 0})
			finalOut = out.String()
		})
		if finalOut != tc.want {
			t.Errorf("= with filename cmd=%q: got %q, want %q", tc.cmd, finalOut, tc.want)
		}
	}
}

// ---- u (undo) / u-N (redo) ----

func TestEditUndo(t *testing.T) {
	ctx, buf, _ := editCtx("original\n")

	buf.ReplaceRangeRunes(0, 8, []rune("first"))
	buf.ReplaceRangeRunes(0, 5, []rune("second"))

	if got := buf.GetText(); got != "second\n" {
		t.Fatalf("pre-undo buf = %q", got)
	}

	runEdit(t, ctx, Range{0, 0}, "u")
	if got := buf.GetText(); got != "first\n" {
		t.Errorf("after u1: got %q, want %q", got, "first\n")
	}

	runEdit(t, ctx, Range{0, 0}, "u")
	if got := buf.GetText(); got != "original\n" {
		t.Errorf("after u2: got %q, want %q", got, "original\n")
	}
}

func TestEditRedo(t *testing.T) {
	ctx, buf, _ := editCtx("original\n")
	buf.ReplaceRangeRunes(0, 8, []rune("changed"))
	buf.Undo()

	if got := buf.GetText(); got != "original\n" {
		t.Fatalf("after undo: got %q", got)
	}

	runEdit(t, ctx, Range{0, 0}, "u-1")
	if got := buf.GetText(); got != "changed\n" {
		t.Errorf("after u-1: got %q, want %q", got, "changed\n")
	}
}

func TestEditUndoMultiple(t *testing.T) {
	ctx, buf, _ := editCtx("a\n")
	buf.ReplaceRangeRunes(0, 1, []rune("b"))
	buf.ReplaceRangeRunes(0, 1, []rune("c"))
	buf.ReplaceRangeRunes(0, 1, []rune("d"))

	runEdit(t, ctx, Range{0, 0}, "u3")
	if got := buf.GetText(); got != "a\n" {
		t.Errorf("after u3: got %q, want %q", got, "a\n")
	}
}

// ---- { } (group) ----

func TestEditGroup(t *testing.T) {
	ctx, _, _ := editCtx(editContents)
	// Loop over each line's content (excluding \n), prepend @ and append %.
	// [^\\n]+ in Go source = literal [^\n]+ regexp (backslash-n), matching non-newlines.
	// The \n chars in the group body are real newlines separating sub-commands.
	got, _ := runEdit(t, ctx, allOf(editContents), ",x/[^\\n]+/ {\n i/@/ \n a/%/\n }")
	want := "@This is a%\n@short text%\n@to try addressing%\n"
	if got != want {
		t.Errorf("group:\n got  %q\n want %q", got, want)
	}
}

// ---- pipe commands (need a real Window for filename/winid) ----

func TestEditPipeFilter(t *testing.T) {
	// |tr a-z A-Z: uppercases the selected range
	_, _, win, _ := setupWindowTest(t)
	win.editor.Call(func() {
		win.body.GetBuffer().SetText("hello world\n")
	})
	buf := win.body.GetBuffer()
	var out bytes.Buffer
	log := &Elog{}
	ctx := &Context{
		Editor: win.editor, Column: win.parent, Window: win,
		Buffer: buf, Out: &out, Log: log,
	}
	dot := allOf("hello world\n")
	res, err := SregxCompile("|tr a-z A-Z", &out)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	win.editor.Call(func() {
		res.Cmd.Execute(ctx, dot)
		log.Apply(buf)
	})
	var got string
	win.editor.Call(func() { got = buf.GetText() })
	if got != "HELLO WORLD\n" {
		t.Errorf("|tr: got %q, want %q", got, "HELLO WORLD\n")
	}
}

func TestEditPipeOutput(t *testing.T) {
	// >cat: writes selection to cat stdin; body must remain unchanged
	_, _, win, _ := setupWindowTest(t)
	win.editor.Call(func() { win.body.GetBuffer().SetText("hello\n") })
	buf := win.body.GetBuffer()
	var out bytes.Buffer
	log := &Elog{}
	ctx := &Context{
		Editor: win.editor, Column: win.parent, Window: win,
		Buffer: buf, Out: &out, Log: log,
	}
	res, err := SregxCompile(">cat", &out)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	win.editor.Call(func() {
		res.Cmd.Execute(ctx, allOf("hello\n"))
		log.Apply(buf)
	})
	var got string
	win.editor.Call(func() { got = buf.GetText() })
	if got != "hello\n" {
		t.Errorf(">cat: body changed to %q, want unchanged", got)
	}
}

func TestEditPipeInput(t *testing.T) {
	// <echo -n replacement: replaces selection with command stdout
	_, _, win, _ := setupWindowTest(t)
	win.editor.Call(func() { win.body.GetBuffer().SetText("hello\n") })
	buf := win.body.GetBuffer()
	var out bytes.Buffer
	log := &Elog{}
	ctx := &Context{
		Editor: win.editor, Column: win.parent, Window: win,
		Buffer: buf, Out: &out, Log: log,
	}
	res, err := SregxCompile("<echo -n replacement", &out)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	win.editor.Call(func() {
		res.Cmd.Execute(ctx, Range{0, 5}) // "hello"
		log.Apply(buf)
	})
	var got string
	win.editor.Call(func() { got = buf.GetText() })
	if got != "replacement\n" {
		t.Errorf("<echo: got %q, want %q", got, "replacement\n")
	}
}

// ---- m move-to-self is a no-op ----

func TestEditMoveSelf(t *testing.T) {
	// m. moves the selection to after dot itself — overlap → no-op (edwood semantics).
	ctx, _, _ := editCtx(editContents)
	got, _ := runEdit(t, ctx, Range{4, 8}, "m.")
	if got != editContents {
		t.Errorf("m.: got %q, want unchanged %q", got, editContents)
	}
}

// ---- ; compound address ----

func TestEditSemicolonAddress(t *testing.T) {
	// Input: "abc\nabc\n"
	// 1,/abc/d  — a2=/abc/ evaluated from original origin (q1=0) → finds {0,3} → deletes only first "abc"
	// 1;/abc/d  — a2=/abc/ evaluated from end of a1 (q1=3) → finds {4,7} → deletes "abc\nabc"
	{
		ctx, _, _ := editCtx("abc\nabc\n")
		got, _ := runEdit(t, ctx, Range{0, 0}, "1,/abc/d")
		want := "\nabc\n"
		if got != want {
			t.Errorf("1,/abc/d: got %q, want %q", got, want)
		}
	}
	{
		ctx, _, _ := editCtx("abc\nabc\n")
		got, _ := runEdit(t, ctx, Range{0, 0}, "1;/abc/d")
		want := "\n"
		if got != want {
			t.Errorf("1;/abc/d: got %q, want %q", got, want)
		}
	}
}

// ---- lastpat reuse ----

func TestEditPatternReuse(t *testing.T) {
	// /text/ sets lastpat; the empty regexp in s// reuses it.
	ctx, _, _ := editCtx(editContents)
	got, _ := runEdit(t, ctx, Range{0, 0}, "/text/ s//NEW/")
	want := "This is a\nshort NEW\nto try addressing\n"
	if got != want {
		t.Errorf("pattern reuse: got %q, want %q", got, want)
	}
}

// ---- \n in substitution replacement ----

func TestEditSubstNewline(t *testing.T) {
	// \n in getrhs is converted to a real newline in the replacement.
	ctx, _, _ := editCtx("hello world\n")
	// s/hello/bye\ncruel/ — the \\n in Go source is backslash-n fed to getrhs,
	// which converts it to a literal newline character.
	got, _ := runEdit(t, ctx, allOf("hello world\n"), `s/hello/bye\ncruel/`)
	want := "bye\ncruel world\n"
	if got != want {
		t.Errorf("s with \\n: got %q, want %q", got, want)
	}
}

// ---- multiline text form ----

func TestEditMultilineText(t *testing.T) {
	// Multiline text terminated by "." on its own line.
	// SregxCompile receives real newlines in the command string.
	ctx, _, _ := editCtx("abc\n")
	got, _ := runEdit(t, ctx, allOf("abc\n"), "a\nline1\nline2\n.\n")
	want := "abc\nline1\nline2\n"
	if got != want {
		t.Errorf("multiline a: got %q, want %q", got, want)
	}

	// Delimiter form with \n inside the text.
	ctx, _, _ = editCtx("abc\n")
	got, _ = runEdit(t, ctx, allOf("abc\n"), "a/line1\\nline2/")
	want = "abc\nline1\nline2"
	if got != want {
		t.Errorf("a with \\n: got %q, want %q", got, want)
	}
}

// ---- r (read file into selection) ----

func TestEditReadFile(t *testing.T) {
	f, err := os.CreateTemp("", "peak-edit-r-*")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("GREET")
	f.Close()
	defer os.Remove(f.Name())

	// r replaces the selection with the file's contents.
	ctx, _, _ := editCtx("hello world\n")
	got, _ := runEdit(t, ctx, Range{0, 5}, "r "+f.Name())
	want := "GREET world\n"
	if got != want {
		t.Errorf("r: got %q, want %q", got, want)
	}
}

// ---- parser correctness ----

func TestParsecmd(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		// valid commands
		{"\n", false},
		{"a/junk/\n", false},
		{"d\n", false},
		{"s/abc/def/\n", false},
		{"s/abc/def/g\n", false},
		{"s2/abc/def/\n", false},
		{"x/abc/\n", false},
		{"x/abc/j\n", true}, // 'j' is unknown sub-command
		{"x a/@/\n", false}, // x without pattern (linelooper)
		{"y a/>/\n", false}, // y without pattern (linelooper)
		{"Y a/>/\n", true},  // Y requires a pattern (sam spec)
		{"g/abc/d\n", false},
		{"v/abc/d\n", false},
		{"{\nd\n}\n", false},
		{"{}\n", false},
		{"}\n", true}, // right brace with no left brace
		{"u\n", false},
		{"u5\n", false},
		{"u-3\n", false},
		// bad address syntax
		{"3.,17d\n", true},
		// command that takes no address given one
		{"5u\n", true},
		// unknown command
		{"j\n", true},
		// valid multiline address
		{",s/a/b/\n", false},
		{"1,3d\n", false},
		// empty braces
		{"{\n}\n", false},
		// multiline text forms
		{"a\nabc\n.\n", false},
		{"a\nabc", false},      // no dot terminator at EOF is also ok
		{"a/a\\\nc/\n", false}, // line continuation in delimited text
		// t requires a target address
		{"t\n", true},
		// bad address syntax for m/t
		{"t 42.\n", true},
		// pattern reuse: /abc/ sets lastpat, then s// reuses it
		{"/abc/ s//def/\n", false},
		// no prior pattern when lastpat is empty
		{"s//xyz/\n", true},
	}
	for _, tc := range tests {
		lastpat = ""
		cp := &cmdParser{buf: []rune(tc.input), pos: 0}
		_, err := cp.parse(0)
		if tc.wantErr && err == nil {
			t.Errorf("parse(%q): expected error, got nil", tc.input)
		} else if !tc.wantErr && err != nil {
			t.Errorf("parse(%q): unexpected error: %v", tc.input, err)
		}
	}
}

// ---- w (write) ----

func TestEditWrite(t *testing.T) {
	f, err := os.CreateTemp("", "peak-edit-w-*")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	ctx, _, _ := editCtx(editContents)
	runEdit(t, ctx, allOf(editContents), "w "+f.Name())

	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != editContents {
		t.Errorf("w wrote %q, want %q", string(data), editContents)
	}
}
