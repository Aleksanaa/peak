package main

import (
	"testing"
)

// These tests cover selection and copy correctness for text containing
// multi-byte (UTF-8) characters such as Chinese.
//
// Cursor.x is a rune index within a line (into the []rune slice). Any code
// path that treats it as a byte offset into the UTF-8-encoded string will
// produce wrong results for non-ASCII text.

// ── Buffer rune-offset / cursor roundtrip ────────────────────────────────────

// "你好世界" has 4 runes but 12 bytes. RuneOffsetToCursor must produce rune
// indices, not byte indices.
func TestRuneOffsetToCursorUTF8(t *testing.T) {
	b := NewBuffer("你好世界")
	tests := []struct{ off, wantX, wantY int }{
		{0, 0, 0},
		{1, 1, 0},
		{2, 2, 0},
		{3, 3, 0},
		{4, 4, 0},
	}
	for _, tc := range tests {
		c := b.RuneOffsetToCursor(tc.off)
		if c.x != tc.wantX || c.y != tc.wantY {
			t.Errorf("RuneOffsetToCursor(%d) = {x:%d y:%d}, want {x:%d y:%d}",
				tc.off, c.x, c.y, tc.wantX, tc.wantY)
		}
	}
}

// Line 0: "你好" (2 runes), Line 1: "ABC" (3 runes).
// In the flat rune sequence 你好\nABC the newline at offset 2 maps to the
// end-of-line cursor {2, 0}; offset 3 starts line 1 at {0, 1}.
func TestRuneOffsetToCursorMultiLineUTF8(t *testing.T) {
	b := NewBuffer("你好\nABC")
	tests := []struct{ off, wantX, wantY int }{
		{0, 0, 0}, // 你
		{1, 1, 0}, // 好
		{2, 2, 0}, // end of line 0 (the implicit \n lives here)
		{3, 0, 1}, // A
		{4, 1, 1}, // B
		{5, 2, 1}, // C
	}
	for _, tc := range tests {
		c := b.RuneOffsetToCursor(tc.off)
		if c.x != tc.wantX || c.y != tc.wantY {
			t.Errorf("RuneOffsetToCursor(%d) = {x:%d y:%d}, want {x:%d y:%d}",
				tc.off, c.x, c.y, tc.wantX, tc.wantY)
		}
	}
}

func TestCursorToRuneOffsetUTF8(t *testing.T) {
	b := NewBuffer("你好世界")
	tests := []struct {
		x, y    int
		wantOff int
	}{
		{0, 0, 0},
		{1, 0, 1},
		{2, 0, 2},
		{4, 0, 4},
	}
	for _, tc := range tests {
		got := b.CursorToRuneOffset(Cursor{tc.x, tc.y})
		if got != tc.wantOff {
			t.Errorf("CursorToRuneOffset({x:%d,y:%d}) = %d, want %d", tc.x, tc.y, got, tc.wantOff)
		}
	}
}

func TestRuneOffsetRoundtripUTF8(t *testing.T) {
	b := NewBuffer("一二三\n四五六")
	n := b.Len()
	for off := 0; off < n; off++ {
		c := b.RuneOffsetToCursor(off)
		back := b.CursorToRuneOffset(c)
		if back != off {
			t.Errorf("roundtrip failure at offset %d: cursor=%v back=%d", off, c, back)
		}
	}
}

// ── Copy scenario: GetSelectedText ───────────────────────────────────────────
//
// These tests replicate the user-visible bug: make a selection over Chinese
// text (cursor positions set as rune indices, as visualToBuffer produces),
// then Snarf → GetSelectedText. The result must be the exact rune range.

// Single line, selection does not start at col 0 and does not end at line end.
func TestCopyUTF8SingleLine(t *testing.T) {
	b := NewBuffer("你好世界")
	// Select runes 1–3: "好世"
	b.SetSelection(Cursor{1, 0}, Cursor{3, 0})
	got := b.GetSelectedText()
	want := "好世"
	if got != want {
		t.Errorf("copy single-line UTF-8: got %q, want %q", got, want)
	}
}

