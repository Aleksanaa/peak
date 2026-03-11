package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

type VisualLine struct {
	BufferLine int
	Start, End int
}

type TextView struct {
	buffer      *Buffer
	x, y, w, h  int
	style       tcell.Style
	scroll      int
	drag        bool
	singleLine  bool
	scrollable  bool
	layout      []VisualLine
	lastWidth   int
	lastVersion int
	theme       *Theme
	tabWidth    int
}

func NewTextView(text string, x, y, w, h int, style tcell.Style, singleLine, scrollable bool) *TextView {
	tv := &TextView{
		buffer: NewBuffer(text),
		x:      x, y: y, w: w, h: h,
		style:       style,
		singleLine:  singleLine,
		scrollable:  scrollable,
		lastVersion: -1,
		tabWidth:    4,
	}
	tv.UpdateLayout()
	return tv
}

func (tv *TextView) UpdateLayout() {
	if tv.w <= 0 {
		return
	}
	if len(tv.layout) > 0 && tv.w == tv.lastWidth && tv.buffer.version == tv.lastVersion {
		return
	}

	ratio := 0.0
	if len(tv.layout) > 0 {
		ratio = float64(tv.scroll) / float64(len(tv.layout))
	}

	tv.lastWidth = tv.w
	tv.lastVersion = tv.buffer.version
	tv.layout = nil
	for i, line := range tv.buffer.lines {
		if len(line) == 0 {
			tv.layout = append(tv.layout, VisualLine{i, 0, 0})
			continue
		}
		visualPos, start := 0, 0
		for idx, r := range line {
			width := 1
			if r == '\t' {
				width = tv.tabWidth - (visualPos % tv.tabWidth)
			}
			if visualPos+width > tv.w && visualPos > 0 {
				tv.layout = append(tv.layout, VisualLine{i, start, idx})
				start, visualPos = idx, 0
				if r == '\t' {
					width = tv.tabWidth
				}
			}
			visualPos += width
		}
		tv.layout = append(tv.layout, VisualLine{i, start, len(line)})
	}

	if len(tv.layout) > 0 && ratio > 0 {
		tv.scroll = int(ratio * float64(len(tv.layout)))
		if tv.scroll >= len(tv.layout) {
			tv.scroll = len(tv.layout) - 1
		}
	}
	if tv.scroll < 0 {
		tv.scroll = 0
	}
}

func (tv *TextView) Scroll(n int) {
	tv.UpdateLayout()
	tv.scroll += n
	limit := len(tv.layout) - 1
	if tv.scroll > limit {
		tv.scroll = limit
	}
	if tv.scroll < 0 {
		tv.scroll = 0
	}
}

func (tv *TextView) GotoLine(lineNum int) {
	if lineNum < 0 {
		lineNum = 0
	}
	if lineNum >= len(tv.buffer.lines) {
		lineNum = len(tv.buffer.lines) - 1
	}
	if lineNum < 0 {
		return
	}
	tv.buffer.cursor = Cursor{0, lineNum}
	tv.buffer.ClearSelection()
	tv.UpdateLayout()
	// Find the visual line for this buffer line and scroll to it
	for i, vl := range tv.layout {
		if vl.BufferLine == lineNum {
			tv.scroll = i
			break
		}
	}
	tv.SyncScroll()
}

// bufferToVisual translates a buffer position to visual coordinates (vx, vrow).
func (tv *TextView) bufferToVisual(bx, by int) (int, int) {
	for lidx, vl := range tv.layout {
		if vl.BufferLine == by && bx >= vl.Start && bx <= vl.End {
			vx := 0
			line := tv.buffer.lines[by]
			for i := vl.Start; i < bx; i++ {
				if line[i] == '\t' {
					vx += tv.tabWidth - (vx % tv.tabWidth)
				} else {
					vx++
				}
			}
			// Wrap edge case: if cursor is exactly at width, move to next visual line
			if vx >= tv.w && lidx+1 < len(tv.layout) && tv.layout[lidx+1].BufferLine == by {
				continue
			}
			return vx, lidx
		}
	}
	return 0, -1
}

// visualToBuffer translates visual coordinates (vx, vidx) to buffer position (bx, by).
func (tv *TextView) visualToBuffer(vx, vidx int) (int, int) {
	if vidx < 0 {
		vidx = 0
	}
	if vidx >= len(tv.layout) {
		vidx = len(tv.layout) - 1
	}
	vl := tv.layout[vidx]
	line := tv.buffer.lines[vl.BufferLine]
	bx, currVX := vl.Start, 0
	for i := vl.Start; i < vl.End; i++ {
		w := 1
		if line[i] == '\t' {
			w = tv.tabWidth - (currVX % tv.tabWidth)
		}
		if currVX+w/2 > vx {
			break
		}
		currVX += w
		bx = i + 1
	}
	return bx, vl.BufferLine
}

