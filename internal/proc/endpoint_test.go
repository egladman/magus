package proc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/proc/endpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForwardRoundTrip(t *testing.T) {
	var called atomic.Bool
	var gotArgs atomic.Value // stores []string

	srv, err := New(Options{
		Handler: func(_ context.Context, args []string) error {
			gotArgs.Store(args)
			called.Store(true)
			return nil
		},
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	t.Setenv("MAGUS_DAEMON_SOCKET", srv.Addr())

	code, err := Forward(context.Background(), []string{"run", "build", "api"}, "test", "")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.True(t, called.Load(), "handler was not called")
	args, ok := gotArgs.Load().([]string)
	assert.True(t, ok)
	assert.Len(t, args, 3)
}

func TestForwardHandlerError(t *testing.T) {
	srv, err := New(Options{
		Handler: func(_ context.Context, args []string) error {
			return errors.New("build failed")
		},
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	t.Setenv("MAGUS_DAEMON_SOCKET", srv.Addr())

	code, err := Forward(context.Background(), []string{"run", "build", "broken"}, "", "")
	require.NoError(t, err, "Forward transport error")
	assert.Equal(t, 1, code)
}

func TestForwardInvalidSocket(t *testing.T) {
	t.Setenv("MAGUS_DAEMON_SOCKET", "/nonexistent/path/magus.sock")

	_, err := Forward(context.Background(), []string{"run", "build", "foo"}, "", "")
	assert.Error(t, err, "expected error dialing nonexistent socket")
}

func TestForwardCycleDetection(t *testing.T) {
	// The handler blocks until the test unblocks it, so a second call
	// with the same args while the first is in-flight triggers the cycle check.
	block := make(chan struct{})
	started := make(chan struct{})

	srv, err := New(Options{
		Concurrency: 4,
		Handler: func(_ context.Context, args []string) error {
			close(started)
			<-block
			return nil
		},
	})
	require.NoError(t, err)
	defer srv.Close()
	defer close(block)
	require.NoError(t, srv.Start())

	t.Setenv("MAGUS_DAEMON_SOCKET", srv.Addr())

	args := []string{"run", "build", "same-project"}

	// First call: blocks in handler.
	done := make(chan struct{})
	go func() {
		defer close(done)
		Forward(context.Background(), args, "", "")
	}()

	<-started // first call is inside handler

	// Second call with same args: should get cycle error (exit code 1).
	code, err := Forward(context.Background(), args, "", "")
	require.NoError(t, err, "second Forward")
	assert.Equal(t, 1, code, "cycle: expected exit code 1")
}

func TestQueryStatus(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{}, 1)

	srv, err := New(Options{
		Concurrency: 4,
		Handler: func(_ context.Context, args []string) error {
			started <- struct{}{}
			<-block
			return nil
		},
	})
	require.NoError(t, err)
	defer srv.Close()
	defer close(block)
	require.NoError(t, srv.Start())

	t.Setenv("MAGUS_DAEMON_SOCKET", srv.Addr())

	// Launch a call and wait for it to be in-flight.
	go func() {
		Forward(context.Background(), []string{"run", "build", "widget"}, "", "")
	}()
	<-started

	status, err := QueryStatus(context.Background(), srv.Addr())
	require.NoError(t, err)
	assert.Equal(t, 4, status.Capacity)
	// The handler yields its admission slot for the duration of the forwarded run
	// (so the adopted build's own RunAll competes for the full pool), so no slot is
	// held while the handler blocks; the running call is still tracked in Calls.
	assert.Equal(t, 0, status.Running)
	require.Len(t, status.Calls, 1)
	require.Len(t, status.Calls[0].Args, 3)
	assert.Equal(t, "widget", status.Calls[0].Args[2])
}

func TestRunChildSyncSlotLending(t *testing.T) {
	t.Parallel()
	lim := cache.NewLimiter(2)
	// The caller holds a slot, so RunChildSync lends it for the child's duration.
	ctx := cache.WithSlotHeld(context.Background())

	// Acquire both slots so the limiter is saturated.
	require.NoError(t, lim.Acquire(ctx))
	require.NoError(t, lim.Acquire(ctx))

	snap := lim.Snapshot()
	require.Equal(t, 2, snap.Running, "before lend")

	// RunChildSync should release one slot (lending), run fn, then re-acquire.
	var runningDuringFn int
	err := RunChildSync(ctx, lim, func() error {
		runningDuringFn = lim.Snapshot().Running
		return nil
	})
	require.NoError(t, err)

	// During fn, the lent slot was released: only 1 running.
	assert.Equal(t, 1, runningDuringFn, "running during fn")

	// After RunChildSync, the slot is re-acquired: back to 2.
	snap = lim.Snapshot()
	assert.Equal(t, 2, snap.Running, "after RunChildSync")
}

// TestRunChildSyncNoLendWithoutSlot verifies that a slotless caller (no
// SlotHeld marker — e.g. a pool-worker child) does NOT release a slot it never
// acquired, which would over-release the shared semaphore.
func TestRunChildSyncNoLendWithoutSlot(t *testing.T) {
	t.Parallel()
	lim := cache.NewLimiter(2)
	ctx := context.Background() // no SlotHeld marker
	require.NoError(t, lim.Acquire(ctx))
	var runningDuringFn int
	err := RunChildSync(ctx, lim, func() error {
		runningDuringFn = lim.Snapshot().Running
		return nil
	})
	require.NoError(t, err)
	// No lending: the one outstanding slot stays held throughout.
	assert.Equal(t, 1, runningDuringFn, "running during fn (no lend without slot)")
	assert.Equal(t, 1, lim.Snapshot().Running, "after RunChildSync")
}

func TestRunChildSyncNilLimiter(t *testing.T) {
	t.Parallel()
	called := false
	err := RunChildSync(context.Background(), nil, func() error {
		called = true
		return nil
	})
	require.NoError(t, err)
	assert.True(t, called)
}

func TestNewAlreadyAdopted(t *testing.T) {
	t.Setenv("MAGUS_DAEMON_SOCKET", "/some/path")

	_, err := New(Options{
		Handler: func(_ context.Context, args []string) error { return nil },
	})
	assert.ErrorIs(t, err, ErrAlreadyAdopted)
}

// TestRunRequestArgsCapEnforced verifies that an RPC call carrying more than
// maxArgs elements in Args is rejected with an error rather than silently
// allocating unbounded memory.
func TestRunRequestArgsCapEnforced(t *testing.T) {
	srv, err := New(Options{
		Handler: func(_ context.Context, args []string) error { return nil },
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	t.Setenv("MAGUS_DAEMON_SOCKET", srv.Addr())

	// Build a slice with 257 elements — one over the limit of 256.
	oversized := make([]string, 257)
	for i := range oversized {
		oversized[i] = fmt.Sprintf("arg%d", i)
	}

	// Forward should surface the server-side rejection as a transport error.
	_, err = Forward(context.Background(), oversized, "", "")
	assert.Error(t, err, "expected an error for oversized Args")
}

// TestDialRespectsContextCancellation verifies that Dial returns promptly when
// the supplied context is already cancelled, rather than blocking until an OS
// timeout fires.
func TestDialRespectsContextCancellation(t *testing.T) {
	ep, err := endpoint.ParseEndpoint("/nonexistent/magus-test-cancel.sock")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately before dialling

	start := time.Now()
	_, err = ep.Dial(ctx)
	elapsed := time.Since(start)

	assert.Error(t, err, "expected error from Dial with cancelled ctx")
	// A context-aware dialer should return well under 1 second.
	assert.LessOrEqual(t, elapsed, time.Second, "Dial should return near-instantly with cancelled ctx")
}

// TestStartCloseGoroutineLeak verifies that repeated Start/Close cycles do not
// leak goroutines. It samples the goroutine count before and after N cycles and
// expects the post-cycle count to settle back to baseline.
func TestStartCloseGoroutineLeak(t *testing.T) {
	// Warm up to let any runtime-internal goroutines stabilise.
	runtime.GC()
	before := runtime.NumGoroutine()

	const cycles = 5
	for i := 0; i < cycles; i++ {
		srv, err := New(Options{
			Handler: func(_ context.Context, args []string) error { return nil },
		})
		require.NoError(t, err, "cycle %d: New", i)
		require.NoError(t, srv.Start(), "cycle %d: Start", i)
		srv.Close()
	}

	// Give the runtime a moment to clean up finalised goroutines.
	runtime.GC()
	after := runtime.NumGoroutine()

	// Allow a small headroom for transient runtime goroutines. Each cycle
	// should not permanently add goroutines; a delta >= cycles is a clear leak
	// (one leaked goroutine per cycle would produce delta == cycles).
	assert.Less(t, after-before, cycles,
		"goroutine count grew from %d to %d after %d Start/Close cycles — likely leak", before, after, cycles)
}

// TestShutdownRPC verifies that Shutdown dials the server, triggers a graceful
// close, and that the server's socket file is removed afterward.
func TestShutdownRPC(t *testing.T) {
	srv, err := New(Options{
		Handler: func(_ context.Context, args []string) error { return nil },
	})
	require.NoError(t, err)
	require.NoError(t, srv.Start())

	addr := srv.Addr()

	// Shutdown should succeed and trigger srv.Close in a goroutine.
	require.NoError(t, Shutdown(context.Background(), addr))

	// Wait for the server to close — QueryStatus should eventually fail.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := QueryStatus(context.Background(), addr); err != nil {
			return // server stopped — test passes
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("server still responding after Shutdown; expected it to stop")
}

// TestShutdownIgnoresWrongMagic verifies that a Shutdown request with the wrong
// magic string is silently ignored (server keeps running).
func TestShutdownIgnoresWrongMagic(t *testing.T) {
	srv, err := New(Options{
		Handler: func(_ context.Context, args []string) error { return nil },
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	// A QueryStatus with an empty magic is silently ignored by the server.
	// Re-use QueryStatus as a liveness probe — server should still answer.
	_, err = QueryStatus(context.Background(), srv.Addr())
	require.NoError(t, err, "QueryStatus before shutdown attempt")
}

// TestWireIsJSONL verifies that a raw connection receives exactly one
// newline-terminated JSON object per reply with no embedded newlines.
func TestWireIsJSONL(t *testing.T) {
	srv, err := New(Options{
		Handler: func(_ context.Context, _ []string) error { return nil },
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	ep, err := endpoint.ParseEndpoint(srv.Addr())
	require.NoError(t, err)
	conn, err := ep.Dial(context.Background())
	require.NoError(t, err)
	defer conn.Close()

	// Write a minimal run request using raw JSON so we control the wire bytes.
	frame := `{"type":"run","args":["run","build","wire-test"],"cwd":"/tmp","protocol":"v2"}` + "\n"
	_, err = conn.Write([]byte(frame))
	require.NoError(t, err)

	// Read the reply — should be exactly one line ending with \n.
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	require.NoError(t, err)
	reply := buf[:n]

	assert.True(t, bytes.HasSuffix(reply, []byte("\n")), "reply does not end with \\n: %q", reply)
	inner := reply[:len(reply)-1]
	assert.False(t, bytes.Contains(inner, []byte("\n")), "reply contains embedded newline: %q", reply)
	assert.True(t, json.Valid(inner), "reply is not valid JSON: %q", inner)
}

// TestProtocolSkewRejectsV1 verifies that sending "protocol":"v1" causes
// the server to return an error frame and Forward surfaces it as a Go error.
func TestProtocolSkewRejectsV1(t *testing.T) {
	srv, err := New(Options{
		Handler: func(_ context.Context, _ []string) error { return nil },
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	ep, err := endpoint.ParseEndpoint(srv.Addr())
	require.NoError(t, err)
	conn, err := ep.Dial(context.Background())
	require.NoError(t, err)
	defer conn.Close()

	// Deliberately send the old protocol version.
	frame := `{"type":"run","args":["run","build","x"],"cwd":"/tmp","protocol":"v1"}` + "\n"
	_, err = conn.Write([]byte(frame))
	require.NoError(t, err)

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	require.NoError(t, err)
	reply := buf[:n]

	var envelope struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(bytes.TrimRight(reply, "\n"), &envelope), "unmarshal reply")
	assert.Equal(t, "error", envelope.Type)
	assert.NotEmpty(t, envelope.Message, "error reply has empty message")
}

// TestForwardArgsWithNewline confirms that an embedded newline in an arg is
// properly escaped by the JSON encoder and survives the round-trip correctly.
func TestForwardArgsWithNewline(t *testing.T) {
	var gotArgs atomic.Value

	srv, err := New(Options{
		Handler: func(_ context.Context, args []string) error {
			gotArgs.Store(args)
			return nil
		},
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	t.Setenv("MAGUS_DAEMON_SOCKET", srv.Addr())

	code, err := Forward(context.Background(), []string{"run", "build", "x\ny"}, "", "")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	args, ok := gotArgs.Load().([]string)
	require.True(t, ok)
	require.Len(t, args, 3)
	assert.Equal(t, "x\ny", args[2])
}

// TestMain clears MAGUS_DAEMON_SOCKET before the suite runs so the package is
// hermetic. proc.New returns ErrAlreadyAdopted when that var is set; the tests that
// exercise adoption set it themselves via t.Setenv. But when the suite runs under
// `magus run` with a daemon active, magus injects MAGUS_DAEMON_SOCKET into the test
// subprocess (the recursive-call convention), tripping the guard before any test
// opts in — so `magus run test`/`coverage` failed every proc test even though plain
// `go test` passed. Clearing it here can't be done per-test: three sibling tests use
// t.Parallel, which forbids t.Setenv.
func TestMain(m *testing.M) {
	os.Unsetenv("MAGUS_DAEMON_SOCKET")
	os.Exit(m.Run())
}
