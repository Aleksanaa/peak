package main

import (
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

type VisualLine struct {
	BufferLine int
	Start, End int
}

type View interface {
	Draw(tcell.Screen)
	ShowCursor(tcell.Screen)
	Resize(x, y, w, h int)
	HandleEvent(tcell.Event) bool
	GetPos() (x, y, w, h int)
	SetPos(x, y, w, h int)
	GetClickWord(mx, my int) string
	GetBuffer() *Buffer
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
	typingStart *Cursor
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

func (tv *TextView) runeWidth(r rune, visualPos int) int {
	if r == '\t' {
		return tv.tabWidth - (visualPos % tv.tabWidth)
	}
	w := uniseg.StringWidth(string(r))
	if w == 0 {
		return 0
	}
	return w
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
			width := tv.runeWidth(r, visualPos)
			if visualPos+width > tv.w && visualPos > 0 {
				tv.layout = append(tv.layout, VisualLine{i, start, idx})
				start, visualPos = idx, 0
				width = tv.runeWidth(r, visualPos)
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

func (tv *TextView) GotoLineCol(lineNum, colNum int) {
	lineNum = max(0, min(lineNum, len(tv.buffer.lines)-1))
	colNum = max(0, min(colNum, len(tv.buffer.lines[lineNum])))

	tv.buffer.cursor = Cursor{colNum, lineNum}
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
				vx += tv.runeWidth(line[i], vx)
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
		w := tv.runeWidth(line[i], currVX)
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
			width := tv.runeWidth(r, vcol)
			if r == '\t' {
				for k := 0; k < width && vcol < tv.w; k++ {
					s.SetContent(tv.x+vcol, tv.y+vrow, ' ', nil, style)
					vcol++
				}
			} else {
				if vcol+width <= tv.w {
					s.SetContent(tv.x+vcol, tv.y+vrow, r, nil, style)
					vcol += width
				} else {
					// Character doesn't fit, fill with spaces
					for vcol < tv.w {
						s.SetContent(tv.x+vcol, tv.y+vrow, ' ', nil, style)
						vcol++
					}
				}
			}
		}
		for ; vcol < tv.w; vcol++ {
			style := tv.style
			if tv.buffer.IsSelected(vl.End, vl.BufferLine) {
				style = selStyle
			}
			s.SetContent(tv.x+vcol, tv.y+vrow, ' ', nil, style)
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
	bx, by := tv.visualToBuffer(mx-tv.x, my-tv.y+tv.scroll)
	if tv.buffer.IsSelected(bx, by) {
		word := strings.TrimSpace(tv.buffer.GetSelectedText())
		if word != "" {
			return word
		}
	}
	return strings.TrimSpace(tv.buffer.GetWordAt(bx, by))
}

func (tv *TextView) ShowCursor(s tcell.Screen) {
	vx, vrow := tv.bufferToVisual(tv.buffer.cursor.x, tv.buffer.cursor.y)
	if vrow >= tv.scroll && vrow < tv.scroll+tv.h {
		if vx >= tv.w {
			vx = tv.w - 1
		}
		if vx < 0 {
			vx = 0
		}
		s.ShowCursor(tv.x+vx, tv.y+(vrow-tv.scroll))
	} else {
		s.HideCursor()
	}
}

func (tv *TextView) Resize(x, y, w, h int) {
	tv.x, tv.y, tv.w, tv.h = x, y, w, h
	tv.UpdateLayout()
}

func (tv *TextView) GetPos() (x, y, w, h int) {
	return tv.x, tv.y, tv.w, tv.h
}

func (tv *TextView) SetPos(x, y, w, h int) {
	tv.x, tv.y, tv.w, tv.h = x, y, w, h
}

func (tv *TextView) GetBuffer() *Buffer {
	return tv.buffer
}

func (tv *TextView) prepareTyping() bool {
	if tv.buffer.selectionStart != nil {
		start, _ := tv.buffer.orderedSelection()
		tv.typingStart = &Cursor{start.x, start.y}
		return true
	}
	if tv.typingStart == nil {
		tv.typingStart = &Cursor{tv.buffer.cursor.x, tv.buffer.cursor.y}
	}
	return false
}

func (tv *TextView) HandleEvent(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *tcell.EventKey:
		switch ev.Key() {
		case tcell.KeyEsc:
			if tv.typingStart != nil {
				tv.buffer.SetSelection(*tv.typingStart, tv.buffer.cursor)
				tv.typingStart = nil
			} else if tv.buffer.selectionStart != nil {
				start, _ := tv.buffer.orderedSelection()
				tv.buffer.cursor = start
				tv.buffer.ClearSelection()
				tv.typingStart = nil
			}
		case tcell.KeyCtrlZ:
			tv.typingStart = nil
			if ev.Modifiers()&tcell.ModShift != 0 {
				tv.buffer.Redo()
			} else {
				tv.buffer.Undo()
			}
		case tcell.KeyCtrlY:
			tv.typingStart = nil
			tv.buffer.Redo()
		case tcell.KeyCtrlC:
			tv.buffer.Snarf()
		case tcell.KeyCtrlX:
			tv.typingStart = nil
			tv.buffer.Cut()
		case tcell.KeyCtrlV:
			tv.prepareTyping()
			tv.buffer.Paste()
		case tcell.KeyCtrlU:
			tv.typingStart = nil
			tv.buffer.ClearSelection()
			tv.buffer.DeleteLine()
		case tcell.KeyCtrlW:
			tv.typingStart = nil
			tv.buffer.ClearSelection()
			tv.buffer.DeleteWordBefore()
		case tcell.KeyCtrlH, tcell.KeyBackspace, tcell.KeyBackspace2:
			tv.prepareTyping()
			tv.buffer.Backspace()
		case tcell.KeyDelete:
			tv.prepareTyping()
			tv.buffer.Delete()
		case tcell.KeyPgUp:
			tv.typingStart = nil
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
			tv.typingStart = nil
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
			tv.typingStart = nil
			tv.buffer.ClearSelection()
			if !tv.singleLine {
				tv.buffer.MoveUp()
			}
		case tcell.KeyDown:
			tv.typingStart = nil
			tv.buffer.ClearSelection()
			if !tv.singleLine {
				tv.buffer.MoveDown()
			}
		case tcell.KeyLeft:
			tv.typingStart = nil
			tv.buffer.ClearSelection()
			if ev.Modifiers()&tcell.ModCtrl != 0 {
				tv.buffer.MoveWordLeft()
			} else {
				tv.buffer.MoveLeft()
			}
		case tcell.KeyRight:
			tv.typingStart = nil
			tv.buffer.ClearSelection()
			if ev.Modifiers()&tcell.ModCtrl != 0 {
				tv.buffer.MoveWordRight()
			} else {
				tv.buffer.MoveRight()
			}
		case tcell.KeyHome:
			tv.typingStart = nil
			tv.buffer.ClearSelection()
			tv.buffer.MoveHome()
		case tcell.KeyEnd:
			tv.typingStart = nil
			tv.buffer.ClearSelection()
			tv.buffer.MoveEnd()
		case tcell.KeyEnter:
			if tv.prepareTyping() {
				tv.buffer.DeleteSelection()
				tv.typingStart = nil
			}
			if !tv.singleLine {
				tv.buffer.NewLine()
			}
		case tcell.KeyTab:
			if tv.prepareTyping() {
				tv.buffer.DeleteSelection()
			}
			tv.buffer.Insert('\t')
		case tcell.KeyRune:
			if tv.prepareTyping() {
				tv.buffer.DeleteSelection()
			}
			tv.buffer.Insert(ev.Rune())
		}
		tv.UpdateLayout()
		tv.SyncScroll()
		return false
	case *tcell.EventMouse:
		buttons := ev.Buttons()
		if buttons != tcell.ButtonNone {
			tv.typingStart = nil
		}
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
			if buttons == tcell.Button1 && !tv.drag {
				tv.buffer.ClearSelection()
			}
			if buttons == tcell.Button1 {
				if !tv.drag {
					tv.drag = true
					tv.buffer.cursor = Cursor{bx, by}
					tv.buffer.SetSelection(tv.buffer.cursor, tv.buffer.cursor)
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
	body           View
	parent         *Column
	editor         *Editor
	x, y, w, h     int
	onExec         func(*Column, *Window, string) bool
	explicitHeight int

	isDir         bool
	hasVersion    bool
	savedVersion  int
	warnedVersion int
}

func NewTermWindow(tag string, parent *Column, editor *Editor, x, y, w, h int, cmd string, onExec func(*Column, *Window, string) bool) (*Window, error) {
	tagStyle := tcell.StyleDefault.Background(editor.theme.TagBG).Foreground(editor.theme.TagFG)
	win := &Window{
		tag:    NewTextView(tag, x+1, y, w-1, 1, tagStyle, false, false),
		parent: parent, editor: editor, x: x, y: y, w: w, h: h, onExec: onExec,
	}
	win.tag.theme = &editor.theme

	term, err := NewTermView(editor, cmd, x+1, y+1, w-1, h-1, func() {
		// Auto-delete window when terminal exits?
		// For now, let's just let it stay there closed.
	})
	if err != nil {
		return nil, err
	}
	win.body = term
	return win, nil
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
	if tv, ok := win.body.(*TextView); ok {
		tv.theme = &editor.theme
	}
	return win
}

func (win *Window) bodyTextView() *TextView {
	if tv, ok := win.body.(*TextView); ok {
		return tv
	}
	return nil
}

func (win *Window) IsDirty() bool {
	if !win.hasVersion {
		return false
	}
	if buf := win.body.GetBuffer(); buf != nil {
		return buf.version != win.savedVersion
	}
	return false
}

func (win *Window) Warned() bool {
	if buf := win.body.GetBuffer(); buf != nil {
		return win.warnedVersion == buf.version
	}
	return true
}

func (win *Window) Warn() {
	if buf := win.body.GetBuffer(); buf != nil {
		win.warnedVersion = buf.version
	}
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
		win.tag.buffer.SetText(" " + strings.Join(fields, " ") + " ")
	} else {
		win.tag.buffer.SetText(" " + name + " Get Put Del ")
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
	bh := win.h - th
	if bh < 0 {
		bh = 0
	}
	win.body.SetPos(win.x+1, win.y+th, win.w-1, bh)
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
	if tv, ok := win.body.(*TextView); ok && tv.h > 0 {
		tv.UpdateLayout() // Ensure layout is fresh for scroll calculation
		total := len(tv.layout)
		visible := tv.h

		thumbStyle := tcell.StyleDefault.Background(win.editor.theme.ScrollThumb)

		thumbStart, thumbHeight := -1, -1
		if total > visible {
			thumbHeight = (visible * visible) / total
			if thumbHeight < 1 {
				thumbHeight = 1
			}
			thumbStart = (tv.scroll * visible) / total
			if thumbStart+thumbHeight > visible {
				thumbStart = visible - thumbHeight
			}
		}

		for i := 0; i < visible; i++ {
			if i >= thumbStart && i < thumbStart+thumbHeight {
				s.SetContent(win.x, tv.y+i, ' ', nil, thumbStyle)
			}
		}
	}

	win.tag.Draw(s)
	win.body.Draw(s)
}

func (win *Window) Resize(x, y, w, h int) {
	win.x, win.y, win.w, win.h = x, y, w, h
	win.tag.Resize(x+1, y, w-1, win.tagHeight())
	win.layout()
	_, by, bw, bh := win.body.GetPos()
	win.body.Resize(x+1, by, bw, bh)
}

func (win *Window) HandleEvent(ev tcell.Event) bool {
	if me, ok := ev.(*tcell.EventMouse); ok {
		mx, my := me.Position()
		win.tag.UpdateLayout()
		if tv, ok := win.body.(*TextView); ok {
			tv.UpdateLayout()
		}
		th := win.tagHeight()

		if mx == win.x && my >= win.y+th {
			if tv, ok := win.body.(*TextView); ok {
				// Scrolling speed based on distance from top: closer = slower
				amount := (my - (win.y + th)) + 1
				if me.Buttons()&tcell.Button1 != 0 {
					if win.editor.scrollWin == nil {
						tv.Scroll(-amount)
						win.editor.scrollStartTime = time.Now()
					}
					win.editor.scrollWin, win.editor.scrollAmount, win.editor.scrollDir = win, amount, -1
				} else if me.Buttons()&tcell.Button2 != 0 {
					if win.editor.scrollWin == nil {
						tv.Scroll(amount)
						win.editor.scrollStartTime = time.Now()
					}
					win.editor.scrollWin, win.editor.scrollAmount, win.editor.scrollDir = win, amount, 1
				} else if me.Buttons()&tcell.Button3 != 0 {
					// Middle-click: Align top of scrollbar (thumb) with click position
					visible := tv.h
					total := len(tv.layout)
					if visible > 0 && total > 0 {
						yClick := my - (win.y + th)
						// Use ceiling division (a + b - 1) / b to ensure the thumb aligns with the click
						tv.scroll = (yClick*total + visible - 1) / visible
						tv.Scroll(0) // Apply bounds check
					}
				}
			}
			return false
		}

		// If click was on the vertical separator (handle area), stop here
		if mx == win.x {
			return false
		}

		var target View
		if my < win.y+th {
			target = win.tag
		} else {
			target = win.body
		}

		target.HandleEvent(ev)
		if me.Buttons() == tcell.Button3 || me.Buttons() == tcell.Button2 {
			var word string
			if tv, ok := target.(*TextView); ok {
				word = tv.GetClickWord(mx, my)
			}
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
	} else {
		// Non-mouse events (keys) go to focused view, which is usually win.body
		// This is handled in Editor.HandleEvent.
	}
	return false
}