// Multi-line selection: starts mid-line on a Chinese line, ends mid-line on
// another Chinese line. This is the exact scenario the user describes.
func TestCopyUTF8MultiLine(t *testing.T) {
	b := NewBuffer("你好世界\n一二三四")
	// Select from "好" (line 0, col 1) to "二" (line 1, col 2).
	// Expected: "好世界\n一二"
	b.SetSelection(Cursor{1, 0}, Cursor{2, 1})
	got := b.GetSelectedText()
	want := "好世界\n一二"
	if got != want {
		t.Errorf("copy multi-line UTF-8: got %q, want %q", got, want)
	}
}

// Mixed: Chinese prefix then ASCII suffix, non-trivial start offset.
func TestCopyMixedUTF8(t *testing.T) {
	b := NewBuffer("你好ABC")
	// Select "好A" — rune indices 1–3
	b.SetSelection(Cursor{1, 0}, Cursor{3, 0})
	got := b.GetSelectedText()
	want := "好A"
	if got != want {
		t.Errorf("copy mixed UTF-8: got %q, want %q", got, want)
	}
}

// Selection reversed (End before Start); Ordered() must still return the right text.
func TestCopyUTF8ReversedSelection(t *testing.T) {
	b := NewBuffer("你好世界")
	// Set End < Start: Ordered() must fix it up
	b.SetSelection(Cursor{3, 0}, Cursor{1, 0})
	got := b.GetSelectedText()
	want := "好世"
	if got != want {
		t.Errorf("copy reversed selection: got %q, want %q", got, want)
	}
}

// Three-line content; selection spans the middle two lines but not the outer ones.
func TestCopyUTF8ThreeLinesMidSelection(t *testing.T) {
	b := NewBuffer("忽略此行\n你好世界\n一二三四\n忽略此行")
	// Select from "世" (line 1, col 2) to "二" (line 2, col 2).
	// Expected: "世界\n一二"
	b.SetSelection(Cursor{2, 1}, Cursor{2, 2})
	got := b.GetSelectedText()
	want := "世界\n一二"
	if got != want {
		t.Errorf("copy three-line mid-selection: got %q, want %q", got, want)
	}
}

// ── RunesInRange ──────────────────────────────────────────────────────────────

func TestRunesInRangeUTF8(t *testing.T) {
	b := NewBuffer("你好世界")
	got := string(b.RunesInRange(1, 3))
	want := "好世"
	if got != want {
		t.Errorf("RunesInRange(1,3) = %q, want %q", got, want)
	}
}

func TestRunesInRangeMultiLineUTF8(t *testing.T) {
	b := NewBuffer("你好\n世界")
	// Flat: 你(0) 好(1) \n(2) 世(3) 界(4). Range [1,4) → "好\n世"
	got := string(b.RunesInRange(1, 4))
	want := "好\n世"
	if got != want {
		t.Errorf("RunesInRange(1,4) = %q, want %q", got, want)
	}
}

// ── IsSelected ───────────────────────────────────────────────────────────────

// IsSelected must compare rune positions, not byte positions.
func TestIsSelectedUTF8(t *testing.T) {
	b := NewBuffer("你好世界")
	// Select rune indices 1–3 ("好世")
	b.SetSelection(Cursor{1, 0}, Cursor{3, 0})

	inside := []int{1, 2}
	outside := []int{0, 3, 4}

	for _, x := range inside {
		if !b.IsSelected(x, 0) {
			t.Errorf("IsSelected(%d, 0) = false, want true", x)
		}
	}
	for _, x := range outside {
		if b.IsSelected(x, 0) {
			t.Errorf("IsSelected(%d, 0) = true, want false", x)
		}
	}
}

// ── Search ───────────────────────────────────────────────────────────────────

// Search must return cursor Start/End as rune indices, not byte offsets.
// "好世" starts at rune index 1 in "你好世界" (byte offset 3). If Search
// returns byte offsets, Start.x = 3 instead of the correct 1.
func TestSearchUTF8ReturnsRunePositions(t *testing.T) {
	b := NewBuffer("你好世界")
	_, sel, ok := Search(b, "好世", Cursor{0, 0})
	if !ok {
		t.Fatal("Search did not find '好世'")
	}
	if sel.Start.x != 1 || sel.Start.y != 0 {
		t.Errorf("Search start = {x:%d, y:%d}, want {x:1, y:0} (rune index)", sel.Start.x, sel.Start.y)
	}
	if sel.End.x != 3 || sel.End.y != 0 {
		t.Errorf("Search end = {x:%d, y:%d}, want {x:3, y:0} (rune index)", sel.End.x, sel.End.y)
	}
}

