// Package httpapi is the HTTP boundary of crossagentschat.
//
// It owns the wire format and nothing else: the fan-out lives in internal/relay,
// which has no idea a transport exists.
//
// One rule governs everything here and is easy to break by accident: the channel
// query parameter is the only credential in this system, so it must never be
// logged, echoed into an error body, or written anywhere it can be captured. See
// docs/adr/ADR-001-the-channel-is-the-credential.md §2.
package httpapi

import (
	"net/http"
	"time"

	"github.com/atvirokodosprendimai/crossagentschat/internal/relay"
)

// heartbeatEvery is how often an idle stream emits a comment frame. A var rather
// than a const so tests can shorten it; nothing in production changes it.
var heartbeatEvery = 20 * time.Second

// maxMessageBytes caps one posted message. Generous for chat between agents, and
// small enough that an unbounded body cannot be used to exhaust memory.
const maxMessageBytes = 64 << 10

// handler carries the hub every route needs.
type handler struct {
	hub *relay.Hub
}

// route is one served endpoint. Handlers are looked up through a function rather
// than stored directly so this table can be declared before handler exists.
type route struct {
	method  string
	path    string
	handler func(h *handler) http.HandlerFunc
}

// routes is the single source of truth for what this service serves.
//
// NewMux registers from it and /llm.txt is checked against it (T3), so a route
// cannot exist without being documented and a documented route cannot be absent.
// A second list kept beside this one is exactly the drift the check exists to
// prevent, which is why Paths derives from here rather than repeating it.
var routes = []route{
	{http.MethodGet, "/events", func(h *handler) http.HandlerFunc { return h.getEvents }},
	{http.MethodPost, "/send", func(h *handler) http.HandlerFunc { return h.postSend }},
}

// NewMux builds the router. It is the composition point that makes every route
// reachable: a route absent from the table above is served by nothing, however
// complete its handler.
func NewMux(hub *relay.Hub) *http.ServeMux {
	h := &handler{hub: hub}
	mux := http.NewServeMux()
	for _, r := range routes {
		mux.HandleFunc(r.method+" "+r.path, r.handler(h))
	}
	return mux
}

// Paths returns the path of every route NewMux registers, in registration order.
func Paths() []string {
	paths := make([]string, 0, len(routes))
	for _, r := range routes {
		paths = append(paths, r.path)
	}
	return paths
}

// credentials pulls the channel and name from the query string.
//
// It returns ok=false when either is missing, and deliberately returns no detail
// about which: the caller must not build an error message out of these values.
func credentials(r *http.Request) (channel, name string, ok bool) {
	q := r.URL.Query()
	channel, name = q.Get("channel"), q.Get("name")
	return channel, name, channel != "" && name != ""
}

// badCredentials writes the one 400 both routes share.
//
// The message is a fixed string with nothing interpolated into it. Echoing the
// submitted channel back — the obvious, helpful thing to do — would copy the
// credential into a response body, and from there into whatever logs it.
func badCredentials(w http.ResponseWriter) {
	http.Error(w, "both the channel and name query parameters are required", http.StatusBadRequest)
}
