// Package e2e drives the built crossagentschat binary over a real socket.
//
// It exists separately because every other test in this repository stubs
// something: internal/relay tests the fan-out with no transport, and
// internal/httpapi tests the transport with a hub it constructed itself. Neither
// runs the composition root, so neither can catch a binary that starts, serves,
// and does the wrong thing — nor the class of defect that lives entirely in
// prose, like /llm.txt telling an agent to use a query parameter the server does
// not read. Types are contracted; documentation is not.
//
// The suite binds 127.0.0.1 only, regardless of the binary's 0.0.0.0 default: a
// test must not open a port to the network.
package e2e
