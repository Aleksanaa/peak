package terminal

import (
	"io"
	"log"
	"sync"
	"unicode"

	uwidth "golang.org/x/text/width"
)

const (
	tabspaces = 8
)

const (
	AttrReverse = 1 << iota
	AttrUnderline
	AttrBold
	AttrGfx
	AttrItalic
	AttrBlink
	AttrWrap
)

const (
	cursorDefault = 1 << iota
	cursorWrapNext
	cursorOrigin
)

// ModeFlag represents various terminal mode states.
type ModeFlag uint32

// Terminal modes
const (
	ModeWrap ModeFlag = 1 << iota
	ModeInsert
	ModeAppKeypad
	ModeAltScreen
	ModeCRLF
	ModeMouseButton
	ModeMouseMotion
	ModeReverse
	ModeKeyboardLock
	ModeHide
	ModeEcho
	ModeAppCursor
	ModeMouseSgr
	Mode8bit
	ModeBlink
	ModeFBlink
	ModeFocus
	ModeMouseX10
	ModeMouseMany
	ModeMouseMask = ModeMouseButton | ModeMouseMotion | ModeMouseX10 | ModeMouseMany
)

// ChangeFlag represents possible state changes of the terminal.
type ChangeFlag uint32

// Terminal changes to occur in VT.ReadState
const (
	ChangedScreen ChangeFlag = 1 << iota
	ChangedTitle
)

type glyph struct {
	c      rune
	mode   int16
	fg, bg Color
}

type line []glyph

type cursor struct {
	attr  glyph
	x, y  int
	state uint8
}

// parseState selects which byte-parsing routine put dispatches to. It was
// previously a stored method value (func(rune)), but assigning a method value
// on every state transition heap-allocates a closure; an enum + switch avoids
// that per-byte allocation entirely.
type parseState uint8

const (
	stParse parseState = iota
	stEsc
	stEscCSI
	stEscStr
	stEscStrEnd
	stEscAltCharset
	stEscTest
)

// State represents the terminal emulation state. Use Lock/Unlock
// methods to synchronize data access with VT.
type State struct {
	DebugLogger    *log.Logger
	ResponseWriter io.Writer
	OnCWD          func(uri string) // if non-nil, called under mu with OSC 7 URI; must not block
	FGColor        Color            // actual RGB for DefaultFG (used for OSC 10 queries)
	BGColor        Color            // actual RGB for DefaultBG (used for OSC 11 queries)

	mu            sync.Mutex
	changed       ChangeFlag
	cols, rows    int
	lines         []line
	altLines      []line
	head, altHead int    // ring offset: logical row 0 lives at lines[head]
	dirty         []bool // line dirtiness
	anydirty      bool
	cur, curSaved cursor
	top, bottom   int // scroll limits
	mode          ModeFlag
	state         parseState
	str           strEscape
	csi           csiEscape
	numlock       bool
	tabs          []bool
	title         string
}

func (t *State) logf(format string, args ...interface{}) {
	if t.DebugLogger != nil {
		t.DebugLogger.Printf(format, args...)
	}
}

func (t *State) logln(s string) {
	if t.DebugLogger != nil {
		t.DebugLogger.Println(s)
	}
}

func (t *State) respond(s string) {
	if t.ResponseWriter != nil {
		t.ResponseWriter.Write([]byte(s))
	}
}

func (t *State) oscFGColor() (r, g, b uint8) {
	if t.FGColor.IsRGB() {
		return t.FGColor.RGBComponents()
	}
	return 0xcc, 0xcc, 0xcc
}

func (t *State) oscBGColor() (r, g, b uint8) {
	if t.BGColor.IsRGB() {
		return t.BGColor.RGBComponents()
	}
	return 0x00, 0x00, 0x00
}

func (t *State) lock() {
	t.mu.Lock()
}

func (t *State) unlock() {
	t.mu.Unlock()
}

// Lock locks the state object's mutex.
func (t *State) Lock() {
	t.mu.Lock()
}

// Unlock resets change flags and unlocks the state object's mutex.
func (t *State) Unlock() {
	t.resetChanges()
	t.mu.Unlock()
}

// bmpWidth caches the display width (0, 1 or 2) of every Basic Multilingual
// Plane rune, so the common case is a single array lookup instead of three
// Unicode table searches. Built once at init from the same data as widthSlow.
var bmpWidth [0x10000]uint8

