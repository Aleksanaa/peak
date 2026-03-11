package main

import (
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
	SelectionBG, SelectionFG          tcell.Color
	Corner                            tcell.Color
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
	Corner:       tcell.NewHexColor(0xb4befe),
}

// Editor is the main application state.
type Editor struct {
	CmdChan     chan func()
	screen      tcell.Screen
	tag         *TextView
	columns     []*Column
	active      *Window
	width       int
	height      int
	dragView    *TextView
	dragWin     *Window
	dragCol     *Column
	focusedView *TextView

	scrollWin       *Window
	scrollAmount    int
	scrollDir       int
	scrollStartTime time.Time
	lastWidth       int
	lastClickY      int
	theme           Theme
	nextWinID       int
	ninep           *NineP
}

func (e *Editor) Call(f func()) {
	done := make(chan struct{})
	e.CmdChan <- func() {
		f()
		close(done)
	}
	<-done
}

// Init sets up the initial editor state with two columns.
func (e *Editor) Init() {
	user, _ := os.UserHomeDir()
	logDir := filepath.Join(user, ".peak")
	os.MkdirAll(logDir, 0700)
	logFile, err := os.OpenFile(filepath.Join(logDir, "log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err == nil {
		log.SetOutput(logFile)
	}

	e.CmdChan = make(chan func())
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

	// Start with two columns
	colLeft := NewColumn(0, 1, e.width/2, e.height-1, e, e.Execute)
	e.columns = append(e.columns, colLeft)

	colRight := NewColumn(e.width/2, 1, e.width-e.width/2, e.height-1, e, e.Execute)
	e.columns = append(e.columns, colRight)

	dir := getwd()
	win := colRight.AddWindow(" "+dir+" Get Put Undo Redo Snarf Zerox Del ", "")
	e.ActivateWindow(win)

	// Initial directory listing
	e.Execute(colRight, win, "Get")
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

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		e.Draw()
		select {
		case ev := <-events:
			if ev == nil {
				return
			}
			switch ev := ev.(type) {
			case *tcell.EventInterrupt:
				if f, ok := ev.Data().(func()); ok {
					f()
				}
			default:
				if e.HandleEvent(ev) {
					return
				}
			}
		case fn := <-e.CmdChan:
			fn()
		case <-ticker.C:
			if e.scrollWin != nil && time.Since(e.scrollStartTime) > 200*time.Millisecond {
				e.scrollWin.body.Scroll(e.scrollDir * e.scrollAmount)
			}
		}
	}
}

func (e *Editor) Draw() {
	e.screen.Clear()
	e.tag.Draw(e.screen)
	for _, col := range e.columns {
		col.Draw(e.screen)
	}
	if e.focusedView != nil {
		e.focusedView.ShowCursor(e.screen)
	}
	e.screen.Show()
}

func (e *Editor) HandleEvent(ev tcell.Event) bool {
	if me, ok := ev.(*tcell.EventMouse); ok {
		if me.Buttons() != tcell.ButtonNone {
			_, my := me.Position()
			e.lastClickY = my
		}
	}

	switch ev := ev.(type) {
	case *tcell.EventKey:
		if ev.Key() == tcell.KeyCtrlF {
			if e.focusedView != nil && e.focusedView.buffer.GetSelectedText() != "" {
				return e.Execute(nil, nil, "Look")
			}
		}
		if e.focusedView != nil {
			return e.focusedView.HandleEvent(ev)
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
				return false
			}
			e.dragCol = nil
			return false
		}
		if e.dragWin != nil {
			if buttons&tcell.Button1 != 0 {
				e.moveWindowTo(e.dragWin, mx, my)
				return false
			}
			e.dragWin = nil
			return false
		}
		if e.dragView != nil {
			e.dragView.HandleEvent(ev)
			if buttons == tcell.ButtonNone {
				e.dragView = nil
			}
			return false
		}

		// Global Tag clicks
		if my == 0 {
			word := e.tag.GetClickWord(mx, my)
			if word != "" {
				if buttons == tcell.Button3 { // Middle-click
					return e.Execute(nil, nil, word)
				}
				if buttons == tcell.Button2 { // Right-click
					return e.Plumb(nil, word)
				}
			}
			if buttons == tcell.Button1 {
				e.dragView, e.focusedView = e.tag, e.tag
			}
			return e.tag.HandleEvent(ev)
		}

		for _, col := range e.columns {
			if col.Contains(mx, my) {
				return col.HandleEvent(ev)
			}
		}
	case *tcell.EventResize:
		e.width, e.height = e.screen.Size()
		e.resize()
		e.screen.Sync()
	}
	return false
}

func (e *Editor) ActivateWindow(win *Window) {
	if win == nil {
		return
	}
	e.active = win
	e.focusedView = win.body
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
	e.tag.Resize(0, 0, e.width, 1)

	widths := distributeSpace(e.width, len(e.columns), func(i int) int {
		return e.columns[i].explicitWidth
	}, func(i int) int {
		return 5
	}, e.lastWidth, e.width)
	e.lastWidth = e.width

	xOffset := 0
	for i, col := range e.columns {
		cw := widths[i]
		col.explicitWidth = cw
		col.Resize(xOffset, 1, cw, e.height-1)
		xOffset += cw
	}
}

func distributeSpace(totalSpace int, count int, getExplicit func(int) int, getMin func(int) int, lastTotal, currentTotal int) []int {
	heights := make([]int, count)
	totalExplicit, numAuto := 0, 0

	// 1. Proportional scaling
	scaleRatio := 1.0
	if lastTotal > 0 && lastTotal != currentTotal {
		scaleRatio = float64(currentTotal) / float64(lastTotal)
	}

	for i := 0; i < count; i++ {
		exp := getExplicit(i)
		if exp > 0 {
			heights[i] = int(float64(exp) * scaleRatio)
			totalExplicit += heights[i]
		} else {
			numAuto++
		}
	}

	// 2. Redistribute if full
	if numAuto > 0 && totalExplicit >= totalSpace {
		targetTotalAuto := (totalSpace * numAuto) / (count + 1)
		if targetTotalAuto < 5*numAuto {
			targetTotalAuto = 5 * numAuto
		}
		if totalExplicit > 0 {
			scale := float64(totalSpace-targetTotalAuto) / float64(totalExplicit)
			totalExplicit = 0
			for i := 0; i < count; i++ {
				if getExplicit(i) > 0 {
					heights[i] = int(float64(heights[i]) * scale)
					totalExplicit += heights[i]
				}
			}
		}
	}

	// 3. Final layout
	autoSpace := 0
	if numAuto > 0 {
		autoSpace = (totalSpace - totalExplicit) / numAuto
		if autoSpace < 5 {
			autoSpace = 5
		}
	}

	actualTotal := 0
	for i := 0; i < count; i++ {
		h := heights[i]
		if h <= 0 {
			h = autoSpace
		}
		min := getMin(i)
		if h < min {
			h = min
		}
		heights[i] = h
		actualTotal += h
	}

	// Adjust last one to fit exactly
	if count > 0 {
		diff := totalSpace - actualTotal
		heights[count-1] += diff
		if heights[count-1] < getMin(count-1) {
			heights[count-1] = getMin(count - 1)
		}
	}

	return heights
}

var appEditor *Editor

func main() {
	appEditor = &Editor{}
	appEditor.Init()
	defer appEditor.screen.Fini()
	appEditor.Run()
}
