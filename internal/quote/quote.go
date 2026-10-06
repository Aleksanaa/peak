// Package quote implements peak's backtick quoting of fields.
//
// Fields are separated by white space. A backtick toggles quoting: inside a
// quoted span, white space does not separate fields. Two backticks in a row
// stand for one literal backtick, in or out of quotes. Quoted and unquoted text
// join into one field, as in a shell: pre`a b`suf is the field "prea bsuf".
//
//	Fields("New `my file.txt`") → ["New", "my file.txt"]
//	Quote("my file.txt")        → "`my file.txt`"
//	Quote("plain")              → "plain"
package quote

import (
	"strings"
	"unicode"
)

// Quote returns s written as a single field: unchanged if it needs no quoting,
// otherwise with backticks doubled and, if it contains white space, wrapped
// in backticks.
func Quote(s string) string {
	if strings.Contains(s, "`") {
		s = strings.ReplaceAll(s, "`", "``")
	}
	if strings.IndexFunc(s, unicode.IsSpace) >= 0 {
		return "`" + s + "`"
	}
	return s
}

// Unquote returns the field s quotes if s is exactly its quoted form, as for
// "`Win btop`"; otherwise it returns s unchanged.
func Unquote(s string) string {
	if f := Fields(s); len(f) == 1 && Quote(f[0]) == s {
		return f[0]
	}
	return s
}

// Fields splits s into fields, honoring quotes.
func Fields(s string) []string {
	r := []rune(s)
	var fields []string
	for i := 0; ; {
		_, end, field, ok := scan(len(r), func(j int) rune { return r[j] }, i)
		if !ok {
			return fields
		}
		fields = append(fields, field)
		i = end
	}
}

// Cut returns the first field of s and the text after it, unchanged.
func Cut(s string) (first, rest string) {
	r := []rune(s)
	_, end, first, _ := scan(len(r), func(j int) rune { return r[j] }, 0)
	return first, string(r[end:])
}

// FieldAt returns the bounds [start, end) of the field around position x of
// a text of n runes, read through at. A position just past a field's end
// counts as in it. If x is in white space between fields, start == end == x.
func FieldAt(x, n int, at func(int) rune) (start, end int) {
	for i := 0; ; {
		s, e, _, ok := scan(n, at, i)
		if !ok || s > x {
			return x, x
		}
		if x <= e {
			return s, e
		}
		i = e
	}
}

// scan finds the first field at or after position i, returning its bounds
// and its value with quoting removed. ok is false if there is none.
func scan(n int, at func(int) rune, i int) (start, end int, field string, ok bool) {
	for i < n && unicode.IsSpace(at(i)) {
		i++
	}
	if i == n {
		return i, i, "", false
	}
	start = i
	var b strings.Builder
	quoted := false
	for ; i < n; i++ {
		switch r := at(i); {
		case r == '`' && i+1 < n && at(i+1) == '`':
			b.WriteRune('`')
			i++
		case r == '`':
			quoted = !quoted
		case unicode.IsSpace(r) && !quoted:
			return start, i, b.String(), true
		default:
			b.WriteRune(r)
		}
	}
	return start, n, b.String(), true
}
