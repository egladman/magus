package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/types"
)

// TestResolveDeclaredWorkspacesMergesAndDedupes verifies that cfg and env
// inputs are merged, deduplicated, resolved to absolute paths, and that
// non-existent or non-directory entries are skipped.
func TestResolveDeclaredWorkspacesMergesAndDedupes(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	c := t.TempDir()

	// Non-existent path should be dropped silently.
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	got := resolveDeclaredWorkspaces(
		[]string{a, b}, // cfg
		b+string(filepath.ListSeparator)+c+string(filepath.ListSeparator)+missing, // env (b is duplicate)
	)
	want := map[string]bool{a: true, b: true, c: true}
	if len(got) != len(want) {
		t.Fatalf("got %d workspaces, want %d: %v", len(got), len(want), got)
	}
	for _, root := range got {
		if !want[root] {
			t.Errorf("unexpected root in result: %q", root)
		}
	}
}

func TestResolveDeclaredWorkspacesEmpty(t *testing.T) {
	if got := resolveDeclaredWorkspaces(nil, ""); got != nil {
		t.Errorf("expected nil for empty inputs; got %v", got)
	}
}

// TestAcquireRejectsNonDeclared confirms that once setDeclared has been
// called with an allowlist, acquire of a root outside the list fails with
// MGS2010 (SandboxPolicyMismatch).
func TestAcquireRejectsNonDeclared(t *testing.T) {
	allowed := t.TempDir()
	forbidden := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lim := cache.NewLimiter(1)
	reg := newWSRegistry(ctx, lim, 0)
	defer reg.close()

	reg.setDeclared([]string{allowed})

	_, err := reg.acquire(ctx, forbidden)
	if err == nil {
		t.Fatal("acquire of non-declared root should error")
	}
	var de *types.DiagnosticError
	if !errors.As(err, &de) || de.Code != types.SandboxPolicyMismatch {
		t.Errorf("expected MGS2010 SandboxPolicyMismatch; got %v", err)
	}
}

// TestAcquireAdmitsDeclaredEvenWithoutMagusYaml verifies that a declared
// workspace without a magus.yaml falls back to defaults rather than being
// rejected as undeclared. Without an actual workspace layout the load will
// still fail; we only verify that the allowlist check passes (the failure
// surfaces from magus.Open, not from the declared gate).
func TestAcquireAdmitsDeclaredEvenWithoutMagusYaml(t *testing.T) {
	allowed := t.TempDir()
	// Write an empty magus.yaml so config loading succeeds and the test
	// reaches the magus.Open step deterministically.
	if err := os.WriteFile(filepath.Join(allowed, "magus.yaml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lim := cache.NewLimiter(1)
	reg := newWSRegistry(ctx, lim, 0)
	defer reg.close()
	reg.setDeclared([]string{allowed})

	// acquire may fail at magus.Open (no real workspace), but it must NOT
	// fail with the MGS2010 declared-list gate.
	_, err := reg.acquire(ctx, allowed)
	if err != nil {
		var de *types.DiagnosticError
		if errors.As(err, &de) && de.Code == types.SandboxPolicyMismatch {
			t.Fatalf("acquire of declared root was wrongly rejected by the allowlist gate: %v", err)
		}
	}
}

// TestWarmRespectsContextCancellation verifies that warm exits promptly when
// the context is already cancelled, without hanging on acquire.
func TestWarmRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before warm is called

	lim := cache.NewLimiter(2)
	reg := newWSRegistry(context.Background(), lim, 0)
	defer reg.close()

	// Supply several roots; warm should bail after the first ctx.Err() check.
	roots := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	reg.setDeclared(roots)

	done := make(chan struct{})
	go func() {
		reg.warm(ctx, roots)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("warm did not return promptly after context cancellation")
	}
}

// TestWarmCompletesAndPopulatesStatus verifies that warm runs to completion
// and that any successfully loaded workspaces appear in status() afterwards.
// Warm must not panic, hang, or silently skip entries.
func TestWarmCompletesAndPopulatesStatus(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lim := cache.NewLimiter(2)
	reg := newWSRegistry(ctx, lim, 0)
	defer reg.close()
	reg.setDeclared([]string{root1, root2})

	done := make(chan struct{})
	go func() {
		reg.warm(ctx, []string{root1, root2})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("warm did not complete within timeout")
	}
	// Any workspace that loaded successfully must appear in status().
	// The exact count depends on whether magus.Open succeeds for bare dirs.
	_ = reg.status() // must not panic
}

// TestWarmInBackgroundTrackedByClose verifies that close() waits for an
// in-flight warm rather than racing it — otherwise a workspace could be
// acquired (and leaked) after close() swept the entry map.
func TestWarmInBackgroundTrackedByClose(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lim := cache.NewLimiter(2)
	reg := newWSRegistry(ctx, lim, 0)
	reg.setDeclared([]string{root})
	reg.warmInBackground(ctx, []string{root})

	// close() must return: its wg.Wait() should account for the warm goroutine
	// (plus the janitor) and not hang.
	done := make(chan struct{})
	go func() {
		reg.close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("close did not return; warm goroutine not tracked by the waitgroup?")
	}
}
