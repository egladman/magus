package proc_test

import (
	"context"
	"fmt"
	"runtime"
	"testing"

	"github.com/egladman/magus/internal/proc"
)

// newBenchServer starts an proc server with a no-op handler and registers
// b.Cleanup to close it. It sets MAGUS_DAEMON_SOCKET so Forward/QueryStatus
// can dial it.
func newBenchServer(b *testing.B) string {
	b.Helper()
	srv, err := proc.New(proc.Options{
		Concurrency: runtime.GOMAXPROCS(0),
		Handler:     func(_ context.Context, _ []string) error { return nil },
	})
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	if err := srv.Start(); err != nil {
		b.Fatalf("Start: %v", err)
	}
	b.Cleanup(srv.Close)
	b.Setenv("MAGUS_DAEMON_SOCKET", srv.Addr())
	return srv.Addr()
}

// BenchmarkForwardRoundTrip measures the full dial→request→reply→close
// latency for a single adopted Run call with a no-op handler.
func BenchmarkForwardRoundTrip(b *testing.B) {
	newBenchServer(b)
	args := []string{"run", "build", "bench-target"}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := proc.Forward(ctx, args, "", ""); err != nil {
			b.Fatalf("Forward: %v", err)
		}
	}
}

// BenchmarkForwardParallel measures throughput under concurrency.
// Each goroutine independently dials and forwards; slot contention is
// shared across GOMAXPROCS goroutines.
func BenchmarkForwardParallel(b *testing.B) {
	newBenchServer(b)
	args := []string{"run", "build", "bench-parallel"}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := proc.Forward(ctx, args, "", ""); err != nil {
				b.Errorf("Forward: %v", err)
				return
			}
		}
	})
}

// BenchmarkQueryStatusRoundTrip measures the round-trip cost for the
// Status call, which carries a small structured reply (no inflight entries).
func BenchmarkQueryStatusRoundTrip(b *testing.B) {
	addr := newBenchServer(b)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := proc.QueryStatus(ctx, addr); err != nil {
			b.Fatalf("QueryStatus: %v", err)
		}
	}
}

// BenchmarkQueryStatusInflight is like BenchmarkQueryStatusRoundTrip but
// holds 32 in-flight entries so the reply includes a populated Inflight slice.
// This catches regressions on the non-trivial fan-out marshalling path.
func BenchmarkQueryStatusInflight(b *testing.B) {
	const inflightN = 32

	block := make(chan struct{})
	ready := make(chan struct{}, inflightN)

	srv, err := proc.New(proc.Options{
		Concurrency: inflightN,
		Handler: func(_ context.Context, _ []string) error {
			ready <- struct{}{}
			<-block
			return nil
		},
	})
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	if err := srv.Start(); err != nil {
		b.Fatalf("Start: %v", err)
	}
	b.Cleanup(func() {
		close(block)
		srv.Close()
	})

	addr := srv.Addr()
	b.Setenv("MAGUS_DAEMON_SOCKET", addr)
	ctx := context.Background()

	// Fill the server's inflight registry with 32 blocked handlers.
	// Each goroutine uses unique args to avoid cycle detection, which
	// triggers when (root, cwd, args) are identical across concurrent calls.
	for i := range inflightN {
		go func(i int) {
			proc.Forward(ctx, []string{"run", "build", fmt.Sprintf("bench-inflight-%d", i)}, "", "")
		}(i)
	}
	for range inflightN {
		<-ready
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := proc.QueryStatus(ctx, addr); err != nil {
			b.Fatalf("QueryStatus: %v", err)
		}
	}
}
