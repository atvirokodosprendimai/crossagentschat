// Command crossagentschat serves the chat pipe: one hub, three routes, no state.
//
// This file is the composition root. It is the only place that selects anything
// — delete the httpapi.NewMux call and the binary still builds while serving
// nothing — so the wiring is asserted by main_test.go rather than assumed.
//
// See docs/adr/ADR-001-the-channel-is-the-credential.md for why there is no
// auth, no storage and no logging here.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/atvirokodosprendimai/crossagentschat/internal/httpapi"
	"github.com/atvirokodosprendimai/crossagentschat/internal/relay"
)

// defaultAddr binds every interface, so two agents on two machines work without
// a flag. ADR-001 accepts the exposure and answers it with the start-up banner;
// --addr 127.0.0.1:8080 is the one-flag opt out.
const defaultAddr = "0.0.0.0:8080"

// newServer builds the served *http.Server. Factored out of main so the
// configuration below can be asserted without binding a port.
//
// No request logging is wired here, and that is a decision rather than an
// omission: the channel query parameter is the only credential in this system,
// so logging r.URL would disclose it permanently to access logs, proxy logs and
// anything downstream. The deferred alternative — a logger that redacts the
// parameter at construction — is recorded in docs/adr/BACKLOG.md under
// "Redacting request logger". Read that before adding one here.
func newServer(addr string) *http.Server {
	return &http.Server{
		Addr:    addr,
		Handler: httpapi.NewMux(relay.NewHub()),
		// Bounds the slowloris window on the headers without touching the body
		// or the response, so it is safe for a stream that lives for hours.
		ReadHeaderTimeout: 10 * time.Second,
		// MUST stay 0. Any non-zero value kills every SSE stream at the deadline
		// with no error on either side, minutes into a session, and it surfaces
		// as "the agent stopped responding" rather than as a server bug.
		// internal/httpapi clears the per-stream deadline as well; both are
		// needed, since either one alone is a refactor away from being lost.
		WriteTimeout: 0,
		// An idle SSE connection is the normal case here, not an abandoned one.
		IdleTimeout: 0,
	}
}

func main() {
	addr := flag.String("addr", defaultAddr, "address to listen on (use 127.0.0.1:8080 to stay on this machine)")
	flag.Parse()

	srv := newServer(*addr)

	// Listen before announcing, so the banner can report the port the OS picked
	// when the caller asked for :0 — the e2e suite reads the address back from
	// this line.
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(1)
	}
	fmt.Println("crossagentschat listening on", ln.Addr().String())
	fmt.Println("the channel string is the only credential: anyone who knows it is in the conversation")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		// Open SSE streams are cancelled rather than drained. That is correct
		// under zero retention: there is nothing to flush, the clients see curl
		// exit, and they reconnect.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "serve:", err)
		os.Exit(1)
	}
}
