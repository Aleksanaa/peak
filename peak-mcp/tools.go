package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// openTimeout bounds the wait for peak to report a window it was asked to
// open. Everything peak does here is local, so a slow answer means something
// went wrong rather than something being slow.
const openTimeout = 5 * time.Second

// ideTools is the tool set Claude Code drives an editor with. Only some of
// these are offered to the model; the rest the CLI calls on its own -
// openDiff on every edit, the selection and editor queries for context.
func (s *server) ideTools() []tool {
	return []tool{{
		Name:        "getWorkspaceFolders",
		Description: "Get the directories peak currently has open.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		call: func(_ context.Context, _ json.RawMessage) (any, error) {
			return s.getWorkspaceFolders()
		},
	}, {
		Name:        "getOpenEditors",
		Description: "List the windows open in peak.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		call: func(_ context.Context, _ json.RawMessage) (any, error) {
			return s.getOpenEditors()
		},
	}, {
		Name:        "getCurrentSelection",
		Description: "Get the text selected in the focused peak window.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		call: func(_ context.Context, _ json.RawMessage) (any, error) {
			return s.getSelection()
		},
	}, {
		Name:        "getLatestSelection",
		Description: "Get the most recent selection in peak.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		call: func(_ context.Context, _ json.RawMessage) (any, error) {
			return s.getSelection()
		},
	}, {
		Name:        "openFile",
		Description: "Open a file in peak.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"filePath":{"type":"string"}},"required":["filePath"]}`),
		call: func(ctx context.Context, args json.RawMessage) (any, error) {
			var a struct {
				FilePath string `json:"filePath"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, err
			}
			return s.openFile(ctx, a.FilePath)
		},
	}, {
		Name:        "openDiff",
		Description: "Load proposed contents into the file's peak window and wait for the user to Put (accept) or revert (reject).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"old_file_path":{"type":"string"},"new_file_path":{"type":"string"},"new_file_contents":{"type":"string"},"tab_name":{"type":"string"}},"required":["new_file_contents"]}`),
		call: func(ctx context.Context, args json.RawMessage) (any, error) {
			var a diffRequest
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, err
			}
			return s.openDiff(ctx, a)
		},
	}, {
		Name:        "close_tab",
		Description: "Close a peak window by name.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"tab_name":{"type":"string"}},"required":["tab_name"]}`),
		call: func(_ context.Context, args json.RawMessage) (any, error) {
			var a struct {
				TabName string `json:"tab_name"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, err
			}
			return s.closeTab(a.TabName)
		},
	}, {
		Name:        "closeAllDiffTabs",
		Description: "Close the review windows peak-mcp opened.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		call: func(_ context.Context, _ json.RawMessage) (any, error) {
			return s.closeAllDiffTabs()
		},
	}, {
		Name:        "checkDocumentDirty",
		Description: "Report whether a file has unsaved changes in peak.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"filePath":{"type":"string"}},"required":["filePath"]}`),
		call: func(_ context.Context, args json.RawMessage) (any, error) {
			var a struct {
				FilePath string `json:"filePath"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, err
			}
			return s.checkDocumentDirty(a.FilePath)
		},
	}, {
		Name:        "saveDocument",
		Description: "Save a file open in peak (Put).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"filePath":{"type":"string"}},"required":["filePath"]}`),
		call: func(_ context.Context, args json.RawMessage) (any, error) {
			var a struct {
				FilePath string `json:"filePath"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, err
			}
			return s.saveDocument(a.FilePath)
		},
	}, {
		Name:        "getDiagnostics",
		Description: "Language diagnostics. peak has no language server, so this is always empty.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"uri":{"type":"string"}}}`),
		call: func(_ context.Context, _ json.RawMessage) (any, error) {
			// Answering with an empty set is the honest result and keeps the
			// agent from waiting on diagnostics that will never arrive.
			return []any{}, nil
		},
	}}
}

func fileURL(path string) string {
	if path == "" {
		return ""
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func (s *server) getWorkspaceFolders() (any, error) {
	dirs := workspaceFolders(s.peak)
	folders := make([]map[string]any, 0, len(dirs))
	for _, d := range dirs {
		folders = append(folders, map[string]any{
			"name": filepath.Base(d),
			"uri":  fileURL(d),
			"path": d,
		})
	}
	out := map[string]any{"folders": folders}
	if len(dirs) > 0 {
		out["rootPath"] = dirs[0]
	}
	return out, nil
}

func (s *server) getOpenEditors() (any, error) {
	wins, err := s.peak.Index()
	if err != nil {
		return nil, err
	}
	focused := s.peak.Focused()
	tabs := make([]map[string]any, 0, len(wins))
	for _, w := range wins {
		if w.IsDir {
			continue // a directory listing is not an editor tab
		}
		tabs = append(tabs, map[string]any{
			"uri":        fileURL(w.Name),
			"filePath":   w.Name,
			"label":      filepath.Base(w.Name),
			"isActive":   w.ID == focused,
			"isDirty":    w.Dirty,
			"languageId": languageID(w.Name),
		})
	}
	return map[string]any{"tabs": tabs}, nil
}

// getSelection reports what is selected in the focused window.
func (s *server) getSelection() (any, error) {
	id := s.peak.Focused()
	if id == 0 {
		return map[string]any{"success": false, "message": "peak has not reported a focused window yet"}, nil
	}
	sel, err := s.selectionOf(id)
	if err != nil {
		return nil, err
	}
	sel["success"] = true
	return sel, nil
}

// selectionOf describes what is selected in a window, in the shape both
// getCurrentSelection and the selection_changed notification use. peak exposes
// the selected text but not its offsets, so the range is only reported when
// the text occurs exactly once in the body and can be located unambiguously.
func (s *server) selectionOf(id int) (map[string]any, error) {
	text, err := s.peak.Selection(id)
	if err != nil {
		return nil, err
	}
	name := ""
	if wins, err := s.peak.Index(); err == nil {
		for _, w := range wins {
			if w.ID == id {
				name = w.Name
				break
			}
		}
	}
	if text == "" {
		return map[string]any{
			"text":     "",
			"filePath": name,
			"fileUrl":  fileURL(name),
			"selection": map[string]any{
				"start":   map[string]int{"line": 0, "character": 0},
				"end":     map[string]int{"line": 0, "character": 0},
				"isEmpty": true,
			},
		}, nil
	}

	sel := map[string]any{
		"start":   map[string]int{"line": 0, "character": 0},
		"end":     map[string]int{"line": 0, "character": 0},
		"isEmpty": false,
	}
	if body, err := s.peak.Body(id); err == nil {
		if off := strings.Index(body, text); off >= 0 && off == strings.LastIndex(body, text) {
			startLine, startChar := position(body, off)
			endLine, endChar := position(body, off+len(text))
			sel["start"] = map[string]int{"line": startLine, "character": startChar}
			sel["end"] = map[string]int{"line": endLine, "character": endChar}
		}
	}
	return map[string]any{
		"text":      text,
		"filePath":  name,
		"fileUrl":   fileURL(name),
		"selection": sel,
	}, nil
}

func (s *server) openFile(ctx context.Context, path string) (any, error) {
	if path == "" {
		return nil, fmt.Errorf("filePath is required")
	}
	id, err := s.openPath(ctx, path)
	if err != nil {
		return nil, err
	}
	return fmt.Sprintf("Opened %s in peak window %d", path, id), nil
}

func (s *server) closeTab(name string) (any, error) {
	w, ok := s.peak.FindWindow(name)
	if !ok {
		return nil, fmt.Errorf("no peak window named %s", name)
	}
	if err := s.peak.Ctl(w.ID, "Del"); err != nil {
		return nil, err
	}
	return fmt.Sprintf("Closed %s", name), nil
}

func (s *server) checkDocumentDirty(path string) (any, error) {
	w, ok := s.peak.FindWindow(path)
	if !ok {
		return map[string]any{"success": false, "message": fmt.Sprintf("%s is not open in peak", path)}, nil
	}
	return map[string]any{
		"success":  true,
		"filePath": path,
		"isDirty":  w.Dirty,
	}, nil
}

func (s *server) saveDocument(path string) (any, error) {
	w, ok := s.peak.FindWindow(path)
	if !ok {
		return nil, fmt.Errorf("%s is not open in peak", path)
	}
	if err := s.peak.Ctl(w.ID, "Put"); err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "filePath": path}, nil
}

// openPath returns the window editing path, opening it if peak does not have
// it already. peak has no "open this file" control file, so the file is opened
// by running New in an existing window, exactly as a user would.
func (s *server) openPath(ctx context.Context, path string) (int, error) {
	if w, ok := s.peak.FindWindow(path); ok {
		return w.ID, nil
	}
	if strings.ContainsAny(path, " \t") {
		return 0, fmt.Errorf("cannot open %q: peak commands split on whitespace", path)
	}

	events, stop := s.peak.Subscribe()
	defer stop()

	host, err := s.hostWindow()
	if err != nil {
		return 0, err
	}
	if err := s.peak.Ctl(host, "New "+path); err != nil {
		return 0, err
	}

	deadline := time.After(openTimeout)
	for {
		select {
		case ev := <-events:
			if ev.Kind == "new" && ev.Name == path {
				return ev.ID, nil
			}
		case <-deadline:
			// The event may have been dropped under load; fall back to asking.
			if w, ok := s.peak.FindWindow(path); ok {
				return w.ID, nil
			}
			return 0, fmt.Errorf("peak did not open %s", path)
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

// hostWindow returns a window whose ctl can run a command. Commands need a
// window for their column context; any open one will do.
func (s *server) hostWindow() (int, error) {
	if id := s.peak.Focused(); id != 0 {
		return id, nil
	}
	wins, err := s.peak.Index()
	if err != nil {
		return 0, err
	}
	if len(wins) > 0 {
		return wins[0].ID, nil
	}
	return s.peak.NewWindow()
}

// position converts a byte offset into zero-based line and character, the
// coordinates the IDE protocol uses.
func position(text string, off int) (line, char int) {
	if off > len(text) {
		off = len(text)
	}
	line = strings.Count(text[:off], "\n")
	lineStart := strings.LastIndexByte(text[:off], '\n') + 1
	return line, len([]rune(text[lineStart:off]))
}

// languageID maps a filename to the identifier editors use for its language.
// It covers what peak itself highlights; anything else is reported by
// extension, which is more useful to the agent than an empty string.
func languageID(path string) string {
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	switch ext {
	case "go":
		return "go"
	case "py":
		return "python"
	case "ts":
		return "typescript"
	case "js":
		return "javascript"
	case "rs":
		return "rust"
	case "c", "h":
		return "c"
	case "cc", "cpp", "hpp":
		return "cpp"
	case "md":
		return "markdown"
	case "sh", "bash":
		return "shellscript"
	case "":
		return "plaintext"
	}
	return ext
}
