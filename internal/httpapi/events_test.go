package httpapi

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/crossagentschat/internal/relay"
)

// startServer runs the real mux on a loopback listener and returns it with the
// hub behind it. httptest.NewServer rather than a recorder throughout: a
// recorder does not flush like a connection, so it cannot observe the streaming
// behaviour these tests are about.
func startServer(t *testing.T) (*httptest.Server, *relay.Hub) {
	t.Helper()
	hub := relay.NewHub()
	srv := httptest.NewServer(NewMux(hub))
	t.Cleanup(srv.Close)
	return srv, hub
}

// openStream opens an SSE connection and reads its ready frame before returning.
//
// Consuming ready here is what makes every later publish deterministic: the
// handler subscribes and only then writes ready, so a client holding that frame
// has proof the subscription is live. Without that ordering a test would have to
// poll the hub, and the hub retains nothing — a message published a moment too
// early is legitimately gone, which would show up as a flake.
func openStream(t *testing.T, srv *httptest.Server, channel, name string) (*bufio.Reader, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		srv.URL+"/events?channel="+channel+"&name="+name, nil)
	if err != nil {
		cancel()
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("open stream: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		cancel()
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	r := bufio.NewReader(resp.Body)
	if frame := readFrame(t, r); !strings.Contains(frame, "event: ready") {
		cancel()
		t.Fatalf("first frame = %q, want event: ready", frame)
	}
	return r, cancel
}

// readFrame reads lines until a frame terminates. The read is bounded by the
// test binary's own timeout rather than a deadline here; a stream that never
// delivers is the failure, and it reports as a stuck read on this line.
func readFrame(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	var b strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read frame: %v (partial %q)", err, b.String())
		}
		if line == "\n" {
			return b.String()
		}
		b.WriteString(line)
	}
}

func TestEventsSendsReadyFrameFirst(t *testing.T) {
	srv, _ := startServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		srv.URL+"/events?channel=chan-abc&name=alice", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()

	frame := readFrame(t, bufio.NewReader(resp.Body))
	if !strings.Contains(frame, "event: ready") {
		t.Errorf("first frame = %q, want event: ready", frame)
	}
	if !strings.Contains(frame, `"channel":"chan-abc"`) || !strings.Contains(frame, `"name":"alice"`) {
		t.Errorf("ready frame %q must echo channel and name so a subscriber can confirm what it attached to", frame)
	}
}

func TestEventsRequiresChannelAndName(t *testing.T) {
	srv, _ := startServer(t)
	for _, q := range []string{"", "?channel=sekrit", "?name=sekrit", "?channel=&name=sekrit", "?channel=sekrit&name="} {
		resp, err := http.Get(srv.URL + "/events" + q)
		if err != nil {
			t.Fatalf("get %q: %v", q, err)
		}
		buf := make([]byte, 512)
		n, _ := resp.Body.Read(buf)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("status for %q = %d, want 400", q, resp.StatusCode)
		}
		// The channel is the credential. An error body that repeats it back is
		// one more place it can be captured.
		if strings.Contains(string(buf[:n]), "sekrit") {
			t.Errorf("error body for %q echoed the query value back: %q", q, buf[:n])
		}
	}
}

func TestEventsDeliversPublishedMessage(t *testing.T) {
	srv, hub := startServer(t)
	r, cancel := openStream(t, srv, "c", "bob")
	defer cancel()

	hub.Publish("c", "alice", "hello\nworld")

	frame := readFrame(t, r)
	if !strings.Contains(frame, "event: message") {
		t.Errorf("frame = %q, want event: message", frame)
	}
	if !strings.Contains(frame, `"from":"alice"`) {
		t.Errorf("frame = %q, want from alice", frame)
	}
	if !strings.Contains(frame, `hello\nworld`) {
		t.Errorf("frame = %q, want the newline escaped into one data line", frame)
	}
	if strings.Count(frame, "data:") != 1 {
		t.Errorf("frame = %q, want exactly one data line", frame)
	}
}

func TestEventsClearsWriteDeadline(t *testing.T) {
	hub := relay.NewHub()
	srv := httptest.NewUnstartedServer(NewMux(hub))
	// A server-wide write deadline is the trap this test exists for: it kills a
	// long-lived stream mid-session with no error visible on either side, and it
	// surfaces as "the agent stopped responding", not as a server bug.
	srv.Config.WriteTimeout = 250 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)

	r, cancel := openStream(t, srv, "c", "bob")
	defer cancel()

	time.Sleep(600 * time.Millisecond) // well past the server's deadline

	hub.Publish("c", "alice", "still alive")

	frame := readFrame(t, r)
	if !strings.Contains(frame, "still alive") {
		t.Errorf("frame after the write deadline = %q; the stream should have survived it", frame)
	}
}

func TestEventsClosesOnContextCancel(t *testing.T) {
	srv, hub := startServer(t)
	_, cancel := openStream(t, srv, "c", "bob")

	cancel()

	// Once the handler returns it must have retired its subscription, so a
	// publish reaches nobody. Bounded wait: cancellation is asynchronous.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hub.Publish("c", "alice", "after cancel") == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("subscription still live 2s after the request was cancelled — the handler leaked it")
}
