# Task ADR-001-T2: Serve the stream and the post over exact, tested SSE bytes

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** M
**Produces:** `httpapi.NewMux(*relay.Hub) *http.ServeMux`, the `GET /events` and `POST /send` routes, the `{from,text,ts}` wire contract
**Consumes:** `relay.Hub`, `Hub.Subscribe`, `Hub.Publish` (T1)
**Data dependency:** hermetic

## Goal

Expose the hub over HTTP as `GET /events` (SSE) and `POST /send`, with the frame writer proved
byte-for-byte.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/httpapi/httpapi.go` | add | `NewMux` — **the registry that makes every route reachable.** Deleting a `mux.HandleFunc` line here unmounts a route with everything else still compiling. |
| `internal/httpapi/events.go` | add | SSE handler: subscribe, `ready` frame, event loop, 20s heartbeat, deadline clearing, flush. |
| `internal/httpapi/send.go` | add | POST handler: read body, publish, reply `{"delivered":N}`. |
| `internal/httpapi/sse.go` | add | `writeEvent(w, name string, payload any) error` — the one place SSE bytes are produced. |
| `internal/httpapi/sse_test.go` | add | Exact-bytes test for the frame writer. |
| `internal/httpapi/events_test.go` | add | Stream behaviour over `httptest`. |
| `internal/httpapi/send_test.go` | add | Post behaviour and the delivered count. |

## Ordered Steps

1. Write the failing tests first — `TestWriteEventExactBytes` above all, since it pins the wire
   format that everything else assumes. Confirm red (package does not exist).
2. `sse.go`: `writeEvent` marshals `payload` to JSON and writes `event: <name>\ndata: <json>\n\n`,
   then flushes via `http.NewResponseController(w).Flush()`. JSON on one line is what makes a
   multi-line message legal in a single `data:` field — assert in the test that a payload containing
   `\n` produces exactly one `data:` line.
3. `events.go`: validate `channel` and `name` are non-empty (`400` otherwise, with a message that
   does **not** echo the channel back). Set `Content-Type: text/event-stream`,
   `Cache-Control: no-cache`, `Connection: keep-alive`.
4. Clear the write deadline: `http.NewResponseController(w).SetWriteDeadline(time.Time{})`, with a
   comment naming the failure it prevents — a server write timeout silently kills every long-lived
   stream minutes in, and it looks like an agent hang, not a server bug.
5. Subscribe via T1, `defer unsubscribe()`, emit `event: ready` with `{channel,name}` immediately so
   a stateless subscriber can confirm what it attached to.
6. Loop on `select` over `r.Context().Done()`, the subscription channel (returning when it is
   closed — that is T1's slow-subscriber disconnect), and a 20s `time.Ticker` writing a bare `:\n\n`
   comment so proxies do not reap an idle stream.
7. `send.go`: read the body under `http.MaxBytesReader` (64 KiB), reject empty, call `Hub.Publish`,
   reply `200` with `{"delivered":N}`.
8. `httpapi.go`: `NewMux` registers both routes. Run the fence.

## Acceptance

```bash
test -f internal/httpapi/httpapi.go || { echo "FAIL: internal/httpapi/httpapi.go missing"; exit 1; }
go test ./internal/httpapi/... -run 'TestWriteEvent|TestEvents|TestSend' -count=1 -race 2>&1 | tee /tmp/acc-t2.out
grep -qE '^ok[[:space:]]+.*internal/httpapi' /tmp/acc-t2.out || { echo "FAIL: no ok line for internal/httpapi"; exit 1; }
! grep -qE 'no tests to run|^FAIL|^--- FAIL|matched no packages|\[build failed\]|no Go files' /tmp/acc-t2.out
go test ./internal/relay/... -count=1 -race 2>&1 | tee /tmp/acc-t2-reg.out
! grep -qE '^FAIL|^--- FAIL|\[build failed\]' /tmp/acc-t2-reg.out
```

Two chained commands, deliberately: the new package alone first, so T1's already-green suite cannot
carry the verdict, then T1 as regression. Neither can stand in for the other.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestWriteEventExactBytes` | `internal/httpapi/sse_test.go` | Output is exactly `event: message\ndata: {…}\n\n`; a payload containing a newline yields exactly one `data:` line; subtest `falsifiable` asserts a missing trailing blank line is detected | — |
| `TestEventsSendsReadyFrameFirst` | `internal/httpapi/events_test.go` | First frame is `event: ready` carrying the requested channel and name | — |
| `TestEventsRequiresChannelAndName` | `internal/httpapi/events_test.go` | Missing/empty either → `400`, and the response body does not contain the channel string | — |
| `TestEventsDeliversPublishedMessage` | `internal/httpapi/events_test.go` | A message published while the stream is open arrives as `event: message` with `from`/`text` | — |
| `TestEventsClearsWriteDeadline` | `internal/httpapi/events_test.go` | The handler survives past a deliberately short `http.Server.WriteTimeout` set on the test server | — |
| `TestEventsClosesOnContextCancel` | `internal/httpapi/events_test.go` | Cancelling the request returns the handler and retires the subscription | — |
| `TestSendReturnsDeliveredCount` | `internal/httpapi/send_test.go` | `{"delivered":0}` with nobody listening; `{"delivered":1}` with one eligible peer | — |
| `TestSendRejectsEmptyBody` | `internal/httpapi/send_test.go` | Empty body → `400`, nothing published | — |
| `TestSendCapsBodySize` | `internal/httpapi/send_test.go` | A body over 64 KiB is rejected rather than buffered | — |

`TestEventsClearsWriteDeadline` is the one worth writing carefully. It must run against a real
`httptest.Server` whose `WriteTimeout` is short, because that is the only configuration in which the
bug appears — asserting on the handler in isolation proves nothing.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The nine tests above |
| 2 — something selects it | `NewMux` registers both routes; the mutant deletes the `mux.HandleFunc("/send", …)` line and `TestSendReturnsDeliveredCount` must go red |
| 3 — the caller can discover it | T3's `/llm.txt` is the declared interface, and T3's drift test binds it to this mux |
| 4 — it is used | Nothing measures this yet |

## Mutation Log

## Invariants

- SSE bytes are produced in exactly one function (`writeEvent`); no handler writes frames by hand.
- No handler logs, echoes or reflects the `channel` value — including in error bodies.
- `http.Server` write deadlines are cleared per stream; the stream ends only on client disconnect or a closed subscription.
- Every frame is flushed before the handler blocks again.

## Risks

- `http.NewResponseController` requires the `ResponseWriter` to support it; `httptest.NewRecorder` does not flush like a real connection. Stream tests must use `httptest.NewServer`, not the recorder.
- A heartbeat ticker left running after the handler returns leaks a goroutine — `defer ticker.Stop()`.

## Stop Condition

Stop if `writeEvent` needs to grow a second shape (multiple `data:` lines, `id:` fields, retry
hints). That is a wire-contract change and belongs in the ADR before it is written.

## Out of Scope

- `/llm.txt` — T3.
- Flags, listener, shutdown — T4.
- End-to-end two-client behaviour over a real socket — T5.

## Verification Log