func init() {
	for r := rune(0); r < 0x10000; r++ {
		bmpWidth[r] = uint8(widthSlow(r))
	}
}

// widthSlow computes a rune's display width via the Unicode tables.
func widthSlow(r rune) int {
	if r < 32 {
		return 0
	}
	if r < 0x80 {
		return 1
	}
	if unicode.IsMark(r) || unicode.Is(unicode.Cf, r) {
		return 1
	}
	k := uwidth.LookupRune(r).Kind()
	if k == uwidth.EastAsianWide || k == uwidth.EastAsianFullwidth {
		return 2
	}
	return 1
}

func (t *State) runeWidth(r rune) int {
	if r < 0x80 { // printable ASCII fast path (also covers control chars)
		if r < 32 {
			return 0
		}
		return 1
	}
	if r < rune(len(bmpWidth)) {
		return int(bmpWidth[r])
	}
	return widthSlow(r) // astral planes: rare, compute on demand
}

// rowIdx maps a logical row y (0 == top of the buffer) to its physical index
// in the ring-backed lines slice. Valid for 0 <= y < t.rows.
func (t *State) rowIdx(y int) int {
	i := t.head + y
	if i >= t.rows {
		i -= t.rows
	}
	return i
}

// row returns the backing line for logical row y. Mutating elements of the
// returned slice mutates the buffer in place.
func (t *State) row(y int) line {
	return t.lines[t.rowIdx(y)]
}

// Cell returns the character code, foreground color, background
// color and mode at position (x, y) relative to the top left of the terminal.
func (t *State) Cell(x, y int) (ch rune, fg Color, bg Color, mode int16) {
	ln := t.row(y)
	return ln[x].c, Color(ln[x].fg), Color(ln[x].bg), ln[x].mode
}

// Cursor returns the current position of the cursor.
func (t *State) Cursor() (int, int) {
	return t.cur.x, t.cur.y
}

// CursorVisible returns the visible state of the cursor.
func (t *State) CursorVisible() bool {
	return t.mode&ModeHide == 0
}

// Mode tests if mode is currently set.
func (t *State) Mode(mode ModeFlag) bool {
	return t.mode&mode != 0
}

// Title returns the current title set via the tty.
func (t *State) Title() string {
	return t.title
}

/*
// ChangeMask returns a bitfield of changes that have occured by VT.
func (t *State) ChangeMask() ChangeFlag {
	return t.changed
}
*/

// Changed returns true if change has occured.
func (t *State) Changed(change ChangeFlag) bool {
	return t.changed&change != 0
}

// resetChanges resets the change mask and dirtiness.
func (t *State) resetChanges() {
	for i := range t.dirty {
		t.dirty[i] = false
	}
	t.anydirty = false
	t.changed = 0
}

func (t *State) saveCursor() {
	t.curSaved = t.cur
}

func (t *State) restoreCursor() {
	t.cur = t.curSaved
	t.moveTo(t.cur.x, t.cur.y)
}

func (t *State) put(c rune) {
	switch t.state {
	case stParse:
		t.parse(c)
	case stEsc:
		t.parseEsc(c)
	case stEscCSI:
		t.parseEscCSI(c)
	case stEscStr:
		t.parseEscStr(c)
	case stEscStrEnd:
		t.parseEscStrEnd(c)
	case stEscAltCharset:
		t.parseEscAltCharset(c)
	case stEscTest:
		t.parseEscTest(c)
	}
}

func (t *State) putTab(forward bool) {
	x := t.cur.x
	if forward {
		if x == t.cols {
			return
		}
		for x++; x < t.cols && !t.tabs[x]; x++ {
		}
	} else {
		if x == 0 {
			return
		}
		for x--; x > 0 && !t.tabs[x]; x-- {
		}
	}
	t.moveTo(x, t.cur.y)
}

func (t *State) newline(firstCol bool) {
	y := t.cur.y
	if y == t.bottom {
		cur := t.cur
		t.cur = t.defaultCursor()
		t.ScrollUp(t.top, 1)
		t.cur = cur
	} else {
		y++
	}
	if firstCol {
		t.moveTo(0, y)
	} else {
		t.moveTo(t.cur.x, y)
	}
}

