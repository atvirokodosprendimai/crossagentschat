# Task ADR-001-T5: Prove two real clients talk through the built binary

**Depends-on:** T3, T4
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** M
**Produces:** the end-to-end suite; the evidence that the ADR's served-path claim is true
**Consumes:** the built `cmd/crossagentschat` binary (T4), `/llm.txt` (T3), the routes (T2)
**Data dependency:** hermetic — the test builds and starts the binary itself; no external service, no fixture corpus

## Goal

Start the real binary on a real port, attach two HTTP clients, and prove the conversation works in
both directions with the sender excluded — the whole ADR, observed from outside.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `e2e/e2e_test.go` | add | Builds the binary with `go build -o`, starts it on `127.0.0.1:0`, drives it over HTTP. |
| `e2e/doc.go` | add | Package comment stating why this suite exists separately: every other test stubs something, and this one is the only place the composition root is exercised. |

Bound to `127.0.0.1` in the test regardless of the shipped `0.0.0.0` default — a test suite must not
open a port to the network.

## Ordered Steps

1. Write `e2e/e2e_test.go` first and confirm it is red.
2. `TestMain` builds the binary into `t.TempDir()`, starts it with `--addr 127.0.0.1:0`, reads the
   chosen port from the start-up banner (T4 prints it), and kills it on exit. Skip the whole suite
   with `t.Skip` under `-short` so the unit loop stays fast.
3. `TestTwoAgentsExchangeMessages`: subscribe `alice` and `bob` to one channel, wait for both
   `ready` frames, post as `alice`, assert `bob` receives it and `alice` does not, then post as
   `bob` and assert the mirror. **Assert the negative with a bounded wait**, not by checking an
   empty buffer immediately — a race that passes by arriving late is the failure mode here.
4. `TestDeliveredCountReflectsListeners`: `{"delivered":0}` with nobody subscribed, `{"delivered":1}`
   with one eligible peer, `{"delivered":0}` when the only subscriber is the sender.
5. `TestChannelsDoNotLeak`: a subscriber on channel A receives nothing posted to channel B. This is
   the security property observed end to end rather than at the hub — if it holds only in `relay`
   and the handler passes the wrong channel through, the credential means nothing.
6. `TestStreamSurvivesIdlePeriod`: hold a stream open past **one** heartbeat interval with no
   messages, assert a `:` comment arrived and the connection is still usable for a real message
   afterwards. This is the regression test for the `WriteTimeout` class of bug.

   **Amended during execution (2026-08-29):** originally "past two heartbeat intervals". The
   heartbeat is fixed at 20s, so two intervals is a >40s test — and the obvious fix, a `--heartbeat`
   flag, was declined in T4 because ADR-001 scoped the binary to one flag and widening an accepted
   surface for test convenience is a decision for the record, not for an executing session. One
   interval proves the same thing: the frame arrives on an idle connection, and the stream still
   works afterwards. What it gives up is evidence about the *second* interval, i.e. that the ticker
   repeats rather than fires once — so the test explicitly asserts the ticker is still armed by
   requiring the post-heartbeat message to arrive on the same connection.
7. `TestLLMTxtIsFetchableWithoutAChannel`: `GET /llm.txt` returns `200` and the trust rule, with no
   channel supplied — an agent must be able to bootstrap before it has anything.
8. `TestLLMTxtProcedureActuallyWorks`: extract the `/events` and `/send` URL shapes from the served
   document and drive them. This is the cross-shard-prose check (`cqrs` §2b step 6): the document is
   the only instruction the agent gets, it is uncontracted by any type, and it goes stale silently.
   If `/llm.txt` says `?chan=` while the handler wants `?channel=`, every other test in this repo
   still passes and the product is broken for its only user.
9. Run the fence.

## Acceptance

```bash
test -f e2e/e2e_test.go || { echo "FAIL: e2e/e2e_test.go missing"; exit 1; }
go test ./e2e/... -count=1 -timeout 120s 2>&1 | tee /tmp/acc-t5.out
grep -qE '^ok[[:space:]]+.*e2e' /tmp/acc-t5.out || { echo "FAIL: no ok line for e2e"; exit 1; }
! grep -qE 'no tests to run|^FAIL|^--- FAIL|matched no packages|\[build failed\]|no Go files' /tmp/acc-t5.out
go test ./... -count=1 -race -short 2>&1 | tee /tmp/acc-t5-reg.out
! grep -qE '^FAIL|^--- FAIL|\[build failed\]' /tmp/acc-t5-reg.out
```

