package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

// authHeader is the header Claude Code sends with the token from the lock file.
const authHeader = "x-claude-code-ide-authorization"

// server holds the tool set and the agents connected to it. Each Win running
// an agent is its own session, so several can drive the same peak at once.
type server struct {
	peak  *peakConn
	token string
	tools map[string]tool
	order []string

	mu          sync.Mutex
	sessions    map[*session]struct{}
	diffWindows map[int]struct{}
}

// sender is the half of a connection peak-mcp writes to. The IDE protocol
// carries JSON-RPC over a WebSocket and plain MCP carries it over stdio; only
// the framing differs, so both transports share every tool and the dispatch.
type sender interface {
	write(ctx context.Context, b []byte) error
}

// session is one connected agent.
type session struct {
	server *server
	out    sender

	writeMu sync.Mutex // one writer at a time; notifications race replies
}

func newServer(p *peakConn, token string) *server {
	s := &server{
		peak:        p,
		token:       token,
		tools:       make(map[string]tool),
		sessions:    make(map[*session]struct{}),
		diffWindows: make(map[int]struct{}),
	}
	s.register(s.ideTools())
	return s
}

func (s *server) register(ts []tool) {
	for _, t := range ts {
		s.tools[t.Name] = t
		s.order = append(s.order, t.Name)
	}
}

func (s *server) tool(name string) (tool, bool) {
	t, ok := s.tools[name]
	return t, ok
}

// ---- WebSocket transport (the IDE protocol) ----

type wsSender struct{ conn *websocket.Conn }

func (w wsSender) write(ctx context.Context, b []byte) error {
	return w.conn.Write(ctx, websocket.MessageText, b)
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Logged before the auth check: a client that is turned away here still
	// reached us, which is the first thing worth knowing when it reports a
	// connection timeout.
	logf("HTTP %s %s from %s upgrade=%q subprotocols=%q auth=%v",
		r.Method, r.URL.Path, r.RemoteAddr,
		r.Header.Get("Upgrade"), r.Header.Values("Sec-WebSocket-Protocol"),
		r.Header.Get(authHeader) != "")
	if r.Header.Get(authHeader) != s.token {
		log.Printf("rejected connection from %s: auth token mismatch", r.RemoteAddr)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// The client is a local process, not a browser, so there is no
		// meaningful Origin to check. The auth token above is the gate.
		InsecureSkipVerify: true,
		// Agree to whatever the client proposes. A client that asks for a
		// subprotocol and is answered with none may hang up on the spot.
		Subprotocols: requestedSubprotocols(r),
	})
	if err != nil {
		log.Printf("accept: %v", err)
		return
	}
	defer conn.CloseNow()

	sess := &session{server: s, out: wsSender{conn}}
	s.add(sess)
	defer s.remove(sess)

	// The session outlives nothing but the connection itself. r.Context() is
	// tied to the HTTP handler, which is the wrong lifetime for a hijacked
	// socket a blocking openDiff may be waiting on.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Tell the new agent what the user is looking at, rather than making it
	// wait for the next time the selection changes.
	s.notifySelection(ctx, sess)

	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			logf("read: %v (close status %v)", err, websocket.CloseStatus(err))
			return // the agent went away; that is the normal exit
		}
		if typ != websocket.MessageText {
			continue
		}
		sess.handle(ctx, data)
	}
}

// requestedSubprotocols returns the subprotocols the client offered, in order.
func requestedSubprotocols(r *http.Request) []string {
	var out []string
	for _, v := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// listenLocal binds a port on loopback only - nothing off this machine should
// be able to drive the editor - on both address families. A client that
// resolves "localhost" to ::1 gets no answer from a v4-only listener, and
// waits instead of failing.
func listenLocal() ([]net.Listener, int, error) {
	ln4, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, 0, err
	}
	port := ln4.Addr().(*net.TCPAddr).Port

	lns := []net.Listener{ln4}
	ln6, err := net.Listen("tcp6", fmt.Sprintf("[::1]:%d", port))
	if err != nil {
		logf("no IPv6 loopback listener: %v", err) // v4 alone is fine
	} else {
		lns = append(lns, ln6)
	}
	return lns, port, nil
}

// ---- shared session handling ----

// handle processes one incoming message, replying when it is a request.
func (s *session) handle(ctx context.Context, data []byte) {
	logf("<- %s", data)

	var req rpcRequest
	if err := json.Unmarshal(data, &req); err != nil {
		_ = s.send(ctx, newError(nil, codeParse, "parse error: %v", err))
		return
	}
	if req.JSONRPC != "2.0" {
		_ = s.send(ctx, newError(req.ID, codeInvalidRequest, "not JSON-RPC 2.0"))
		return
	}

	// Tools can block for a long time (openDiff waits for the user), so each
	// request runs on its own goroutine and replies when it is done.
	go func() {
		defer func() {
			// A panic in one tool must not take down the agent's whole
			// connection, let alone the process.
			if r := recover(); r != nil {
				log.Printf("panic in %s: %v", req.Method, r)
				_ = s.send(ctx, newError(req.ID, codeInternal, "%s panicked: %v", req.Method, r))
			}
		}()
		if resp := s.server.dispatch(ctx, req); resp != nil {
			if err := s.send(ctx, *resp); err != nil {
				log.Printf("reply to %s: %v", req.Method, err)
			}
		}
	}()
}

func (s *session) send(ctx context.Context, msg any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	logf("-> %s", b)
	return s.out.write(ctx, b)
}

func (s *server) add(sess *session) {
	s.mu.Lock()
	s.sessions[sess] = struct{}{}
	s.mu.Unlock()
}

func (s *server) remove(sess *session) {
	s.mu.Lock()
	delete(s.sessions, sess)
	s.mu.Unlock()
}

// connected returns the current sessions.
func (s *server) connected() []*session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*session, 0, len(s.sessions))
	for sess := range s.sessions {
		out = append(out, sess)
	}
	return out
}

// broadcast sends a notification to every connected agent.
func (s *server) broadcast(ctx context.Context, method string, params any) {
	for _, sess := range s.connected() {
		if err := sess.send(ctx, newNotification(method, params)); err != nil {
			logf("notify %s: %v", method, err)
		}
	}
}
