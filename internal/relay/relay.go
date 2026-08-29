// Package relay is the domain of crossagentschat: channels, subscribers, and the
// fan-out between them.
//
// It deliberately knows nothing about HTTP — no import of net/http, enforced by
// TestRelayDoesNotImportNetHTTP — so the delivery semantics can be tested without
// binding a listener. The transport lives in internal/httpapi.
//
// Nothing here is retained. A message is written to the buffers of the
// subscribers live at the moment it is published, and then forgotten: there is no
// history, no replay and no persistence anywhere in this package. See
// docs/adr/ADR-001-the-channel-is-the-credential.md.
package relay

import (
	"sync"
	"time"
)

// subscriberBuffer is how many messages may queue for one subscriber before the
// hub gives up on it. Exported to the tests so the overflow case is driven by
// this constant rather than by a number copied beside it.
const subscriberBuffer = 32

// Message is one chat message as it crosses the hub.
type Message struct {
	// From is the sender's self-declared name. It is a label, never an identity:
	// the sender chooses it, so nothing here authenticates it.
	From string
	// Text is the message body, verbatim — newlines and all.
	Text string
	// TS is when the hub accepted the message, in UTC.
	TS time.Time
}

// subscriber is one live listener on one channel.
type subscriber struct {
	name string
	ch   chan Message
}

// Hub fans messages out to the subscribers of a channel. The zero value is not
// usable; call NewHub.
type Hub struct {
	// One mutex rather than an owner goroutine per channel. Subscribe and retire
	// have to be race-free against publish, and doing that bookkeeping over a
	// channel handshake deadlocks: the subscriber holds the hub while the hub
	// wants the subscriber (cqrs §9, "bookkeeping locks deadlock against actor
	// loops"). A plain mutex around a map has neither problem at this scale.
	mu       sync.Mutex
	channels map[string]map[*subscriber]struct{}
}

// NewHub returns an empty Hub.
func NewHub() *Hub {
	return &Hub{channels: make(map[string]map[*subscriber]struct{})}
}

// Subscribe registers a listener on channel under name and returns its delivery
// channel plus an idempotent unsubscribe.
//
// The channel string is the only credential in this system: a channel exists
// precisely while somebody is subscribed to it, and is created here by its first
// subscriber. Subscribing to a channel nobody else knows is indistinguishable
// from subscribing to one that never existed, which is what makes channels
// non-enumerable.
//
// The returned channel is closed either by unsubscribe or by the hub giving up on
// a subscriber that stopped reading. Callers must treat a closed channel as "you
// are no longer subscribed" and reconnect if they still want messages.
func (h *Hub) Subscribe(channel, name string) (<-chan Message, func()) {
	sub := &subscriber{name: name, ch: make(chan Message, subscriberBuffer)}

	h.mu.Lock()
	if h.channels[channel] == nil {
		h.channels[channel] = make(map[*subscriber]struct{})
	}
	h.channels[channel][sub] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	return sub.ch, func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.retire(channel, sub)
		})
	}
}

// Publish delivers text to every subscriber of channel except those named from,
// and reports how many it reached.
//
// The count is the only feedback this system offers a sender. Because nothing is
// retained, a return of 0 means the message reached nobody and is gone — there is
// no queue it is waiting in.
func (h *Hub) Publish(channel, from, text string) int {
	msg := Message{From: from, Text: text, TS: time.Now().UTC()}

	h.mu.Lock()
	defer h.mu.Unlock()

	delivered := 0
	var overflowed []*subscriber
	for sub := range h.channels[channel] {
		// Sender exclusion. Not cosmetic: an agent that received its own posts
		// would act on them and drive itself in a loop.
		if sub.name == from {
			continue
		}
		select {
		case sub.ch <- msg:
			delivered++
		default:
			overflowed = append(overflowed, sub)
		}
	}

	// A full buffer disconnects the subscriber rather than dropping the message.
	// cqrs §9 prescribes drop-oldest, on the stated grounds that "dropping a
	// superseded snapshot is free — the next event re-reads everything anyway".
	// That premise does not hold here: this hub carries the data itself and keeps
	// no history, so a dropped message is unrecoverable AND invisible — the
	// receiver never learns one existed. A disconnect is loud, the client's curl
	// exits, and it reconnects. Deliberate departure, recorded in ADR-001 §4.
	for _, sub := range overflowed {
		h.retire(channel, sub)
	}
	return delivered
}

// retire removes sub from channel and closes its delivery channel. The caller
// must hold h.mu.
//
// Idempotent by checking membership first, which is what makes unsubscribe safe
// to call after the hub has already disconnected an overflowing subscriber —
// otherwise the handler's deferred unsubscribe would close an closed channel and
// panic.
func (h *Hub) retire(channel string, sub *subscriber) {
	subs, ok := h.channels[channel]
	if !ok {
		return
	}
	if _, ok := subs[sub]; !ok {
		return
	}
	delete(subs, sub)
	close(sub.ch)
	// Drop the channel entirely once its last subscriber leaves, so an idle
	// process holds no memory for conversations that ended.
	if len(subs) == 0 {
		delete(h.channels, channel)
	}
}
