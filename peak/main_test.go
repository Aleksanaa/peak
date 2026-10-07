package main

import (
	"image"
	"testing"
	"time"

	"github.com/aleksana/peak/internal/session"
)

// Work the main goroutine waits on may itself Call into the main goroutine
// (e.g. an Edit pipe command reading peak's files over 9P); await must serve
// those Calls instead of deadlocking.
func TestAwaitServesCalls(t *testing.T) {
	e := &Editor{callCh: make(chan func())} // nothing else serves callCh

	done := make(chan bool)
	go func() {
		ran := false
		e.await(func() { e.Call(func() { ran = true }) })
		done <- ran
	}()

	select {
	case ran := <-done:
		if !ran {
			t.Error("await returned before the Call ran")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("await deadlocked on a Call made by the awaited work")
	}
}

// screenAt returns where v is on screen: the top-left corner of a column, a
// window or a view. Tests aim the mouse with it.
func screenAt(e *Editor, v any) image.Point {
	if v == any(e.tag) {
		return image.Point{}
	}
	for _, c := range e.columns {
		p := image.Pt(c.x, c.y)
		switch v {
		case c:
			return p
		case c.tag:
			return p.Add(image.Pt(c.tagRect().x, c.tagRect().y))
		}
		for _, w := range c.windows {
			p := p.Add(image.Pt(w.x, w.y))
			switch v {
			case w:
				return p
			case w.tag:
				return p.Add(image.Pt(w.tagRect().x, w.tagRect().y))
			case w.body:
				return p.Add(image.Pt(w.bodyRect().x, w.bodyRect().y))
			}
		}
	}
	panic("not on screen")
}

// addTerm adds to col a terminal running cmd in /tmp, skipping the test
// where none can start.
func addTerm(t *testing.T, col *Column, tag, cmd string) *Window {
	t.Helper()
	sess, err := session.NewLocal(cmd, "/tmp")
	if err == nil {
		var win *Window
		if win, err = col.AddTermWindow(tag, cmd, sess); err == nil {
			return win
		}
	}
	t.Skipf("cannot create term window: %v", err)
	return nil
}
