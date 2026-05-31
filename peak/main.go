package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/gdamore/tcell/v2"
)

type Theme struct {
	TagBG, TagFG                      tcell.Color
	BodyBG, BodyFG                    tcell.Color
	ColTagBG, ColTagFG                tcell.Color
	GlobalTagBG, GlobalTagFG          tcell.Color
	Handle, ScrollThumb, ScrollGutter tcell.Color
	HandleDirty, HandleError          tcell.Color
	HandleWritable, HandleUnwritable  tcell.Color
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
	TreeNode
	CmdChan     chan func()
	redrawCh    chan struct{} // capacity-1; 9P goroutines signal after state changes
	execCh      chan execReq  // buffered; 9P goroutines send executive ops here
	screen      tcell.Screen
	tag         *TextView
	columns     []*Column
	columnNodes []DrawNode
	active      *Window
	dragView    View
	dragWin     *Window
	dragCol     *Column
	focusedView View

	scrollWin       *Window
	scrollAmount    int
	scrollDir       int
	scrollStartTime time.Time
	lastClickY      int
	theme           Theme
	nextWinID       int
	ninep           *NineP
}

func (e *Editor) syncChildren() {
	e.children = []DrawNode{e.tag}
	for _, c := range e.columns {
		e.children = append(e.children, c)
	}
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
	e.nextWinID = 1
	e.ninep = NewNineP(e)
	if err := e.ApplyTheme("catppuccin_mocha"); err != nil {
		log.Printf("theme: %v", err)
	}
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
	e.w, e.h = e.screen.Size()

	tagStyle := tcell.StyleDefault.Background(e.theme.GlobalTagBG).Foreground(e.theme.GlobalTagFG)
	e.tag = NewTextView(" NewCol Help Exit ", 0, 0, e.w, 1, tagStyle, true, false)
	e.tag.style = func() tcell.Style {
		return tcell.StyleDefault.Background(e.theme.GlobalTagBG).Foreground(e.theme.GlobalTagFG)
	}
	e.tag.theme = &e.theme
	e.focusedView = e.tag

	if numCols < 1 {
		numCols = 1
	}
	colWidth := e.w / numCols
	for i := 0; i < numCols; i++ {
		w := colWidth
		if i == numCols-1 {
			w = e.w - (i * colWidth)
		}
		col := NewColumn(i*colWidth, 1, w, e.h-1, e, e.Execute)
		col.explicitWidth = w
		e.columns = append(e.columns, col)
	}
	e.resize()
	e.syncChildren()

	if len(args) > 0 {
		for _, arg := range args {
			full := e.resolvePathWithContext(nil, arg)
			content, isDir, writable, err := readFileOrDir(full)
			if err == nil {
				e.createWindow(e.columns[0], full, content, isDir, writable, -1, 0)
			}
		}
	} else {
		dir := getwd()
		lastCol := e.columns[len(e.columns)-1]
		win := e.createWindow(lastCol, dir, "", true, true, -1, 0)

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
			if e.tickRepeatingScroll() {
				e.Draw()
			}
		}
	}
}

func (e *Editor) Draw() {
	for y := 1; y < e.h; y++ {
		for x := 0; x < e.w; x++ {
			e.screen.SetContent(x, y, ' ', nil, tcell.StyleDefault)
		}
	}
	e.syncChildren()
	e.WalkLayout()
	e.WalkDraw(e.screen)
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
			if tv, ok := e.dragView.(*TextView); ok {
				e.updateSelectionScroll(e.windowOf(tv), tv, my, buttons)
			}
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
			if col.Contains(mx, my) {
				return col.HandleEvent(ev), true
			}
		}
	case *tcell.EventResize:
		e.w, e.h = e.screen.Size()
		e.resize()
		e.screen.Sync()
		return false, true
	}
	return false, true
}
func (e *Editor) updateSelectionScroll(win *Window, tv *TextView, my int, buttons tcell.ButtonMask) {
	if win == nil || buttons&tcell.Button1 == 0 || !tv.drag {
		if e.scrollWin == win {
			e.scrollWin = nil
		}
		return
	}
	dir := tv.selectionEdgeScrollDir(my)
	if dir == 0 {
		if e.scrollWin == win {
			e.scrollWin = nil
		}
		return
	}
	if e.scrollWin == nil {
		e.scrollStartTime = time.Now()
	}
	e.scrollWin, e.scrollAmount, e.scrollDir = win, 1, dir
}

