# ADR-002: Ship a container, do not ship a shared room

**Status:** Proposed
**Date:** 2026-08-29
**Owner:** unassigned — proposed by the session that packaged the image
**Spec:** None
**Cross-references:** ADR-001 (the channel is the credential; Out of Scope named "containers,
deployment of any kind"), `docs/adr/BACKLOG.md` §"Ring buffer replay", §"Abuse controls"
**Governs:** `Dockerfile`, `compose.yaml`, `docs/DEPLOY.md`

**Served-path change:** `docker compose up -d` starts the relay on loopback. Nothing about the
protocol, the credential model or the retention model changes.

## Context

ADR-001 put containers and deployment out of scope, and the request that prompted this record went
further than packaging: run one central relay that every Claude, Cursor and Codex session joins.
Those are two different decisions and this record deliberately takes only the first.

Five sessions on this machine were consulted on 2026-08-29 — quality-harness, agentsmemory,
depozitas, pirkiniukampelis and infrastructure — and the evidence they returned is what shapes the
split below. It is recorded here because it is the kind of finding that is expensive to re-derive
and invisible in code.

## Decision

**1. Package the service as a container, published to loopback by default.** An image is honest
packaging: it removes "build a Go binary" as a prerequisite for a teammate on another machine
without changing what the service is. The image is `scratch` with a static binary and no shell,
runs as uid 65534, drops all capabilities and mounts its root read-only, because a shell in this
image would principally be a way to read a channel out of a compromised host's environment.

**2. Do not ship a shared, well-known channel, and do not document one.** A room every agent joins
automatically requires a channel string that lives in every agent's environment, every transcript
and every shell history simultaneously, with no rotation and no revocation. ADR-001's model —
unguessable, carried across by a human, per conversation — is not a usability wart to be optimised
away; the human-carried property IS the access control, and the first contributor who finds it
inconvenient will remove the control without knowing that is what they are removing.

**3. State the disqualifying property rather than deferring it.** The relay retains nothing, and
participants in this system are detached between turns *by construction*. So a dropped message is
the normal case, and its failure mode is silent to the receiver and visible only to the sender via
`{"delivered":0}` — a receiver cannot distinguish "nobody messaged me" from "three people did while
I was between turns". A transport whose ordinary failure is indistinguishable from silence cannot be
the substrate for coordination, and no amount of soak time changes that. What would change it is
retention, which is a different system (BACKLOG §"Ring buffer replay").

## What would have to be true before a room is a good idea

Not another ADR — an experiment, with its criterion written **before** it runs, because a criterion
written afterwards is unfalsifiable. Auth and replay are prerequisites for a safe *deployment*; they
are not prerequisites for *measuring loss*, which needs two consenting sessions and a throwaway
channel for an afternoon.

- **Loss:** over a week of ordinary use, what fraction of published messages reached zero
  subscribers? Measured from senders' `delivered` counts, not from impressions.
- **Attention:** what fraction of delivered messages was worth delivering to each recipient? A room
  broadcasts, so a noisy participant's cost is paid by every reader's context window. This is the
  property that kills shared rooms in practice, usually long before anyone loses a message.
- **Reach, honestly:** at the time of writing, nobody involved has evidence about cross-vendor
  traffic — every peer in the consultation was a Claude Code session. "Codex and Cursor can join" is
  an untested capability claim, not an observed benefit.

**Withdrawal condition:** if measured loss is non-trivial and replay has not landed, this is
withdrawn rather than softened, and the durable channel keeps the traffic.

## Consequences

Anyone can now run the relay in one command, which makes the sharp edges reachable by people who did
not read ADR-001. `docs/DEPLOY.md` therefore leads with the credential model and the two silent
proxy failures (access logs capturing `$request`, `error_log` not covered by an access-log
exclusion) rather than with the run command.

## Risks

- **The image's `0.0.0.0` default reads as an invitation.** It is correct inside a container
  namespace and wrong as a host publication; only a comment in `compose.yaml` and this record stand
  between the two. Mitigation is the loopback default, and it is one edit away from being lost.
- **A container makes the room easier to build than to justify.** This record exists to make the
  second half explicit, and it will be worth nothing if the criterion above is skipped.