Explicit `-timeout 120s`: the idle-heartbeat test deliberately waits, and the default 10m timeout
would let a hung stream sit until the agent's own tool timeout killed the run — which writes no
verification entry at all and reads as unverified rather than as slow.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestTwoAgentsExchangeMessages` | `e2e/e2e_test.go` | Both directions work; each sender is excluded from its own message, asserted with a bounded wait | — |
| `TestDeliveredCountReflectsListeners` | `e2e/e2e_test.go` | 0 / 1 / 0 for no peer, one peer, self-only | — |
| `TestChannelsDoNotLeak` | `e2e/e2e_test.go` | Channel A hears nothing from channel B, through the full stack | — |
| `TestStreamSurvivesIdlePeriod` | `e2e/e2e_test.go` | A bare `:` keep-alive arrives on a silent connection after one heartbeat interval, and a real message still gets through afterwards | — |
| `TestLLMTxtIsFetchableWithoutAChannel` | `e2e/e2e_test.go` | Bootstrap works from nothing | — |
| `TestLLMTxtProcedureActuallyWorks` | `e2e/e2e_test.go` | The URL shapes the document tells an agent to use are the shapes the server serves | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The six tests above |
| 2 — something selects it | This suite *is* the selector for the whole binary — it runs the composition root as a process. Recorded mutant: cutting sender exclusion in `internal/relay` turns `TestTwoAgentsExchangeMessages` red, proving the e2e path reaches the domain through the built binary — killed 2026-08-29 |
| 3 — the caller can discover it | `TestLLMTxtProcedureActuallyWorks` — the served document is the declared interface, and this is the only test that executes what it declares. Recorded mutant: changing `?channel=` to `?chan=` **in llm.txt** turns it red while every typed test in the repository stays green — killed 2026-08-29 |
| 4 — it is used | Nothing measures this yet |

## Mutation Log

- 2026-08-29 · dbfe86c* · mutant killed · exit 1 · `internal/relay/relay.go` · cuts sender exclusion in the domain: proves the e2e path really reaches internal/relay through the built binary · acceptance-sha256:9783e175137a095e0ac582678b62033a95874b63ff8a38b663c13c00e58d1e9d
- 2026-08-29 · dbfe86c* · mutant killed · exit 1 · `internal/httpapi/llm.txt` · the document tells the agent a query parameter the server does not read: every typed test stays green and the product is broken for its only user · acceptance-sha256:9783e175137a095e0ac582678b62033a95874b63ff8a38b663c13c00e58d1e9d

## Invariants

- The suite builds and runs the real binary; it never imports `internal/httpapi` to shortcut a listener.
- It binds `127.0.0.1` only.
- Every negative assertion is a bounded wait, never an immediate empty check.
- It leaves no process behind on failure — kill in `t.Cleanup`, not at the end of the happy path.

## Risks

- Port-from-banner parsing is brittle if T4's banner format changes. Mitigation: T4 prints the resolved address on its own line in a fixed shape, and this test fails loudly rather than hanging if it cannot parse it.
- Heartbeat timing makes the idle test the slowest here. Keep the interval configurable in tests, or the suite grows to a minute for one assertion.
- A leaked binary process on a failed build wedges the next run. `t.Cleanup` with `Process.Kill`.

## Stop Condition

Stop if `TestLLMTxtProcedureActuallyWorks` cannot be written without duplicating the document's
parameter names in the test — that duplication would make the test agree with itself rather than
with the document, which is the exact failure it exists to catch. If it comes to that, the fix is a
machine-readable line in `/llm.txt`, and that is an ADR-level change to the document's contract.

## Out of Scope

- Load, soak or concurrency-at-scale testing — two agents is the design point.
- Testing across two machines, or any network topology beyond loopback.
- Testing that a model *obeys* the trust rule; this suite proves the text is served and the URLs work.

## Verification Log
- 2026-08-29 · dbfe86c* · exit 0 · `test -f e2e/e2e_test.go || { echo "FAIL: e2e/e2e_test.go missing"; exit 1; } …` · acceptance-sha256:9783e175137a095e0ac582678b62033a95874b63ff8a38b663c13c00e58d1e9d
