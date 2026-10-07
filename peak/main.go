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
)

// Editor is the main application state.
type Editor struct {
	w, h        int
	lastSize    int
	redrawCh    chan struct{} // capacity-1; 9P goroutines signal after state changes
	callCh      chan func()   // buffered; background goroutines dispatch UI callbacks
	screen      tcell.Screen
	tag         *TextView
	columns     []*Column
	active      *Window
	focusedView View

	// capture, while a press's gesture lasts, receives its later mouse
	// events; it reports whether the gesture goes on.
	capture func(*tcell.EventMouse) bool
	// repeat, while a held button auto-scrolls, runs every tick.
	repeat func()

	theme     Theme
	nextWinID int
	ninep     *NineP
	gesture   mouseGesture
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

// await runs fn on another goroutine and serves Calls until it returns. The
// main goroutine waits this way on work that may itself need the main
// goroutine, such as a shell command reading peak's files over 9P.
func (e *Editor) await(fn func()) {
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	for {
		select {
		case <-done:
			return
		case f := <-e.callCh:
			f()
		}
	}
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

	s, err := tcell.NewTerminfoScreen()
	if err != nil {
		log.Fatalf("%+v", err)
	}
	if err := s.Init(); err != nil {
		log.Fatalf("%+v", err)
	}
	s.EnableMouse()
	e.setup(s)
	e.ninep.Listen()

	if numCols < 1 {
		numCols = 1
	}
	colWidth := e.w / numCols
	for i := 0; i < numCols; i++ {
		w := colWidth
		if i == numCols-1 {
			w = e.w - (i * colWidth)
		}
		col := NewColumn(i*colWidth, 1, w, e.h-1, e)
		col.explicitWidth = w
		e.columns = append(e.columns, col)
	}
	e.resize()

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

// setup builds an empty editor on screen s: no columns yet, and the 9P
// socket not yet listening.
func (e *Editor) setup(s tcell.Screen) {
	e.screen = s
	e.w, e.h = s.Size()
	e.redrawCh = make(chan struct{}, 1)
	e.callCh = make(chan func(), 16)
	e.nextWinID = 1
	e.ninep = NewNineP(e)
	if err := e.ApplyTheme("catppuccin_mocha"); err != nil {
		log.Printf("theme: %v", err)
	}

	e.tag = NewTextView(" NewCol Help Exit ", e.w, 1, &e.theme, &e.theme.GlobalTag, true, false)
	e.focusedView = e.tag
}

// Run enters the main event loop.
func (e *Editor) Run() {
	events := e.screen.EventQ()

	e.Draw()
	for {
		var timer *time.Timer
		var tick <-chan time.Time
		if e.repeat != nil {
			timer = time.NewTimer(50 * time.Millisecond)
			tick = timer.C
		}

		select {
		case ev := <-events:
			if ev == nil {
				return
			}
			if quit, redraw := e.HandleEvent(ev); quit {
				return
			} else if redraw {
				e.Draw()
			}
		case fn := <-e.callCh:
			fn()
			// Coalesce: a burst of calls (e.g. 9P traffic) costs one Draw.
			e.Redraw()
		case <-e.redrawCh:
			e.Draw()
		case <-tick:
			e.repeat()
			e.Draw()
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

func (e *Editor) Draw() {
	e.screen.Clear()
	e.screen.HideCursor()
	cv := canvas{e.screen, 0, 0, e.w, e.h}
	e.drawView(e.tag, cv.sub(rect{0, 0, e.w, 1}))
	for _, c := range e.columns {
		c.Draw(cv.sub(c.rect))
	}
	e.screen.Show()
}

// drawView lays out and draws v on cv, with the cursor if v has the focus.
func (e *Editor) drawView(v View, cv canvas) {
	v.Layout()
	v.Draw(cv)
	if v == e.focusedView {
		v.ShowCursor(cv)
	}
}

func (e *Editor) HandleEvent(ev tcell.Event) (bool, bool) {
	if me, ok := ev.(*tcell.EventMouse); ok && me.Buttons() == tcell.ButtonNone &&
		e.capture == nil && !e.gesture.chorded {
		// Skip redraw on mouse moves with no buttons/drag/scroll
		return false, false
	}

	switch ev := ev.(type) {
	case *tcell.EventKey:
		if ev.Key() == tcell.KeyCtrlF {
			if tv, ok := e.focusedView.(*TextView); ok && tv.buffer.GetSelectedText() != "" {
				return e.Execute(nil, nil, "Look"), true
			}
		}
		e.focusedView.HandleEvent(ev)
		return false, true
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

func (e *Editor) ActivateWindow(win *Window) {
	prev := e.active
	e.active = win
	e.focusedView = win.body
	if prev != win {
		e.ninep.BroadcastFocus(win)
	}
}

// showWindow activates win, just added to its column, and lays the column out
// around it.
func (e *Editor) showWindow(win *Window) {
	e.ActivateWindow(win)
	win.parent.Resize(win.parent.rect)
}

// moveColumnTo drags col, origW wide when the drag began, to the screen
// column mx.
func (e *Editor) moveColumnTo(col *Column, mx, origW int) {
	idx := slices.Index(e.columns, col)
	n := len(e.columns)

	if idx < n-1 && mx > e.columns[idx+1].x+e.columns[idx+1].w/2 {
		delta := origW - col.explicitWidth
		e.columns[idx], e.columns[idx+1] = e.columns[idx+1], e.columns[idx]
		e.columns[idx+1].explicitWidth = origW
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
		e.columns[idx-1].explicitWidth = origW
		e.columns[idx].explicitWidth = combinedW - origW
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

// moveWindowTo drags win, origH high when the drag began, to the screen
// position (mx, my): into the next or previous column once the pointer is
// far enough into it, otherwise within its own. It reports whether win moved
// to another column.
func (e *Editor) moveWindowTo(win *Window, mx, my, origH int) bool {
	cur := win.parent
	i := slices.Index(e.columns, cur)
	var to *Column
	if i < len(e.columns)-1 && mx >= cur.x+cur.w {
		to = e.columns[i+1]
	} else if i > 0 {
		if prev := e.columns[i-1]; mx < prev.x+prev.w-prev.w/4 {
			to = prev
		}
	}
	if to == nil {
		cur.moveWindow(win, my-cur.y, origH)
		return false
	}
	cur.remove(win)
	to.insert(win, my-to.y)
	return true
}

func (e *Editor) resize() {
	e.tag.Resize(e.w, 1)
	sizes := distribute(e.columns, e.w, e.lastSize)
	e.lastSize = e.w

	x := 0
	for i, col := range e.columns {
		col.explicitWidth = sizes[i]
		col.Resize(rect{x, 1, sizes[i], e.h - 1})
		x += sizes[i]
	}
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [-c columns] [file...]\n", os.Args[0])
		flag.PrintDefaults()
	}
	cols := flag.Int("c", 2, "number of columns")
	load := flag.String("l", "", "session file to load at startup")
	flag.Parse()

	e := &Editor{}
	e.Init(*cols, flag.Args(), *load)
	defer e.screen.Fini()
	e.Run()
}
