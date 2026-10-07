// Tests for Edit. The tables are ported from Edwood's edit_test.go
// (https://github.com/rjkroege/edwood), with what acme does where Edwood
// differs.
//
//	Portions Copyright © 2021 Plan 9 Foundation
//	Portions Copyright © 2001-2008 Russ Cox
//	Portions Copyright © 2008-2009 Google Inc.
//	Portions Copyright © 2017-2021 Google Inc.
//
// The Plan 9 Foundation's code is under the MIT license and Edwood's under
// the BSD 3-Clause license; sregx.LICENSE has both.

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const (
	contents    = "This is a\nshort text\nto try addressing\n"
	altContents = "A different text\nWith other contents\nSo there!\n"
	testName    = "/tmp/test"
	altName     = "/tmp/alt_example_2"
)

// editSetup returns an editor with two windows holding files as read: one
// named name holding contents, with dot at dot, and one named altName
// holding altContents.
func editSetup(t *testing.T, name string, dot Range) (*Editor, *Column) {
	t.Helper()
	e, _ := setupTest(t, 120, 40)
	col := NewColumn(0, 1, e.w, e.h-1, e)
	e.Call(func() {
		e.columns = append(e.columns, col)
		w := col.AddWindow(tagText(name, "Del"), contents)
		w.loaded(false, true)
		w.body.GetBuffer().SetDot(dot.q0, dot.q1)
		col.AddWindow(tagText(altName, "Del"), altContents).loaded(false, true)
		e.resize()
	})
	return e, col
}

// runEdit runs cmd in win as the Edit command does, and returns what it
// printed.
func runEdit(e *Editor, win *Window, cmd string) (out string) {
	e.Call(func() {
		e.Edit(win.parent, win, cmd)
		for _, w := range e.allWindows() {
			if w.kind == WinOut {
				out += w.body.GetBuffer().GetText()
				e.RemoveWindow(w)
			}
		}
	})
	return out
}

func bodies(e *Editor) (texts []string) {
	e.Call(func() {
		for _, w := range e.allWindows() {
			texts = append(texts, w.body.GetBuffer().GetText())
		}
	})
	return texts
}

