// Package broker is the client and server for the broker: the one process per user
// that holds this host's capacity (concurrency slots and declared memory) and the
// services every magus on it shares.
//
// The broker loads no workspace, records no telemetry and listens on a unix socket
// only; nothing in this package reaches a network listener. A run starts one when none
// answers (see cmd/magus), binding the socket is the only lock, and it exits once it
// has held nothing for its idle window.
//
// State is scoped to a connection. A client holds one connection for its life and every
// claim, place in the line for capacity, and service reference it takes rides that
// connection, so the kernel closing it
// (a clean exit, a crash, SIGKILL) releases all of it at once. When a broker dies with
// holders still running, each holder re-asserts its claims on the next broker it
// reaches, so a restart loses nothing a running step is using.
//
// Every frame type, field and error code on the wire is declared in wire.go and
// nowhere else.
package broker
