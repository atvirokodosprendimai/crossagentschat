package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// postSend posts a body and returns the status and decoded delivered count.
func postSend(t *testing.T, url, body string) (int, int) {
	t.Helper()
	resp, err := http.Post(url, "text/plain", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Delivered int `json:"delivered"`
	}
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}
	return resp.StatusCode, out.Delivered
}

func TestSendReturnsDeliveredCount(t *testing.T) {
	srv, _ := startServer(t)

	// Nobody listening. Under zero retention this is the only signal the sender
	// gets that the message reached no one and is gone.
	status, delivered := postSend(t, srv.URL+"/send?channel=c&name=alice", "into the void")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if delivered != 0 {
		t.Errorf("delivered = %d with nobody subscribed, want 0", delivered)
	}

	r, cancel := openStream(t, srv, "c", "bob")
	defer cancel()

	_, delivered = postSend(t, srv.URL+"/send?channel=c&name=alice", "one peer")
	if delivered != 1 {
		t.Errorf("delivered = %d with one eligible peer, want 1", delivered)
	}
	if frame := readFrame(t, r); !strings.Contains(frame, "one peer") {
		t.Errorf("subscriber got %q, want the posted text", frame)
	}

	// The sender is excluded from its own channel even over HTTP.
	_, delivered = postSend(t, srv.URL+"/send?channel=c&name=bob", "bob talking to himself")
	if delivered != 0 {
		t.Errorf("delivered = %d when the only subscriber is the sender, want 0", delivered)
	}
}

func TestSendRequiresChannelAndName(t *testing.T) {
	srv, _ := startServer(t)
	for _, q := range []string{"", "?channel=sekrit", "?name=sekrit"} {
		status, _ := postSend(t, srv.URL+"/send"+q, "hi")
		if status != http.StatusBadRequest {
			t.Errorf("status for %q = %d, want 400", q, status)
		}
	}
}

func TestSendRejectsEmptyBody(t *testing.T) {
	srv, hub := startServer(t)
	r, cancel := openStream(t, srv, "c", "bob")
	defer cancel()

	for _, body := range []string{"", "   ", "\n\t "} {
		if status, _ := postSend(t, srv.URL+"/send?channel=c&name=alice", body); status != http.StatusBadRequest {
			t.Errorf("status for %q = %d, want 400", body, status)
		}
	}
	// Nothing was published, so the stream stays silent and the next real
	// message is the one the subscriber sees.
	hub.Publish("c", "alice", "the only message")
	if frame := readFrame(t, r); !strings.Contains(frame, "the only message") {
		t.Errorf("subscriber got %q first, want no frame from the rejected posts", frame)
	}
}

func TestSendCapsBodySize(t *testing.T) {
	srv, _ := startServer(t)
	huge := strings.Repeat("x", maxMessageBytes+1)
	if status, _ := postSend(t, srv.URL+"/send?channel=c&name=alice", huge); status != http.StatusBadRequest {
		t.Errorf("status for an oversized body = %d, want 400 — it must be refused, not buffered", status)
	}
}