// Searching from a non-zero start position requires the start offset to be
// interpreted as a rune index, not a byte offset.
// If Search uses startRX as a byte offset for string slicing, it will scan
// from the wrong position and miss/misidentify the second occurrence.
func TestSearchUTF8WithStartOffset(t *testing.T) {
	b := NewBuffer("你好世界你好")
	// First "好" is at rune 1; start after rune 2, so only the second "好" at rune 5 matches.
	_, sel, ok := Search(b, "好", Cursor{2, 0})
	if !ok {
		t.Fatal("Search did not find second '好'")
	}
	if sel.Start.x != 5 {
		t.Errorf("Search second '好' start.x = %d, want 5 (rune index)", sel.Start.x)
	}
}

// The cursor positions returned by Search, when used to select text and then
// copy, must reproduce the exact search word.  When Search has Chinese text
// before the match, byte vs. rune offset confusion causes both wrong cursor
// positions AND (in the current codebase) wrong selected text.
func TestSearchUTF8SelectionRoundtrip(t *testing.T) {
	// "你好" sits before "ABC" so that rune-offset ≠ byte-offset for the match.
	b := NewBuffer("你好ABC")
	_, sel, ok := Search(b, "AB", Cursor{0, 0})
	if !ok {
		t.Fatal("Search did not find 'AB'")
	}
	// "AB" is at rune positions 2–4 (after "你好"), but byte positions 6–8.
	if sel.Start.x != 2 {
		t.Errorf("Search 'AB' start.x = %d, want 2 (rune index after two Chinese chars)", sel.Start.x)
	}
	b.selection = sel
	got := b.GetSelectedText()
	if got != "AB" {
		t.Errorf("selected text after Search = %q, want %q", got, "AB")
	}
}

// Search across lines with UTF-8 content.
func TestSearchUTF8MultiLine(t *testing.T) {
	b := NewBuffer("第一行\n你好世界\n最后一行")
	_, sel, ok := Search(b, "好世", Cursor{0, 0})
	if !ok {
		t.Fatal("Search did not find '好世'")
	}
	if sel.Start.y != 1 {
		t.Errorf("Search line = %d, want 1", sel.Start.y)
	}
	if sel.Start.x != 1 {
		t.Errorf("Search start.x = %d, want 1 (rune index on line 1)", sel.Start.x)
	}
	if sel.End.x != 3 {
		t.Errorf("Search end.x = %d, want 3", sel.End.x)
	}
}

// ── copy edge cases ───────────────────────────────────────────────────────────

// An empty line inside a selection must appear as a blank line in the output,
// not be silently dropped.
func TestCopyUTF8EmptyLineInSelection(t *testing.T) {
	b := NewBuffer("你好\n\n世界")
	b.SetSelection(Cursor{1, 0}, Cursor{1, 2})
	got := b.GetSelectedText()
	want := "好\n\n世"
	if got != want {
		t.Errorf("empty line in selection: got %q, want %q", got, want)
	}
}

// Selection starting at column 0 and ending at exactly len(line).
func TestCopyUTF8ExactLineBounds(t *testing.T) {
	b := NewBuffer("你好世界")
	b.SetSelection(Cursor{0, 0}, Cursor{4, 0})
	got := b.GetSelectedText()
	want := "你好世界"
	if got != want {
		t.Errorf("full line selection: got %q, want %q", got, want)
	}
}

// Selection that ends at column 0 of the next line captures the newline but
// none of the next line's characters.
func TestCopyUTF8EndAtNextLineStart(t *testing.T) {
	b := NewBuffer("你好\n世界")
	b.SetSelection(Cursor{0, 0}, Cursor{0, 1})
	got := b.GetSelectedText()
	want := "\n" // no chars from line 0 start, no chars from line 1 start
	// Actually: y=0 start.y, x1=0, x2=len("你好")=2 (not end.y). Write "你好", then '\n'.
	// y=1 end.y, x1=0, x2=end.x=0. x1<x2 false, nothing.
	want = "你好\n"
	if got != want {
		t.Errorf("end at next-line col 0: got %q, want %q", got, want)
	}
}

