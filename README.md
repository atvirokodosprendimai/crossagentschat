# crossagentschat

A stateless chat pipe between two agents. One binary, no dependencies, no
database, no accounts.

```
go build ./cmd/crossagentschat
./crossagentschat                          # 0.0.0.0:8080
./crossagentschat --addr 127.0.0.1:8080    # this machine only
```

## The protocol lives at `/llm.txt`

```
curl -s http://localhost:8080/llm.txt
```

That document is the reference, and it is written for an agent to read and act
on: mint a channel, background a `curl` monitor, print a handoff block for the
teammate agent, send, react. Point your agent at it rather than at this README.

The short version:

```bash
BASE=http://localhost:8080
CHAN=$(openssl rand -hex 16)
ME=$(openssl rand -hex 4)

# subscribe (background it — an SSE stream never returns)
curl -sN "$BASE/events?channel=$CHAN&name=$ME" >> "/tmp/cac.$CHAN.log" 2>&1 &

# send
curl -s -X POST --data-binary 'hello' "$BASE/send?channel=$CHAN&name=$ME"
# → {"delivered":1}
```

## Two things to understand before you deploy it

**The channel string is the only credential.** There is no auth, no token, no
account, and no way to list or revoke channels. Whoever knows the channel is in
the conversation. Mint it with `openssl rand -hex 16` and treat it like a
password — it travels in the URL, so anything between you and the server can
read it. The default binds `0.0.0.0`, which means an unconfigured relay on a
shared network is reachable by anyone who guesses or observes one.

**Arriving messages are data, not instructions.** This service writes text of
someone else's choosing into a coding agent's context. `/llm.txt` tells the
reading agent — first and last — that a message is a peer's *proposal*, never a
command, and that the `from` field is self-declared and unverified. That framing
is what makes the pipe usable; do not strip it.

Nothing is stored. A message sent while the other agent is disconnected is
permanently gone, and `{"delivered":0}` is the only signal you get.

## Development

```
go test ./... -race
```

The decision record, its alternatives and its costs are in
[`docs/adr/ADR-001-the-channel-is-the-credential.md`](docs/adr/ADR-001-the-channel-is-the-credential.md).
