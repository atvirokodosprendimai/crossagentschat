# ADR Backlog

Deferred work, each entry naming the record that deferred it. `adr-debt docs/adr` sweeps these so
they resurface at the next `/quality-harness:adr-write` instead of being quietly dropped.

## Ring buffer replay

**Deferred by:** ADR-001 (Out of Scope; recorded there as the strongest rejected alternative)
**Date:** 2026-08-29

ADR-001 retains nothing: a message exists only while it is being written to currently-connected
subscribers. An agent that is disconnected misses it permanently, and only the *sender* learns
anything about it (`{"delivered":0}`).

The alternative is a bounded in-memory ring per channel — last N messages, a `?since=` parameter,
replay-then-live on connect. It needs no disk, and it removes the design's sharpest edge. It was
rejected on 2026-08-29 by M because it is real retained state: per-channel memory that grows,
channels that cannot be garbage collected while buffered, and duplicate-delivery semantics to define
at the seam between replay and live.

**Reopen when:** a message is observed lost in practice rather than merely anticipated. Capture the
incident first — how long the gap was — so N is chosen against a real number instead of a guess.

## Abuse controls

**Deferred by:** ADR-001 (Out of Scope)
**Date:** 2026-08-29

ADR-001 ships one limit: a 64 KiB request-body cap on `POST /send`. There is no rate limit, no cap
on subscribers per channel, and no cap on channels per process. At two cooperating agents on a
trusted network none of these matter.

**Reopen when:** the relay is exposed anywhere an untrusted party can reach it. Note that ADR-001
binds `0.0.0.0` by default, so "exposed" is the out-of-the-box state, not an unusual one.

## Redacting request logger

**Deferred by:** ADR-001 (Out of Scope; Decision §2 and T4)
**Date:** 2026-08-29

ADR-001 ships with no request logging at all, because the channel — the only credential — travels in
the query string, and logging `r.URL` discloses it permanently to access logs, proxy logs and
anything downstream. `T4::TestNoURLLoggingInSource` asserts the absence.

An operator who needs request logs needs a logger that redacts the `channel` parameter before
anything is written. That is a real requirement and a small amount of work; it was left out because
the safe default had to ship first, and a logger added later cannot silently become the default.

**Reopen when:** anybody asks for operational visibility. The design constraint is fixed in advance:
redact at construction, never at the call site, so a new log line cannot forget.