// Full middle lines: start mid-line on line 0, end mid-line on line 3.
// Lines 1 and 2 must be captured in their entirety.
func TestCopyUTF8FullMiddleLines(t *testing.T) {
	b := NewBuffer("SKIP\n你好世界\n完整复制\nSKIP")
	b.SetSelection(Cursor{4, 0}, Cursor{0, 3})
	got := b.GetSelectedText()
	want := "\n你好世界\n完整复制\n"
	if got != want {
		t.Errorf("full middle lines: got %q, want %q", got, want)
	}
}

// Combining characters: each codepoint is one rune and addressable separately.
// "é" is e + combining acute accent = 2 runes.
func TestCopyCombiningChars(t *testing.T) {
	b := NewBuffer("é café") // second é is a single precomposed rune
	b.SetSelection(Cursor{0, 0}, Cursor{2, 0})
	got := b.GetSelectedText()
	if got != "é" {
		t.Errorf("combining chars full: got %q, want %q", got, "é")
	}
	// Select just the combining mark: rune index 1.
	b.SetSelection(Cursor{1, 0}, Cursor{2, 0})
	got2 := b.GetSelectedText()
	if got2 != "́" {
		t.Errorf("combining mark only: got %q, want %q", got2, "́")
	}
}

// ── IsSelected multi-line boundaries ─────────────────────────────────────────

// On the start line, only columns ≥ start.x are selected.
// On the end line, only columns < end.x are selected.
// Middle lines are fully selected regardless of column.
func TestIsSelectedUTF8MultiLineBoundaries(t *testing.T) {
	b := NewBuffer("你好世界\n一二三四\n ABCD")
	// Select from "世" (line 0, col 2) to "三" (line 2, col 2).
	b.SetSelection(Cursor{2, 0}, Cursor{2, 2})

	// Line 0 (start line): col 1 not selected, col 2 and 3 selected.
	if b.IsSelected(1, 0) {
		t.Error("line 0 col 1 should not be selected")
	}
	if !b.IsSelected(2, 0) {
		t.Error("line 0 col 2 should be selected")
	}
	if !b.IsSelected(3, 0) {
		t.Error("line 0 col 3 should be selected")
	}

	// Line 1 (middle): every column is selected.
	for x := 0; x <= 4; x++ {
		if !b.IsSelected(x, 1) {
			t.Errorf("line 1 col %d should be selected (middle line)", x)
		}
	}

	// Line 2 (end line): col 0 and 1 selected, col 2 not.
	if !b.IsSelected(0, 2) {
		t.Error("line 2 col 0 should be selected")
	}
	if !b.IsSelected(1, 2) {
		t.Error("line 2 col 1 should be selected")
	}
	if b.IsSelected(2, 2) {
		t.Error("line 2 col 2 should not be selected (exclusive end)")
	}
}

// ── RuneOffsetToCursor edge cases ─────────────────────────────────────────────

// Offset equal to Len() should clamp to end of last line, not panic.
func TestRuneOffsetAtLen(t *testing.T) {
	b := NewBuffer("你好")
	n := b.Len()
	c := b.RuneOffsetToCursor(n)
	if c.x != 2 || c.y != 0 {
		t.Errorf("RuneOffsetToCursor(Len()) = %v, want {2 0}", c)
	}
}

// Large offset beyond Len() should also clamp gracefully.
func TestRuneOffsetBeyondLen(t *testing.T) {
	b := NewBuffer("你好\nABC")
	c := b.RuneOffsetToCursor(999)
	last := len(b.lines) - 1
	if c.y != last || c.x != len(b.lines[last]) {
		t.Errorf("RuneOffsetToCursor(999) = %v, want end of buffer", c)
	}
}

// Binary search on a long Chinese line must land at the exact rune position.
func TestRuneOffsetLongChineseLine(t *testing.T) {
	const n = 200
	runes := make([]rune, n)
	for i := range runes {
		runes[i] = '你' + rune(i%50)
	}
	b := NewBuffer(string(runes))
	for off := 0; off <= n; off++ {
		c := b.RuneOffsetToCursor(off)
		if c.y != 0 || c.x != min(off, n) {
			t.Errorf("long line: RuneOffsetToCursor(%d) = %v, want {%d 0}", off, c, min(off, n))
		}
	}
}

// ── RunesInRange edge cases ───────────────────────────────────────────────────