func (tv *TextView) Draw(s tcell.Screen) {
	tv.UpdateLayout()
	if !tv.scrollable {
		tv.scroll = 0
	}
	selStyle := tcell.StyleDefault.Background(tcell.ColorSilver).Foreground(tcell.ColorBlack)
	if tv.theme != nil {
		selStyle = tcell.StyleDefault.Background(tv.theme.SelectionBG).Foreground(tv.theme.SelectionFG)
	}

	vrow := 0
	for lidx := tv.scroll; lidx < len(tv.layout) && vrow < tv.h; lidx++ {
		vl := tv.layout[lidx]
		line := tv.buffer.lines[vl.BufferLine]
		vcol := 0
		for idx := vl.Start; idx < vl.End && vcol < tv.w; idx++ {
			r, style := line[idx], tv.style
			if tv.buffer.IsSelected(idx, vl.BufferLine) {
				style = selStyle
			}
			if r == '\t' {
				tw := tv.tabWidth - (vcol % tv.tabWidth)
				for k := 0; k < tw && vcol < tv.w; k++ {
					s.SetContent(tv.x+vcol, tv.y+vrow, ' ', nil, style)
					vcol++
				}
			} else {
				s.SetContent(tv.x+vcol, tv.y+vrow, r, nil, style)
				vcol++
			}
		}
		for ; vcol < tv.w; vcol++ {
			s.SetContent(tv.x+vcol, tv.y+vrow, ' ', nil, tv.style)
		}
		vrow++
	}
	for ; vrow < tv.h; vrow++ {
		for col := 0; col < tv.w; col++ {
			s.SetContent(tv.x+col, tv.y+vrow, ' ', nil, tv.style)
		}
	}
}

func (tv *TextView) GetClickWord(mx, my int) string {
	word := strings.TrimSpace(tv.buffer.GetSelectedText())
	if word != "" {
		return word
	}
	bx, by := tv.visualToBuffer(mx-tv.x, my-tv.y+tv.scroll)
	return strings.TrimSpace(tv.buffer.GetWordAt(bx, by))
}

func (tv *TextView) ShowCursor(s tcell.Screen) {
	vx, vrow := tv.bufferToVisual(tv.buffer.cursor.x, tv.buffer.cursor.y)
	if vrow >= tv.scroll && vrow < tv.scroll+tv.h {
		s.ShowCursor(tv.x+vx, tv.y+(vrow-tv.scroll))
	}
}

func (tv *TextView) Resize(x, y, w, h int) {
	tv.x, tv.y, tv.w, tv.h = x, y, w, h
	tv.UpdateLayout()
}

