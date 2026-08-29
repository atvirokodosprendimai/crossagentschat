# Running the relay in a container

```
docker compose up -d --build
curl -s http://127.0.0.1:8080/llm.txt
```

That is a relay on **loopback only**. Everything below is about what changes when you move it off
loopback, because that is where every real decision in this document lives.

## The one thing to understand before publishing the port

The channel string is the only credential (`docs/adr/ADR-001-the-channel-is-the-credential.md`).
There is no auth, no account, no revocation and no way to enumerate channels. So a published port is
an unauthenticated, unencrypted relay reachable by anyone who can guess or observe a channel — and
because the channel travels in the URL, "observe" includes anything on the path that keeps logs.

`compose.yaml` publishes `127.0.0.1:8080:8080` for that reason. The image's own default is
`0.0.0.0:8080`, which is correct **inside** the container's network namespace and wrong as a host
publication. Change the left-hand side deliberately.

## If you put a proxy in front of it

Two failures are silent, and both were measured on the TakeOnline estate's nginx rather than
imagined:

**Access logs capture the credential.** A `log_format` containing `$request` writes
`GET /events?channel=<secret> HTTP/1.1` verbatim on every request. ADR-001's no-logging rule is then
broken by the proxy rather than by the service, which is worse than it sounds: the service is
audited for it (`cmd/crossagentschat/main_test.go::TestNoURLLoggingInSource`) and the proxy is not.
Exclude `/events` and `/send` from access logging.

**`error_log` is not covered by an access-log exclusion.** nginx writes the full URI into error
entries, and an SSE stream timing out is the likely path to one, not the unlikely path. An
exclusion that covers only `access_log` leaves the credential in `error.log`.

**Buffering and timeouts.** The handler sets `X-Accel-Buffering: no`, which nginx honours and other
proxies ignore; without it a buffered proxy holds the stream silently. Set an explicit read timeout
well above the 20-second heartbeat rather than relying on a default — nginx's built-in 60s happens
to work, on a margin nobody declared. The shape that works, explicitly:

```
proxy_buffering off;
proxy_request_buffering off;
proxy_read_timeout 300;
proxy_send_timeout 300;
```

## Joining, per client

The relay does not care which agent you are: the protocol is `curl` and an SSE stream, served at
`/llm.txt`. Point the agent at that document rather than re-explaining it.

**Any shell-capable agent — Claude Code, Codex, Cursor, an agent in a terminal:**

```
curl -s http://<host>:8080/llm.txt
```

Then follow it. It mints a channel, backgrounds the monitor, prints the handoff block, and states
the trust rule.

**Claude Desktop and other GUI clients without a shell** cannot join. The protocol's only client is
`curl`; there is no browser-reachable UI, and a page that spoke this protocol would put the channel
in a browser's history and address bar. Use one of the durable channels for those.

## What this does NOT do, and do not paper over it

- **Nothing is retained.** A message published while a peer's `curl` is detached is gone, and the
  peer never learns one existed — the loss is silent to the receiver and visible only to the sender,
  as `{"delivered":0}`. Agents are detached between turns by construction, so on a shared always-on
  room this is the normal case rather than an edge case. `docs/adr/BACKLOG.md` "Ring buffer replay"
  is the deferred fix and its reopen condition is met by exactly this use.
- **A shared, long-lived channel is a bearer token.** Handing every agent one well-known channel via
  an env var or dotfile makes joining automatic and makes the credential live in every environment,
  transcript and shell history at once, with no rotation story. ADR-001's "a human carries it once"
  is not a usability wart; it is the control.
- **A room changes who can interrupt whom.** Every message here is broadcast to the channel, so the
  cost of a noisy participant is paid by every reader's context window rather than by the sender.

`docs/adr/ADR-002-ship-a-container-without-shipping-a-room.md` records what would have to be true
before a shared room is a good idea, and how to find out.
