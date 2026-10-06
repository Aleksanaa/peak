package quote

import (
	"reflect"
	"testing"
)

func TestFields(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"foo", []string{"foo"}},
		{"  foo  bar  ", []string{"foo", "bar"}},
		{"foo\tbar\nbaz", []string{"foo", "bar", "baz"}},
		{"`foo bar` baz", []string{"foo bar", "baz"}},
		{"New `my file.txt`", []string{"New", "my file.txt"}},
		{"``", []string{"`"}},
		{"a``b", []string{"a`b"}},
		{"`foo``bar`", []string{"foo`bar"}},
		{"`foo bar", []string{"foo bar"}}, // unclosed: quoted to the end
		{"pre`foo bar`suf", []string{"prefoo barsuf"}},
		{"` foo `", []string{" foo "}},
		{"Get `a b` Put `c d` Del", []string{"Get", "a b", "Put", "c d", "Del"}},
	}
	for _, tt := range tests {
		if got := Fields(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Fields(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain", "plain"},
		{"/path/to/file.txt", "/path/to/file.txt"},
		{"with space", "`with space`"},
		{"tab\there", "`tab\there`"},
		{"a`b", "a``b"},
		{"`a b`", "```a b```"},
	}
	for _, tt := range tests {
		if got := Quote(tt.in); got != tt.want {
			t.Errorf("Quote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// Every non-empty string over an alphabet of the tricky characters reads
// back unchanged as one field after quoting.
func TestQuoteRoundTrip(t *testing.T) {
	alphabet := []rune{'a', ' ', '\n', '`'}
	var walk func(s []rune)
	walk = func(s []rune) {
		if len(s) > 0 {
			q := Quote(string(s))
			if got := Fields(q); len(got) != 1 || got[0] != string(s) {
				t.Fatalf("Fields(Quote(%q)) = Fields(%q) = %q", string(s), q, got)
			}
		}
		if len(s) < 6 {
			for _, r := range alphabet {
				walk(append(s, r))
			}
		}
	}
	walk(nil)
}

func TestUnquote(t *testing.T) {
	tests := []struct{ in, want string }{
		{"`Win btop`", "Win btop"},
		{"`a``b c`", "a`b c"},
		{"Get", "Get"},
		{"`date`", "`date`"},     // not how peak quotes anything: left to the shell
		{"`a b`:12", "`a b`:12"}, // more than the quoted span
		{"`a b` `c d`", "`a b` `c d`"},
	}
	for _, tt := range tests {
		if got := Unquote(tt.in); got != tt.want {
			t.Errorf("Unquote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCut(t *testing.T) {
	first, rest := Cut(" `my file.go` Get  Put ")
	if first != "my file.go" || rest != " Get  Put " {
		t.Errorf("Cut = %q, %q", first, rest)
	}
}

func TestFieldAt(t *testing.T) {
	tests := []struct {
		s          string
		x          int
		start, end int
	}{
		{"foo bar", 1, 0, 3},
		{"foo bar", 3, 0, 3},  // just past a field counts as in it
		{"foo  bar", 4, 4, 4}, // between fields
		{"`my file.go`:12 x", 5, 0, 15},
		{"`a b` `c d`", 7, 6, 11},
		{"foo ``bar", 6, 4, 9}, // `` is a literal, not a quote
	}
	for _, tt := range tests {
		r := []rune(tt.s)
		start, end := FieldAt(tt.x, len(r), func(i int) rune { return r[i] })
		if start != tt.start || end != tt.end {
			t.Errorf("FieldAt(%q, %d) = [%d, %d), want [%d, %d)", tt.s, tt.x, start, end, tt.start, tt.end)
		}
	}
}
