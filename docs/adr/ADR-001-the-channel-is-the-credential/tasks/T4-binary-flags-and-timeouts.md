# Task ADR-001-T4: Wire the binary with no write timeout and no URL logging

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** S (single file plus its test)
**Owner:** M
**Produces:** `cmd/crossagentschat` binary, `--addr` flag, the configured `*http.Server`
**Consumes:** `httpapi.NewMux(*relay.Hub) *http.ServeMux` (T2)
**Data dependency:** hermetic

## Goal

Compose hub + mux into a runnable binary whose server configuration cannot kill a long-lived stream
and cannot log a credential.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/crossagentschat/main.go` | add | **Composition root** — flag parsing, `relay.NewHub()`, `httpapi.NewMux(hub)`, the `*http.Server`, graceful shutdown. |
| `cmd/crossagentschat/main_test.go` | add | Server-configuration assertions below. |
| `README.md` | add | Build, run, and the two curl one-liners. Points at `/llm.txt` as the real reference. |

`main.go` is the only file that selects anything: delete the `httpapi.NewMux` call and the binary
still builds while serving nothing. That is what T4's mutant cuts.

## Ordered Steps

1. Write `cmd/crossagentschat/main_test.go` first and confirm it is red. Factor the server
   construction into `newServer(addr string) *http.Server` so it is testable without binding a port
   — a `main()` that builds its server inline cannot be asserted on.
2. `--addr` flag, default `0.0.0.0:8080` (ADR Decision — two machines work out of the box). Print
   the listen address and a one-line reminder that the channel is the only credential, so an
   operator on a shared network sees it at start-up.
3. Configure the server: `ReadHeaderTimeout: 10s`, `WriteTimeout: 0`, `IdleTimeout: 0`. Comment
   `WriteTimeout: 0` with the failure it prevents — any non-zero value silently kills every SSE
   stream at that deadline, and it surfaces as "the agent stopped responding", not as a server bug.
   T2 clears the per-stream deadline as well; both are needed, since either alone is one refactor
   away from being lost.
4. Do **not** add request logging. Add a comment at the mux wiring saying why: the query string
   carries the channel, which is the credential, so logging `r.URL` discloses it permanently to
   access logs, proxy logs and anything downstream. Name the deferred alternative
   (`docs/adr/BACKLOG.md` "Redacting request logger") so the next contributor finds the decision
   instead of re-litigating it.
5. Graceful shutdown on SIGINT/SIGTERM: `Shutdown` with a short timeout. Note in a comment that open
   SSE streams do not drain — they are cancelled, the clients see `curl` exit and reconnect, and
   that is the correct behaviour under zero retention.
6. Write `README.md`.
7. Run the fence.

## Acceptance

```bash
test -f cmd/crossagentschat/main.go || { echo "FAIL: cmd/crossagentschat/main.go missing"; exit 1; }
go build ./... 2>&1 | tee /tmp/acc-t4-build.out
! grep -qE '.' /tmp/acc-t4-build.out || { echo "FAIL: build output not empty"; exit 1; }
go test ./cmd/... -run 'TestServer|TestNoURLLogging' -count=1 2>&1 | tee /tmp/acc-t4.out
grep -qE '^ok[[:space:]]+.*cmd/crossagentschat' /tmp/acc-t4.out || { echo "FAIL: no ok line for cmd/crossagentschat"; exit 1; }
! grep -qE 'no tests to run|^FAIL|^--- FAIL|matched no packages|\[build failed\]|no Go files' /tmp/acc-t4.out
go vet ./... && go test ./internal/... -count=1 -race 2>&1 | tee /tmp/acc-t4-reg.out
! grep -qE '^FAIL|^--- FAIL|\[build failed\]' /tmp/acc-t4-reg.out
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestServerHasNoWriteTimeout` | `cmd/crossagentschat/main_test.go` | `newServer(…).WriteTimeout == 0`, with a comment naming the SSE failure it prevents | — |
| `TestServerDefaultAddrIsAllInterfaces` | `cmd/crossagentschat/main_test.go` | The default is `0.0.0.0:8080` and `--addr` overrides it | — |
| `TestServerMountsAllRoutes` | `cmd/crossagentschat/main_test.go` | `/events`, `/send` and `/llm.txt` all resolve on the server's handler; subtest `falsifiable` asserts an unregistered path 404s, so the test cannot pass by matching everything | — |
| `TestNoURLLoggingInSource` | `cmd/crossagentschat/main_test.go` | No file under `cmd/` or `internal/httpapi/` passes `r.URL`, `r.RequestURI` or `r.URL.RawQuery` to a logging call — parsed with `go/parser`, not grepped | — |

`TestNoURLLoggingInSource` is a source check rather than a behavioural one on purpose: the risk is a
*future* contributor adding a logger, and no runtime assertion catches a line that has not been
written yet. It is deliberately narrow — it cannot catch a logger that formats the URL indirectly —
and the ADR says so rather than claiming more.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests above |
| 2 — something selects it | `main()` calls `newServer`; the mutant removes the `httpapi.NewMux(hub)` argument and `TestServerMountsAllRoutes` must go red |
| 3 — the caller can discover it | `--help` lists `--addr`; `README.md` and `/llm.txt` document the run |
| 4 — it is used | Nothing measures this yet |

## Mutation Log

- 2026-08-29 · 9adbdb3* · mutant killed · exit 1 · `cmd/crossagentschat/main.go` · reintroduces a write deadline, which silently kills every long-lived SSE stream · acceptance-sha256:3d4fa31fcdd360328d4cef35761be4e9c4cc1845d4c8339065d2e343d692c725
- 2026-08-29 · 9adbdb3* · mutant killed · exit 1 · `cmd/crossagentschat/main.go` · composition root stops wiring the real mux: every package still compiles and passes its own tests while the binary serves nothing · acceptance-sha256:3d4fa31fcdd360328d4cef35761be4e9c4cc1845d4c8339065d2e343d692c725
- 2026-08-29 · 9adbdb3* · mutant killed · exit 1 · `internal/httpapi/events.go` · adds the exact violation the source guard forbids: a logging call receiving the request URL, which carries the channel credential · acceptance-sha256:3d4fa31fcdd360328d4cef35761be4e9c4cc1845d4c8339065d2e343d692c725

## Invariants

- `WriteTimeout` is 0 on the served server.
- No code path logs `r.URL`, `r.RequestURI` or the raw query.
- The binary has no dependency outside the standard library.
- `newServer` is the single place the served `*http.Server` is configured.

## Risks

- `0.0.0.0` default exposes an unconfigured relay on an untrusted network. Mitigated by the start-up banner and documented in the ADR's Consequences and Risks; `--addr 127.0.0.1:8080` is one flag.
- `IdleTimeout: 0` means an abandoned half-open connection holds a subscriber slot until TCP notices. Acceptable at two agents; the heartbeat write from T2 is what actually detects the dead peer.

## Stop Condition

Stop and ask before adding any logging, metrics or health endpoint. Each is a plausible-looking
addition that either discloses the credential or makes channels enumerable, and both are decisions
the ADR settled.

## Out of Scope

- TLS, systemd units, containers, deployment of any kind.
- Config files or environment variables — one flag is the whole surface.
- Metrics, health checks, readiness probes (see Stop Condition).

## Verification Log
- 2026-08-29 · 9adbdb3 · exit 1 · `test -f cmd/crossagentschat/main.go || { echo "FAIL: cmd/crossagentschat/main.go missing"; exit 1; } …` · acceptance-sha256:3d4fa31fcdd360328d4cef35761be4e9c4cc1845d4c8339065d2e343d692c725
  ```
  FAIL: cmd/crossagentschat/main.go missing
  ```
- 2026-08-29 · 9adbdb3* · exit 0 · `test -f cmd/crossagentschat/main.go || { echo "FAIL: cmd/crossagentschat/main.go missing"; exit 1; } …` · acceptance-sha256:3d4fa31fcdd360328d4cef35761be4e9c4cc1845d4c8339065d2e343d692c725
