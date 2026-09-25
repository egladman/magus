package proc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/proc/endpoint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
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

	t.Setenv("MAGUS_PROC_SOCKET", srv.Addr())

	code, err := Forward(context.Background(), []string{"run", "build", "api"}, "test", "")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.True(t, called.Load(), "handler was not called")
	args, ok := gotArgs.Load().([]string)
	assert.True(t, ok)
	assert.Len(t, args, 3)
}

// TestForwardCarriesTheLease exercises the whole seam rather than the request struct: the
// client reads the BAGGAGE lease, the server validates it, and the adopted handler sees it. A run
// launched under a lease used to lose it the moment the server adopted the run, because proc
// forwarded argv, cwd and root and no environment at all.
func TestForwardCarriesTheLease(t *testing.T) {
	got := make(chan string, 1)
	srv, err := New(Options{
		Handler: func(ctx context.Context, _ []string) error {
			got <- LeaseFromContext(ctx)
			return nil
		},
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	t.Setenv("MAGUS_PROC_SOCKET", srv.Addr())
	t.Setenv(trail.EnvBaggage, trail.BaggageLease+"=fleet/f3")

	code, err := Forward(context.Background(), []string{"run", "build"}, "test", "")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "fleet/f3", <-got)
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

	t.Setenv("MAGUS_PROC_SOCKET", srv.Addr())

	code, err := Forward(context.Background(), []string{"run", "build", "broken"}, "", "")
	require.NoError(t, err, "Forward transport error")
	assert.Equal(t, 1, code)
}

func TestForwardInvalidSocket(t *testing.T) {
	t.Setenv("MAGUS_PROC_SOCKET", "/nonexistent/path/magus.sock")

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

	t.Setenv("MAGUS_PROC_SOCKET", srv.Addr())

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

	t.Setenv("MAGUS_PROC_SOCKET", srv.Addr())

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
	t.Setenv("MAGUS_PROC_SOCKET", "/some/path")

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

	t.Setenv("MAGUS_PROC_SOCKET", srv.Addr())

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
	ep, err := endpoint.Parse("/nonexistent/magus-test-cancel.sock")
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

// rawServer starts a raw unix-socket listener that hands its one connection to serve, and
// returns its unix:// address. Uses a short-named temp dir (like New's real sockName, not
// t.TempDir()): the unix socket path max is ~104 bytes on darwin, and t.TempDir() embeds the
// full test name, which overflows that limit for these test names.
func rawServer(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "magus-w")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	addr := filepath.Join(dir, "w.sock")
	ln, err := net.Listen("unix", addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		serve(conn)
	}()
	return "unix://" + addr
}

// newWedgedServer accepts one connection, reads whatever the client sends, and never
// replies: a wedged server. The read ends once the client gives up and closes, so the test
// leaks nothing.
func newWedgedServer(t *testing.T) string {
	return rawServer(t, func(conn net.Conn) { _, _ = io.Copy(io.Discard, conn) })
}

// oldLineServer answers the way a server from before the socket carried HTTP does: it reads
// the request's first line as a JSONL frame, fails to decode it, and writes one error frame.
func oldLineServer(t *testing.T) string {
	return rawServer(t, func(conn net.Conn) {
		_, _ = bufio.NewReader(conn).ReadBytes('\n')
		_, _ = conn.Write([]byte(`{"type":"error","message":"proc: decode frame type: invalid character 'P' looking for beginning of value"}` + "\n"))
	})
}

// A client that meets a server still on the old line protocol gets MGS3025, which says to
// restart it, on every operation rather than a transport error nobody can act on.
func TestAClientMeetingAnOldServerIsToldToRestartIt(t *testing.T) {
	for name, call := range map[string]func(addr string) error{
		"status":   func(addr string) error { _, err := QueryStatus(t.Context(), addr); return err },
		"shutdown": func(addr string) error { return Shutdown(t.Context(), addr) },
		"reload":   func(addr string) error { _, _, err := ReloadConfig(t.Context(), addr); return err },
		"job": func(addr string) error {
			_, err := SubmitJob(t.Context(), addr, []string{"graph", "build"}, "")
			return err
		},
		"forward": func(addr string) error {
			t.Setenv("MAGUS_PROC_SOCKET", addr)
			_, err := Forward(t.Context(), []string{"run", "build"}, "", "")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := call(oldLineServer(t))
			var de *types.DiagnosticError
			require.ErrorAs(t, err, &de)
			assert.Equal(t, types.ServerProtocolOutdated, de.Code)
			assert.Contains(t, err.Error(), "restart it")
			assert.True(t, ServerOutdated(err))
			assert.False(t, NotAdopted(err), "an outdated server is a failure to say out loud, not a quiet local fallback")
		})
	}
}

