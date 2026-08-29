package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// sendResponse is the body of a successful POST /send.
type sendResponse struct {
	// Delivered is how many subscribers the message was written to. It is the
	// only feedback this service offers: because nothing is retained, 0 means
	// the message reached nobody and is gone, not that it is queued somewhere.
	Delivered int `json:"delivered"`
}

// postSend serves POST /send?channel=<c>&name=<me>, publishing the request body
// as a message from name.
func (h *handler) postSend(w http.ResponseWriter, r *http.Request) {
	channel, name, ok := credentials(r)
	if !ok {
		badCredentials(w)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMessageBytes))
	if err != nil {
		// Covers both an oversized body and a truncated one. No detail is
		// echoed back: the request line carries the credential.
		http.Error(w, "message body unreadable or larger than 64 KiB", http.StatusBadRequest)
		return
	}
	text := string(body)
	if strings.TrimSpace(text) == "" {
		http.Error(w, "message body must not be empty", http.StatusBadRequest)
		return
	}

	delivered := h.hub.Publish(channel, name, text)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sendResponse{Delivered: delivered})
}
