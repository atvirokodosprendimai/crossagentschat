package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// readyPayload is the body of the `ready` frame: proof to a stateless subscriber
// of which channel and identity it actually attached as. A client that picked up
// the wrong $ME sees it here rather than by wondering why nobody answers.
type readyPayload struct {
	Channel string `json:"channel"`
	Name    string `json:"name"`
}

// messagePayload is the body of a `message` frame — the stable wire contract an
// agent codes its `jq` filter against. Keep these field names stable.
type messagePayload struct {
	// From is the sender's self-declared name, copied verbatim from their query
	// string. It is a label and never provenance; nothing authenticates it.
	From string `json:"from"`
	Text string `json:"text"`
	TS   string `json:"ts"` // RFC3339, UTC
}

// writeEvent writes one SSE frame and flushes it.
//
// The payload is JSON on a single line, and that is a mechanical requirement
// rather than a style choice: an SSE `data:` field cannot carry a literal
// newline, so a raw multi-line body would have to be split across several data
// lines and rejoined by the client. JSON escaping gives that for free, and keeps
// `jq -r .text` working.
//
// Flushing is not optional. Without it the frame sits in Go's buffer until
// something else fills it, which on a quiet channel looks exactly like "no
// messages".
func writeEvent(w http.ResponseWriter, event string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s payload: %w", event, err)
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, body); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}

// writeHeartbeat writes a bare SSE comment.
//
// Clients ignore it; proxies do not. An idle stream with no bytes on it gets
// reaped somewhere in the middle, and the agent holding it just stops receiving
// with no error to notice.
func writeHeartbeat(w http.ResponseWriter) error {
	if _, err := io.WriteString(w, ":\n\n"); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}