func TestRunesInRangeEmptyInterval(t *testing.T) {
	b := NewBuffer("你好世界")
	if got := b.RunesInRange(2, 2); got != nil {
		t.Errorf("RunesInRange(2,2) = %v, want nil", got)
	}
}

func TestRunesInRangeClampsBeyondLen(t *testing.T) {
	b := NewBuffer("你好")
	got := string(b.RunesInRange(0, 999))
	if got != "你好" {
		t.Errorf("RunesInRange clamped: got %q, want %q", got, "你好")
	}
}

// ── Search edge cases ─────────────────────────────────────────────────────────

// Search wraps: cursor is past the only match, so pass 2 must find it.
func TestSearchUTF8Wraps(t *testing.T) {
	b := NewBuffer("第一行\n你好\n第三行")
	// Start on line 2 — past the match on line 1.
	_, sel, ok := Search(b, "你好", Cursor{0, 2})
	if !ok {
		t.Fatal("Search did not wrap and find '你好'")
	}
	if sel.Start.y != 1 || sel.Start.x != 0 {
		t.Errorf("wrap: start = {x:%d, y:%d}, want {x:0, y:1}", sel.Start.x, sel.Start.y)
	}
	if sel.End.x != 2 {
		t.Errorf("wrap: end.x = %d, want 2", sel.End.x)
	}
}

// Match is on the same line but before the cursor; pass 2 must find it.
func TestSearchUTF8SameLineBeforeCursor(t *testing.T) {
	b := NewBuffer("一你好三")
	// cursor at col 3 (past "你好"); word "你好" is at col 1.
	_, sel, ok := Search(b, "你好", Cursor{3, 0})
	if !ok {
		t.Fatal("Search did not find '你好' before cursor on same line")
	}
	if sel.Start.x != 1 || sel.End.x != 3 {
		t.Errorf("same-line before cursor: start.x=%d end.x=%d, want 1 and 3", sel.Start.x, sel.End.x)
	}
}

// Single Chinese character as search word.
func TestSearchUTF8SingleCharWord(t *testing.T) {
	b := NewBuffer("abcdef你好世界")
	// "世" is at rune index 8 (6 ASCII + 你(6) 好(7) 世(8)).
	_, sel, ok := Search(b, "世", Cursor{0, 0})
	if !ok {
		t.Fatal("Search did not find '世'")
	}
	if sel.Start.x != 8 || sel.End.x != 9 {
		t.Errorf("single char search: start.x=%d end.x=%d, want 8 and 9", sel.Start.x, sel.End.x)
	}
}

// GetTextInSelection with Active=false must return "".
func TestGetTextInSelectionInactive(t *testing.T) {
	b := NewBuffer("你好世界")
	// SetSelection activates it; ClearSelection deactivates.
	b.SetSelection(Cursor{1, 0}, Cursor{3, 0})
	b.ClearSelection()
	if got := b.GetSelectedText(); got != "" {
		t.Errorf("inactive selection: got %q, want empty", got)
	}
}

// ── mixed Chinese/ASCII cases ─────────────────────────────────────────────────
//
// These cases place Chinese characters BEFORE the selection start or search
// target so that byte offset ≠ rune offset at the boundary. A regression to
// byte-based indexing would produce garbled output or wrong cursor positions.

// Selection starting after a Chinese prefix on a single line.
// "你好hello": 你(0)好(1)h(2)e(3)l(4)l(5)o(6).
// Selecting runes 2–6 must yield "hello", not bytes 2–6 of the UTF-8 string.
func TestCopyMixedChinesePrefixSingleLine(t *testing.T) {
	b := NewBuffer("你好hello")
	b.SetSelection(Cursor{2, 0}, Cursor{6, 0})
	got := b.GetSelectedText()
	want := "hell"
	if got != want {
		t.Errorf("Chinese prefix, ASCII selection: got %q, want %q", got, want)
	}
}

// ASCII surrounded by Chinese blocks: "abc你好def".
// a(0)b(1)c(2)你(3)好(4)d(5)e(6)f(7).
// Select runes 2–5 (c, 你, 好, d) — boundary lands on both sides of the block.
func TestCopyMixedASCIISurroundsChineseBlock(t *testing.T) {
	b := NewBuffer("abc你好def")
	b.SetSelection(Cursor{2, 0}, Cursor{6, 0})
	got := b.GetSelectedText()
	want := "c你好d"
	if got != want {
		t.Errorf("ASCII surrounds Chinese: got %q, want %q", got, want)
	}
}

