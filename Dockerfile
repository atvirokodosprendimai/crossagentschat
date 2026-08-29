# Build the static binary. CGO is off so the result runs on scratch: this
# service imports nothing outside the standard library, and the image should
# prove that rather than assert it.
FROM golang:1.26 AS build
WORKDIR /src
# go.mod alone — there are no dependencies to download, and copying it first
# keeps the layer cached when only source changes.
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOFLAGS=-trimpath go build -ldflags='-s -w' -o /crossagentschat ./cmd/crossagentschat

# scratch, not alpine: the binary needs no libc, no shell and no CA bundle (it
# makes no outbound connections). A shell in this image would be a way to read
# the channel out of the process environment of a compromised host and nothing
# else.
FROM scratch
COPY --from=build /crossagentschat /crossagentschat
# Unprivileged by uid — scratch has no /etc/passwd to name a user in.
USER 65534:65534
EXPOSE 8080
# Inside the container 0.0.0.0 is correct: the container's own network namespace
# is the boundary. What must not be careless is the HOST port publication —
# see compose.yaml and docs/DEPLOY.md.
ENTRYPOINT ["/crossagentschat"]
CMD ["--addr", "0.0.0.0:8080"]
