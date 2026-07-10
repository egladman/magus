package watch

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// awaitEventFor re-invokes trigger on a ticker until a debounced batch containing wantPath
// arrives, then returns; it fails the test at a generous deadline. This is the robust pattern
// for fsnotify tests: there is no portable signal that an OS watch is "hot" after Add()
// returns, so a single trigger can drop in the establishment gap (and stay dropped) under
// load. Re-firing recovers it once the watch goes live. The ticker interval sits ABOVE the
// tests' debounce window on purpose - re-triggering faster than the debounce would keep
// resetting the timer and starve the flush, so a batch would never emit.
func awaitEventFor(t *testing.T, w *Watcher, wantPath string, trigger func()) {
	t.Helper()
	const retickInterval = 200 * time.Millisecond // > the 50ms debounce used by these tests
	deadline := time.After(5 * time.Second)
	retick := time.NewTicker(retickInterval)
	defer retick.Stop()
	trigger()
	for {
		select {
		case batch := <-w.Events():
			if slices.Contains(batch.Paths, wantPath) {
				return
			}
		case <-retick.C:
			trigger()
		case <-deadline:
			t.Fatalf("timeout: no event for %s", wantPath)
		}
	}
}

func TestWatcherDetectsFileWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Pre-create the file so the watcher is set up before the write.
	f, err := os.CreateTemp(dir, "*.go")
	require.NoError(t, err)
	f.Close()

	w, err := New(
		context.Background(),
		WithRoot(dir),
		WithDebounce(50*time.Millisecond),
	)
	require.NoError(t, err)
	defer w.Close()

	// Re-write until the watcher reports it: a single write can drop in the gap between
	// Add() returning and the OS watch becoming hot. See awaitEventFor.
	awaitEventFor(t, w, f.Name(), func() {
		require.NoError(t, os.WriteFile(f.Name(), []byte("hello"), 0o644))
	})
}

func TestWatcherDebounceCoalesces(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, "*.go")
	require.NoError(t, err)
	f.Close()

	w, err := New(
		context.Background(),
		WithRoot(dir),
		WithDebounce(100*time.Millisecond),
	)
	require.NoError(t, err)
	defer w.Close()

	// Write 10 times in rapid succession.
	for i := range 10 {
		require.NoError(t, os.WriteFile(f.Name(), []byte{byte(i)}, 0o644))
	}

	var batches int
	deadline := time.After(2 * time.Second)
	// Drain events for 500ms after the first batch to confirm no second batch fires.
	got := false
	for {
		select {
		case _, ok := <-w.Events():
			if !ok {
				goto done
			}
			batches++
			got = true
		case <-deadline:
			goto done
		case <-func() <-chan time.Time {
			if got {
				return time.After(300 * time.Millisecond)
			}
			return nil
		}():
			goto done
		}
	}
done:
	require.NotZero(t, batches, "no batches received")
	// Tolerate at most 3 batches (debounce may fire between writes on slow CI).
	assert.LessOrEqual(t, batches, 3, "debounce should coalesce 10 rapid writes into ≤3 batches")
}

func TestWatcherIgnoresBuiltinPaths(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create a .git directory and a file inside it.
	gitDir := filepath.Join(dir, ".git")
	require.NoError(t, os.Mkdir(gitDir, 0o755))

	w, err := New(
		context.Background(),
		WithRoot(dir),
		WithDebounce(50*time.Millisecond),
		WithIgnore(BuiltinIgnore),
	)
	require.NoError(t, err)
	defer w.Close()

	// Write inside .git once — it must never surface in any batch.
	gitFile := filepath.Join(gitDir, "HEAD")
	require.NoError(t, os.WriteFile(gitFile, []byte("ref: refs/heads/main"), 0o644))

	// Re-write a legitimate file until it surfaces (the single-write establishment-gap race
	// applies here too), asserting the ignored .git file is absent from every batch we see.
	legit := filepath.Join(dir, "main.go")
	deadline := time.After(5 * time.Second)
	retick := time.NewTicker(200 * time.Millisecond)
	defer retick.Stop()
	writeLegit := func() {
		require.NoError(t, os.WriteFile(legit, []byte("package main"), 0o644))
	}
	writeLegit()
	for {
		select {
		case batch := <-w.Events():
			assert.NotContains(t, batch.Paths, gitFile, "received event for .git file; should have been ignored")
			if slices.Contains(batch.Paths, legit) {
				return
			}
		case <-retick.C:
			writeLegit()
		case <-deadline:
			t.Fatal("timeout waiting for legitimate event")
		}
	}
}