// Interleaved pattern: every other rune switches script.
// "a你b好c世d界e": a(0)你(1)b(2)好(3)c(4)世(5)d(6)界(7)e(8).
// Select runes 1–7 → "你b好c世d".
func TestCopyMixedInterleaved(t *testing.T) {
	b := NewBuffer("a你b好c世d界e")
	b.SetSelection(Cursor{1, 0}, Cursor{7, 0})
	got := b.GetSelectedText()
	want := "你b好c世d"
	if got != want {
		t.Errorf("interleaved: got %q, want %q", got, want)
	}
}

// Multi-line: line 0 is Chinese-then-ASCII, line 1 is ASCII-then-Chinese.
// Selection starts in the ASCII tail of line 0 and ends in the ASCII body
// of line 1. Every byte-indexed slice would be wrong for line 0.
// Line 0: "你好hello" — 你(0)好(1)h(2)e(3)l(4)l(5)o(6)
// Line 1: "world世界" — w(0)o(1)r(2)l(3)d(4)世(5)界(6)
// Select {2,0}→{5,1}: "hello\nworld"
func TestCopyMixedMultiLineCrossCharsets(t *testing.T) {
	b := NewBuffer("你好hello\nworld世界")
	b.SetSelection(Cursor{2, 0}, Cursor{5, 1})
	got := b.GetSelectedText()
	want := "hello\nworld"
	if got != want {
		t.Errorf("cross-charset multi-line: got %q, want %q", got, want)
	}
}

// Full middle lines with mixed content: start and end land inside mixed lines,
// and the middle lines must be captured entirely regardless of their charset mix.
// Line 0: "SKIP你好"   S(0)K(1)I(2)P(3)你(4)好(5)
// Line 1: "hello世界"  h(0)…o(4)世(5)界(6)
// Line 2: "好ab"       好(0)a(1)b(2)
// Select {4,0}→{0,2}: captures tail of line 0, all of line 1, nothing from line 2.
func TestCopyMixedFullMiddleLines(t *testing.T) {
	b := NewBuffer("SKIP你好\nhello世界\n好ab")
	b.SetSelection(Cursor{4, 0}, Cursor{0, 2})
	got := b.GetSelectedText()
	want := "你好\nhello世界\n"
	if got != want {
		t.Errorf("mixed full middle lines: got %q, want %q", got, want)
	}
}

// Search: ASCII target preceded by Chinese on the same line.
// "你好world": 你(0)好(1)w(2)o(3)r(4)l(5)d(6).
// "world" starts at rune 2; byte offset of 'w' is 6 (two 3-byte chars before it).
func TestSearchMixedASCIIAfterChinesePrefix(t *testing.T) {
	b := NewBuffer("你好world")
	_, sel, ok := Search(b, "world", Cursor{0, 0})
	if !ok {
		t.Fatal("Search did not find 'world'")
	}
	if sel.Start.x != 2 {
		t.Errorf("start.x = %d, want 2 (rune index, not byte offset 6)", sel.Start.x)
	}
	if sel.End.x != 7 {
		t.Errorf("end.x = %d, want 7", sel.End.x)
	}
}

// Search: target word spans Chinese→ASCII boundary ("好w").
// "你hello好world": 你(0)h(1)e(2)l(3)l(4)o(5)好(6)w(7)…
// "好w" at rune 6; byte offset is 3+5=8 (一3-byte char + 5 ASCII).
func TestSearchMixedWordSpanningCharsets(t *testing.T) {
	b := NewBuffer("你hello好world")
	_, sel, ok := Search(b, "好w", Cursor{0, 0})
	if !ok {
		t.Fatal("Search did not find '好w'")
	}
	if sel.Start.x != 6 {
		t.Errorf("start.x = %d, want 6 (rune), not 8 (byte)", sel.Start.x)
	}
	if sel.End.x != 8 {
		t.Errorf("end.x = %d, want 8", sel.End.x)
	}
}

