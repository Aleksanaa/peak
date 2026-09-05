package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// The IDE protocol is MCP over a WebSocket rather than stdio. Claude Code
// speaks JSON-RPC 2.0 and expects the ordinary MCP handshake: initialize,
// then tools/list and tools/call.
//
// mcpProtocolVersion is only the fallback for a client that names no version;
// whatever the client asks for is echoed back.
const (
	mcpProtocolVersion = "2025-03-26"
	serverName         = "peak-mcp"
	serverVersion      = "0.1.0"
)

// JSON-RPC 2.0 error codes used here.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

func newResponse(id json.RawMessage, result any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func newError(id json.RawMessage, code int, format string, args ...any) rpcResponse {
	return rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: fmt.Sprintf(format, args...)},
	}
}

func newNotification(method string, params any) rpcNotification {
	return rpcNotification{JSONRPC: "2.0", Method: method, Params: params}
}

// tool is one entry in the IDE's tool list.
type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`

	// call runs the tool. Returning an error turns into an MCP tool error
	// rather than a transport-level JSON-RPC error, which is what clients
	// expect for a tool that ran and failed. ctx ends when the agent
	// disconnects, which is what unblocks a waiting openDiff.
	call func(ctx context.Context, args json.RawMessage) (any, error)
}

// toolContent is one block of a tools/call result. Every tool here answers
// with JSON in a text block, which is how the IDE tools report structured
// results over MCP.
type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// textResult wraps a value as the single JSON text block of a tool result.
func textResult(v any) (toolResult, error) {
	if s, ok := v.(string); ok {
		return toolResult{Content: []toolContent{{Type: "text", Text: s}}}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return toolResult{}, err
	}
	return toolResult{Content: []toolContent{{Type: "text", Text: string(b)}}}, nil
}

func errorResult(err error) toolResult {
	return toolResult{
		Content: []toolContent{{Type: "text", Text: err.Error()}},
		IsError: true,
	}
}

// dispatch handles one decoded request and returns the response to send, or
// nil for a notification that needs no reply.
func (s *server) dispatch(ctx context.Context, req rpcRequest) *rpcResponse {
	switch req.Method {
	case "initialize":
		// Echo the version the client asked for. MCP says a client that gets
		// back a version it did not request may disconnect, and this server
		// is version-agnostic: it speaks the same four methods either way.
		version := mcpProtocolVersion
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(req.Params, &params); err == nil && params.ProtocolVersion != "" {
			version = params.ProtocolVersion
		}
		logf("initialize: client speaks %s", version)
		resp := newResponse(req.ID, map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
		})
		return &resp

	case "notifications/initialized", "initialized":
		return nil

	case "tools/list":
		list := make([]tool, 0, len(s.tools))
		for _, name := range s.order {
			t := s.tools[name]
			list = append(list, t)
		}
		resp := newResponse(req.ID, map[string]any{"tools": list})
		return &resp

	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp := newError(req.ID, codeInvalidParams, "bad tools/call params: %v", err)
			return &resp
		}
		t, ok := s.tool(params.Name)
		if !ok {
			resp := newError(req.ID, codeMethodNotFound, "no such tool: %s", params.Name)
			return &resp
		}
		out, err := t.call(ctx, params.Arguments)
		if err != nil {
			resp := newResponse(req.ID, errorResult(err))
			return &resp
		}
		result, err := textResult(out)
		if err != nil {
			resp := newError(req.ID, codeInternal, "encode %s result: %v", params.Name, err)
			return &resp
		}
		resp := newResponse(req.ID, result)
		return &resp

	case "ping":
		resp := newResponse(req.ID, map[string]any{})
		return &resp

	default:
		if len(req.ID) == 0 {
			return nil // an unknown notification is not an error
		}
		resp := newError(req.ID, codeMethodNotFound, "unknown method: %s", req.Method)
		return &resp
	}
}
