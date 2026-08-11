//go:build !wasm

package std

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listenOnFreePort starts a real listener and returns its port, so these test
// against an actual TCP stack rather than a stub.
func listenOnFreePort(t *testing.T) (int, func()) {
	t.Helper()
	l, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	return l.Addr().(*net.TCPAddr).Port, func() { _ = l.Close() }
}

func TestNetFreePort(t *testing.T) {
	ctx := context.Background()
	p, err := NetFreePort(ctx)
	require.NoError(t, err)
	assert.Greater(t, p, 0)
	assert.LessOrEqual(t, p, 65535)

	// It is genuinely free: binding it right after must succeed.
	l, err := net.Listen("tcp", net.JoinHostPort("localhost", itoa(p)))
	require.NoError(t, err, "free_port returned a port that was not bindable")
	_ = l.Close()
}

func TestNetIsPortOpen(t *testing.T) {
	ctx := context.Background()

	port, closeFn := listenOnFreePort(t)
	open, err := NetIsPortOpen(ctx, "localhost", port)
	require.NoError(t, err)
	assert.True(t, open, "a live listener must read as open")

	closeFn()
	open, err = NetIsPortOpen(ctx, "localhost", port)
	require.NoError(t, err)
	assert.False(t, open, "a closed port must read as closed")
}

func TestNetPortRangeIsValidated(t *testing.T) {
	ctx := context.Background()
	for _, bad := range []int{0, -1, 65536} {
		_, err := NetIsPortOpen(ctx, "localhost", bad)
		assert.Errorf(t, err, "port %d", bad)
		_, err = NetWaitForPort(ctx, "localhost", bad, 10)
		assert.Errorf(t, err, "port %d", bad)
	}
}

func TestNetWaitForPortReturnsWhenReady(t *testing.T) {
	port, closeFn := listenOnFreePort(t)
	defer closeFn()

	ok, err := NetWaitForPort(context.Background(), "localhost", port, 5000)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestNetWaitForPortTimesOutFalse(t *testing.T) {
	// A port nothing is listening on. Timing out must be FALSE, not an error, so
	// a caller can fall back or report its own message.
	port, closeFn := listenOnFreePort(t)
	closeFn()

	start := time.Now()
	ok, err := NetWaitForPort(context.Background(), "localhost", port, 200)
	require.NoError(t, err)
	assert.False(t, ok)
	// Bounded by the timeout the caller named, not by the OS connect default.
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestNetWaitForPortBecomesReady(t *testing.T) {
	// The real shape: nothing is listening yet, something starts, the wait
	// notices. This is what replaces `until nc -z ...; do sleep; done`.
	port, closeFn := listenOnFreePort(t)
	closeFn()

	go func() {
		time.Sleep(150 * time.Millisecond)
		l, err := net.Listen("tcp", net.JoinHostPort("localhost", itoa(port)))
		if err == nil {
			t.Cleanup(func() { _ = l.Close() })
		}
	}()

	ok, err := NetWaitForPort(context.Background(), "localhost", port, 5000)
	require.NoError(t, err)
	assert.True(t, ok, "wait_for_port did not notice the listener starting")
}

func TestNetWaitForPortHonorsCancellation(t *testing.T) {
	port, closeFn := listenOnFreePort(t)
	closeFn()

	// An interrupted run must stop waiting immediately rather than sitting out
	// the remaining timeout.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	ok, err := NetWaitForPort(ctx, "localhost", port, 60_000)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Less(t, time.Since(start), 10*time.Second, "cancellation was not honored")
}

func TestNetRecordModeDoesNotTouchTheNetwork(t *testing.T) {
	rec := types.WithTrace(context.Background())

	// A record pass must not spend the timeout on a service it never started.
	ok, err := NetWaitForPort(rec, "localhost", 1, 60_000)
	require.NoError(t, err)
	assert.True(t, ok)

	p, err := NetFreePort(rec)
	require.NoError(t, err)
	assert.Greater(t, p, 0)
}

func itoa(i int) string { return strconv.Itoa(i) }