func (e *Editor) tickRepeatingScroll() bool {
	if e.scrollWin == nil {
		return false
	}
	tv, isTextView := e.scrollWin.body.(*TextView)
	selectionScroll := isTextView && tv.drag
	if !selectionScroll && time.Since(e.scrollStartTime) <= 200*time.Millisecond {
		return false
	}
	before, _, _ := e.scrollWin.body.GetScroll()
	e.scrollWin.body.Scroll(e.scrollDir * e.scrollAmount)
	after, total, visible := e.scrollWin.body.GetScroll()
	if selectionScroll && e.scrollDir > 0 {
		after = min(after, max(0, total-visible))
		tv.scroll.Pos = after
	}
	if after == before {
		e.scrollWin = nil
		return false
	}
	if selectionScroll {
		tv.extendSelectionForEdgeScroll(e.scrollDir)
	}
	return true
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
		if len(e.columns) > 1 && mx > e.columns[1].x+e.columns[1].w/2 {
			e.columns[0], e.columns[1] = e.columns[1], e.columns[0]
			e.columns[0].explicitWidth, e.columns[1].explicitWidth = 0, 0
		}
	} else {
		prev := e.columns[idx-1]
		if mx < prev.x+2 { // Swap left
			e.columns[idx], e.columns[idx-1] = e.columns[idx-1], e.columns[idx]
			e.columns[idx].explicitWidth, e.columns[idx-1].explicitWidth = 0, 0
		} else {
			combinedW := prev.w + col.w
			minW := 5
			if mx < prev.x+minW {
				mx = prev.x + minW
			}
			if mx > prev.x+combinedW-minW {
				mx = prev.x + combinedW - minW
			}
			prev.explicitWidth = mx - prev.x
			col.explicitWidth = combinedW - prev.explicitWidth
		}
	}
	e.resize()
}

func (e *Editor) moveWindowTo(win *Window, mx, my int) {
	var target *Column
	for _, col := range e.columns {
		if mx >= col.x && mx < col.x+col.w {
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
				old.Resize(old.x, old.y, old.w, old.h)
				break
			}
		}
		win.parent, win.explicitHeight = target, 0
		newIdx := 0
		for _, w := range target.windows {
			if my < w.y+w.h/2 {
				break
			}
			newIdx++
		}
		target.windows = append(target.windows[:newIdx], append([]*Window{win}, target.windows[newIdx:]...)...)
		target.Resize(target.x, target.y, target.w, target.h)
		return
	}

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
		if len(target.windows) > 1 && my > target.windows[1].y+target.windows[1].tagHeight() {
			target.windows[0], target.windows[1] = target.windows[1], target.windows[0]
			target.windows[0].explicitHeight, target.windows[1].explicitHeight = 0, 0
		}
	} else {
		prev := target.windows[idx-1]
		if my < prev.y+prev.tagHeight() { // Swap up
			target.windows[idx], target.windows[idx-1] = target.windows[idx-1], target.windows[idx]
			target.windows[idx].explicitHeight, target.windows[idx-1].explicitHeight = 0, 0
		} else {
			combinedH := prev.h + win.h
			minH := win.tagHeight() + 1
			prevMinH := prev.tagHeight() + 1
			if my < prev.y+prevMinH {
				my = prev.y + prevMinH
			}
			if my > prev.y+combinedH-minH {
				my = prev.y + combinedH - minH
			}
			prev.explicitHeight = my - prev.y
			win.explicitHeight = combinedH - prev.explicitHeight
		}
	}
	target.Resize(target.x, target.y, target.w, target.h)
}

func (e *Editor) Resize() {
	e.resize()
}

func (e *Editor) resize() {
	if len(e.columns) == 0 {
		return
	}
	e.tag.Resize(0, 0, e.w, 1)

	if cap(e.columnNodes) < len(e.columns) {
		e.columnNodes = make([]DrawNode, len(e.columns))
	}
	nodes := e.columnNodes[:len(e.columns)]
	for i, col := range e.columns {
		nodes[i] = col
	}
	sizes := distribute(nodes, e.w, e.lastSize)
	e.lastSize = e.w

	x := 0
	for i, col := range e.columns {
		if col.explicitWidth > 0 {
			col.explicitWidth = sizes[i]
		}
		col.Resize(x, 1, sizes[i], e.h-1)
		x += sizes[i]
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
