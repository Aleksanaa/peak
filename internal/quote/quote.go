// Package quote implements peak's backtick field-quoting format.
//
// A lone backtick (not adjacent to another backtick) toggles quoting mode.
// Inside a quoted span, whitespace is not a field separator.
// A double backtick (``) represents a literal backtick character.
//
// Examples:
//
//	Fields("New `my file.txt`") → ["New", "my file.txt"]
//	Quote("my file.txt")        → "`my file.txt`"
//	Quote("plain")              → "plain"
package quote

import "strings"

// Fields splits s into whitespace-separated fields, respecting backtick quoting.
func Fields(s string) []string {
	var fields []string
	var cur []rune
	inQuote := false
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '`' {
			if i+1 < len(runes) && runes[i+1] == '`' {
				cur = append(cur, '`')
				i++
			} else {
				inQuote = !inQuote
			}
		} else if (r == ' ' || r == '\t') && !inQuote {
			if len(cur) > 0 {
				fields = append(fields, string(cur))
				cur = nil
			}
		} else {
			cur = append(cur, r)
		}
	}
	if len(cur) > 0 {
		fields = append(fields, string(cur))
	}
	return fields
}

// Quote wraps s in backticks if it contains whitespace, otherwise returns s unchanged.
func Quote(s string) string {
	if strings.ContainsAny(s, " \t") {
		return "`" + s + "`"
	}
	return s
}

// SpanAt reports whether position x falls inside a backtick-quoted span in a
// sequence of length runes accessed via getChar. If so, it returns the
// [start, end) indices of the full span (including the backtick delimiters)
// and true. A double backtick (``) is a literal and does not start a span.
func SpanAt(x, length int, getChar func(int) rune) (start, end int, ok bool) {
	inQuote := false
	spanStart := -1
	for i := 0; i < length; i++ {
		if getChar(i) != '`' {
			continue
		}
		if i+1 < length && getChar(i+1) == '`' {
			i++ // skip literal double-backtick
			continue
		}
		if !inQuote {
			inQuote = true
			spanStart = i
		} else {
			if x >= spanStart && x <= i {
				return spanStart, i + 1, true
			}
			inQuote = false
			spanStart = -1
		}
	}
	return 0, 0, false
}