// A client still on the old line protocol reaches a new server's HTTP parser, which refuses
// its frame as a malformed request. Nothing runs.
func TestAnOldLineClientRunsNothing(t *testing.T) {
	var called atomic.Bool
	srv, err := New(Options{
		Handler: func(context.Context, []string) error { called.Store(true); return nil },
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	ep, err := endpoint.Parse(srv.Addr())
	require.NoError(t, err)
	conn, err := ep.Dial(t.Context())
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.Write([]byte(`{"type":"run","args":["run","build"],"cwd":"/tmp","protocol":"v2"}` + "\n"))
	require.NoError(t, err)
	reply, err := io.ReadAll(conn)
	require.NoError(t, err)
	assert.True(t, bytes.HasPrefix(reply, []byte("HTTP/1.1 400")), "%q", reply)
	assert.False(t, called.Load())
}

// TestShutdownRespectsContextCancellation is the B-1 regression test: a server that
// accepts the connection and never replies must not block Shutdown past ctx.
func TestShutdownRespectsContextCancellation(t *testing.T) {
	addr := newWedgedServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := Shutdown(ctx, addr)
	elapsed := time.Since(start)

	require.Error(t, err, "Shutdown against a wedged server must return an error, not hang")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 5*time.Second,
		"Shutdown must return once ctx is done, not block on the unresponsive server")
}

// TestForwardRespectsContextCancellation is B-1's highest-value case: Forward is the
// hot path every adopted run takes, so a wedged server must not be able to hang the
// client's whole process past ctx cancellation.
func TestForwardRespectsContextCancellation(t *testing.T) {
	t.Setenv("MAGUS_PROC_SOCKET", newWedgedServer(t))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := Forward(ctx, []string{"run", "build"}, "test", "")
	elapsed := time.Since(start)

	require.Error(t, err, "Forward against a wedged server must return an error, not hang")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 5*time.Second,
		"Forward must return once ctx is done, not block on the unresponsive server")
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

// TestSubmitJobRoundTrip submits a background job over the socket: the reply names its
// invocation at once, and the handler then runs it as a job.
func TestSubmitJobRoundTrip(t *testing.T) {
	got := make(chan []string, 1)
	srv, err := New(Options{
		Handler: func(ctx context.Context, args []string) error {
			if IsJob(ctx) {
				got <- args
			}
			return nil
		},
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	inv, err := SubmitJob(t.Context(), srv.Addr(), []string{"graph", "build"}, "")
	require.NoError(t, err)
	assert.NotEmpty(t, inv)
	select {
	case args := <-got:
		assert.Equal(t, []string{"graph", "build"}, args)
	case <-time.After(5 * time.Second):
		t.Fatal("the job never ran")
	}
}

func TestReloadConfigRoundTrip(t *testing.T) {
	srv, err := New(Options{
		Handler:        func(context.Context, []string) error { return nil },
		ConfigReloader: func() (int, int) { return 3, 1 },
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	dropped, busy, err := ReloadConfig(t.Context(), srv.Addr())
	require.NoError(t, err)
	assert.Equal(t, [2]int{3, 1}, [2]int{dropped, busy})
}

// TestSocketRoutes pins the socket's route table from outside: each operation answers on its
// own method and path, a wrong method or an unknown /proc/ path is a 404 that runs nothing,
// and the paths outside /proc/ belong to whatever is mounted, 404 until something is.
func TestSocketRoutes(t *testing.T) {
	var called atomic.Bool
	srv, err := New(Options{
		Handler: func(context.Context, []string) error { called.Store(true); return nil },
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())
	ep, err := endpoint.Parse(srv.Addr())
	require.NoError(t, err)
	client := socketClient(ep)

	do := func(method, path string) int {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, socketURL(path), strings.NewReader(`{"args":["run","build"]}`))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	assert.Equal(t, http.StatusOK, do(http.MethodGet, pathStatus))
	assert.Equal(t, http.StatusOK, do(http.MethodPost, pathReload))
	assert.Equal(t, http.StatusNotFound, do(http.MethodGet, pathRun), "a run is a POST")
	assert.Equal(t, http.StatusNotFound, do(http.MethodPost, "/proc/v0/run"))
	assert.False(t, called.Load(), "no refused request reached the handler")
	assert.Equal(t, http.StatusNotFound, do(http.MethodPost, "/mcp"), "nothing is mounted yet")

	if !srv.peerChecked {
		return
	}
	unmount, err := srv.Mount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if trail.CredentialFromContext(r.Context()) != types.CredentialSocketPeer {
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, do(http.MethodPost, "/mcp"), "the mount serves the socket peer's credential")
	unmount()
	assert.Equal(t, http.StatusNotFound, do(http.MethodPost, "/mcp"), "unmounted")
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

	t.Setenv("MAGUS_PROC_SOCKET", srv.Addr())

	code, err := Forward(context.Background(), []string{"run", "build", "x\ny"}, "", "")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	args, ok := gotArgs.Load().([]string)
	require.True(t, ok)
	require.Len(t, args, 3)
	assert.Equal(t, "x\ny", args[2])
}

// TestMain isolates the package, which among other things drops MAGUS_PROC_SOCKET.
// proc.New returns ErrAlreadyAdopted when that var is set; the tests that exercise
// adoption set it themselves via t.Setenv. But under `magus run` magus injects it into
// the test subprocess (the recursive-call convention), tripping the guard before any test
// opts in, so `magus run test`/`coverage` failed every proc test even though plain `go
// test` passed. Clearing it here can't be done per-test: three sibling tests use
// t.Parallel, which forbids t.Setenv.
func TestMain(m *testing.M) { testkit.Main(m) }
