# Task ADR-001-T1: A channel hub that fans a message to every subscriber except its sender

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** M
**Produces:** `relay.Hub`, `relay.Message`, `Hub.Subscribe(channel, name) (<-chan Message, func())`, `Hub.Publish(channel, from, text string) int`
**Consumes:** none
**Data dependency:** hermetic

## Goal

Implement the in-memory fan-out for one channel: deliver each published message to every live
subscriber of that channel except the one whose `name` matches the sender, and report how many
sockets it reached.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/relay/relay.go` | add | Package doc, `Message`, `Hub`, `Subscribe`, `Publish`, subscriber bookkeeping under one mutex. |
| `internal/relay/relay_test.go` | add | The behaviours below, including the falsifiability subtest. |
| `go.mod` | edit | No new dependency — stdlib only. Touched only if `go mod tidy` reformats it. |

Nothing selects this package yet; T2 is the caller. That is why T1's Reachability rung 2 is
explicitly deferred to T2 rather than claimed here.

## Ordered Steps

1. Write `internal/relay/relay_test.go` first and confirm it is red: `TestHubExcludesSender`,
   `TestHubDeliversToOtherSubscribers`, `TestHubPublishReturnsDeliveredCount`,
   `TestHubUnsubscribeStopsDelivery`, `TestHubSlowSubscriberIsDisconnectedNotDropped`,
   `TestRelayDoesNotImportNetHTTP`. Red here means "does not compile — no such package", which is
   the correct starting state.
2. Define `Message{From, Text string; TS time.Time}` and `Hub` holding
   `map[string]map[*subscriber]struct{}` behind a `sync.Mutex`. Comment *why* the mutex rather than
   an actor goroutine: subscribe and retire must be race-free against publish, and a channel
   handshake here deadlocks (`cqrs` §9).
3. `Subscribe(channel, name)` returns a receive-only `<-chan Message` buffered at 32 and an
   idempotent `unsubscribe func()`. Retire under the same lock that publish takes.
4. `Publish(channel, from, text)` iterates the channel's subscribers under the lock, skips every
   subscriber whose `name == from`, and does a non-blocking send to the rest. Returns the count
   actually written.
5. On a full buffer, close the subscriber's channel and retire it — do **not** drop the message.
   Comment the reason inline: with no replay a dropped message is unrecoverable and invisible,
   whereas a disconnect is visible and the client reconnects. This is a deliberate departure from
   `cqrs` §9's drop-oldest rule, whose stated premise (the next event re-reads everything) does not
   hold here.
6. Add `TestRelayDoesNotImportNetHTTP`, reading `internal/relay/*.go` with `go/parser` and failing
   on a `net/http` import. This is what keeps the domain testable without a listener.
7. Run the acceptance fence and confirm green.

## Acceptance

```bash
test -f internal/relay/relay.go || { echo "FAIL: internal/relay/relay.go missing"; exit 1; }
go test ./internal/relay/... -run 'TestHub|TestRelay' -count=1 -race 2>&1 | tee /tmp/acc-t1.out
grep -qE '^ok[[:space:]]+.*internal/relay' /tmp/acc-t1.out || { echo "FAIL: no ok line for internal/relay"; exit 1; }
! grep -qE 'no tests to run|^FAIL|^--- FAIL|matched no packages|\[build failed\]|no Go files' /tmp/acc-t1.out
```

The `test -f` guard and the `ok internal/relay` requirement exist because `go test ./internal/relay/...`
against a directory that does not exist prints a warning about matching no packages and can exit 0 —
the fence would pass with nothing built. The `-race` flag is not decoration: every behaviour here is
concurrent, and a fan-out test without it proves very little.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestHubExcludesSender` | `internal/relay/relay_test.go` | A subscriber named `alice` never receives a message published with `from == "alice"` | — |
| `TestHubDeliversToOtherSubscribers` | `internal/relay/relay_test.go` | A subscriber named `bob` receives `alice`'s message with `From`/`Text` intact | — |
| `TestHubPublishReturnsDeliveredCount` | `internal/relay/relay_test.go` | Returns 0 for an empty channel, 1 with one eligible peer, and 0 when the only subscriber is the sender | — |
| `TestHubUnsubscribeStopsDelivery` | `internal/relay/relay_test.go` | After `unsubscribe()`, publish returns 0 and the channel receives nothing; calling it twice does not panic | — |
| `TestHubSlowSubscriberIsDisconnectedNotDropped` | `internal/relay/relay_test.go` | Publishing 33+ messages to a subscriber that never reads closes its channel rather than silently discarding a message; includes subtest `falsifiable` which asserts the closed-channel signal is observable | — |
| `TestHubChannelsAreIsolated` | `internal/relay/relay_test.go` | A subscriber on channel A receives nothing published to channel B | — |
| `TestRelayDoesNotImportNetHTTP` | `internal/relay/relay_test.go` | No file in the package imports `net/http` | — |

`TestHubChannelsAreIsolated` is the one that matters most for the security property: it is the
mechanical half of "the channel is the credential". If channels leak into each other, knowing a
channel stops meaning anything.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The seven tests above |
| 2 — something selects it | **Not yet — T2 is the only caller and does not exist.** Claiming a call site here would be false; T2's Reachability carries it, and T2's mutant cuts the `Publish` call |
| 3 — the caller can discover it | `go doc ./internal/relay` — exported surface is `Hub`, `Message`, `Subscribe`, `Publish` and nothing else |
| 4 — it is used | Nothing measures this yet |

## Mutation Log

- 2026-08-29 · no-git · mutant killed · exit 1 · `internal/relay/relay.go` · removes sender exclusion: an agent would receive its own posts and loop on them · acceptance-sha256:99410476f4bda9330cf2498e1f235e0921109703670c14816d3e1ede1310c29c
- 2026-08-29 · no-git · mutant killed · exit 1 · `internal/relay/relay.go` · reverts to a silent drop instead of disconnecting: the receiver never learns a message existed · acceptance-sha256:99410476f4bda9330cf2498e1f235e0921109703670c14816d3e1ede1310c29c

## Invariants

- `internal/relay` imports no `net/http` and no other project package — the domain does not know a transport exists.
- Sender exclusion is by exact `name` string equality; two clients sharing a name are one identity.
- Nothing is retained: `Publish` returns after writing to live subscribers and keeps no copy.
- Subscribe and retire happen under the same lock as publish.

## Risks

- A non-blocking send under the hub lock briefly blocks other publishes. Acceptable at two agents; if it ever matters the fix is per-subscriber goroutines, not a lock-free map.
- `-race` may expose a flake in the slow-subscriber test if it uses timing rather than a deterministic fill. Fill the buffer by count, never by `time.Sleep`.

## Stop Condition

Stop and ask if sender exclusion by `name` turns out to be wanted per-connection instead — that
would mean minting a server-side subscriber id and changing the `Produces` contract, which is a
different decision and belongs in the ADR, not in this task.

## Out of Scope

- HTTP, SSE framing, JSON — T2.
- Replay, buffering beyond the 32-slot socket buffer, `?since=` — ruled out by the ADR.
- Channel garbage collection beyond removing the map entry when its last subscriber retires.

## Verification Log
- 2026-08-29 · no-git · exit 1 · `test -f internal/relay/relay.go || { echo "FAIL: internal/relay/relay.go missing"; exit 1; } …` · acceptance-sha256:99410476f4bda9330cf2498e1f235e0921109703670c14816d3e1ede1310c29c
  ```
  FAIL: internal/relay/relay.go missing
  ```
- 2026-08-29 · no-git · exit 0 · `test -f internal/relay/relay.go || { echo "FAIL: internal/relay/relay.go missing"; exit 1; } …` · acceptance-sha256:99410476f4bda9330cf2498e1f235e0921109703670c14816d3e1ede1310c29c