func (tv *TextView) HandleEvent(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *tcell.EventKey:
		switch ev.Key() {
		case tcell.KeyCtrlZ:
			if ev.Modifiers()&tcell.ModShift != 0 {
				tv.buffer.Redo()
			} else {
				tv.buffer.Undo()
			}
		case tcell.KeyCtrlY:
			tv.buffer.Redo()
		case tcell.KeyCtrlC:
			tv.buffer.Snarf()
		case tcell.KeyCtrlX:
			tv.buffer.Cut()
		case tcell.KeyCtrlV:
			tv.buffer.Paste()
		case tcell.KeyCtrlU:
			tv.buffer.ClearSelection()
			tv.buffer.DeleteLine()
		case tcell.KeyCtrlW:
			tv.buffer.ClearSelection()
			tv.buffer.DeleteWordBefore()
		case tcell.KeyCtrlH, tcell.KeyBackspace, tcell.KeyBackspace2:
			tv.buffer.Backspace()
		case tcell.KeyDelete:
			tv.buffer.Delete()
		case tcell.KeyPgUp:
			tv.buffer.ClearSelection()
			tv.scroll -= tv.h
			if tv.scroll < 0 {
				tv.scroll = 0
			}
			_, vrow := tv.bufferToVisual(tv.buffer.cursor.x, tv.buffer.cursor.y)
			if vrow >= tv.scroll+tv.h {
				bx, by := tv.visualToBuffer(0, tv.scroll)
				tv.buffer.cursor = Cursor{bx, by}
			}
		case tcell.KeyPgDn:
			tv.buffer.ClearSelection()
			tv.scroll += tv.h
			if tv.scroll >= len(tv.layout) {
				tv.scroll = len(tv.layout) - 1
			}
			if tv.scroll < 0 {
				tv.scroll = 0
			}
			_, vrow := tv.bufferToVisual(tv.buffer.cursor.x, tv.buffer.cursor.y)
			if vrow < tv.scroll {
				bx, by := tv.visualToBuffer(0, tv.scroll)
				tv.buffer.cursor = Cursor{bx, by}
			}
		case tcell.KeyUp:
			tv.buffer.ClearSelection()
			if !tv.singleLine {
				tv.buffer.MoveUp()
			}
		case tcell.KeyDown:
			tv.buffer.ClearSelection()
			if !tv.singleLine {
				tv.buffer.MoveDown()
			}
		case tcell.KeyLeft:
			tv.buffer.ClearSelection()
			if ev.Modifiers()&tcell.ModCtrl != 0 {
				tv.buffer.MoveWordLeft()
			} else {
				tv.buffer.MoveLeft()
			}
		case tcell.KeyRight:
			tv.buffer.ClearSelection()
			if ev.Modifiers()&tcell.ModCtrl != 0 {
				tv.buffer.MoveWordRight()
			} else {
				tv.buffer.MoveRight()
			}
		case tcell.KeyHome:
			tv.buffer.ClearSelection()
			tv.buffer.MoveHome()
		case tcell.KeyEnd:
			tv.buffer.ClearSelection()
			tv.buffer.MoveEnd()
		case tcell.KeyEnter:
			tv.buffer.ClearSelection()
			if !tv.singleLine {
				tv.buffer.NewLine()
			}
		case tcell.KeyTab:
			if tv.buffer.selectionStart != nil {
				tv.buffer.DeleteSelection()
			}
			tv.buffer.Insert('\t')
		case tcell.KeyRune:
			if tv.buffer.selectionStart != nil {
				tv.buffer.DeleteSelection()
			}
			tv.buffer.Insert(ev.Rune())
		}
		tv.UpdateLayout()
		tv.SyncScroll()
		return false
	case *tcell.EventMouse:
		buttons := ev.Buttons()
		if tv.scrollable {
			if buttons&tcell.WheelUp != 0 {
				if tv.scroll > 0 {
					tv.scroll--
				}
				return false
			}
			if buttons&tcell.WheelDown != 0 {
				if tv.scroll < len(tv.layout)-1 {
					tv.scroll++
				}
				return false
			}
		}
		mx, my := ev.Position()
		if buttons != tcell.ButtonNone {
			bx, by := tv.visualToBuffer(mx-tv.x, my-tv.y+tv.scroll)
			if buttons == tcell.Button1 && !tv.drag && !tv.buffer.IsSelected(bx, by) {
				tv.buffer.ClearSelection()
			}
			if buttons == tcell.Button1 {
				if !tv.drag {
					tv.drag = true
					if !tv.buffer.IsSelected(bx, by) {
						tv.buffer.cursor = Cursor{bx, by}
						tv.buffer.SetSelection(tv.buffer.cursor, tv.buffer.cursor)
					}
				} else {
					tv.buffer.cursor = Cursor{bx, by}
					tv.buffer.selectionEnd = &Cursor{bx, by}
				}
			} else if tv.buffer.selectionStart == nil {
				tv.buffer.cursor = Cursor{bx, by}
			}
		} else {
			tv.drag = false
			if tv.buffer.selectionStart != nil && tv.buffer.selectionEnd != nil {
				if *tv.buffer.selectionStart == *tv.buffer.selectionEnd {
					tv.buffer.ClearSelection()
				}
			}
		}
	}
	return false
}

func (tv *TextView) SyncScroll() {
	if !tv.scrollable {
		return
	}
	_, vrow := tv.bufferToVisual(tv.buffer.cursor.x, tv.buffer.cursor.y)
	if vrow != -1 {
		if vrow < tv.scroll {
			tv.scroll = vrow
		} else if vrow >= tv.scroll+tv.h {
			tv.scroll = vrow - tv.h + 1
		}
	}
}

