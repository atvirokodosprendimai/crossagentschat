package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteEventExactBytes(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := writeEvent(rec, "message", messagePayload{From: "alice", Text: "hello", TS: "2026-08-29T09:14:02Z"}); err != nil {
		t.Fatalf("writeEvent: %v", err)
	}

	want := "event: message\n" +
		`data: {"from":"alice","text":"hello","ts":"2026-08-29T09:14:02Z"}` + "\n\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("frame bytes\n got %q\nwant %q", got, want)
	}

	t.Run("multiline text stays on one data line", func(t *testing.T) {
		// The SSE spec gives `data:` no way to carry a literal newline, so a raw
		// multi-line body would have to be split across several data lines and
		// rejoined by the client. JSON escaping is what makes one line correct.
		rec := httptest.NewRecorder()
		if err := writeEvent(rec, "message", messagePayload{From: "a", Text: "line one\nline two", TS: "t"}); err != nil {
			t.Fatalf("writeEvent: %v", err)
		}
		body := rec.Body.String()
		if n := strings.Count(body, "data:"); n != 1 {
			t.Errorf("data lines = %d, want exactly 1 for a multi-line message", n)
		}
		if !strings.Contains(body, `line one\nline two`) {
			t.Errorf("newline not escaped in %q", body)
		}
	})

	t.Run("falsifiable", func(t *testing.T) {
		// A frame without its terminating blank line is never dispatched by an
		// SSE client — it sits in the parser waiting for more. If this detector
		// cannot see a missing terminator, the exact-bytes assertion above is
		// decoration.
		if strings.HasSuffix("event: x\ndata: {}\n", "\n\n") {
			t.Fatal("terminator detector cannot distinguish one newline from two")
		}
	})
}

func TestWriteHeartbeatIsAnSSEComment(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := writeHeartbeat(rec); err != nil {
		t.Fatalf("writeHeartbeat: %v", err)
	}
	if got := rec.Body.String(); got != ":\n\n" {
		t.Errorf("heartbeat = %q, want %q — a bare comment a client ignores", got, ":\n\n")
	}
}
