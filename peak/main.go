package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
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

// Editor is the main application state.
type Editor struct {
	TreeNode
	redrawCh      chan struct{} // capacity-1; 9P goroutines signal after state changes
	callCh        chan func()   // buffered; background goroutines dispatch UI callbacks
	screen        tcell.Screen
	tag           *TextView
	columns       []*Column
	columnNodes   []DrawNode
	active        *Window
	dragView      View
	dragWin       *Window
	dragWinOrigH  int
	dragWinButton tcell.ButtonMask
	dragWinStartY int
	dragCol       *Column
	dragColOrigW  int
	focusedView   View

	scrollWin       *Window
	scrollAmount    int
	scrollDir       int
	scrollStartTime time.Time
	lastClickY      int
	theme           Theme
	nextWinID       int
	ninep           *NineP
	gesture         mouseGesture
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
	e.callCh <- func() {
		f()
		close(done)
	}
	<-done
}

// Init sets up the initial editor state with the specified number of columns.
func (e *Editor) Init(numCols int, args []string, sessionFile string) {
	user, _ := os.UserHomeDir()
	logDir := filepath.Join(user, ".peak")
	os.MkdirAll(logDir, 0700)
	logFile, err := os.OpenFile(filepath.Join(logDir, "log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err == nil {
		log.SetOutput(logFile)
	}

	e.redrawCh = make(chan struct{}, 1)
	e.callCh = make(chan func(), 16)
	e.nextWinID = 1
	e.ninep = NewNineP(e)
	if err := e.ApplyTheme("catppuccin_mocha"); err != nil {
		log.Printf("theme: %v", err)
	}
	e.ninep.Listen()
	s, err := tcell.NewTerminfoScreen()
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

	if sessionFile != "" {
		if err := e.Load(sessionFile); err != nil {
			log.Printf("load session: %v", err)
		} else {
			return
		}
	}

	if len(args) > 0 {
		for _, arg := range args {
			full := normalizePath(arg, "")
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
	e.resize()
}

// Run enters the main event loop.
func (e *Editor) Run() {
	events := e.screen.EventQ()

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
			if quit, redraw := e.HandleEvent(ev); quit {
				return
			} else if redraw {
				e.Draw()
			}
		case fn := <-e.callCh:
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
		case <-tick:
			if e.scrollWin != nil && time.Since(e.scrollStartTime) > 200*time.Millisecond {
				scroll, total, visible := e.scrollWin.body.GetScroll()
				if !(e.scrollDir > 0 && scroll+visible >= total) {
					e.scrollWin.body.Scroll(e.scrollDir * e.scrollAmount)
					if dc, ok := e.scrollWin.body.(dragCursor); ok {
						dc.AdvanceDragCursor(e.scrollDir)
					}
				}
				e.Draw()
			}
		}
	}
}

// trackDragScroll sets or clears scrollWin when a drag selection reaches a
// view edge, so the 50ms timer keeps extending the selection automatically.
func (e *Editor) trackDragScroll(view View, my int) {
	if e.active == nil || view != e.active.body {
		return
	}
	bv := e.active.bodyView
	var dir int
	if my >= bv.y+bv.h-1 {
		dir = 1
	} else if my <= bv.y {
		dir = -1
	}
	if dir != 0 {
		e.scrollWin = e.active
		e.scrollAmount, e.scrollDir = 1, dir
		e.scrollStartTime = time.Time{} // zero bypasses the 200ms delay
	} else if e.scrollWin != nil && e.scrollWin.body == view {
		e.scrollWin = nil
	}
}

func (e *Editor) Draw() {
	e.screen.Clear()
	e.syncChildren()
	e.WalkLayout()
	e.WalkDraw(e.screen)
	e.focusedView.ShowCursor(e.screen)
	e.screen.Show()
}

func (e *Editor) HandleEvent(ev tcell.Event) (bool, bool) {
	if me, ok := ev.(*tcell.EventMouse); ok {
		if me.Buttons() != tcell.ButtonNone {
			_, my := me.Position()
			e.lastClickY = my
		} else if e.dragCol == nil && e.dragWin == nil && e.dragView == nil && e.scrollWin == nil && !e.gesture.chorded {
			// Skip redraw on mouse moves with no buttons/drag/scroll
			return false, false
		}
	}

	switch ev := ev.(type) {
	case *tcell.EventKey:
		if ev.Key() == tcell.KeyCtrlF {
			if tv, ok := e.focusedView.(*TextView); ok && tv.buffer.GetSelectedText() != "" {
				return e.Execute(nil, nil, "Look"), true
			}
		}
		win := e.windowOf(e.focusedView)
		if win != nil {
			win.lk.Lock()
		}
		quit := e.focusedView.HandleEvent(ev)
		if win != nil {
			win.lk.Unlock()
		}
		return quit, true
	case *tcell.EventMouse:
		return e.handleMouse(ev), true
	case *tcell.EventResize:
		e.w, e.h = e.screen.Size()
		e.resize()
		e.screen.Sync()
		return false, true
	}
	return false, true
}

// windowOf returns the Window that owns view v, or nil for the global tag.
func (e *Editor) windowOf(v View) *Window {
	var found *Window
	e.Walk(func(d DrawNode) {
		if w, ok := d.(*Window); ok && (w.body == v || w.tag == v) {
			found = w
		}
	})
	return found
}

func (e *Editor) ActivateWindow(win *Window) {
	prev := e.active
	e.active = win
	e.focusedView = win.body
	if prev != win {
		e.ninep.BroadcastFocus(win)
	}
}

func (e *Editor) moveColumnTo(col *Column, mx int) {
	idx := slices.Index(e.columns, col)
	n := len(e.columns)

	if idx < n-1 && mx > e.columns[idx+1].x+e.columns[idx+1].w/2 {
		delta := e.dragColOrigW - col.explicitWidth
		e.columns[idx], e.columns[idx+1] = e.columns[idx+1], e.columns[idx]
		e.columns[idx+1].explicitWidth = e.dragColOrigW
		if idx > 0 {
			e.columns[idx-1].explicitWidth -= delta
		}
		e.resize()
		return
	}
	if idx == 0 {
		return
	}
	prev := e.columns[idx-1]
	combinedW := prev.w + col.w
	if mx < prev.x+2 {
		e.columns[idx], e.columns[idx-1] = e.columns[idx-1], e.columns[idx]
		e.columns[idx-1].explicitWidth = e.dragColOrigW
		e.columns[idx].explicitWidth = combinedW - e.dragColOrigW
	} else {
		newW := max(5, min(combinedW-5, mx-prev.x))
		if newW == prev.explicitWidth {
			return
		}
		col.explicitWidth += prev.explicitWidth - newW
		prev.explicitWidth = newW
	}
	e.resize()
}

func (e *Editor) moveWindowTo(win *Window, mx, my int) {
	colIdx := slices.Index(e.columns, win.parent)
	cur := e.columns[colIdx]

	var toCol *Column
	if colIdx < len(e.columns)-1 && mx >= cur.x+cur.w {
		toCol = e.columns[colIdx+1]
	} else if colIdx > 0 {
		prev := e.columns[colIdx-1]
		if mx < prev.x+prev.w-prev.w/4 {
			toCol = prev
		}
	}

	if toCol != nil {
		i := slices.Index(cur.windows, win)
		cur.windows = slices.Delete(cur.windows, i, i+1)
		if cur.maximized == win {
			cur.maximized = nil
		}
		cur.Resize(cur.x, cur.y, cur.w, cur.h)
		win.parent, win.explicitHeight = toCol, 0
		newIdx := 0
		for _, w := range toCol.windows {
			if my < w.y+w.h/2 {
				break
			}
			newIdx++
		}
		toCol.windows = slices.Insert(toCol.windows, newIdx, win)
		toCol.Resize(toCol.x, toCol.y, toCol.w, toCol.h)
		e.dragWinOrigH = win.explicitHeight
		e.dragWinStartY = -1 // window moved columns; suppress grow-on-release
		return
	}

	wins := cur.windows
	idx := slices.Index(wins, win)
	n := len(wins)

	if idx < n-1 && my > wins[idx+1].y {
		delta := e.dragWinOrigH - win.explicitHeight
		wins[idx], wins[idx+1] = wins[idx+1], wins[idx]
		wins[idx+1].explicitHeight = e.dragWinOrigH
		if idx > 0 {
			wins[idx-1].explicitHeight -= delta
		}
		cur.Resize(cur.x, cur.y, cur.w, cur.h)
		return
	}
	if idx == 0 {
		return
	}
	prev := wins[idx-1]
	combinedH := prev.h + win.h
	if my < prev.y+prev.tagHeight() {
		wins[idx], wins[idx-1] = wins[idx-1], wins[idx]
		wins[idx-1].explicitHeight = e.dragWinOrigH
		wins[idx].explicitHeight = combinedH - e.dragWinOrigH
	} else {
		newH := max(prev.tagHeight(), min(combinedH-win.tagHeight(), my-prev.y))
		if newH == prev.explicitHeight {
			return
		}
		win.explicitHeight += prev.explicitHeight - newH
		prev.explicitHeight = newH
	}
	cur.Resize(cur.x, cur.y, cur.w, cur.h)
}

func (e *Editor) resize() {
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
		col.explicitWidth = sizes[i]
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
		return color.Default
	}
}

var appEditor *Editor

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [-c columns] [file...]\n", os.Args[0])
		flag.PrintDefaults()
	}
	cols := flag.Int("c", 2, "number of columns")
	load := flag.String("l", "", "session file to load at startup")
	flag.Parse()

	appEditor = &Editor{}
	appEditor.Init(*cols, flag.Args(), *load)
	defer appEditor.screen.Fini()
	appEditor.Run()
}