func (tv *TextView) Search(word string) int {
	if word == "" {
		return -1
	}

	startRX, startRY := tv.buffer.cursor.x+1, tv.buffer.cursor.y
	if startRY >= len(tv.buffer.lines) {
		startRY, startRX = 0, 0
	}

	for y := startRY; y < len(tv.buffer.lines); y++ {
		line := string(tv.buffer.lines[y])
		sx := 0
		if y == startRY {
			sx = startRX
			if sx > len(line) {
				sx = len(line)
			}
		}
		if x := strings.Index(line[sx:], word); x != -1 {
			tv.buffer.cursor = Cursor{sx + x + len(word), y}
			tv.buffer.ClearSelection()
			tv.buffer.SetSelection(Cursor{sx + x, y}, tv.buffer.cursor)
			return y
		}
	}

	for y := 0; y <= startRY; y++ {
		line := string(tv.buffer.lines[y])
		limit := len(line)
		if y == startRY {
			limit = startRX
			if limit > len(line) {
				limit = len(line)
			}
		}
		if x := strings.Index(line[:limit], word); x != -1 {
			tv.buffer.cursor = Cursor{x + len(word), y}
			tv.buffer.ClearSelection()
			tv.buffer.SetSelection(Cursor{x, y}, tv.buffer.cursor)
			return y
		}
	}
	return -1
}

func (tv *TextView) ShowLineAt(lineNum int, vrow int) {
	tv.UpdateLayout()
	vidx := -1
	for i, vl := range tv.layout {
		if vl.BufferLine == lineNum {
			vidx = i
			break
		}
	}
	if vidx != -1 {
		tv.scroll = vidx - vrow
		if tv.scroll < 0 {
			tv.scroll = 0
		}
		if len(tv.layout) > 0 && tv.scroll >= len(tv.layout) {
			tv.scroll = len(tv.layout) - 1
		}
	}
	tv.SyncScroll()
}

type Window struct {
	ID             int
	tag            *TextView
	body           *TextView
	parent         *Column
	editor         *Editor
	x, y, w, h     int
	onExec         func(*Column, *Window, string) bool
	explicitHeight int

	savedVersion  int
	warnedVersion int
}

func (win *Window) CtlPrint(all bool) string {
	dirty := 0
	if win.IsDirty() {
		dirty = 1
	}
	// Format: %11d %11d %11d %11d %11d
	// (id, tag nchars, body nchars, isdir, isdirty)
	// For peak, we'll simplify nchars to byte count of the first line/buffer
	tagLen := win.tag.buffer.Len()
	bodyLen := win.body.buffer.Len()
	isdir := 0
	if isDir(win.GetFilename()) {
		isdir = 1
	}
	return fmt.Sprintf("%11d %11d %11d %11d %11d ", win.ID, tagLen, bodyLen, isdir, dirty)
}

func NewWindow(tag, body string, parent *Column, editor *Editor, x, y, w, h int, onExec func(*Column, *Window, string) bool) *Window {
	tagStyle := tcell.StyleDefault.Background(editor.theme.TagBG).Foreground(editor.theme.TagFG)
	bodyStyle := tcell.StyleDefault.Background(editor.theme.BodyBG).Foreground(editor.theme.BodyFG)
	win := &Window{
		tag:    NewTextView(tag, x+1, y, w-1, 1, tagStyle, false, false),
		body:   NewTextView(body, x+1, y+1, w-1, h-1, bodyStyle, false, true),
		parent: parent, editor: editor, x: x, y: y, w: w, h: h, onExec: onExec,
	}
	win.tag.theme = &editor.theme
	win.body.theme = &editor.theme
	return win
}

func (win *Window) IsDirty() bool {
	fn := win.GetFilename()
	if !isFile(fn) || isPeakPath(fn) || isSpecial(fn) {
		return false
	}
	return win.body.buffer.version != win.savedVersion
}

func (win *Window) Warned() bool {
	return win.warnedVersion == win.body.buffer.version
}

func (win *Window) Warn() {
	win.warnedVersion = win.body.buffer.version
}

func (win *Window) GetFilename() string {
	if len(win.tag.buffer.lines) == 0 {
		return ""
	}
	fields := strings.Fields(string(win.tag.buffer.lines[0]))
	if len(fields) > 0 {
		return fields[0]
	}
	return ""
}

func (win *Window) GetDir() string {
	return getPathDir(win.GetFilename())
}

func (win *Window) SetName(name string) {
	tag := win.tag.buffer.GetText()
	fields := strings.Fields(tag)
	if len(fields) > 0 {
		fields[0] = name
		win.tag.buffer.SetText(strings.Join(fields, " ") + " ")
	} else {
		win.tag.buffer.SetText(name + " Get Put Del ")
	}
}

func (win *Window) Contains(x, y int) bool {
	return x >= win.x && x < win.x+win.w && y >= win.y && y < win.y+win.h
}

