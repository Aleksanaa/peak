package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aleksana/peak/internal/quote"
	"github.com/aleksana/peak/internal/wevent"
	"github.com/gdamore/tcell/v3"
)

// A name with spaces is quoted in the tag and read back whole; renaming
// replaces only the name and keeps the rest of the tag as typed.
func TestTagNameWithSpaces(t *testing.T) {
	_, col := newTestEditorWithColumn(t)
	win := col.AddWindow(tagText("/tmp/my file.txt", "Get  Put"), "")
	if got := win.tag.buffer.GetText(); got != " `/tmp/my file.txt` Get  Put " {
		t.Errorf("tag = %q", got)
	}
	if got := win.GetFilename(); got != "/tmp/my file.txt" {
		t.Errorf("GetFilename = %q", got)
	}

	win.SetName("/tmp/other name.txt")
	if got := win.tag.buffer.GetText(); got != " `/tmp/other name.txt` Get  Put " {
		t.Errorf("tag after SetName = %q", got)
	}
}

// Right-clicking a quoted name in a directory listing opens that file.
func TestPlumbQuotedNameFromListing(t *testing.T) {
	e, col := newTestEditorWithColumn(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "my file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	listing, err := listDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if listing != "`my file.txt`" {
		t.Fatalf("listing = %q", listing)
	}
	win := e.createWindow(col, toDir(dir), listing, true, false, -1, 0)
	col.Resize(col.rect)

	tv := win.bodyTextView()
	_, _, word := clickRange(tv.buffer, tv.PosAt(5, 0)) // inside the quoted name
	if word != "my file.txt" {
		t.Fatalf("click word = %q", word)
	}
	e.Plumb(win, word)

	want := filepath.Join(dir, "my file.txt")
	deadline := time.Now().Add(3 * time.Second)
	for {
		var opened bool
		e.Call(func() {
			for _, w := range e.allWindows() {
				opened = opened || w.GetFilename() == want
			}
		})
		if opened {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no window opened for %q", want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// What /bind lists can be written back, and /unmount takes a listed name.
func TestBindListingRoundTrip(t *testing.T) {
	_, _, nsFs, _ := setupExecFsTest(t)
	src := filepath.Join(t.TempDir(), "a dir")
	if err := os.Mkdir(src, 0755); err != nil {
		t.Fatal(err)
	}
	dst := "/peak/my bind"
	writeControl(t, nsFs, "bind", "`"+src+"` `"+dst+"`\n")

	f, err := nsFs.OpenFile("bind", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(f)
	f.Close()
	listing := strings.TrimSpace(string(data))
	if want := "`" + src + "/` `" + dst + "/`"; listing != want {
		t.Fatalf("bind listing = %q, want %q", listing, want)
	}

	listedDst := quote.Quote(quote.Fields(listing)[1])
	writeControl(t, nsFs, "unmount", listedDst+"\n")
	if mp, _ := ns.FindMount(dst); mp == dst {
		t.Errorf("%q still mounted after unmounting its listed name", dst)
	}
}

// Middle-clicking quoted text runs its contents, as if they were selected:
// `Tab 7` runs Tab with the argument 7, whether the click is on the quoted
// text or on a selection of exactly it. The click event reports the contents
// and their range, inside the backticks.
func TestExecuteQuotedClick(t *testing.T) {
	for _, selected := range []bool{false, true} {
		e, col := newTestEditorWithColumn(t)
		win := col.AddWindow(tagText("/tmp/exec.txt", "Del"), "run `Tab 7` here")
		col.Resize(col.rect)
		tv := win.bodyTextView()
		if selected {
			tv.buffer.SetDot(4, 11)
		}
		sub := win.events.subscribe()

		p := screenAt(e, tv)
		e.HandleEvent(tcell.NewEventMouse(p.X+8, p.Y, tcell.ButtonMiddle, 0))
		e.HandleEvent(tcell.NewEventMouse(p.X+8, p.Y, tcell.ButtonNone, 0))

		if tv.tabWidth != 7 {
			t.Errorf("selected=%v: tab width = %d, want 7 (the click should run \"Tab 7\")", selected, tv.tabWidth)
		}
		ev, err := wevent.Read(bytes.NewReader(<-sub.ch))
		if err != nil {
			t.Fatal(err)
		}
		if ev.Type != 'x' || ev.Text != "Tab 7" || ev.Q0 != 5 || ev.Q1 != 10 {
			t.Errorf("selected=%v: event = %c %q [%d, %d), want x \"Tab 7\" [5, 10)", selected, ev.Type, ev.Text, ev.Q0, ev.Q1)
		}
	}
}

// A command's argument is the text after its name: free-text commands take it
// as is, others read it as quoted names.
func TestCommandArguments(t *testing.T) {
	e, _ := newTestEditorWithColumn(t)
	texts := map[string]string{
		"Win echo `date`": "echo `date`",
		"Edit s/a  b/c/":  "s/a  b/c/",
		"Look `foo` bar":  "`foo` bar",
		"`Win` btop":      "btop",
	}
	for cmd, want := range texts {
		if got := e.argText(nil, cmd); got != want {
			t.Errorf("argText(%q) = %q, want %q", cmd, got, want)
		}
	}
	names := map[string]string{
		"Get my file.txt":    "my file.txt",
		"Get `my  file.txt`": "my  file.txt",
		"Get a``b":           "a`b",
	}
	for cmd, want := range names {
		if got := e.argName(nil, cmd); got != want {
			t.Errorf("argName(%q) = %q, want %q", cmd, got, want)
		}
	}
	if got := e.argFields(nil, "Mount `/srv/a b` /mnt"); !reflect.DeepEqual(got, []string{"/srv/a b", "/mnt"}) {
		t.Errorf("argFields = %q", got)
	}
}

// A click stands for one range, and the text it reports is that range's.
func TestClickRange(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		sel    [2]int // dot
		at     int    // the rune offset clicked
		q0, q1 int
		want   string
	}{
		{"word", "run foo bar", [2]int{}, 9, 8, 11, "bar"},
		{"in the selection", "run foo bar here", [2]int{4, 11}, 9, 4, 11, "foo bar"},
		{"blank, with a selection", "run foo  bar", [2]int{0, 3}, 8, 0, 3, "run"},
		{"selection trimmed", "a  foo  b", [2]int{1, 8}, 4, 3, 6, "foo"},
		{"quoted", "run `Tab 7` x", [2]int{}, 6, 5, 10, "Tab 7"},
		{"selected quote", "run `Tab 7` x", [2]int{4, 11}, 6, 5, 10, "Tab 7"},
		{"doubled backtick", "x a``b y", [2]int{}, 2, 2, 6, "a`b"},
	}
	for _, tt := range tests {
		b := NewBuffer(tt.text)
		b.SetDot(tt.sel[0], tt.sel[1])
		q0, q1, text := clickRange(b, tt.at)
		if q0 != tt.q0 || q1 != tt.q1 || text != tt.want {
			t.Errorf("%s: clickRange = [%d, %d) %q, want [%d, %d) %q", tt.name, q0, q1, text, tt.q0, tt.q1, tt.want)
		}
	}
}