func TestEdit(t *testing.T) {
	n := len(contents)
	tests := []struct {
		dot  Range
		expr string
		want string
		out  string
	}{
		// a
		{Range{0, 0}, "a/junk", "junkThis is a\nshort text\nto try addressing\n", ""},
		{Range{7, 12}, "a/junk", "This is a\nshjunkort text\nto try addressing\n", ""},
		{Range{0, 0}, "/This/a/junk", "Thisjunk is a\nshort text\nto try addressing\n", ""},
		{Range{0, 0}, "/^/a/junk", "This is a\njunkshort text\nto try addressing\n", ""},
		{Range{0, 0}, "/$/a/junk", "This is ajunk\nshort text\nto try addressing\n", ""},

		// i
		{Range{0, 0}, "i/junk", "junkThis is a\nshort text\nto try addressing\n", ""},
		{Range{2, 6}, "i/junk", "Thjunkis is a\nshort text\nto try addressing\n", ""},
		{Range{0, 0}, "/text/i/junk", "This is a\nshort junktext\nto try addressing\n", ""},

		// c
		{Range{0, 0}, "c/junk", "junkThis is a\nshort text\nto try addressing\n", ""},
		{Range{2, 6}, "c/junk", "Thjunks a\nshort text\nto try addressing\n", ""},
		{Range{0, 0}, "/text/c/junk", "This is a\nshort junk\nto try addressing\n", ""},

		// d
		{Range{0, 0}, "d", contents, ""},
		{Range{2, 6}, "d", "Ths a\nshort text\nto try addressing\n", ""},
		{Range{0, 0}, "/text/d", "This is a\nshort \nto try addressing\n", ""},

		// g/v
		{Range{0, 0}, "g/This/d", contents, ""},
		{Range{0, 12}, "g/This/d", "ort text\nto try addressing\n", ""},
		{Range{0, 3}, "v/This/d", "s is a\nshort text\nto try addressing\n", ""},
		{Range{0, 12}, "v/This/d", contents, ""},

		// m/t
		{Range{0, 4}, "m/try", " is a\nshort text\nto tryThis addressing\n", ""},
		{Range{0, 3}, "t/try", "This is a\nshort text\nto tryThi addressing\n", ""},
		{Range{1, 3}, "m0", "hiTs is a\nshort text\nto try addressing\n", ""},
		{Range{4, 8}, "m.", contents, ""},

		// s
		{Range{0, n}, "s/short/long/", "This is a\nlong text\nto try addressing\n", ""},
		{Range{0, n}, `s/(i.)/!\1!/g`, "Th!is! !is! a\nshort text\nto try address!in!g\n", ""},
		{Range{0, n}, "s2/is/IS/", "This IS a\nshort text\nto try addressing\n", ""},
		{Range{0, n}, `s/ /\n/`, "This\nis a\nshort text\nto try addressing\n", ""},
		{Range{0, 0}, "/short/ s//long/", "This is a\nlong text\nto try addressing\n", ""},
		{Range{0, 0}, "a\nfoo\nbar\n.", "foo\nbar\nThis is a\nshort text\nto try addressing\n", ""},

		// =
		{Range{1, 3}, "=", contents, testName + ":1\n"},
		{Range{1, 3}, "=+", contents, testName + ":1+#1\n"},
		{Range{1, 3}, "=#", contents, testName + ":#1,#3\n"},

		// p
		{Range{0, 4}, "p", contents, "This"},

		// x, y: a line is its text and its newline
		{Range{0, 4}, ",x/$/ a/@/", "This is a@\nshort text@\nto try addressing@\n@", ""},
		{Range{0, 4}, ",x a/@/", "This is a\n@short text\n@to try addressing\n@", ""},
		{Range{0, n}, ",y/ / d", "     ", ""},

		// | > <
		{Range{0, 4}, "|tr a-z A-Z", "THIS is a\nshort text\nto try addressing\n", ""},
		{Range{0, 4}, ">cat", contents, "This"},
		{Range{0, 4}, "<printf less", "less is a\nshort text\nto try addressing\n", ""},
		{Range{0, 4}, "<false", contents, "Edit: exit status 1\n"},

		// { } sets . the same for each of the commands.
		{Range{0, 0}, ",x {\n i/@/ \n a/%/\n }", "@This is a\n%@short text\n%@to try addressing\n%", ""},

		// addresses
		{Range{0, 0}, "2d", "This is a\nto try addressing\n", ""},
		{Range{12, 12}, "-d", "short text\nto try addressing\n", ""},
		{Range{n, n}, "/This/d", " is a\nshort text\nto try addressing\n", ""}, // wraps
		{Range{0, 0}, "?addressing?d", "This is a\nshort text\nto try \n", ""}, // wraps
		{Range{0, 0}, "/short/;/try/d", "This is a\n addressing\n", ""},

		// errors
		{Range{0, n}, "s/zzz/y/", contents, "Edit: no substitution\n"},
		{Range{0, 0}, "/zzz/d", contents, "Edit: no match for regexp\n"},
		{Range{0, 0}, "5d", contents, "Edit: address out of range\n"},
		{Range{0, 0}, "j", contents, "Edit: unknown command j\n"},
		{Range{0, 0}, "= x", contents, "Edit: newline expected\n"},
		{Range{0, 0}, "a/x/\nw", contents, "Edit: can't write file with pending modifications\n"},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			lastpat = ""
			e, col := editSetup(t, testName, tc.dot)
			out := runEdit(e, col.windows[0], tc.expr)
			if got := bodies(e)[0]; got != tc.want {
				t.Errorf("body = %q, want %q", got, tc.want)
			}
			if out != tc.out {
				t.Errorf("printed %q, want %q", out, tc.out)
			}
		})
	}
}