func (win *Window) tagHeight() int {
	h := len(win.tag.layout)
	if h < 1 {
		return 1
	}
	return h
}

func (win *Window) layout() {
	th := win.tagHeight()
	win.tag.h = th
	win.body.y = win.y + th
	win.body.h = win.h - th
	if win.body.h < 0 {
		win.body.h = 0
	}
}

func (win *Window) Draw(s tcell.Screen) {
	win.layout()

	handleColor := win.editor.theme.Handle
	fn := win.GetFilename()
	if isSpecial(fn) {
		handleColor = win.editor.theme.HandleError
	} else if win.IsDirty() {
		handleColor = win.editor.theme.HandleDirty
	}

	handleStyle := tcell.StyleDefault.Background(handleColor).Foreground(tcell.ColorBlack)
	for i := 0; i < win.tag.h; i++ {
		s.SetContent(win.x, win.y+i, ' ', nil, handleStyle)
	}

	// Draw scrollbar/handle for the body
	if win.body.h > 0 {
		win.body.UpdateLayout() // Ensure layout is fresh for scroll calculation
		total := len(win.body.layout)
		visible := win.body.h

		thumbStyle := tcell.StyleDefault.Background(win.editor.theme.ScrollThumb)
		gutterStyle := tcell.StyleDefault.Background(win.editor.theme.ScrollGutter)

		thumbStart, thumbHeight := -1, -1
		if total > visible {
			thumbHeight = (visible * visible) / total
			if thumbHeight < 1 {
				thumbHeight = 1
			}
			thumbStart = (win.body.scroll * visible) / total
			if thumbStart+thumbHeight > visible {
				thumbStart = visible - thumbHeight
			}
		}

		for i := 0; i < visible; i++ {
			style := gutterStyle
			if i >= thumbStart && i < thumbStart+thumbHeight {
				style = thumbStyle
			}
			s.SetContent(win.x, win.body.y+i, ' ', nil, style)
		}
	}

	win.tag.Draw(s)
	win.body.Draw(s)
}

func (win *Window) Resize(x, y, w, h int) {
	win.x, win.y, win.w, win.h = x, y, w, h
	win.tag.Resize(x+1, y, w-1, win.tagHeight())
	win.layout()
	win.body.Resize(x+1, win.body.y, w-1, win.body.h)
}

func (win *Window) HandleEvent(ev tcell.Event) bool {
	if me, ok := ev.(*tcell.EventMouse); ok {
		mx, my := me.Position()
		win.tag.UpdateLayout()
		win.body.UpdateLayout()
		th := win.tagHeight()

		if mx == win.x && my >= win.y+th {
			// Scrolling speed based on distance from top: closer = slower
			amount := (my - (win.y + th)) + 1
			if me.Buttons()&tcell.Button1 != 0 {
				if win.editor.scrollWin == nil {
					win.body.Scroll(-amount)
					win.editor.scrollStartTime = time.Now()
				}
				win.editor.scrollWin, win.editor.scrollAmount, win.editor.scrollDir = win, amount, -1
			} else if me.Buttons()&tcell.Button2 != 0 {
				if win.editor.scrollWin == nil {
					win.body.Scroll(amount)
					win.editor.scrollStartTime = time.Now()
				}
				win.editor.scrollWin, win.editor.scrollAmount, win.editor.scrollDir = win, amount, 1
			} else if me.Buttons()&tcell.Button3 != 0 {
				// Middle-click: Align top of scrollbar (thumb) with click position
				visible := win.body.h
				win.body.UpdateLayout()
				total := len(win.body.layout)
				if visible > 0 && total > 0 {
					yClick := my - (win.y + th)
					// Use ceiling division (a + b - 1) / b to ensure the thumb aligns with the click
					win.body.scroll = (yClick*total + visible - 1) / visible
					win.body.Scroll(0) // Apply bounds check
				}
			}
			return false
		}

		// If click was on the vertical separator (handle area), stop here
		if mx == win.x {
			return false
		}

		target := win.body
		if my < win.y+th {
			target = win.tag
		}
		target.HandleEvent(ev)
		if me.Buttons() == tcell.Button3 || me.Buttons() == tcell.Button2 {
			word := target.GetClickWord(mx, my)
			if word != "" {
				if me.Buttons() == tcell.Button3 { // Middle-click (Execute)
					if win.onExec != nil {
						return win.onExec(win.parent, win, word)
					}
				} else { // Right-click (Plumb)
					return win.editor.Plumb(win, word)
				}
			}
		}
	}
	return false
}
