package terminal

import (
	"bytes"
	"fmt"
	"testing"
)

// TestFullScrollOrder floods more lines than the buffer is tall, forcing many
// full-buffer scrolls (the ring fast path), and checks that the visible rows
// hold the most recent lines in the correct order.
func TestFullScrollOrder(t *testing.T) {
	var st State
	vt, err := Create(&st, nil)
	if err != nil {
		t.Fatal(err)
	}
	vt.Resize(20, 10) // 10 rows tall

	// Print line0..line19; a \r\n after every line except the last. With 10
	// rows, the visible buffer should end up holding line10..line19.
	for i := 0; i < 20; i++ {
		if i > 0 {
			vt.Write([]byte("\r\n"))
		}
		vt.Write([]byte(fmt.Sprintf("line%d", i)))
	}

	for y := 0; y < 10; y++ {
		want := fmt.Sprintf("line%d", y+10)
		got := trimStr(extractStr(&st, 0, 19, y))
		if got != want {
			t.Errorf("row %d = %q, want %q", y, got, want)
		}
	}
}

// TestRegionScrollUntouched verifies that a DECSTBM scroll region uses the
// row-swap path and leaves rows outside the region intact.
func TestRegionScrollUntouched(t *testing.T) {
	var st State
	vt, err := Create(&st, nil)
	if err != nil {
		t.Fatal(err)
	}
	vt.Resize(20, 6)

	// Fill 6 rows with markers A..F.
	for i := 0; i < 6; i++ {
		vt.Write([]byte(fmt.Sprintf("\033[%d;1H", i+1))) // CUP row i+1, col 1
		vt.Write([]byte{byte('A' + i)})
	}
	// Scroll region rows 2..4 (1-based), then scroll it up once via RI-free
	// path: put cursor at bottom of region and emit a newline.
	vt.Write([]byte("\033[2;4r")) // top=1 bottom=3 (0-based)
	vt.Write([]byte("\033[4;1H")) // cursor to last region row
	vt.Write([]byte("\n"))        // scroll region up by 1

	// Row 0 (A) and row 5 (F) are outside the region and must be untouched.
	if got := firstRune(&st, 0); got != 'A' {
		t.Errorf("row 0 = %q, want 'A'", got)
	}
	if got := firstRune(&st, 5); got != 'F' {
		t.Errorf("row 5 = %q, want 'F'", got)
	}
	// Region shifted up: old row 2 content 'C' now on row 1.
	if got := firstRune(&st, 1); got != 'C' {
		t.Errorf("row 1 = %q, want 'C' after region scroll", got)
	}
}

func firstRune(t *State, row int) rune {
	c, _, _, _ := t.Cell(0, row)
	return c
}

func trimStr(s string) string {
	return string(bytes.TrimRight([]byte(s), " \x00"))
}

func scrollPayload(lines int) []byte {
	var buf bytes.Buffer
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&buf, "The quick brown fox jumps over the lazy dog %010d\r\n", i)
	}
	return buf.Bytes()
}

// BenchmarkScrollTall mirrors peak's real config: a tall history buffer
// (maxHistory) with newline-flooding, the case Cause 1 targets.
func BenchmarkScrollTall(b *testing.B) {
	payload := scrollPayload(2000)
	var st State
	vt, _ := Create(&st, nil)
	vt.Resize(80, 1000)
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vt.Write(payload)
	}
}

// BenchmarkScrollScreen is the same stream against a normal 80x24 screen.
func BenchmarkScrollScreen(b *testing.B) {
	payload := scrollPayload(2000)
	var st State
	vt, _ := Create(&st, nil)
	vt.Resize(80, 24)
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vt.Write(payload)
	}
}
