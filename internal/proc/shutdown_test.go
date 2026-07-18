package proc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShutdownClosesServer pins the fix for the silent `server stop` no-op: a shutdown RPC
// must actually tear the server down, not just acknowledge. It asserts the observable
// signals a blocking daemon loop and `server stop`'s verification rely on - Done() closing
// and the socket going dead - because the shutdown handler cancels only the listener's own
// context, and a caller cannot see that from the reply alone.
func TestShutdownClosesServer(t *testing.T) {
	srv, err := New(Options{Handler: func(context.Context, []string) error { return nil }})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	addr := srv.Addr()
	require.True(t, SocketLive(context.Background(), addr), "server should be live before shutdown")

	// Done must not have fired yet; the daemon loop would still be blocked here.
	select {
	case <-srv.Done():
		t.Fatal("Done fired before shutdown was requested")
	default:
	}

	require.NoError(t, Shutdown(context.Background(), addr))

	// The blocking daemon loop wakes on Done(); it must close after the RPC shutdown.
	select {
	case <-srv.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done did not close after shutdown; the daemon process would keep running")
	}

	// And the socket must actually stop answering, which is what `server stop` polls to
	// verify the daemon is gone instead of trusting the shutdown reply.
	require.Eventually(t, func() bool {
		return !SocketLive(context.Background(), addr)
	}, 2*time.Second, 20*time.Millisecond, "socket kept answering after shutdown")
}

// TestSocketLiveFalseForBogusAddr guards the negative path the stop/start probes depend on:
// an address with no daemon (or a malformed one) reports not-live rather than erroring, so a
// stop against nothing exits cleanly non-zero and a start does not think a daemon exists.
func TestSocketLiveFalseForBogusAddr(t *testing.T) {
	assert.False(t, SocketLive(context.Background(), "unix:///nonexistent/magus-daemon.sock"))
	assert.False(t, SocketLive(context.Background(), "not-a-valid-endpoint::::"))
}
