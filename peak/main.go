package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/aleksana/peak/peak/tview"
	"github.com/gdamore/tcell/v2"
)

type Theme struct {
	TagBG, TagFG                      tcell.Color
	BodyBG, BodyFG                    tcell.Color
	ColTagBG, ColTagFG                tcell.Color
	GlobalTagBG, GlobalTagFG          tcell.Color
	Handle, ScrollThumb, ScrollGutter tcell.Color
	HandleDirty, HandleError          tcell.Color
	SelectionBG, SelectionFG          tcell.Color
	HandleColumn                      tcell.Color

	SynKeyword  tcell.Color
	SynType     tcell.Color
	SynComment  tcell.Color
	SynString   tcell.Color
	SynNumber   tcell.Color
	SynFunction tcell.Color
	SynOperator tcell.Color
	SynVariable tcell.Color
	SynConstant tcell.Color
	SynError    tcell.Color
}

var defaultTheme = Theme{
	GlobalTagBG:  tcell.NewHexColor(0x11111b),
	GlobalTagFG:  tcell.NewHexColor(0xbac2de),
	ColTagBG:     tcell.NewHexColor(0x181825),
	ColTagFG:     tcell.NewHexColor(0xbac2de),
	TagBG:        tcell.NewHexColor(0x1e1e2e),
	TagFG:        tcell.NewHexColor(0xbac2de),
	BodyBG:       tcell.NewHexColor(0x313244),
	BodyFG:       tcell.NewHexColor(0xcdd6f4),
	Handle:       tcell.NewHexColor(0x89dceb),
	HandleDirty:  tcell.NewHexColor(0xf38ba8),
	HandleError:  tcell.NewHexColor(0xfab387),
	ScrollThumb:  tcell.NewHexColor(0x45475a),
	ScrollGutter: tcell.NewHexColor(0x181825),
	SelectionBG:  tcell.NewHexColor(0x585b70),
	SelectionFG:  tcell.NewHexColor(0xbac2de),
	HandleColumn: tcell.NewHexColor(0xb4befe),

	// Catppuccin Mocha syntax palette
	SynKeyword:  tcell.NewHexColor(0xcba6f7), // mauve
	SynType:     tcell.NewHexColor(0x89b4fa), // blue
	SynComment:  tcell.NewHexColor(0x6c7086), // overlay0
	SynString:   tcell.NewHexColor(0xa6e3a1), // green
	SynNumber:   tcell.NewHexColor(0xf9e2af), // yellow
	SynFunction: tcell.NewHexColor(0x89dceb), // sky
	SynOperator: tcell.NewHexColor(0x89dceb), // sky
	SynVariable: tcell.NewHexColor(0xcdd6f4), // text (unstyled)
	SynConstant: tcell.NewHexColor(0xfab387), // peach
	SynError:    tcell.NewHexColor(0xf38ba8), // red
}

// execReq is a non-blocking request for the UI thread to run an executive
// operation: execute a command, plumb a string, or append to the error window.
type execReq struct {
	col  *Column
	win  *Window
	text string
	kind byte // 'x'=Execute, 'l'=Plumb, 'e'=appendToErrorWindow
}

// Editor is the main application state.
type Editor struct {
	CmdChan     chan func()
	redrawCh    chan struct{} // capacity-1; 9P goroutines signal after state changes
	execCh      chan execReq  // buffered; 9P goroutines send executive ops here
	screen      tcell.Screen
	tag         *TextView
	columns     []*Column
	active      *Window
	width       int
	height      int
	dragView    View
	dragWin     *Window
	dragCol     *Column
	focusedView View

	scrollWin       *Window
	scrollAmount    int
	scrollDir       int
	scrollStartTime time.Time
	lastClickY      int
	lastWidth       int
	theme           Theme
	nextWinID       int
	ninep           *NineP

	colFlex  *tview.Flex
	rootFlex *tview.Flex
}

// Redraw signals the main loop to redraw on the next iteration.
// Non-blocking: if a redraw is already pending the signal is coalesced.
func (e *Editor) Redraw() {
	select {
	case e.redrawCh <- struct{}{}:
	default:
	}
}

func (e *Editor) Call(f func()) {
	done := make(chan struct{})
	e.CmdChan <- func() {
		f()
		close(done)
	}
	<-done
}

