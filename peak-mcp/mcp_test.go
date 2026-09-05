package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// dialTestServer starts the IDE server on a real loopback listener and returns
// a connected client, exercising the transport, the auth header, and the
// JSON-RPC framing the way Claude Code does.
func dialTestServer(t *testing.T, p *peakConn) (*websocket.Conn, context.Context) {
	t.Helper()
	srv := newServer(p, "test-token")
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(hs.URL, "http"), &websocket.DialOptions{
		HTTPHeader: map[string][]string{authHeader: {"test-token"}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn, ctx
}

func roundTrip(t *testing.T, conn *websocket.Conn, ctx context.Context, req string) map[string]any {
	t.Helper()
	if err := conn.Write(ctx, websocket.MessageText, []byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	return resp
}

func TestHandshakeListsAndCallsTools(t *testing.T) {
	conn, ctx := dialTestServer(t, testPeak(t, map[string]string{"/index": ""}))

	resp := roundTrip(t, conn, ctx, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize returned %+v", resp)
	}
	if result["protocolVersion"] != mcpProtocolVersion {
		t.Errorf("protocolVersion = %v, want %s", result["protocolVersion"], mcpProtocolVersion)
	}
	if _, ok := result["capabilities"].(map[string]any)["tools"]; !ok {
		t.Errorf("server must advertise tools capability, got %+v", result["capabilities"])
	}

	resp = roundTrip(t, conn, ctx, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools, ok := resp["result"].(map[string]any)["tools"].([]any)
	if !ok {
		t.Fatalf("tools/list returned %+v", resp)
	}
	have := make(map[string]bool, len(tools))
	for _, entry := range tools {
		tool := entry.(map[string]any)
		name := tool["name"].(string)
		have[name] = true
		if tool["description"] == "" {
			t.Errorf("%s has no description", name)
		}
		if _, ok := tool["inputSchema"].(map[string]any); !ok {
			t.Errorf("%s has no input schema", name)
		}
	}
	// The two the model can call, and the ones the CLI drives itself.
	for _, want := range []string{
		"getDiagnostics", "openDiff", "openFile", "getCurrentSelection",
		"getLatestSelection", "getOpenEditors", "getWorkspaceFolders",
		"close_tab", "closeAllDiffTabs", "checkDocumentDirty", "saveDocument",
	} {
		if !have[want] {
			t.Errorf("tools/list is missing %q", want)
		}
	}

	// peak has no language server, so diagnostics come back empty rather than
	// leaving the agent waiting.
	resp = roundTrip(t, conn, ctx, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"getDiagnostics","arguments":{}}}`)
	content := resp["result"].(map[string]any)["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != "[]" {
		t.Errorf("getDiagnostics = %+v, want one empty JSON array", content)
	}
}

func TestUnknownMethodAndToolErrors(t *testing.T) {
	conn, ctx := dialTestServer(t, testPeak(t, map[string]string{"/index": ""}))

	resp := roundTrip(t, conn, ctx, `{"jsonrpc":"2.0","id":1,"method":"nope/nope"}`)
	rpcErr, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("unknown method returned %+v", resp)
	}
	if int(rpcErr["code"].(float64)) != codeMethodNotFound {
		t.Errorf("code = %v, want %d", rpcErr["code"], codeMethodNotFound)
	}

	// A tool that ran and failed is a tool error, not a transport error: the
	// agent needs to see the message, not a dead JSON-RPC channel.
	resp = roundTrip(t, conn, ctx, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"saveDocument","arguments":{"filePath":"/tmp/not-open.go"}}}`)
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("failed tool returned %+v", resp)
	}
	if result["isError"] != true {
		t.Errorf("failed tool should set isError, got %+v", result)
	}

	// A notification (no id) gets no reply, so the next request's answer must
	// still line up with its own id.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp = roundTrip(t, conn, ctx, `{"jsonrpc":"2.0","id":3,"method":"ping"}`)
	if resp["id"].(float64) != 3 {
		t.Errorf("reply id = %v, want 3 (a notification must not be answered)", resp["id"])
	}
}

func TestUnifiedishRendersChangedLines(t *testing.T) {
	old := "one\ntwo\nthree\n"
	new := "one\ntwo point five\nthree\n"

	out := unifiedish("/tmp/a.txt", old, new)
	if !strings.Contains(out, "--- /tmp/a.txt") || !strings.Contains(out, "+++ /tmp/a.txt (proposed)") {
		t.Errorf("missing header:\n%s", out)
	}
	if !strings.Contains(out, "-two\n") {
		t.Errorf("removed line not marked:\n%s", out)
	}
	if !strings.Contains(out, "+two point five\n") {
		t.Errorf("added line not marked:\n%s", out)
	}
	if !strings.Contains(out, " one\n") || !strings.Contains(out, " three\n") {
		t.Errorf("context lines not kept:\n%s", out)
	}
	// Past the header, every line carries a marker column so nothing reads
	// as ambiguous.
	_, diffBody, _ := strings.Cut(out, "\n\n")
	for _, line := range strings.Split(strings.TrimSuffix(diffBody, "\n"), "\n") {
		if line == "" {
			continue
		}
		if !strings.ContainsAny(line[:1], " +-") {
			t.Errorf("line %q has no diff marker", line)
		}
	}
}

func TestInitializeEchoesClientProtocolVersion(t *testing.T) {
	conn, ctx := dialTestServer(t, testPeak(t, map[string]string{"/index": ""}))

	// A client that gets back a version it did not ask for is entitled to
	// hang up, which reads as the editor disconnecting.
	resp := roundTrip(t, conn, ctx,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`)
	got := resp["result"].(map[string]any)["protocolVersion"]
	if got != "2024-11-05" {
		t.Errorf("protocolVersion = %v, want the client's own 2024-11-05", got)
	}

	// With no version named, the server states its own.
	conn2, ctx2 := dialTestServer(t, testPeak(t, map[string]string{"/index": ""}))
	resp = roundTrip(t, conn2, ctx2, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if got := resp["result"].(map[string]any)["protocolVersion"]; got != mcpProtocolVersion {
		t.Errorf("protocolVersion = %v, want %s", got, mcpProtocolVersion)
	}
}

func TestAcceptsClientSubprotocol(t *testing.T) {
	srv := newServer(testPeak(t, map[string]string{"/index": ""}), "test-token")
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	// A client asking for a subprotocol and answered with none may hang up.
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(hs.URL, "http"), &websocket.DialOptions{
		HTTPHeader:   map[string][]string{authHeader: {"test-token"}},
		Subprotocols: []string{"mcp"},
	})
	if err != nil {
		t.Fatalf("dial with subprotocol: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	if got := conn.Subprotocol(); got != "mcp" {
		t.Errorf("negotiated subprotocol = %q, want mcp", got)
	}
}
