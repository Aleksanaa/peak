package main

import (
	"context"
	"time"
)

// selectionPollInterval is how often the focused window's selection is
// resampled. peak broadcasts focus changes but has no selection event, so a
// drag inside the window already focused is only noticed by looking again.
const selectionPollInterval = time.Second

// notifySelection sends one agent what the user is currently looking at.
func (s *server) notifySelection(ctx context.Context, sess *session) {
	params, ok := s.currentSelection()
	if !ok {
		return
	}
	_ = sess.send(ctx, newNotification("selection_changed", params))
}

// watchSelections pushes selection_changed to every connected agent as the
// user moves around peak. This is how what-you-are-looking-at reaches an
// agent: the CLI injects it as context rather than exposing it as a tool.
func (s *server) watchSelections(ctx context.Context) {
	events, stop := s.peak.Subscribe()
	defer stop()

	ticker := time.NewTicker(selectionPollInterval)
	defer ticker.Stop()

	var lastFile, lastText string
	push := func() {
		// Nobody is listening, so skip the 9P reads entirely.
		if len(s.connected()) == 0 {
			return
		}
		params, ok := s.currentSelection()
		if !ok {
			return
		}
		file, _ := params["filePath"].(string)
		text, _ := params["text"].(string)
		if file == lastFile && text == lastText {
			return
		}
		lastFile, lastText = file, text
		s.broadcast(ctx, "selection_changed", params)
	}

	for {
		select {
		case ev := <-events:
			// A focus change is worth sampling immediately; the poll below
			// covers selections made without one.
			if ev.Kind == "focus" || ev.Kind == "new" {
				push()
			}
		case <-ticker.C:
			push()
		case <-ctx.Done():
			return
		}
	}
}

// currentSelection describes the focused window, or reports false when peak
// has not focused anything yet.
func (s *server) currentSelection() (map[string]any, bool) {
	id := s.peak.Focused()
	if id == 0 {
		return nil, false
	}
	sel, err := s.selectionOf(id)
	if err != nil {
		logf("selection of window %d: %v", id, err)
		return nil, false
	}
	return sel, true
}
