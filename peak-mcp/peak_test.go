package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aleksana/peak/internal/vfs/afero"
)

// testPeak builds a peakConn backed by an in-memory filesystem shaped like
// peak's namespace, so the parsing and tool layers can be exercised without a
// running editor.
func testPeak(t *testing.T, files map[string]string) *peakConn {
	t.Helper()
	fs := afero.NewMemMapFs()
	for name, body := range files {
		if err := afero.WriteFile(fs, name, []byte(body), 0644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	return &peakConn{fs: fs, subs: make(map[chan peakEvent]struct{})}
}

// indexLine renders one /index record in peak.s fixed-width format.
func indexLine(id, taglen, bodylen, isdir, isdirty int, tag string) string {
	return fmt.Sprintf("%11d %11d %11d %11d %11d %s\n", id, taglen, bodylen, isdir, isdirty, tag)
}

func TestTagFilename(t *testing.T) {
	cases := map[string]string{
		" /home/u/peak/main.go Del Snarf Undo Put ": "/home/u/peak/main.go",
		" /home/u/peak/ Del Snarf Get ":             "/home/u/peak/",
		" +Errors Del Snarf ":                       "+Errors",
		" /tmp/a.go Del | look for this":            "/tmp/a.go",
		"":                                          "",
	}
	for tag, want := range cases {
		if got := tagFilename(tag); got != want {
			t.Errorf("tagFilename(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestIndexParsesPeakFormat(t *testing.T) {
	p := testPeak(t, map[string]string{
		"/index": indexLine(1, 40, 900, 0, 1, " /tmp/a.go Del Snarf Put ") +
			indexLine(3, 20, 0, 1, 0, " /tmp/ Del Snarf Get ") +
			indexLine(7, 12, 30, 0, 0, " +Errors Del Snarf "),
	})

	wins, err := p.Index()
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(wins) != 3 {
		t.Fatalf("got %d windows, want 3", len(wins))
	}
	if wins[0].ID != 1 || wins[0].Name != "/tmp/a.go" || !wins[0].Dirty || wins[0].IsDir {
		t.Errorf("window 0 = %+v", wins[0])
	}
	if !wins[1].IsDir || wins[1].Name != "/tmp/" {
		t.Errorf("window 1 = %+v", wins[1])
	}
	if wins[2].Dirty || wins[2].Name != "+Errors" {
		t.Errorf("window 2 = %+v", wins[2])
	}

	if w, ok := p.FindWindow("/tmp/a.go"); !ok || w.ID != 1 {
		t.Errorf("FindWindow(/tmp/a.go) = %+v, %v", w, ok)
	}
	if _, ok := p.FindWindow("/tmp/missing.go"); ok {
		t.Error("FindWindow found a window that is not open")
	}
}

func TestPositionCountsLinesAndRunes(t *testing.T) {
	text := "package main\n\nfunc main() {}\n"
	cases := []struct {
		off              int
		wantLine, wantCh int
	}{
		{0, 0, 0},
		{7, 0, 7},
		{13, 1, 0},
		{14, 2, 0},
		{19, 2, 5},
	}
	for _, c := range cases {
		line, ch := position(text, c.off)
		if line != c.wantLine || ch != c.wantCh {
			t.Errorf("position(off=%d) = %d,%d want %d,%d", c.off, line, ch, c.wantLine, c.wantCh)
		}
	}

	// Characters are runes, not bytes: the IDE protocol counts UTF-16-ish
	// character offsets, and bytes would be wrong for any non-ASCII line.
	if _, ch := position("日本語x", len("日本語")); ch != 3 {
		t.Errorf("multibyte character offset = %d, want 3", ch)
	}

	// An offset past the end clamps rather than panicking.
	if line, _ := position(text, len(text)+50); line != 3 {
		t.Errorf("clamped line = %d, want 3", line)
	}
}

func TestSelectionReportsRangeOnlyWhenUnambiguous(t *testing.T) {
	body := "alpha\nbeta\ngamma\nbeta\n"
	p := testPeak(t, map[string]string{
		"/index":   indexLine(2, 20, len(body), 0, 0, " /tmp/a.txt Del Snarf Put "),
		"/2/body":  body,
		"/2/rdsel": "gamma",
	})
	p.focused = 2
	s := &server{peak: p}

	out, err := s.getSelection()
	if err != nil {
		t.Fatalf("getSelection: %v", err)
	}
	m := out.(map[string]any)
	if m["text"] != "gamma" || m["filePath"] != "/tmp/a.txt" {
		t.Fatalf("selection = %+v", m)
	}
	sel := m["selection"].(map[string]any)
	start := sel["start"].(map[string]int)
	if start["line"] != 2 || start["character"] != 0 {
		t.Errorf("unique selection start = %+v, want line 2 char 0", start)
	}

	// "beta" appears twice, so its offsets cannot be recovered from the text
	// alone; the range is reported empty rather than guessed at.
	p2 := testPeak(t, map[string]string{
		"/index":   indexLine(2, 20, len(body), 0, 0, " /tmp/a.txt Del "),
		"/2/body":  body,
		"/2/rdsel": "beta",
	})
	p2.focused = 2
	out2, err := (&server{peak: p2}).getSelection()
	if err != nil {
		t.Fatalf("getSelection: %v", err)
	}
	sel2 := out2.(map[string]any)["selection"].(map[string]any)
	if got := sel2["start"].(map[string]int); got["line"] != 0 || got["character"] != 0 {
		t.Errorf("ambiguous selection start = %+v, want zeroed", got)
	}
}

func TestGetSelectionWithoutFocus(t *testing.T) {
	p := testPeak(t, map[string]string{"/index": ""})
	out, err := (&server{peak: p}).getSelection()
	if err != nil {
		t.Fatalf("getSelection: %v", err)
	}
	if out.(map[string]any)["success"] != false {
		t.Errorf("no focused window should report success=false, got %+v", out)
	}
}

func TestGetOpenEditorsSkipsDirectories(t *testing.T) {
	p := testPeak(t, map[string]string{
		"/index": indexLine(1, 40, 900, 0, 1, " /tmp/a.go Del ") +
			indexLine(3, 20, 0, 1, 0, " /tmp/ Del "),
	})
	p.focused = 1
	out, err := (&server{peak: p}).getOpenEditors()
	if err != nil {
		t.Fatalf("getOpenEditors: %v", err)
	}
	tabs := out.(map[string]any)["tabs"].([]map[string]any)
	if len(tabs) != 1 {
		t.Fatalf("got %d tabs, want 1 (directory windows are not tabs)", len(tabs))
	}
	if tabs[0]["isActive"] != true || tabs[0]["isDirty"] != true {
		t.Errorf("tab = %+v", tabs[0])
	}
	if tabs[0]["languageId"] != "go" || tabs[0]["uri"] != "file:///tmp/a.go" {
		t.Errorf("tab = %+v", tabs[0])
	}
}

func TestServeHTTPRequiresAuthToken(t *testing.T) {
	s := newServer(testPeak(t, nil), "sekrit")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("missing token: status %d, want 403", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(authHeader, "wrong")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("wrong token: status %d, want 403", rec.Code)
	}
}

func TestIDELockShape(t *testing.T) {
	token, err := newAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 32 {
		t.Errorf("auth token %q is %d chars, want 32 hex", token, len(token))
	}

	b, err := json.Marshal(ideLock{
		PID: 1, WorkspaceFolders: []string{"/tmp"},
		IDEName: "Peak", Transport: "ws", AuthToken: token,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"pid", "workspaceFolders", "ideName", "transport", "authToken"} {
		if _, ok := got[key]; !ok {
			t.Errorf("lock file is missing %q, which Claude Code reads", key)
		}
	}
	if got["transport"] != "ws" {
		t.Errorf("transport = %v, want ws", got["transport"])
	}
}

func TestListenLocalBindsLoopbackOnly(t *testing.T) {
	lns, port, err := listenLocal()
	if err != nil {
		t.Fatalf("listenLocal: %v", err)
	}
	t.Cleanup(func() {
		for _, ln := range lns {
			ln.Close()
		}
	})
	if port <= 0 {
		t.Fatalf("port = %d", port)
	}

	srv := newServer(testPeak(t, nil), "tok")
	hs := &http.Server{Handler: srv}
	for _, ln := range lns {
		go hs.Serve(ln)
		if addr := ln.Addr().String(); !strings.HasPrefix(addr, "127.0.0.1:") && !strings.HasPrefix(addr, "[::1]:") {
			t.Errorf("listening on %s, want loopback only", addr)
		}
	}

	// Every bound address must actually answer, or an agent that resolves
	// localhost to the other family waits out its connect timeout.
	for _, ln := range lns {
		resp, err := http.Get("http://" + ln.Addr().String() + "/")
		if err != nil {
			t.Fatalf("get %s: %v", ln.Addr(), err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s answered %d, want 403 for a request with no token", ln.Addr(), resp.StatusCode)
		}
	}
	t.Logf("bound %d loopback address(es) on port %d", len(lns), port)
}