// table from st, which in turn is from rxvt :)
var gfxCharTable = [62]rune{
	'↑', '↓', '→', '←', '█', '▚', '☃', // A - G
	0, 0, 0, 0, 0, 0, 0, 0, // H - O
	0, 0, 0, 0, 0, 0, 0, 0, // P - W
	0, 0, 0, 0, 0, 0, 0, ' ', // X - _
	'◆', '▒', '␉', '␌', '␍', '␊', '°', '±', // ` - g
	'␤', '␋', '┘', '┐', '┌', '└', '┼', '⎺', // h - o
	'⎻', '─', '⎼', '⎽', '├', '┤', '┴', '┬', // p - w
	'│', '≤', '≥', 'π', '≠', '£', '·', // x - ~
}

func (t *State) setChar(c rune, attr *glyph, x, y int) {
	if attr.mode&AttrGfx != 0 {
		if c >= 0x41 && c <= 0x7e && gfxCharTable[c-0x41] != 0 {
			c = gfxCharTable[c-0x41]
		}
	}
	t.changed |= ChangedScreen
	t.dirty[y] = true
	ln := t.row(y)
	ln[x] = *attr
	ln[x].c = c
	//if t.options.BrightBold && attr.mode&AttrBold != 0 && attr.fg < 8 {
	if attr.mode&AttrBold != 0 && attr.fg < 8 {
		ln[x].fg = attr.fg + 8
	}
	if attr.mode&AttrReverse != 0 {
		ln[x].fg = attr.bg
		ln[x].bg = attr.fg
	}
}

func (t *State) defaultCursor() cursor {
	c := cursor{}
	c.attr.fg = DefaultFG
	c.attr.bg = DefaultBG
	return c
}

func (t *State) reset() {
	t.cur = t.defaultCursor()
	t.saveCursor()
	for i := range t.tabs {
		t.tabs[i] = false
	}
	for i := tabspaces; i < len(t.tabs); i += tabspaces {
		t.tabs[i] = true
	}
	t.top = 0
	t.bottom = t.rows - 1
	t.mode = ModeWrap
	t.clear(0, 0, t.rows-1, t.cols-1)
	t.moveTo(0, 0)
}

// TODO: definitely can improve allocs
func (t *State) resize(cols, rows int) bool {
	if cols == t.cols && rows == t.rows {
		return false
	}
	if cols < 1 || rows < 1 {
		return false
	}
	// Normalise the ring so physical order matches logical order; the resize
	// logic below (slide/copy) assumes lines[i] is logical row i.
	rotateLines(t.lines, t.head)
	rotateLines(t.altLines, t.altHead)
	t.head, t.altHead = 0, 0
	slide := t.cur.y - rows + 1
	if slide > 0 {
		copy(t.lines, t.lines[slide:slide+rows])
		copy(t.altLines, t.altLines[slide:slide+rows])
	}

	lines, altLines, tabs := t.lines, t.altLines, t.tabs
	t.lines = make([]line, rows)
	t.altLines = make([]line, rows)
	t.dirty = make([]bool, rows)
	t.tabs = make([]bool, cols)

	minrows := min(rows, t.rows)
	mincols := min(cols, t.cols)
	t.changed |= ChangedScreen
	for i := 0; i < rows; i++ {
		t.dirty[i] = true
		t.lines[i] = make(line, cols)
		t.altLines[i] = make(line, cols)
	}
	for i := 0; i < minrows; i++ {
		copy(t.lines[i], lines[i])
		copy(t.altLines[i], altLines[i])
	}
	copy(t.tabs, tabs)
	if cols > t.cols {
		i := t.cols - 1
		for i > 0 && !tabs[i] {
			i--
		}
		for i += tabspaces; i < len(tabs); i += tabspaces {
			tabs[i] = true
		}
	}

	t.cols = cols
	t.rows = rows
	t.setScroll(0, rows-1)
	t.moveTo(t.cur.x, t.cur.y)
	for i := 0; i < 2; i++ {
		if mincols < cols && minrows > 0 {
			t.clearRegion(mincols, 0, cols-1, minrows-1, DefaultFG, DefaultBG)
		}
		if cols > 0 && minrows < rows {
			t.clearRegion(0, minrows, cols-1, rows-1, DefaultFG, DefaultBG)
		}
		t.swapScreen()
	}
	return slide > 0
}

