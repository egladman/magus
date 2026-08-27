package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestHintWarmGraphPassesTheErrorThrough: the wrapper sits on five dispatch arms, so a
// swallowed or substituted error there would turn a failing command into a silent success.
//
// Whether the hint itself fires is NOT covered here. It depends on whether another process
// holds the daemon socket, and the two ways of getting that wrong both look like a working
// implementation locally: magus hosts its own proc server on the stable socket and exports
// MAGUS_DAEMON_SOCKET into its own environment, so a check reading either one reports the
// process as served by itself. Covering that needs a real second proc server; until then the
// guard against it is servedByAnotherDaemon's PID comparison and the note above it.
func TestHintWarmGraphPassesTheErrorThrough(t *testing.T) {
	want := errors.New("query failed")
	assert.Same(t, want, hintWarmGraph(t.Context(), want))
}
