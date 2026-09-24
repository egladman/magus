// Package sockdir is where magus keeps its unix sockets: the per-process pools, the
// server and the broker. A leaf, like endpoint, so the broker client can find its
// socket without importing the proc server.
package sockdir