// Init sets up the initial editor state with the specified number of columns.
func (e *Editor) Init(numCols int, args []string) {
	user, _ := os.UserHomeDir()
	logDir := filepath.Join(user, ".peak")
	os.MkdirAll(logDir, 0700)
	logFile, err := os.OpenFile(filepath.Join(logDir, "log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err == nil {
		log.SetOutput(logFile)
	}

	e.CmdChan = make(chan func())
	e.redrawCh = make(chan struct{}, 1)
	e.execCh = make(chan execReq, 8)
	e.theme = defaultTheme
	e.nextWinID = 1
	e.ninep = NewNineP(e)
	e.ninep.Listen()
	s, err := tcell.NewScreen()
	if err != nil {
		log.Fatalf("%+v", err)
	}
	if err := s.Init(); err != nil {
		log.Fatalf("%+v", err)
	}

	e.screen = s
	e.screen.EnableMouse()
	e.width, e.height = e.screen.Size()

	tagStyle := tcell.StyleDefault.Background(e.theme.GlobalTagBG).Foreground(e.theme.GlobalTagFG)
	e.tag = NewTextView(" NewCol Help Exit ", 0, 0, e.width, 1, tagStyle, true, false)
	e.tag.theme = &e.theme
	e.focusedView = e.tag

	e.colFlex = tview.NewFlex()
	e.colFlex.SetDirection(tview.FlexColumn)

	e.rootFlex = tview.NewFlex()
	e.rootFlex.SetDirection(tview.FlexRow)
	e.rootFlex.AddItem(e.tag, 1, 0)

	if numCols < 1 {
		numCols = 1
	}
	for i := 0; i < numCols; i++ {
		col := NewColumn(0, 0, 0, 0, e, e.Execute)
		e.columns = append(e.columns, col)
		e.colFlex.AddItem(col, 0, 1)
	}
	e.rootFlex.AddItem(e.colFlex, 0, 1)
	e.resize()

	if len(args) > 0 {
		for _, arg := range args {
			full := e.resolvePathWithContext(nil, arg)
			content, isDir, err := readFileOrDir(full)
			if err == nil {
				e.createWindow(e.columns[0], full, content, isDir, -1, 0)
			}
		}
	} else {
		dir := getwd()
		lastCol := e.columns[len(e.columns)-1]
		win := e.createWindow(lastCol, dir, "", true, -1, 0)

		// Initial directory listing
		e.Execute(lastCol, win, "Get")
	}
	e.Resize()
}

// Run enters the main event loop.
func (e *Editor) Run() {
	events := make(chan tcell.Event)
	go func() {
		for {
			events <- e.screen.PollEvent()
		}
	}()

	e.Draw()
	for {
		var timer *time.Timer
		var tick <-chan time.Time
		if e.scrollWin != nil {
			timer = time.NewTimer(50 * time.Millisecond)
			tick = timer.C
		}

		select {
		case ev := <-events:
			if timer != nil {
				timer.Stop()
			}
			if ev == nil {
				return
			}
			switch ev := ev.(type) {
			case *tcell.EventInterrupt:
				if f, ok := ev.Data().(func()); ok {
					f()
				}
				e.Draw()
			default:
				if quit, redraw := e.HandleEvent(ev); quit {
					return
				} else if redraw {
					e.Draw()
				}
			}
		case fn := <-e.CmdChan:
			if timer != nil {
				timer.Stop()
			}
			fn()
			e.Draw()
		case <-e.redrawCh:
			if timer != nil {
				timer.Stop()
			}
			e.Draw()
		case req := <-e.execCh:
			if timer != nil {
				timer.Stop()
			}
			switch req.kind {
			case 'x':
				if req.win != nil && req.win.onExec != nil {
					req.win.onExec(req.col, req.win, req.text)
				}
			case 'l':
				e.Plumb(req.win, req.text)
			case 'e':
				e.appendToErrorWindow(req.col, req.win, req.text)
			}
			e.Draw()
		case <-tick:
			if e.scrollWin != nil && time.Since(e.scrollStartTime) > 200*time.Millisecond {
				e.scrollWin.body.Scroll(e.scrollDir * e.scrollAmount)
				e.Draw()
			}
		}
	}
}

func (e *Editor) Draw() {
	// Phase 1: Layout — compute all geometry and scroll before any paint
	e.tag.Layout()
	for _, col := range e.columns {
		col.tag.Layout()
	}
	e.resize()

	// Phase 2: Paint — pure rendering, no state mutation
	e.rootFlex.Draw(e.screen)
	if e.focusedView != nil {
		e.focusedView.ShowCursor(e.screen)
	}
	e.screen.Show()
}

func (e *Editor) HandleEvent(ev tcell.Event) (bool, bool) {
	if me, ok := ev.(*tcell.EventMouse); ok {
		if me.Buttons() != tcell.ButtonNone {
			_, my := me.Position()
			e.lastClickY = my
		} else if e.dragCol == nil && e.dragWin == nil && e.dragView == nil && e.scrollWin == nil {
			// Skip redraw on mouse moves with no buttons/drag/scroll
			return false, false
		}
	}

	switch ev := ev.(type) {
	case *tcell.EventKey:
		if ev.Key() == tcell.KeyCtrlF {
			if e.focusedView != nil {
				if tv, ok := e.focusedView.(*TextView); ok && tv.buffer.GetSelectedText() != "" {
					return e.Execute(nil, nil, "Look"), true
				}
			}
		}
		if e.focusedView != nil {
			win := e.windowOf(e.focusedView)
			if win != nil {
				win.lk.Lock()
			}
			quit := e.focusedView.HandleEvent(ev)
			if win != nil {
				win.lk.Unlock()
			}
			return quit, true
		}
	case *tcell.EventMouse:
		mx, my := ev.Position()
		buttons := ev.Buttons()

		if buttons == tcell.ButtonNone {
			e.scrollWin = nil
		}

		if e.dragCol != nil {
			if buttons&tcell.Button1 != 0 {
				e.moveColumnTo(e.dragCol, mx)
				return false, true
			}
			e.dragCol = nil
			return false, true
		}
		if e.dragWin != nil {
			if buttons&tcell.Button1 != 0 {
				e.moveWindowTo(e.dragWin, mx, my)
				return false, true
			}
			e.dragWin = nil
			return false, true
		}
		if e.dragView != nil {
			quit := e.dragView.HandleEvent(ev)
			if buttons == tcell.ButtonNone {
				e.dragView = nil
			}
			return quit, true
		}

		// Global Tag clicks
		if my == 0 {
			word := e.tag.GetClickWord(mx, my)
			if word != "" {
				if buttons == tcell.Button3 { // Middle-click
					return e.Execute(nil, nil, word), true
				}
				if buttons == tcell.Button2 { // Right-click
					return e.Plumb(nil, word), true
				}
			}
			if buttons == tcell.Button1 {
				e.dragView, e.focusedView = e.tag, e.tag
			}
			return e.tag.HandleEvent(ev), true
		}

		for _, col := range e.columns {
			if col.InRect(mx, my) {
				return col.HandleEvent(ev), true
			}
		}
	case *tcell.EventResize:
		e.width, e.height = e.screen.Size()
		e.resize()
		e.screen.Sync()
		return false, true
	}
	return false, true
}

// windowOf returns the Window that owns view v, or nil for the global tag.
func (e *Editor) windowOf(v View) *Window {
	for _, col := range e.columns {
		for _, win := range col.windows {
			if win.body == v || win.tag == v {
				return win
			}
		}
	}
	return nil
}

func (e *Editor) ActivateWindow(win *Window) {
	if win == nil {
		return
	}
	prev := e.active
	e.active = win
	e.focusedView = win.body
	if prev != win {
		e.ninep.BroadcastFocus(win)
	}
}

func (e *Editor) moveColumnTo(col *Column, mx int) {
	idx := -1
	for i, c := range e.columns {
		if c == col {
			idx = i
			break
		}
	}
	if idx == -1 {
		return
	}

	if idx == 0 {
		if len(e.columns) > 1 {
			c1x, _, c1w, _ := e.columns[1].GetRect()
			if mx > c1x+c1w/2 {
				e.columns[0], e.columns[1] = e.columns[1], e.columns[0]
			}
		}
	} else {
		prev := e.columns[idx-1]
		px, _, pw, _ := prev.GetRect()
		_, _, cw, _ := col.GetRect()
		if mx < px+2 {
			e.columns[idx], e.columns[idx-1] = e.columns[idx-1], e.columns[idx]
		} else {
			combinedW := pw + cw
			minW := 5
			if mx < px+minW {
				mx = px + minW
			}
			if mx > px+combinedW-minW {
				mx = px + combinedW - minW
			}
			e.colFlex.ResizeItem(prev, mx-px, 0)
			e.colFlex.ResizeItem(col, combinedW-(mx-px), 0)
		}
	}
	e.resize()
}

func (e *Editor) moveWindowTo(win *Window, mx, my int) {
	var target *Column
	for _, col := range e.columns {
		if col.InRect(mx, my) {
			target = col
			break
		}
	}
	if target == nil {
		return
	}

	if win.parent != target {
		old := win.parent
		for i, w := range old.windows {
			if w == win {
				old.windows = append(old.windows[:i], old.windows[i+1:]...)
				old.Reflow()
				break
			}
		}
		win.parent = target
		newIdx := 0
		for _, w := range target.windows {
			_, wy, _, wh := w.GetRect()
			if my < wy+wh/2 {
				break
			}
			newIdx++
		}
		target.windows = append(target.windows[:newIdx], append([]*Window{win}, target.windows[newIdx:]...)...)
		tx, ty, tw, th := target.GetRect()
		target.SetRect(tx, ty, tw, th)
		return
	}

	// Intra-column: swap or resize.
	idx := -1
	for i, w := range target.windows {
		if w == win {
			idx = i
			break
		}
	}
	if idx == -1 {
		return
	}

	if idx == 0 {
		if len(target.windows) > 1 {
			_, w1y, _, _ := target.windows[1].GetRect()
			if my > w1y+target.windows[1].tagHeight() {
				target.windows[0], target.windows[1] = target.windows[1], target.windows[0]
				target.winFlex.ResizeItem(target.windows[0], 0, 1)
				target.winFlex.ResizeItem(target.windows[1], 0, 1)
			}
		}
	} else {
		prev := target.windows[idx-1]
		_, py, _, ph := prev.GetRect()
		_, _, _, wh := win.GetRect()
		if my < py+prev.tagHeight() {
			target.windows[idx], target.windows[idx-1] = target.windows[idx-1], target.windows[idx]
			target.winFlex.ResizeItem(target.windows[idx], 0, 1)
			target.winFlex.ResizeItem(target.windows[idx-1], 0, 1)
		} else {
			combinedH := ph + wh
			minH := win.tagHeight() + 1
			prevMinH := prev.tagHeight() + 1
			if my < py+prevMinH {
				my = py + prevMinH
			}
			if my > py+combinedH-minH {
				my = py + combinedH - minH
			}
			target.winFlex.ResizeItem(prev, my-py, 0)
			target.winFlex.ResizeItem(win, combinedH-(my-py), 0)
		}
	}
	tx, ty, tw, th := target.GetRect()
	target.SetRect(tx, ty, tw, th)
}

func (e *Editor) Resize() {
	e.resize()
}

func (e *Editor) resize() {
	if len(e.columns) == 0 {
		return
	}

	if e.rootFlex == nil {
		e.rootFlex = tview.NewFlex()
		e.rootFlex.SetDirection(tview.FlexRow)
		e.rootFlex.AddItem(e.tag, 1, 0)
	}
	if e.colFlex == nil {
		e.colFlex = tview.NewFlex()
		e.colFlex.SetDirection(tview.FlexColumn)
	}
	if e.rootFlex.ItemCount() == 1 {
		e.rootFlex.AddItem(e.colFlex, 0, 1)
	}

	e.rootFlex.SetRect(0, 0, e.width, e.height)

	scaleRatio := 1.0
	if e.lastWidth > 0 && e.lastWidth != e.width {
		scaleRatio = float64(e.width) / float64(e.lastWidth)
	}
	e.lastWidth = e.width

	e.syncColFlex(scaleRatio)
	e.rootFlex.Layout()
}

func (e *Editor) syncColFlex(scaleRatio float64) {
	sizes := make(map[*Column]int)
	for _, col := range e.columns {
		if fixed, _ := e.colFlex.GetItemSize(col); fixed > 0 {
			sizes[col] = fixed
		}
	}
	e.colFlex.Clear()
	for _, col := range e.columns {
		fixedSize := sizes[col]
		if fixedSize > 0 && scaleRatio != 1.0 {
			fixedSize = int(float64(fixedSize)*scaleRatio + 0.5)
		}
		e.colFlex.AddItem(col, fixedSize, 1)
	}
}

func (t *Theme) colorForAttr(attr string) tcell.Color {
	switch attr {
	case "keyword":
		return t.SynKeyword
	case "type":
		return t.SynType
	case "comment":
		return t.SynComment
	case "string":
		return t.SynString
	case "number":
		return t.SynNumber
	case "function":
		return t.SynFunction
	case "operator":
		return t.SynOperator
	case "variable":
		return t.SynVariable
	case "constant":
		return t.SynConstant
	case "error":
		return t.SynError
	default:
		return tcell.ColorDefault
	}
}

var appEditor *Editor

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [-c columns] [file...]\n", os.Args[0])
		flag.PrintDefaults()
	}
	cols := flag.Int("c", 2, "number of columns")
	flag.Parse()

	appEditor = &Editor{}
	appEditor.Init(*cols, flag.Args())
	defer appEditor.screen.Fini()
	appEditor.Run()
}
