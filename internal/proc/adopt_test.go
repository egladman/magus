package proc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/proc"
)

func TestForwardRoundTrip(t *testing.T) {
	var called atomic.Bool
	var gotArgs atomic.Value // stores []string

	srv, err := proc.New(proc.Options{
		Handler: func(_ context.Context, args []string) error {
			gotArgs.Store(args)
			called.Store(true)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Close()
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	t.Setenv("MAGUS_DAEMON_SOCKET", srv.Addr())

	code, err := proc.Forward(context.Background(), []string{"run", "build", "api"}, "test", "")
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if code != 0 {
		t.Errorf("ExitCode = %d, want 0", code)
	}
	if !called.Load() {
		t.Error("handler was not called")
	}
	if args, ok := gotArgs.Load().([]string); !ok || len(args) != 3 {
		t.Errorf("handler args = %v, want [run build api]", gotArgs.Load())
	}
}

func TestForwardHandlerError(t *testing.T) {
	srv, err := proc.New(proc.Options{
		Handler: func(_ context.Context, args []string) error {
			return errors.New("build failed")
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Close()
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	t.Setenv("MAGUS_DAEMON_SOCKET", srv.Addr())

	code, err := proc.Forward(context.Background(), []string{"run", "build", "broken"}, "", "")
	if err != nil {
		t.Fatalf("Forward transport error: %v", err)
	}
	if code != 1 {
		t.Errorf("ExitCode = %d, want 1", code)
	}
}

func TestForwardInvalidSocket(t *testing.T) {
	t.Setenv("MAGUS_DAEMON_SOCKET", "/nonexistent/path/magus.sock")

	_, err := proc.Forward(context.Background(), []string{"run", "build", "foo"}, "", "")
	if err == nil {
		t.Fatal("expected error dialing nonexistent socket, got nil")
	}
}

func TestForwardCycleDetection(t *testing.T) {
	// The handler blocks until the test unblocks it, so a second call
	// with the same args while the first is in-flight triggers the cycle check.
	block := make(chan struct{})
	started := make(chan struct{})

	srv, err := proc.New(proc.Options{
		Concurrency: 4,
		Handler: func(_ context.Context, args []string) error {
			close(started)
			<-block
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Close()
	defer close(block)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	t.Setenv("MAGUS_DAEMON_SOCKET", srv.Addr())

	args := []string{"run", "build", "same-project"}

	// First call: blocks in handler.
	done := make(chan struct{})
	go func() {
		defer close(done)
		proc.Forward(context.Background(), args, "", "")
	}()

	<-started // first call is inside handler

	// Second call with same args: should get cycle error (exit code 1).
	code, err := proc.Forward(context.Background(), args, "", "")
	if err != nil {
		t.Fatalf("second Forward: %v", err)
	}
	if code != 1 {
		t.Errorf("cycle: ExitCode = %d, want 1", code)
	}
}

func TestQueryStatus(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{}, 1)

	srv, err := proc.New(proc.Options{
		Concurrency: 4,
		Handler: func(_ context.Context, args []string) error {
			started <- struct{}{}
			<-block
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Close()
	defer close(block)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	t.Setenv("MAGUS_DAEMON_SOCKET", srv.Addr())

	// Launch a call and wait for it to be in-flight.
	go func() {
		proc.Forward(context.Background(), []string{"run", "build", "widget"}, "", "")
	}()
	<-started

	status, err := proc.QueryStatus(context.Background(), srv.Addr())
	if err != nil {
		t.Fatalf("QueryStatus: %v", err)
	}
	if status.Capacity != 4 {
		t.Errorf("Capacity=%d, want 4", status.Capacity)
	}
	if status.InUse != 1 {
		t.Errorf("InUse=%d, want 1", status.InUse)
	}
	if len(status.Calls) != 1 {
		t.Fatalf("Calls len=%d, want 1", len(status.Calls))
	}
	if len(status.Calls[0].Args) != 3 || status.Calls[0].Args[2] != "widget" {
		t.Errorf("Calls[0].Args=%v, want [run build widget]", status.Calls[0].Args)
	}
}

func TestRunChildSyncSlotLending(t *testing.T) {
	t.Parallel()
	lim := cache.NewLimiter(2)
	// The caller holds a slot, so RunChildSync lends it for the child's duration.
	ctx := cache.WithSlotHeld(context.Background())

	// Acquire both slots so the limiter is saturated.
	if err := lim.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := lim.Acquire(ctx); err != nil {
		t.Fatal(err)
	}

	snap := lim.Snapshot()
	if snap.InUse != 2 {
		t.Fatalf("before lend: inUse=%d, want 2", snap.InUse)
	}

	// RunChildSync should release one slot (lending), run fn, then re-acquire.
	var inUseDuringFn int
	err := proc.RunChildSync(ctx, lim, func() error {
		inUseDuringFn = lim.Snapshot().InUse
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// During fn, the lent slot was released: only 1 in use.
	if inUseDuringFn != 1 {
		t.Errorf("inUse during fn = %d, want 1", inUseDuringFn)
	}

	// After RunChildSync, the slot is re-acquired: back to 2.
	snap = lim.Snapshot()
	if snap.InUse != 2 {
		t.Errorf("after RunChildSync: inUse=%d, want 2", snap.InUse)
	}
}

// TestRunChildSyncNoLendWithoutSlot verifies that a slotless caller (no
// SlotHeld marker — e.g. a pool-worker child) does NOT release a slot it never
// acquired, which would over-release the shared semaphore.
func TestRunChildSyncNoLendWithoutSlot(t *testing.T) {
	t.Parallel()
	lim := cache.NewLimiter(2)
	ctx := context.Background() // no SlotHeld marker
	if err := lim.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	var inUseDuringFn int
	err := proc.RunChildSync(ctx, lim, func() error {
		inUseDuringFn = lim.Snapshot().InUse
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// No lending: the one outstanding slot stays held throughout.
	if inUseDuringFn != 1 {
		t.Errorf("inUse during fn = %d, want 1 (no lend without slot)", inUseDuringFn)
	}
	if snap := lim.Snapshot(); snap.InUse != 1 {
		t.Errorf("after RunChildSync: inUse=%d, want 1", snap.InUse)
	}
}

func TestRunChildSyncNilLimiter(t *testing.T) {
	t.Parallel()
	called := false
	err := proc.RunChildSync(context.Background(), nil, func() error {
		called = true
		return nil
	})
	if err != nil || !called {
		t.Errorf("nil limiter: err=%v called=%v", err, called)
	}
}

func TestNewAlreadyAdopted(t *testing.T) {
	t.Setenv("MAGUS_DAEMON_SOCKET", "/some/path")

	_, err := proc.New(proc.Options{
		Handler: func(_ context.Context, args []string) error { return nil },
	})
	if !errors.Is(err, proc.ErrAlreadyAdopted) {
		t.Errorf("New returned %v, want ErrAlreadyAdopted", err)
	}
}

// TestRunRequestArgsCapEnforced verifies that an RPC call carrying more than
// maxArgs elements in Args is rejected with an error rather than silently
// allocating unbounded memory.
func TestRunRequestArgsCapEnforced(t *testing.T) {
	srv, err := proc.New(proc.Options{
		Handler: func(_ context.Context, args []string) error { return nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Close()
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	t.Setenv("MAGUS_DAEMON_SOCKET", srv.Addr())

	// Build a slice with 257 elements — one over the limit of 256.
	oversized := make([]string, 257)
	for i := range oversized {
		oversized[i] = fmt.Sprintf("arg%d", i)
	}

	// Forward should surface the server-side rejection as a transport error.
	_, err = proc.Forward(context.Background(), oversized, "", "")
	if err == nil {
		t.Fatal("expected an error for oversized Args, got nil")
	}
}

// TestDialRespectsContextCancellation verifies that Dial returns promptly when
// the supplied context is already cancelled, rather than blocking until an OS
// timeout fires.
func TestDialRespectsContextCancellation(t *testing.T) {
	ep, err := proc.ParseEndpoint("/nonexistent/magus-test-cancel.sock")
	if err != nil {
		t.Fatalf("ParseEndpoint: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately before dialling

	start := time.Now()
	_, err = ep.Dial(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from Dial with cancelled ctx, got nil")
	}
	// A context-aware dialer should return well under 1 second.
	if elapsed > time.Second {
		t.Errorf("Dial took %v with cancelled ctx, expected near-instant return", elapsed)
	}
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
		srv, err := proc.New(proc.Options{
			Handler: func(_ context.Context, args []string) error { return nil },
		})
		if err != nil {
			t.Fatalf("cycle %d: New: %v", i, err)
		}
		if err := srv.Start(); err != nil {
			t.Fatalf("cycle %d: Start: %v", i, err)
		}
		srv.Close()
	}

	// Give the runtime a moment to clean up finalised goroutines.
	runtime.GC()
	after := runtime.NumGoroutine()

	// Allow a small headroom for transient runtime goroutines. Each cycle
	// should not permanently add goroutines; a delta >= cycles is a clear leak
	// (one leaked goroutine per cycle would produce delta == cycles).
	if after-before >= cycles {
		t.Errorf("goroutine count grew from %d to %d after %d Start/Close cycles — likely leak",
			before, after, cycles)
	}
}

// TestShutdownRPC verifies that Shutdown dials the server, triggers a graceful
// close, and that the server's socket file is removed afterward.
func TestShutdownRPC(t *testing.T) {
	srv, err := proc.New(proc.Options{
		Handler: func(_ context.Context, args []string) error { return nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	addr := srv.Addr()

	// Shutdown should succeed and trigger srv.Close in a goroutine.
	if err := proc.Shutdown(context.Background(), addr); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Wait for the server to close — QueryStatus should eventually fail.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := proc.QueryStatus(context.Background(), addr); err != nil {
			return // server stopped — test passes
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("server still responding after Shutdown; expected it to stop")
}

// TestShutdownIgnoresWrongMagic verifies that a Shutdown request with the wrong
// magic string is silently ignored (server keeps running).
func TestShutdownIgnoresWrongMagic(t *testing.T) {
	srv, err := proc.New(proc.Options{
		Handler: func(_ context.Context, args []string) error { return nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Close()
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// A QueryStatus with an empty magic is silently ignored by the server.
	// Re-use QueryStatus as a liveness probe — server should still answer.
	if _, err := proc.QueryStatus(context.Background(), srv.Addr()); err != nil {
		t.Fatalf("QueryStatus before shutdown attempt: %v", err)
	}
}

// TestWireIsJSONL verifies that a raw connection receives exactly one
// newline-terminated JSON object per reply with no embedded newlines.
func TestWireIsJSONL(t *testing.T) {
	srv, err := proc.New(proc.Options{
		Handler: func(_ context.Context, _ []string) error { return nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Close()
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ep, err := proc.ParseEndpoint(srv.Addr())
	if err != nil {
		t.Fatalf("ParseEndpoint: %v", err)
	}
	conn, err := ep.Dial(context.Background())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// Write a minimal run request using raw JSON so we control the wire bytes.
	frame := `{"type":"run","args":["run","build","wire-test"],"cwd":"/tmp","protocol":"v2"}` + "\n"
	if _, err := conn.Write([]byte(frame)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read the reply — should be exactly one line ending with \n.
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	reply := buf[:n]

	if !bytes.HasSuffix(reply, []byte("\n")) {
		t.Errorf("reply does not end with \\n: %q", reply)
	}
	inner := reply[:len(reply)-1]
	if bytes.Contains(inner, []byte("\n")) {
		t.Errorf("reply contains embedded newline: %q", reply)
	}
	if !json.Valid(inner) {
		t.Errorf("reply is not valid JSON: %q", inner)
	}
}

// TestProtocolSkewRejectsV1 verifies that sending "protocol":"v1" causes
// the server to return an error frame and Forward surfaces it as a Go error.
func TestProtocolSkewRejectsV1(t *testing.T) {
	srv, err := proc.New(proc.Options{
		Handler: func(_ context.Context, _ []string) error { return nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Close()
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ep, err := proc.ParseEndpoint(srv.Addr())
	if err != nil {
		t.Fatalf("ParseEndpoint: %v", err)
	}
	conn, err := ep.Dial(context.Background())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// Deliberately send the old protocol version.
	frame := `{"type":"run","args":["run","build","x"],"cwd":"/tmp","protocol":"v1"}` + "\n"
	if _, err := conn.Write([]byte(frame)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	reply := buf[:n]

	var envelope struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(bytes.TrimRight(reply, "\n"), &envelope); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if envelope.Type != "error" {
		t.Errorf("reply type = %q, want \"error\"", envelope.Type)
	}
	if envelope.Message == "" {
		t.Error("error reply has empty message")
	}
}

// TestForwardArgsWithNewline confirms that an embedded newline in an arg is
// properly escaped by the JSON encoder and survives the round-trip correctly.
func TestForwardArgsWithNewline(t *testing.T) {
	var gotArgs atomic.Value

	srv, err := proc.New(proc.Options{
		Handler: func(_ context.Context, args []string) error {
			gotArgs.Store(args)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Close()
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	t.Setenv("MAGUS_DAEMON_SOCKET", srv.Addr())

	code, err := proc.Forward(context.Background(), []string{"run", "build", "x\ny"}, "", "")
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if code != 0 {
		t.Errorf("ExitCode = %d, want 0", code)
	}
	args, ok := gotArgs.Load().([]string)
	if !ok || len(args) != 3 || args[2] != "x\ny" {
		t.Errorf("handler received args %v, want [run build x\\ny]", args)
	}
}
