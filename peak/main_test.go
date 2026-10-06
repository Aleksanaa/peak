package main

import (
	"testing"
	"time"
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