func TestEditCmdWithFile(t *testing.T) {
	fname := filepath.Join(t.TempDir(), "example")
	if err := os.WriteFile(fname, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		dot   Range
		expr  string
		want  string
		dirty bool
	}{
		{Range{0, 0}, "e " + fname, contents, false},
		{Range{0, 0}, "r " + fname, contents + contents, true},
		{Range{0, len(contents)}, "r " + fname, contents, false}, // all of it from its file
		{Range{0, 0}, "e", contents, false},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			e, col := editSetup(t, fname, tc.dot)
			w := col.windows[0]
			if out := runEdit(e, w, tc.expr); out != "" {
				t.Errorf("printed %q", out)
			}
			if got := bodies(e)[0]; got != tc.want {
				t.Errorf("body = %q, want %q", got, tc.want)
			}
			var dirty bool
			e.Call(func() { dirty = w.IsDirty() })
			if dirty != tc.dirty {
				t.Errorf("dirty = %v, want %v", dirty, tc.dirty)
			}
		})
	}
}

func TestEditMultipleWindows(t *testing.T) {
	tests := []struct {
		dot  Range
		expr string
		want []string
		out  string
	}{
		// X, Y
		{Range{0, 0}, "X/.*/ ,x i/@/", []string{
			"@This is a\n@short text\n@to try addressing\n",
			"@A different text\n@With other contents\n@So there!\n",
		}, ""},
		{Range{0, 6}, "X/.*/=", []string{contents, altContents}, testName + ":1\n" + altName + ":1\n"},
		{Range{0, 6}, "X/alt.*/D", []string{contents}, ""},
		{Range{0, 6}, "Y/alt.*/=", []string{contents, altContents}, testName + ":1\n"},
		{Range{0, 0}, "X X/.*/=", []string{contents, altContents}, "Edit: can't nest X command\n"},

		// B
		{Range{0, 0}, "B", []string{contents, altContents}, "Edit: no file name given\n"},

		// b
		{Range{0, 0}, "b " + altName + "\ni/inserted/\n", []string{
			contents,
			"inserted" + altContents,
		}, " +  " + altName + "\n"},
		{Range{0, 0}, "b " + altName + "\n1 i/1/\n2 i/2/\n", []string{
			contents,
			"1A different text\n2With other contents\nSo there!\n",
		}, " +  " + altName + "\n"},
		{Range{0, 0}, "b " + altName + "\n2 i/2/\n1 i/1/\n", []string{
			contents,
			"1A different text2\nWith other contents\nSo there!\n",
		}, " +  " + altName + "\nwarning: changes out of sequence\n"},
		{Range{0, 0}, "b " + altName + "\ni/inserted/\nb " + altName + "\n", []string{
			contents,
			"inserted" + altContents,
		}, " +  " + altName + "\n +. " + altName + "\n"},
		{Range{0, 0}, "b nonesuch", []string{contents, altContents}, "Edit: no such file\"nonesuch\"\n"},

		// "
		{Range{0, 0}, `"alt" 1d`, []string{contents, "With other contents\nSo there!\n"}, ""},
		{Range{0, 0}, `"/tmp" 1d`, []string{contents, altContents}, "Edit: too many files match \"/tmp\"\n"},

		// u
		{Range{0, 0}, "u", []string{contents, altContents}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			e, col := editSetup(t, testName, tc.dot)
			out := runEdit(e, col.windows[0], tc.expr)
			if got := bodies(e); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("bodies = %q, want %q", got, tc.want)
			}
			if out != tc.out {
				t.Errorf("printed %q, want %q", out, tc.out)
			}
		})
	}
}

func TestEditUndo(t *testing.T) {
	e, col := editSetup(t, testName, Range{0, 0})
	w := col.windows[0]
	e.Call(func() { w.body.GetBuffer().Insert("hello") })

	if out := runEdit(e, w, "1,$p\nu"); out != "hello"+contents {
		t.Errorf("printed %q", out)
	}
	if got := bodies(e)[0]; got != contents {
		t.Errorf("after u: body = %q", got)
	}
	runEdit(e, w, "u-1")
	if got := bodies(e)[0]; got != "hello"+contents {
		t.Errorf("after u-1: body = %q", got)
	}
}