// rotateLines reorders s in place so that the element at index head becomes
// index 0, undoing a ring offset. O(len(s)); only used on resize.
func rotateLines(s []line, head int) {
	if head <= 0 || head >= len(s) {
		return
	}
	tmp := make([]line, len(s))
	for i := range s {
		j := head + i
		if j >= len(s) {
			j -= len(s)
		}
		tmp[i] = s[j]
	}
	copy(s, tmp)
}

func (t *State) clear(x0, y0, x1, y1 int) {
	t.clearRegion(x0, y0, x1, y1, t.cur.attr.fg, t.cur.attr.bg)
}

// clearRegion blanks a rectangle, filling each cell with a space and the given
// colors. Erase operations (ED/EL/ECH) pass the active pen so background-color
// erase works; resize passes the defaults, because cells newly exposed by a
// resize were never written by the application and must not inherit whatever
// background happened to be set — otherwise a full-screen app that colors its
// background (e.g. a TUI on the alternate screen) leaves a colored band behind
// on the primary screen after it exits.
func (t *State) clearRegion(x0, y0, x1, y1 int, fg, bg Color) {
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	x0 = clamp(x0, 0, t.cols-1)
	x1 = clamp(x1, 0, t.cols-1)
	y0 = clamp(y0, 0, t.rows-1)
	y1 = clamp(y1, 0, t.rows-1)
	t.changed |= ChangedScreen
	for y := y0; y <= y1; y++ {
		t.dirty[y] = true
		ln := t.row(y)
		for x := x0; x <= x1; x++ {
			ln[x].c = ' '
			ln[x].mode = 0
			ln[x].fg = fg
			ln[x].bg = bg
		}
	}
}

func (t *State) clearAll() {
	t.clear(0, 0, t.cols-1, t.rows-1)
}

func (t *State) moveAbsTo(x, y int) {
	if t.cur.state&cursorOrigin != 0 {
		y += t.top
	}
	t.moveTo(x, y)
}

func (t *State) moveTo(x, y int) {
	var miny, maxy int
	if t.cur.state&cursorOrigin != 0 {
		miny = t.top
		maxy = t.bottom
	} else {
		miny = 0
		maxy = t.rows - 1
	}
	x = clamp(x, 0, t.cols-1)
	y = clamp(y, miny, maxy)
	t.changed |= ChangedScreen
	t.cur.state &^= cursorWrapNext
	t.cur.x = x
	t.cur.y = y
}

func (t *State) swapScreen() {
	t.lines, t.altLines = t.altLines, t.lines
	t.head, t.altHead = t.altHead, t.head
	t.mode ^= ModeAltScreen
	t.dirtyAll()
}

func (t *State) dirtyAll() {
	t.changed |= ChangedScreen
	for y := 0; y < t.rows; y++ {
		t.dirty[y] = true
	}
}

