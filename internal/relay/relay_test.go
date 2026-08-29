package relay

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"
)

// recv reads one message with a bounded wait. Every positive assertion in this
// file goes through it so a test can never pass by reading a value that had not
// arrived yet.
func recv(t *testing.T, ch <-chan Message) Message {
	t.Helper()
	select {
	case m, ok := <-ch:
		if !ok {
			t.Fatal("channel closed, want a message")
		}
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a message")
	}
	return Message{}
}

// expectNothing asserts nothing arrives within a bounded window. The wait is the
// point: checking an empty buffer immediately would pass for a message that is
// merely late, which is the exact bug these tests exist to catch.
func expectNothing(t *testing.T, ch <-chan Message) {
	t.Helper()
	select {
	case m, ok := <-ch:
		if ok {
			t.Fatalf("received %+v, want nothing", m)
		}
	case <-time.After(150 * time.Millisecond):
	}
}

func TestHubExcludesSender(t *testing.T) {
	h := NewHub()
	alice, done := h.Subscribe("c", "alice")
	defer done()

	if n := h.Publish("c", "alice", "my own message"); n != 0 {
		t.Errorf("delivered = %d, want 0 — a sender must never receive its own message", n)
	}
	expectNothing(t, alice)
}

func TestHubDeliversToOtherSubscribers(t *testing.T) {
	h := NewHub()
	bob, done := h.Subscribe("c", "bob")
	defer done()

	h.Publish("c", "alice", "hello\nworld")

	got := recv(t, bob)
	if got.From != "alice" {
		t.Errorf("From = %q, want %q", got.From, "alice")
	}
	if got.Text != "hello\nworld" {
		t.Errorf("Text = %q, want the body verbatim including the newline", got.Text)
	}
	if got.TS.IsZero() {
		t.Error("TS is zero, want a stamped time")
	}
}

func TestHubPublishReturnsDeliveredCount(t *testing.T) {
	h := NewHub()

	if n := h.Publish("empty", "alice", "into the void"); n != 0 {
		t.Errorf("delivered = %d on a channel nobody is subscribed to, want 0", n)
	}

	bob, done := h.Subscribe("c", "bob")
	defer done()
	if n := h.Publish("c", "alice", "one peer"); n != 1 {
		t.Errorf("delivered = %d with one eligible peer, want 1", n)
	}
	recv(t, bob)

	self, doneSelf := h.Subscribe("solo", "alice")
	defer doneSelf()
	if n := h.Publish("solo", "alice", "only me here"); n != 0 {
		t.Errorf("delivered = %d when the only subscriber is the sender, want 0", n)
	}
	expectNothing(t, self)
}

func TestHubUnsubscribeStopsDelivery(t *testing.T) {
	h := NewHub()
	bob, done := h.Subscribe("c", "bob")

	done()
	if n := h.Publish("c", "alice", "after unsubscribe"); n != 0 {
		t.Errorf("delivered = %d after unsubscribe, want 0", n)
	}
	expectNothing(t, bob)

	done() // idempotent: the handler's defer must be safe after an overflow disconnect
}

func TestHubSlowSubscriberIsDisconnectedNotDropped(t *testing.T) {
	h := NewHub()
	bob, done := h.Subscribe("c", "bob")
	defer done()

	// Fill by count, never by sleeping — a timing-based fill is a flake under -race.
	// One past the buffer is what forces the overflow decision.
	for range subscriberBuffer + 1 {
		h.Publish("c", "alice", "flood")
	}

	// Bounded drain, not `for range bob`. A bare range terminates only when the
	// channel is closed, so the moment the overflow path stops closing it this
	// test stops being a test and becomes a hang — failing eventually via go
	// test's per-package timeout, ten minutes later, as a goroutine dump that
	// reads like a deadlock. Measured here: the drop-oldest mutant did exactly
	// that. The deadline turns it back into an assertion that names the cause.
	var got int
drain:
	for {
		select {
		case _, ok := <-bob:
			if !ok {
				break drain
			}
			got++
		case <-time.After(2 * time.Second):
			t.Fatalf("drain blocked after %d messages: the channel was never closed, "+
				"so a full buffer dropped silently instead of disconnecting", got)
		}
	}
	if got != subscriberBuffer {
		t.Errorf("drained %d buffered messages, want %d", got, subscriberBuffer)
	}

	t.Run("falsifiable", func(t *testing.T) {
		// The whole design turns on the overflow being OBSERVABLE. If the channel
		// were merely skipped rather than closed, this read would block until the
		// deadline instead of reporting a closed channel, and the disconnect this
		// package promises would be indistinguishable from a silent drop.
		select {
		case _, ok := <-bob:
			if ok {
				t.Fatal("channel still open, want it closed on overflow")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("read blocked, want a closed-channel signal — a full buffer must disconnect, not drop")
		}
	})
}

func TestHubChannelsAreIsolated(t *testing.T) {
	h := NewHub()
	onA, doneA := h.Subscribe("channel-a", "bob")
	defer doneA()

	if n := h.Publish("channel-b", "alice", "not for you"); n != 0 {
		t.Errorf("delivered = %d across channels, want 0", n)
	}
	// The security property: knowing a channel is the only thing that grants
	// access, so a leak between channels makes the credential meaningless.
	expectNothing(t, onA)
}

func TestRelayDoesNotImportNetHTTP(t *testing.T) {
	// os.ReadDir + ParseFile rather than the deprecated parser.ParseDir, which
	// would otherwise be the obvious call — the replacement it points at lives in
	// golang.org/x/tools, and this module ships with no dependencies at all.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
		for _, imp := range file.Imports {
			if imp.Path.Value == `"net/http"` {
				t.Errorf("%s imports net/http — the domain must not know a transport exists", name)
			}
		}
	}
	// Without this the test passes vacuously if the glob ever stops matching,
	// which is the "gate that cannot fail" shape.
	if checked == 0 {
		t.Fatal("checked 0 non-test files, want at least 1 — the scan matched nothing")
	}
}
