package httpapi

import (
	"net/http"
	"time"
)

// getEvents serves GET /events?channel=<c>&name=<me> as an SSE stream.
//
// The stream lives until the client goes away or the hub disconnects it, and it
// carries three things: one `ready` frame, a `message` frame per delivered
// message, and a comment heartbeat while idle. Messages published while nobody
// is attached are gone — this service retains nothing.
func (h *handler) getEvents(w http.ResponseWriter, r *http.Request) {
	channel, name, ok := credentials(r)
	if !ok {
		badCredentials(w)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Tells nginx not to buffer the response. Without it a proxied stream is
	// silent until the proxy's buffer fills, which reads as "nothing is being
	// sent" rather than as a proxy setting.
	w.Header().Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	// Clear the write deadline for this stream. An http.Server.WriteTimeout is
	// good practice for ordinary handlers and fatal here: it kills the
	// connection at the deadline with no error on either side, minutes into a
	// session, and it surfaces as "the agent stopped responding" rather than as
	// a server bug. cmd/crossagentschat also sets WriteTimeout to 0; both are
	// deliberate, because either one alone is a refactor away from being lost.
	_ = rc.SetWriteDeadline(time.Time{})

	// Subscribe BEFORE announcing ready, so a client holding the ready frame has
	// proof its subscription is live. The tests depend on that ordering, and so
	// does any agent that posts immediately after connecting.
	messages, unsubscribe := h.hub.Subscribe(channel, name)
	defer unsubscribe()

	if err := writeEvent(w, "ready", readyPayload{Channel: channel, Name: name}); err != nil {
		return
	}

	heartbeat := time.NewTicker(heartbeatEvery)
	defer heartbeat.Stop() // otherwise every closed stream leaks its ticker

	for {
		select {
		case <-r.Context().Done():
			return
		case m, open := <-messages:
			if !open {
				// The hub gave up on us: our buffer filled because this
				// connection stopped draining. Ending the response is the whole
				// point — the client sees curl exit and reconnects, rather than
				// silently missing messages it will never learn about.
				return
			}
			ev := messagePayload{From: m.From, Text: m.Text, TS: m.TS.Format(time.RFC3339)}
			if err := writeEvent(w, "message", ev); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := writeHeartbeat(w); err != nil {
				return
			}
		}
	}
}