func (t *State) setScroll(top, bottom int) {
	top = clamp(top, 0, t.rows-1)
	bottom = clamp(bottom, 0, t.rows-1)
	if top > bottom {
		top, bottom = bottom, top
	}
	t.top = top
	t.bottom = bottom
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clamp(val, min, max int) int {
	if val < min {
		return min
	} else if val > max {
		return max
	}
	return val
}

func between(val, min, max int) bool {
	if val < min || val > max {
		return false
	}
	return true
}

func (t *State) ScrollDown(orig, n int) {
	n = clamp(n, 0, t.bottom-orig+1)
	if n <= 0 {
		return
	}
	t.changed |= ChangedScreen
	if orig == 0 && t.bottom == t.rows-1 {
		// Full-buffer scroll: rotate the ring instead of shifting every row.
		// The n rows recycled from the bottom become blank rows at the top.
		if t.head -= n; t.head < 0 {
			t.head += t.rows
		}
		t.clear(0, 0, t.cols-1, n-1)
		return
	}
	t.clear(0, t.bottom-n+1, t.cols-1, t.bottom)
	for i := t.bottom; i >= orig+n; i-- {
		a, b := t.rowIdx(i), t.rowIdx(i-n)
		t.lines[a], t.lines[b] = t.lines[b], t.lines[a]
		t.dirty[i] = true
		t.dirty[i-n] = true
	}

	// TODO: selection scroll
}

func (t *State) ScrollUp(orig, n int) {
	n = clamp(n, 0, t.bottom-orig+1)
	if n <= 0 {
		return
	}
	t.changed |= ChangedScreen
	if orig == 0 && t.bottom == t.rows-1 {
		// Full-buffer scroll: rotate the ring instead of shifting every row.
		// The n rows recycled from the top become blank rows at the bottom.
		t.head = t.rowIdx(n)
		t.clear(0, t.rows-n, t.cols-1, t.rows-1)
		return
	}
	t.clear(0, orig, t.cols-1, orig+n-1)
	for i := orig; i <= t.bottom-n; i++ {
		a, b := t.rowIdx(i), t.rowIdx(i+n)
		t.lines[a], t.lines[b] = t.lines[b], t.lines[a]
		t.dirty[i] = true
		t.dirty[i+n] = true
	}

	// TODO: selection scroll
}

func (t *State) modMode(set bool, bit ModeFlag) {
	if set {
		t.mode |= bit
	} else {
		t.mode &^= bit
	}
}

func (t *State) setMode(priv bool, set bool, args []int) {
	if priv {
		for _, a := range args {
			switch a {
			case 1: // DECCKM - cursor key
				t.modMode(set, ModeAppCursor)
			case 5: // DECSCNM - reverse video
				mode := t.mode
				t.modMode(set, ModeReverse)
				if mode != t.mode {
					// TODO: redraw
				}
			case 6: // DECOM - origin
				if set {
					t.cur.state |= cursorOrigin
				} else {
					t.cur.state &^= cursorOrigin
				}
				t.moveAbsTo(0, 0)
			case 7: // DECAWM - auto wrap
				t.modMode(set, ModeWrap)
			// IGNORED:
			case 0, // error
				2,  // DECANM - ANSI/VT52
				3,  // DECCOLM - column
				4,  // DECSCLM - scroll
				8,  // DECARM - auto repeat
				18, // DECPFF - printer feed
				19, // DECPEX - printer extent
				42, // DECNRCM - national characters
				12: // att610 - start blinking cursor
				break
			case 25: // DECTCEM - text cursor enable mode
				t.modMode(!set, ModeHide)
			case 9: // X10 mouse compatibility mode
				t.modMode(false, ModeMouseMask)
				t.modMode(set, ModeMouseX10)
			case 1000: // report button press
				t.modMode(false, ModeMouseMask)
				t.modMode(set, ModeMouseButton)
			case 1002: // report motion on button press
				t.modMode(false, ModeMouseMask)
				t.modMode(set, ModeMouseMotion)
			case 1003: // enable all mouse motions
				t.modMode(false, ModeMouseMask)
				t.modMode(set, ModeMouseMany)
			case 1004: // send focus events to tty
				t.modMode(set, ModeFocus)
			case 1006: // extended reporting mode
				t.modMode(set, ModeMouseSgr)
			case 1034:
				t.modMode(set, Mode8bit)
			case 1049, // = 1047 and 1048
				47, 1047:
				alt := t.mode&ModeAltScreen != 0
				if alt {
					t.clear(0, 0, t.cols-1, t.rows-1)
				}
				if !set || !alt {
					t.swapScreen()
				}
				if a != 1049 {
					break
				}
				fallthrough
			case 1048:
				if set {
					t.saveCursor()
				} else {
					t.restoreCursor()
				}
			case 1001:
				// mouse highlight mode; can hang the terminal by design when
				// implemented
			case 1005:
				// utf8 mouse mode; will confuse applications not supporting
				// utf8 and luit
			case 1015:
				// urxvt mangled mouse mode; incompatiblt and can be mistaken
				// for other control codes
			default:
				t.logf("unknown private set/reset mode %d\n", a)
			}
		}
	} else {
		for _, a := range args {
			switch a {
			case 0: // Error (ignored)
			case 2: // KAM - keyboard action
				t.modMode(set, ModeKeyboardLock)
			case 4: // IRM - insertion-replacement
				t.modMode(set, ModeInsert)
				t.logln("insert mode not implemented")
			case 12: // SRM - send/receive
				t.modMode(set, ModeEcho)
			case 20: // LNM - linefeed/newline
				t.modMode(set, ModeCRLF)
			case 34:
				t.logln("right-to-left mode not implemented")
			case 96:
				t.logln("right-to-left copy mode not implemented")
			default:
				t.logf("unknown set/reset mode %d\n", a)
			}
		}
	}
}

func (t *State) setAttr(attr []int) {
	if len(attr) == 0 {
		attr = []int{0}
	}
	for i := 0; i < len(attr); i++ {
		a := attr[i]
		switch a {
		case 0:
			t.cur.attr.mode &^= AttrReverse | AttrUnderline | AttrBold | AttrItalic | AttrBlink
			t.cur.attr.fg = DefaultFG
			t.cur.attr.bg = DefaultBG
		case 1:
			t.cur.attr.mode |= AttrBold
		case 3:
			t.cur.attr.mode |= AttrItalic
		case 4:
			t.cur.attr.mode |= AttrUnderline
		case 5, 6: // slow, rapid blink
			t.cur.attr.mode |= AttrBlink
		case 7:
			t.cur.attr.mode |= AttrReverse
		case 21, 22:
			t.cur.attr.mode &^= AttrBold
		case 23:
			t.cur.attr.mode &^= AttrItalic
		case 24:
			t.cur.attr.mode &^= AttrUnderline
		case 25, 26:
			t.cur.attr.mode &^= AttrBlink
		case 27:
			t.cur.attr.mode &^= AttrReverse
		case 38:
			if i+2 < len(attr) {
				if attr[i+1] == 5 {
					i += 2
					if between(attr[i], 0, 255) {
						t.cur.attr.fg = Color(attr[i])
					} else {
						t.logf("bad fgcolor %d\n", attr[i])
					}
				} else if attr[i+1] == 2 && i+4 < len(attr) {
					t.cur.attr.fg = RGB(uint8(attr[i+2]), uint8(attr[i+3]), uint8(attr[i+4]))
					i += 4
				} else {
					t.logf("gfx attr %d unknown\n", a)
				}
			} else {
				t.logf("gfx attr %d unknown\n", a)
			}
		case 39:
			t.cur.attr.fg = DefaultFG
		case 48:
			if i+2 < len(attr) {
				if attr[i+1] == 5 {
					i += 2
					if between(attr[i], 0, 255) {
						t.cur.attr.bg = Color(attr[i])
					} else {
						t.logf("bad bgcolor %d\n", attr[i])
					}
				} else if attr[i+1] == 2 && i+4 < len(attr) {
					t.cur.attr.bg = RGB(uint8(attr[i+2]), uint8(attr[i+3]), uint8(attr[i+4]))
					i += 4
				} else {
					t.logf("gfx attr %d unknown\n", a)
				}
			} else {
				t.logf("gfx attr %d unknown\n", a)
			}
		case 49:
			t.cur.attr.bg = DefaultBG
		default:
			if between(a, 30, 37) {
				t.cur.attr.fg = Color(a - 30)
			} else if between(a, 40, 47) {
				t.cur.attr.bg = Color(a - 40)
			} else if between(a, 90, 97) {
				t.cur.attr.fg = Color(a - 90 + 8)
			} else if between(a, 100, 107) {
				t.cur.attr.bg = Color(a - 100 + 8)
			} else {
				t.logf("gfx attr %d unknown\n", a)
			}
		}
	}
}

func (t *State) insertBlanks(n int) {
	src := t.cur.x
	dst := src + n
	size := t.cols - dst
	t.changed |= ChangedScreen
	t.dirty[t.cur.y] = true

	if dst >= t.cols {
		t.clear(t.cur.x, t.cur.y, t.cols-1, t.cur.y)
	} else {
		ln := t.row(t.cur.y)
		copy(ln[dst:dst+size], ln[src:src+size])
		t.clear(src, t.cur.y, dst-1, t.cur.y)
	}
}

func (t *State) insertBlankLines(n int) {
	if t.cur.y < t.top || t.cur.y > t.bottom {
		return
	}
	t.ScrollDown(t.cur.y, n)
}

func (t *State) deleteLines(n int) {
	if t.cur.y < t.top || t.cur.y > t.bottom {
		return
	}
	t.ScrollUp(t.cur.y, n)
}

func (t *State) deleteChars(n int) {
	src := t.cur.x + n
	dst := t.cur.x
	size := t.cols - src
	t.changed |= ChangedScreen
	t.dirty[t.cur.y] = true

	if src >= t.cols {
		t.clear(t.cur.x, t.cur.y, t.cols-1, t.cur.y)
	} else {
		ln := t.row(t.cur.y)
		copy(ln[dst:dst+size], ln[src:src+size])
		t.clear(t.cols-n, t.cur.y, t.cols-1, t.cur.y)
	}
}

func (t *State) setTitle(title string) {
	t.changed |= ChangedTitle
	t.title = title
}

