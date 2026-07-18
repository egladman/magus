package main

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/proc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServerStopTerminatesLiveDaemon drives the real serverStop handler against a live
// in-process proc server, pinning the fix for the silent no-op stop: stop must resolve the
// server, send the shutdown, and verify the socket has actually gone quiet before returning
// success. A full `server start` daemon cannot be driven from a unit test (its auto-background
// path re-execs the binary), so this exercises the discovery+verify logic that stop owns.
func TestServerStopTerminatesLiveDaemon(t *testing.T) {
	// Let proc pick a random socket under SockDir; a t.TempDir() path can exceed the unix
	// socket path length limit on macOS.
	srv, err := proc.New(proc.Options{
		Handler: func(context.Context, []string) error { return nil },
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())
	addr := srv.Addr()
	require.True(t, proc.SocketLive(context.Background(), addr), "server should be live before stop")

	// The explicit --socket bypasses config/discovery so stop targets exactly this server.
	err = serverStop(context.Background(), []string{"--socket", addr})
	require.NoError(t, err, "stop against a live daemon must succeed")

	assert.False(t, proc.SocketLive(context.Background(), addr), "stop must actually terminate the daemon")
	select {
	case <-srv.Done():
	default:
		t.Fatal("stop returned without the server having been closed")
	}
}

// TestServerStopNoDaemonExitsNonzero pins the other half of the fix: stop against nothing must
// not exit 0 silently. It returns a non-zero exit (errSilent) rather than pretending success.
func TestServerStopNoDaemonExitsNonzero(t *testing.T) {
	addr := "unix://" + proc.SockDir() + "/magus-absent-test.sock"
	err := serverStop(context.Background(), []string{"--socket", addr})
	require.Error(t, err, "stop against a dead socket must report failure")

	var silent errSilent
	require.ErrorAs(t, err, &silent)
	assert.NotZero(t, silent.exitCode, "stopping nothing must exit non-zero")
}