// Search: the start-cursor falls after Chinese characters, so the rune-based
// start offset (sx) must NOT be mistaken for a byte offset when scanning.
// "你好abc你好": 你(0)好(1)a(2)b(3)c(4)你(5)好(6).
// Cursor at {1,0} → startRX=2. Searching for "abc" from rune 2.
// As bytes, rune-2 is byte-6 (two 3-byte chars). Treating sx=2 as a byte
// offset on the old string would land inside "好", miss "abc", return wrong.
func TestSearchMixedStartOffsetAfterChinese(t *testing.T) {
	b := NewBuffer("你好abc你好")
	_, sel, ok := Search(b, "abc", Cursor{1, 0})
	if !ok {
		t.Fatal("Search did not find 'abc'")
	}
	if sel.Start.x != 2 {
		t.Errorf("start.x = %d, want 2 (rune), not 7 (byte-based error)", sel.Start.x)
	}
	if sel.End.x != 5 {
		t.Errorf("end.x = %d, want 5", sel.End.x)
	}
}

// Search wraps past end of buffer and finds a Chinese match on an earlier line.
// Cursor on last line; target is on a mixed line earlier in the buffer.
func TestSearchMixedWrapsToChineseLine(t *testing.T) {
	b := NewBuffer("abc你好def\nABCDEF")
	// "你好" is at rune 3 on line 0. Start on line 1, so pass 1 misses it.
	_, sel, ok := Search(b, "你好", Cursor{0, 1})
	if !ok {
		t.Fatal("Search did not wrap and find '你好'")
	}
	if sel.Start.y != 0 || sel.Start.x != 3 {
		t.Errorf("wrap to Chinese: start={x:%d,y:%d}, want {x:3,y:0}", sel.Start.x, sel.Start.y)
	}
	if sel.End.x != 5 {
		t.Errorf("wrap to Chinese: end.x=%d, want 5", sel.End.x)
	}
}

// Search result positions must be usable for IsSelected without mismatch.
// "你好abc世界": 你(0)好(1)a(2)b(3)c(4)世(5)界(6).
// After searching "abc", runes 2–5 must be reported as selected and others not.
func TestSearchMixedThenIsSelected(t *testing.T) {
	b := NewBuffer("你好abc世界")
	_, sel, ok := Search(b, "abc", Cursor{0, 0})
	if !ok {
		t.Fatal("Search did not find 'abc'")
	}
	b.selection = sel

	notSelected := []int{0, 1, 5, 6}
	selected := []int{2, 3, 4}

	for _, x := range notSelected {
		if b.IsSelected(x, 0) {
			t.Errorf("rune %d should NOT be selected after Search('abc')", x)
		}
	}
	for _, x := range selected {
		if !b.IsSelected(x, 0) {
			t.Errorf("rune %d should be selected after Search('abc')", x)
		}
	}
}

// RuneOffsetToCursor and CursorToRuneOffset on a buffer where each line mixes
// Chinese and ASCII so that byte and rune offsets diverge everywhere.
// "你A好B\nC世D界": 你(0)A(1)好(2)B(3) | C(0)世(1)D(2)界(3)
// lsruns: line0=0, line1=5.
func TestRuneOffsetMixedMultiLine(t *testing.T) {
	b := NewBuffer("你A好B\nC世D界")
	cases := []struct {
		off     int
		wantX   int
		wantY   int
		wantStr string
	}{
		{0, 0, 0, "你"},
		{1, 1, 0, "A"},
		{2, 2, 0, "好"},
		{3, 3, 0, "B"},
		{4, 4, 0, ""}, // end of line 0
		{5, 0, 1, "C"},
		{6, 1, 1, "世"},
		{7, 2, 1, "D"},
		{8, 3, 1, "界"},
	}
	for _, tc := range cases {
		c := b.RuneOffsetToCursor(tc.off)
		if c.x != tc.wantX || c.y != tc.wantY {
			t.Errorf("RuneOffsetToCursor(%d) = {x:%d y:%d}, want {x:%d y:%d}",
				tc.off, c.x, c.y, tc.wantX, tc.wantY)
		}
		if tc.wantStr != "" {
			got := b.RunesInRange(tc.off, tc.off+1)
			if string(got) != tc.wantStr {
				t.Errorf("RunesInRange(%d,%d) = %q, want %q", tc.off, tc.off+1, got, tc.wantStr)
			}
		}
		if tc.off < b.Len() {
			back := b.CursorToRuneOffset(c)
			if back != tc.off {
				t.Errorf("CursorToRuneOffset(RuneOffsetToCursor(%d)) = %d, want %d", tc.off, back, tc.off)
			}
		}
	}
}
