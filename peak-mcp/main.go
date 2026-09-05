// peak-mcp connects a coding agent to a running peak.
//
// It speaks the IDE integration protocol Claude Code uses to talk to editors:
// a local WebSocket carrying MCP, advertised through a lock file in
// ~/.claude/ide. Run it alongside peak, the way peak-lsp and peak-git are run.
// It publishes the port to peak's /peak/env, so a terminal opened afterwards
// with Win connects on its own and the agent can drive the editor: read what
// is open and selected, open files, and propose edits the user accepts by
// saving them.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// connectWait bounds the startup race with peak, which may not have created
// its socket yet when both are started together.
const connectWait = 30 * time.Second

func main() {
	socket := flag.String("s", "", "peak 9P socket (default: ~/.peak/9p)")
	logPath := flag.String("log", "", "append logs to this file instead of stderr")
	flag.BoolVar(&verbose, "v", false, "log every message exchanged with the agent")
	flag.Parse()

	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			log.Fatalf("open log: %v", err)
		}
		defer f.Close()
		log.SetOutput(f)
	}
	if *socket == "" {
		home, _ := os.UserHomeDir()
		*socket = filepath.Join(home, ".peak", "9p")
	}

	peak, err := dialPeak(*socket, connectWait)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("connected to peak at %s", *socket)

	go func() {
		if err := peak.Watch(); err != nil {
			log.Printf("event stream: %v", err)
		}
	}()

	token, err := newAuthToken()
	if err != nil {
		log.Fatal(err)
	}
	sweepStaleLocks()
	lns, port, err := listenLocal()
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	srv := newServer(peak, token)
	go srv.watchSelections(context.Background())
	lock, err := writeIDELock(port, token, workspaceFolders(peak))
	if err != nil {
		log.Fatal(err)
	}
	if err := publishEnv(peak, port); err != nil {
		log.Printf("publish env: %v", err) // agents can still connect with /ide
	}
	log.Printf("serving the IDE protocol on 127.0.0.1:%d (%s)", port, lock)
	log.Printf("open a terminal in peak with Win and start your agent there")

	// Withdraw the advertisement on the way out, so a terminal opened later
	// does not try to reach a port nobody is listening on.
	cleanup := func() {
		if err := os.Remove(lock); err != nil && !os.IsNotExist(err) {
			log.Printf("remove %s: %v", lock, err)
		}
		if err := withdrawEnv(peak); err != nil {
			log.Printf("withdraw env: %v", err)
		}
	}
	defer cleanup()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	httpSrv := &http.Server{Handler: srv}
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	}()

	served := make(chan error, len(lns))
	for _, ln := range lns {
		log.Printf("listening on %s", ln.Addr())
		go func(ln net.Listener) { served <- httpSrv.Serve(ln) }(ln)
	}
	if err := <-served; err != nil && err != http.ErrServerClosed {
		log.Printf("serve: %v", err)
	}
}

// verbose turns on wire logging. The IDE protocol is undocumented, so when a
// client hangs up without saying why, the exchange itself is the evidence.
var verbose bool

func logf(format string, args ...any) {
	if verbose {
		log.Printf(format, args...)
	}
}