func TestWatcherDetectsNewSubdir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	w, err := New(
		context.Background(),
		WithRoot(dir),
		WithDebounce(50*time.Millisecond),
	)
	require.NoError(t, err)
	defer w.Close()

	sub := filepath.Join(dir, "newpkg")
	require.NoError(t, os.Mkdir(sub, 0o755))
	newFile := filepath.Join(sub, "foo.go")

	// A newly-created directory's watch is registered asynchronously (the loop walks it off
	// the hot path), so the first write to a file inside it can drop before the watch on
	// `sub` is live. Re-write until it surfaces. See awaitEventFor.
	awaitEventFor(t, w, newFile, func() {
		require.NoError(t, os.WriteFile(newFile, []byte("package newpkg"), 0o644))
	})
}

// TestWatcherContextCancellation verifies that cancelling the context
// closes the watcher (Events channel closes) without an explicit Close call.
func TestWatcherContextCancellation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	w, err := New(
		ctx,
		WithRoot(dir),
		WithDebounce(50*time.Millisecond),
	)
	require.NoError(t, err)

	cancel()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-w.Events():
			if !ok {
				return // success: events channel closed
			}
		case <-deadline:
			t.Fatal("timeout: events channel never closed after ctx cancellation")
		}
	}
}

// TestOutputsIgnoreDoublestar verifies that OutputsIgnore handles ** globs
// correctly so nested paths under output dirs are matched.
func TestOutputsIgnoreDoublestar(t *testing.T) {
	t.Parallel()
	const wsRoot = "/repo"
	ignore := OutputsIgnore(wsRoot, []string{"dist/**", "build/output/**"})

	cases := []struct {
		path    string
		ignored bool
	}{
		// ** matches at any depth.
		{"/repo/dist/bundle.js", true},
		{"/repo/dist/a/b/c.js", true},
		{"/repo/build/output/foo.bin", true},
		{"/repo/build/output/nested/deep/file.o", true},
		// Non-output paths must not be silenced.
		{"/repo/src/main.go", false},
		{"/repo/build/other/file.go", false},
		// Path outside the workspace root.
		{"/other/repo/dist/x.js", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.ignored, ignore(tc.path), "OutputsIgnore(%q)", tc.path)
	}
}

// TestWatcherCloseNoGoroutineLeak verifies that calling Close on a Watcher
// with a non-cancellable context doesn't leave the ctx goroutine running.
// It checks that the Events channel is closed promptly after Close returns.
func TestWatcherCloseNoGoroutineLeak(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Use a background (non-cancellable) context to exercise the Close path.
	w, err := New(
		context.Background(),
		WithRoot(dir),
		WithDebounce(50*time.Millisecond),
	)
	require.NoError(t, err)

	require.NoError(t, w.Close(), "Close() error")

	// Events channel must be closed (loop exited) promptly after Close.
	select {
	case _, ok := <-w.Events():
		if !ok {
			return // success
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: events channel still open after Close() with background ctx")
	}
}

// TestPendingCapFlushesImmediately lives in cap_internal_test.go: the cap is
// pinned deterministically via a fake notifier, since real fsnotify drops
// events under backpressure and cannot deliver an exact count under CPU load.

func TestBuiltinIgnore(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path    string
		ignored bool
	}{
		{"/repo/.git/config", true},
		{"/repo/.magus/abc", true},
		{"/repo/node_modules/lodash/index.js", true},
		{"/repo/api/target/debug/foo", true},
		{"/repo/api/foo.go", false},
		{"/repo/magus-1234-abcd.sock", true},
		{"/repo/api/main_test.go~", true},
		{"/repo/api/.file.go.swp", true},
		{"/repo/dist/bundle.js", false},
		{"/repo/web/app.ts", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.ignored, BuiltinIgnore(tc.path), "BuiltinIgnore(%q)", tc.path)
	}
}
