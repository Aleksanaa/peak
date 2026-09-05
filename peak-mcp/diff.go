package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// The IDE protocol's openDiff is a blocking call: the editor shows the
// proposed change and the tool does not return until the user accepts or
// rejects it. peak has no diff view and no accept/reject buttons, so the
// acme-native equivalent is used instead: the proposal is loaded into the
// file's window as unsaved text, and the verdict is whatever the user does
// with it. Put means accept, reverting or closing means reject. A scratch
// window alongside shows the diff so the change can be read before deciding.
const (
	diffAccepted = "FILE_SAVED"
	diffRejected = "DIFF_REJECTED"
)

type diffRequest struct {
	OldFilePath     string `json:"old_file_path"`
	NewFilePath     string `json:"new_file_path"`
	NewFileContents string `json:"new_file_contents"`
	TabName         string `json:"tab_name"`
}

// target is the file the proposal applies to.
func (r diffRequest) target() string {
	if r.OldFilePath != "" {
		return r.OldFilePath
	}
	return r.NewFilePath
}

func (s *server) openDiff(ctx context.Context, req diffRequest) (any, error) {
	path := req.target()
	if path == "" {
		return nil, fmt.Errorf("openDiff needs old_file_path or new_file_path")
	}

	// Subscribe before touching anything, so the verdict cannot land in the
	// gap between loading the proposal and starting to watch for it.
	events, stop := s.peak.Subscribe()
	defer stop()

	id, err := s.openPath(ctx, path)
	if err != nil {
		return nil, err
	}
	original, err := s.peak.Body(id)
	if err != nil {
		return nil, err
	}
	if original == req.NewFileContents {
		return diffAccepted, nil // nothing to decide
	}

	review, err := s.showDiff(req.TabName, path, original, req.NewFileContents)
	if err != nil {
		log.Printf("diff window: %v", err) // reviewable without it; carry on
	}
	defer s.closeWindow(review)

	if err := s.peak.SetBody(id, req.NewFileContents); err != nil {
		return nil, err
	}

	for {
		select {
		case ev := <-events:
			if ev.ID != id {
				continue
			}
			switch ev.Kind {
			case "put":
				return diffAccepted, nil
			case "get":
				// Get reloads from disk, which is how a user throws the
				// proposal away without closing the window.
				return diffRejected, nil
			case "close":
				return diffRejected, nil
			}
		case <-ctx.Done():
			// The agent disconnected or was interrupted. Leave the buffer as
			// it is rather than silently undoing what the user may be editing.
			return nil, ctx.Err()
		}
	}
}

// showDiff opens a scratch window holding a readable diff and returns its id,
// or 0 when one could not be opened.
func (s *server) showDiff(tabName, path, old, new string) (int, error) {
	id, err := s.peak.NewWindow()
	if err != nil {
		return 0, err
	}
	name := tabName
	if name == "" {
		name = filepath.Base(path)
	}
	// Tag names are whitespace-delimited, and a leading + marks a window as
	// peak's own scratch output rather than a file.
	name = "+mcp/" + strings.Join(strings.Fields(name), "_")
	if err := s.peak.SetTag(id, " "+name+" Del \n"); err != nil {
		return id, err
	}
	if err := s.peak.SetBody(id, unifiedish(path, old, new)); err != nil {
		return id, err
	}
	s.trackDiffWindow(id)
	return id, nil
}

func (s *server) closeWindow(id int) {
	if id == 0 {
		return
	}
	s.untrackDiffWindow(id)
	if err := s.peak.Ctl(id, "Del"); err != nil {
		log.Printf("close window %d: %v", id, err)
	}
}

func (s *server) trackDiffWindow(id int) {
	s.mu.Lock()
	s.diffWindows[id] = struct{}{}
	s.mu.Unlock()
}

func (s *server) untrackDiffWindow(id int) {
	s.mu.Lock()
	delete(s.diffWindows, id)
	s.mu.Unlock()
}

func (s *server) closeAllDiffTabs() (any, error) {
	s.mu.Lock()
	ids := make([]int, 0, len(s.diffWindows))
	for id := range s.diffWindows {
		ids = append(ids, id)
	}
	s.diffWindows = make(map[int]struct{})
	s.mu.Unlock()

	for _, id := range ids {
		if err := s.peak.Ctl(id, "Del"); err != nil {
			log.Printf("close window %d: %v", id, err)
		}
	}
	return fmt.Sprintf("Closed %d review window(s)", len(ids)), nil
}

// unifiedish renders a line diff with the familiar -/+ prefixes. It is not
// unified diff - there are no hunk headers and no line numbers - but it is
// what a reader needs to see what changed, and it stays readable in a peak
// window with no diff view.
func unifiedish(path, old, new string) string {
	dmp := diffmatchpatch.New()
	a, b, lines := dmp.DiffLinesToChars(old, new)
	diffs := dmp.DiffCharsToLines(dmp.DiffMain(a, b, false), lines)

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s (proposed)\n", path, path)
	sb.WriteString("Put the file window to accept, or Get to throw this away.\n\n")
	for _, d := range diffs {
		prefix := " "
		switch d.Type {
		case diffmatchpatch.DiffInsert:
			prefix = "+"
		case diffmatchpatch.DiffDelete:
			prefix = "-"
		}
		for _, line := range strings.SplitAfter(d.Text, "\n") {
			if line == "" {
				continue
			}
			sb.WriteString(prefix)
			sb.WriteString(line)
			if !strings.HasSuffix(line, "\n") {
				sb.WriteString("\n")
			}
		}
	}
	return sb.String()
}