// An Edit's changes undo together.
func TestEditUndoesAsOne(t *testing.T) {
	e, col := editSetup(t, testName, Range{0, 0})
	w := col.windows[0]
	runEdit(e, w, ",x/is/ c/IS/")
	if got := bodies(e)[0]; got != "ThIS IS a\nshort text\nto try addressing\n" {
		t.Fatalf("body = %q", got)
	}
	e.Call(func() { w.body.GetBuffer().Undo() })
	if got := bodies(e)[0]; got != contents {
		t.Errorf("after one undo: body = %q", got)
	}
}

// Dot ends on what the last command changed, moved with the text.
func TestEditDot(t *testing.T) {
	for _, tc := range []struct {
		expr string
		want Range
	}{
		{"/short/c/long/", Range{10, 14}},
		{"/short/d", Range{10, 10}},
		{"/short/a/!/", Range{15, 16}},
		{"3", Range{21, 39}},
	} {
		e, col := editSetup(t, testName, Range{0, 0})
		w := col.windows[0]
		runEdit(e, w, tc.expr)
		var got Range
		e.Call(func() { b := w.body.GetBuffer(); got = Range{b.q0, b.q1} })
		if got != tc.want {
			t.Errorf("%q: dot = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestEditWrite(t *testing.T) {
	e, col := editSetup(t, testName, Range{0, 0})
	dest := filepath.Join(t.TempDir(), "out")
	if out := runEdit(e, col.windows[0], "2w "+dest); out != "" {
		t.Errorf("printed %q", out)
	}
	if data, _ := os.ReadFile(dest); string(data) != "short text\n" {
		t.Errorf("wrote %q", data)
	}
}

func TestEditTerminal(t *testing.T) {
	e, col := editSetup(t, testName, Range{0, 0})
	term := addTerm(t, col, " /tmp/-sh Del ", "sh")
	if out := runEdit(e, term, "a/x/"); out != "Edit: can't change terminal /tmp/-sh\n" {
		t.Errorf("a: printed %q", out)
	}
	if out := runEdit(e, term, "="); out != "/tmp/-sh:1\n" {
		t.Errorf("=: printed %q", out)
	}
	// X passes terminals by.
	if out := runEdit(e, col.windows[0], "X ,x/zzz/ d"); out != "" {
		t.Errorf("X: printed %q", out)
	}
}

func TestParsecmd(t *testing.T) {
	tt := []struct {
		input string
		cmd   *Cmd
		err   error
	}{
		{"\n", &Cmd{cmdc: '\n'}, nil},
		{"a\n", &Cmd{cmdc: 'a', text: "\n"}, nil},
		{"a\nabc", &Cmd{cmdc: 'a', text: "abc\n"}, nil},
		{"a\nabc\n.\n", &Cmd{cmdc: 'a', text: "abc\n"}, nil},
		{"a\nαβ\n.\n", &Cmd{cmdc: 'a', text: "αβ\n"}, nil},
		{"a/abc/\n", &Cmd{cmdc: 'a', text: "abc"}, nil},
		{`a/a\bc/` + "\n", &Cmd{cmdc: 'a', text: `a\bc`}, nil},
		{`a/a\nc/` + "\n", &Cmd{cmdc: 'a', text: "a\nc"}, nil},
		{"a/ab\\\nc/\n", &Cmd{cmdc: 'a', text: `ab\`}, nil},
		{"a/ab\\", nil, errBadRHS},
		{`a\abc\` + "\n", nil, badDelimiterError('\\')},
		{"a/abc/ x\n", nil, errNewlineExpected},
		{"x/abc/\n", &Cmd{re: "abc", cmd: &Cmd{cmdc: 'p'}, cmdc: 'x'}, nil},
		{"x/abc/j\n", nil, invalidCmdError('j')},
		{"s/abc/def/\n", &Cmd{re: "abc", text: "def", num: 1, cmdc: 's'}, nil},
		{"s/abc/def/g\n", &Cmd{re: "abc", text: "def", num: 1, flag: 'g', cmdc: 's'}, nil},
		{"s2/abc/def/\n", &Cmd{re: "abc", text: "def", num: 2, cmdc: 's'}, nil},
		{"/abc/ s//def/\n", &Cmd{
			addr: &Addr{typ: '/', re: "abc"},
			re:   "abc", text: "def", num: 1, cmdc: 's',
		}, nil},
		{"s//xyz/\n", nil, errRegexpMissing},
		{"s/abc/def\\", nil, errBadRHS},
		{"3.,17d\n", nil, errBadAddrSyntax},
		{"5u\n", nil, errAddrNotRequired},
		{"j\n", nil, invalidCmdError('j')},
		{"{}\n", &Cmd{cmdc: '{'}, nil},
		{"{\nd\nu\n}\n", &Cmd{
			cmd:  &Cmd{cmdc: 'd', next: &Cmd{cmdc: 'u', num: 1}},
			cmdc: '{',
		}, nil},
		{"{j}\n", nil, invalidCmdError('j')},
		{"{\nj\n}\n", nil, invalidCmdError('j')},
		{"{ x/a/ }\n", nil, errDefcmd},
		{"}\n", nil, errLeftBraceMissing},
		{"cd\n", nil, invalidCmdError('c' | 0x100)},
		{"t 42.\n", nil, errBadAddrSyntax},
		{"t\n", nil, errBadAddr},
		{"B abc.txt\n", &Cmd{cmdc: 'B', text: " abc.txt"}, nil},
		{"e abc.txt\n", &Cmd{cmdc: 'e', text: " abc.txt"}, nil},
		{"e abc.txt def\n", nil, errNewlineExpected},
		{"g\n", nil, errAddressMissing},
		{`g\abc\` + "\n", nil, badDelimiterError('\\')},
		{"u\n", &Cmd{num: 1, cmdc: 'u'}, nil},
		{"u5\n", &Cmd{num: 5, cmdc: 'u'}, nil},
		{"u-3\n", &Cmd{num: -3, cmdc: 'u'}, nil},
	}
	for _, tc := range tt {
		lastpat = ""
		cp := &cmdParser{buf: []rune(tc.input)}
		cmd, err := cp.parse(0)
		if err != tc.err {
			t.Errorf("parsing command %q returned error %v; expected %v", tc.input, err, tc.err)
			continue
		}
		if !reflect.DeepEqual(cmd, tc.cmd) {
			t.Errorf("bad parse result for command %q:\ngot: %+v\nexpected: %+v", tc.input, cmd, tc.cmd)
		}
	}
}

func TestCollecttoken(t *testing.T) {
	tt := []struct {
		cmd string
		end string
		out string
		err error
	}{
		{" foo bar\t\n", linex, " foo bar\t", nil},
		{" foo bar\t\nquux", linex, " foo bar\t", nil},
		{" αβγ テスト\t\n世界", linex, " αβγ テスト\t", nil},
		{" foo\t\n", wordx, " foo", nil},
		{" αβγ\n世界", wordx, " αβγ", nil},
		{" foo bar\n", wordx, "", errNewlineExpected},
	}
	for _, tc := range tt {
		cp := &cmdParser{buf: []rune(tc.cmd)}
		out, err := cp.collecttoken(tc.end)
		if out != tc.out || err != tc.err {
			t.Errorf("collecttoken(%q) of %q is %q, %v; expected %q, %v", tc.end, tc.cmd, out, err, tc.out, tc.err)
		}
	}
}

type addrTest struct {
	cmd  string
	addr *Addr
	err  error
}

func TestSimpleaddr(t *testing.T) {
	tt := []addrTest{
		{"", nil, nil},
		{"\n", nil, nil},
		{"#123\n", &Addr{typ: '#', num: 123}, nil},
		{"#\n", &Addr{typ: '#', num: 1}, nil},
		{"42\n", &Addr{typ: 'l', num: 42}, nil},
		{"1234567890\n", &Addr{typ: 'l', num: 1234567890}, nil},
		{"/abc\n", &Addr{typ: '/', re: "abc"}, nil},
		{"/abc/\n", &Addr{typ: '/', re: "abc"}, nil},
		{`/a\/bc/` + "\n", &Addr{typ: '/', re: "a/bc"}, nil},
		{`/a\nbc/` + "\n", &Addr{typ: '/', re: `a\nbc`}, nil},
		{`/a\\bc/` + "\n", &Addr{typ: '/', re: `a\\bc`}, nil},
		{"?abc\n", &Addr{typ: '?', re: "abc"}, nil},
		{"?abc?\n", &Addr{typ: '?', re: "abc"}, nil},
		{`?a\?bc?` + "\n", &Addr{typ: '?', re: "a?bc"}, nil},
		{`"abc` + "\n", &Addr{typ: '"', re: "abc"}, nil},
		{`"abc"` + "\n", &Addr{typ: '"', re: "abc"}, nil},
		{".\n", &Addr{typ: '.'}, nil},
		{"$\n", &Addr{typ: '$'}, nil},
		{"+\n", &Addr{typ: '+'}, nil},
		{"-\n", &Addr{typ: '-'}, nil},
		{"'\n", &Addr{typ: '\''}, nil},
		{"abc\n", nil, nil},
		{"42.\n", nil, errBadAddrSyntax},
		{"42$\n", nil, errBadAddrSyntax},
		{"42'\n", nil, errBadAddrSyntax},
		{"42\"\n", nil, errRegexpMissing},
		{`"abc" "cdf" "efg"` + "\n", nil, errBadAddrSyntax},
		{"\"abc\" 42\n", &Addr{typ: '"', re: "abc", next: &Addr{typ: 'l', num: 42}}, nil},
		{".42\n", &Addr{typ: '.', next: &Addr{typ: '+', next: &Addr{typ: 'l', num: 42}}}, nil},
		{"42/abc/\n", &Addr{typ: 'l', num: 42, next: &Addr{typ: '+', next: &Addr{typ: '/', re: "abc"}}}, nil},
		{"+/abc/\n", &Addr{typ: '+', next: &Addr{typ: '/', re: "abc"}}, nil},
		{"-/abc/\n", &Addr{typ: '-', next: &Addr{typ: '/', re: "abc"}}, nil},
		{".+\n", &Addr{typ: '.', next: &Addr{typ: '+'}}, nil},
		{".-\n", &Addr{typ: '.', next: &Addr{typ: '-'}}, nil},
	}
	runAddrTests(t, tt, (*cmdParser).simpleaddr)
}

func TestCompoundaddr(t *testing.T) {
	tt := []addrTest{
		{"3,17\n", &Addr{typ: ',', left: &Addr{typ: 'l', num: 3}, next: &Addr{typ: 'l', num: 17}}, nil},
		{"3,\n", &Addr{typ: ',', left: &Addr{typ: 'l', num: 3}}, nil},
		{",17\n", &Addr{typ: ',', next: &Addr{typ: 'l', num: 17}}, nil},
		{"37;/abc/\n", &Addr{typ: ';', left: &Addr{typ: 'l', num: 37}, next: &Addr{typ: '/', re: "abc"}}, nil},
		{"3.,17\n", nil, errBadAddrSyntax},
		{"3,17.\n", nil, errBadAddrSyntax},
		{"3,,17\n", nil, errBadAddrSyntax},
		{"3;;17\n", nil, errBadAddrSyntax},
	}
	runAddrTests(t, tt, (*cmdParser).compoundaddr)
}

func runAddrTests(t *testing.T, tt []addrTest, parse func(*cmdParser) (*Addr, error)) {
	t.Helper()
	for _, tc := range tt {
		lastpat = ""
		addr, err := parse(&cmdParser{buf: []rune(tc.cmd)})
		if err != tc.err {
			t.Errorf("parsing address %q returned error %v; expected %v", tc.cmd, err, tc.err)
			continue
		}
		if !reflect.DeepEqual(addr, tc.addr) {
			t.Errorf("bad parse result for address %q:\ngot: %+v\nexpected: %+v", tc.cmd, addr, tc.addr)
		}
	}
}
