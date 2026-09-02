package quote_test

import (
	"testing"

	"github.com/aleksana/peak/internal/quote"
)

func TestFields(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"foo", []string{"foo"}},
		{"foo bar", []string{"foo", "bar"}},
		{"  foo  bar  ", []string{"foo", "bar"}},
		{"foo\tbar", []string{"foo", "bar"}},
		{"`foo bar`", []string{"foo bar"}},
		{"`foo bar` baz", []string{"foo bar", "baz"}},
		{"New `my file.txt`", []string{"New", "my file.txt"}},
		{"`a b` `c d`", []string{"a b", "c d"}},
		{"``", []string{"`"}},
		{"a``b", []string{"a`b"}},
		{"`foo``bar`", []string{"foo`bar"}},
		{"`foo bar", []string{"foo bar"}},
		{"pre`foo bar`suf", []string{"prefoo barsuf"}},
		{"` foo `", []string{" foo "}},
		{"Get `a b` Put `c d` Del", []string{"Get", "a b", "Put", "c d", "Del"}},
	}
	for _, tt := range tests {
		got := quote.Fields(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("Fields(%q) = %v, want %v", tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("Fields(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
			}
		}
	}
}

func TestQuote(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"plain", "plain"},
		{"/path/to/file.txt", "/path/to/file.txt"},
		{"with space", "`with space`"},
		{"a b c", "`a b c`"},
		{"tab\there", "`tab\there`"},
		{"", ""},
		{"`foo`", "`foo`"},
	}
	for _, tt := range tests {
		if got := quote.Quote(tt.in); got != tt.want {
			t.Errorf("Quote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSpanAt(t *testing.T) {
	mk := func(s string) (int, func(int) rune) {
		r := []rune(s)
		return len(r), func(i int) rune { return r[i] }
	}

	// Every position in `foo bar` returns [0, 9).
	n, get := mk("`foo bar`")
	for x := 0; x < n; x++ {
		s, e, ok := quote.SpanAt(x, n, get)
		if !ok || s != 0 || e != 9 {
			t.Errorf("`foo bar` x=%d: got (%d,%d,%v), want (0,9,true)", x, s, e, ok)
		}
	}

	// Position outside any span returns false.
	n, get = mk("`a b` c")
	s, e, ok := quote.SpanAt(6, n, get)
	if ok {
		t.Errorf("outside span: got (%d,%d,true), want false", s, e)
	}

	// Double backtick is a literal, not a span delimiter.
	n, get = mk("foo ``bar")
	_, _, ok = quote.SpanAt(4, n, get)
	if ok {
		t.Errorf("double-backtick: expected no span at position 4")
	}

	// Second of two spans on the same line.
	n, get = mk("`a b` `c d`")
	s, e, ok = quote.SpanAt(7, n, get)
	if !ok || s != 6 || e != 11 {
		t.Errorf("second span: got (%d,%d,%v), want (6,11,true)", s, e, ok)
	}
}

func TestRoundTrip(t *testing.T) {
	paths := []string{
		"plain",
		"/path/to/file.txt",
		"/path/with spaces/file.txt",
		"/path/with  double  spaces",
		"/path/with\ttab",
		"/my documents/report 2026.txt",
	}
	for _, path := range paths {
		quoted := quote.Quote(path)
		parts := quote.Fields(quoted)
		if len(parts) != 1 || parts[0] != path {
			t.Errorf("round-trip(%q): Quote=%q Fields=%v, want [%q]",
				path, quoted, parts, path)
		}
	}
}
