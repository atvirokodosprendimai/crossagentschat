# ADR-001 Tasks

Implementation tasks for ADR-001: Make the channel string the credential and retain no message.
See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

Five tasks, sequential. No wave table at this size.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T2 |
| 4 | T4 | T2 |
| 5 | T5 | T3, T4 |

T3 and T4 both depend only on T2 and touch disjoint files (`internal/httpapi/llm*` versus
`cmd/**`), so they may run in parallel if anyone wants to — the sequential order above is simply the
safe reading. T5 needs both.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | A channel hub that fans a message to every subscriber except its sender | done | — | `go test ./internal/relay/... -run 'TestHub\|TestRelay' -race` |
| T2 | Serve the stream and the post over exact, tested SSE bytes | done | — | `go test ./internal/httpapi/... -run 'TestWriteEvent\|TestEvents\|TestSend' -race` |
| T3 | Serve /llm.txt as an operating procedure that draws the trust boundary | done | — | `go test ./internal/httpapi/... -run 'TestLLMTxt'` |
| T4 | Wire the binary with no write timeout and no URL logging | pending | — | `go test ./cmd/... -run 'TestServer\|TestNoURLLogging'` |
| T5 | Prove two real clients talk through the built binary | pending | — | `go test ./e2e/... -timeout 120s` |

Status: `pending` | `running` | `blocked` | `done` | `failed`.

Each Acceptance above is the abbreviated form; the task file carries the full fence, including the
existence guards that stop `go test` on a missing package from exiting 0.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `relay.Hub`, `relay.Message`, `Subscribe`, `Publish` | T2 | T1 before T2 |
| T2 | `httpapi.NewMux(*relay.Hub) *http.ServeMux` | T3, T4, T5 | T2 before all three |
| T2 | `{from,text,ts}` SSE wire contract | T5 | T2 before T5 |
| T3 | `/llm.txt` body ↔ registered routes | T5 | T3 before T5 |
| T4 | built binary on a real listener | T5 | T4 before T5 |

## Notes

- Stdlib only. No dependency is added by any task, so `go.mod` never becomes a shared write target.
- Every task's fence guards against the Go trap where `go test` on a non-existent package prints a
  warning and exits 0 — each requires a literal `ok <pkg>` line.
- T5 is the only task that runs the composition root as a process. Per `cqrs` §2b step 6, prose that
  crosses a boundary — here, `/llm.txt` telling an agent which query parameters to use — is
  uncontracted by any type and stays green through the bug. `TestLLMTxtProcedureActuallyWorks` is
  the check that catches it.
- The security property (channel isolation) is asserted twice on purpose: at the hub in T1 and
  end-to-end in T5. If it holds only in `relay` while a handler passes the wrong channel through,
  the credential means nothing.
