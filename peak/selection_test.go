package main

import (
	"testing"
)

// These tests cover positions, selection and search in text with multi-byte
// (UTF-8) characters such as Chinese. Every position is a rune offset, or a
// line and a rune index within it; code that takes either for a byte offset
// gets non-ASCII text wrong.

// Pos and Offset convert between rune offsets and line/column, both counted
// in runes; the implicit newline is the position at the end of its line.
func TestPosUTF8(t *testing.T) {
	b := NewBuffer("你A好B\nC世D界")
	tests := []struct {
		q, line, col int
		r            string // the rune at q
	}{
		{0, 0, 0, "你"},
		{1, 0, 1, "A"},
		{2, 0, 2, "好"},
		{3, 0, 3, "B"},
		{4, 0, 4, "\n"},
		{5, 1, 0, "C"},
		{6, 1, 1, "世"},
		{7, 1, 2, "D"},
		{8, 1, 3, "界"},
	}
	for _, tt := range tests {
		if line, col := b.Pos(tt.q); line != tt.line || col != tt.col {
			t.Errorf("Pos(%d) = (%d, %d), want (%d, %d)", tt.q, line, col, tt.line, tt.col)
		}
		if q := b.Offset(tt.line, tt.col); q != tt.q {
			t.Errorf("Offset(%d, %d) = %d, want %d", tt.line, tt.col, q, tt.q)
		}
		if got := string(b.RunesInRange(tt.q, tt.q+1)); got != tt.r {
			t.Errorf("RunesInRange(%d, %d) = %q, want %q", tt.q, tt.q+1, got, tt.r)
		}
	}
}

// Pos clamps an offset past the text to its end.
func TestPosPastEnd(t *testing.T) {
	b := NewBuffer("你好\nABC")
	for _, q := range []int{b.Len(), 999} {
		if line, col := b.Pos(q); line != 1 || col != 3 {
			t.Errorf("Pos(%d) = (%d, %d), want (1, 3)", q, line, col)
		}
	}
}

// Pos finds the column on a long line by binary search over line starts.
func TestPosLongLine(t *testing.T) {
	const n = 200
	runes := make([]rune, n)
	for i := range runes {
		runes[i] = '你' + rune(i%50)
	}
	b := NewBuffer(string(runes))
	for q := 0; q <= n; q++ {
		if line, col := b.Pos(q); line != 0 || col != q {
			t.Errorf("Pos(%d) = (%d, %d), want (0, %d)", q, line, col, q)
		}
	}
}

// The selected text is exactly the runes of dot, newlines included.
func TestSelectedTextUTF8(t *testing.T) {
	tests := []struct {
		text   string
		q0, q1 int
		want   string
	}{
		{"你好世界", 1, 3, "好世"},
		{"你好世界", 3, 1, "好世"}, // SetDot takes either order
		{"你好世界", 0, 4, "你好世界"},
		{"你好世界", 2, 2, ""},
		{"你好ABC", 1, 3, "好A"},
		{"你好hello", 2, 6, "hell"},
		{"abc你好def", 2, 6, "c你好d"},
		{"a你b好c世d界e", 1, 7, "你b好c世d"},
		{"你好世界\n一二三四", 1, 7, "好世界\n一二"},
		{"忽略此行\n你好世界\n一二三四\n忽略此行", 7, 12, "世界\n一二"},
		{"你好\n\n世界", 1, 5, "好\n\n世"},
		{"你好\n世界", 0, 3, "你好\n"},
		{"SKIP\n你好世界\n完整复制\nSKIP", 4, 15, "\n你好世界\n完整复制\n"},
		{"你好hello\nworld世界", 2, 13, "hello\nworld"},
		{"e\u0301 caf\u00e9", 0, 2, "e\u0301"}, // e and a combining accent: two runes
		{"e\u0301 caf\u00e9", 1, 2, "\u0301"},
	}
	for _, tt := range tests {
		b := NewBuffer(tt.text)
		b.SetDot(tt.q0, tt.q1)
		if got := b.GetSelectedText(); got != tt.want {
			t.Errorf("%q [%d, %d): selected %q, want %q", tt.text, tt.q0, tt.q1, got, tt.want)
		}
	}
}

func TestRunesInRangeClampsPastEnd(t *testing.T) {
	b := NewBuffer("你好")
	if got := string(b.RunesInRange(0, 999)); got != "你好" {
		t.Errorf("RunesInRange(0, 999) = %q, want %q", got, "你好")
	}
}

// Search selects the next match from the end of dot on, wrapping around: the
// word, at its rune offset.
func TestSearchUTF8(t *testing.T) {
	tests := []struct {
		text, word string
		from, want int
	}{
		{"你好世界", "好世", 0, 1},
		{"你好世界你好", "好", 2, 5},
		{"你好ABC", "AB", 0, 2},
		{"第一行\n你好世界\n最后一行", "好世", 0, 5},
		{"第一行\n你好\n第三行", "你好", 7, 4}, // wraps
		{"一你好三", "你好", 3, 1},         // wraps on the same line
		{"abcdef你好世界", "世", 0, 8},
		{"你好world", "world", 0, 2},
		{"你hello好world", "好w", 0, 6},
		{"你好abc你好", "abc", 2, 2}, // a match at the offset itself
		{"abc你好def\nABCDEF", "你好", 10, 3},
		{"ab\ncd", "b\nc", 0, 1}, // across lines
	}
	for _, tt := range tests {
		b := NewBuffer(tt.text)
		b.SetDot(tt.from, tt.from)
		ok := b.Search(tt.word)
		if got := b.GetSelectedText(); !ok || b.q0 != tt.want || got != tt.word {
			t.Errorf("Search(%q) in %q from %d selected %q at %d, %v; want at %d", tt.word, tt.text, tt.from, got, b.q0, ok, tt.want)
		}
	}
	if NewBuffer("abc").Search("x") {
		t.Error("Search found a word that is not there")
	}
}

// A search from the end of a match finds the next one, not the same again.
func TestSearchFromDotFindsNext(t *testing.T) {
	b := NewBuffer("foofoo foo")
	for _, want := range []int{0, 3, 7, 0} {
		b.Search("foo")
		if b.q0 != want || b.q1 != want+3 {
			t.Fatalf("dot = [%d, %d), want [%d, %d)", b.q0, b.q1, want, want+3)
		}
	}
}
