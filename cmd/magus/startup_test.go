package main

import (
	"context"
	"testing"
)

// TestStartupNoArgsReturnsExitZero locks the shape of startup(): when args
// is empty it prints usage and returns exit code 0 without dispatching.
// This is the cheapest assertion that exercises the full pre-dispatch path
// without requiring a workspace fixture, so it doubles as a guard against
// the refactor accidentally calling os.Exit directly.
func TestStartupNoArgsReturnsExitZero(t *testing.T) {
	// Suppress any inherited daemon socket so the test doesn't forward to
	// a real magus daemon on the host.
	t.Setenv("MAGUS_DAEMON_SOCKET", "")

	res, code := startup(context.Background(), nil)
	if res.cleanup != nil {
		t.Cleanup(res.cleanup)
	}
	if code != 0 {
		t.Fatalf("startup(nil) exit code = %d, want 0", code)
	}
	if res.sub != "" {
		t.Fatalf("startup(nil) sub = %q, want empty (no dispatch)", res.sub)
	}
}
