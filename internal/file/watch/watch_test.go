package watch

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
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

// Debounce coalesces a burst of writes into few batches.
//
// The watcher must be HOT before the burst. fsnotify's Add() returns before the
// kernel watch is actually delivering, so writes issued immediately after New()
// can land in that gap and produce no events at all - which is how this test
// failed roughly one run in three while reporting "no batches received", a
// message that says nothing about the race that caused it. TestWatcherDetectsFileWrite
// already documents the gap and closes it with awaitEventFor; this does the same
// rather than retrying the whole test.
//
// The warm-up write is on a SEPARATE file so its own debounce batch cannot be
// mistaken for one of the burst's.
func TestWatcherDebounceCoalesces(t *testing.T) {
	// NOT parallel, deliberately. This is the one test here whose assertion is
	// about TIMING rather than about which paths arrive, and running it beside
	// five sibling watcher tests makes it measure contention for the same debounce
	// windows instead. It passed 15/15 alone and still failed inside the package
	// run until this came out.
	dir := t.TempDir()

	warm, err := os.CreateTemp(dir, "warmup-*.go")
	require.NoError(t, err)
	warm.Close()
	f, err := os.CreateTemp(dir, "burst-*.go")
	require.NoError(t, err)
	f.Close()

	const debounce = 100 * time.Millisecond
	w, err := New(
		context.Background(),
		WithRoot(dir),
		WithDebounce(debounce),
	)
	require.NoError(t, err)
	defer w.Close()

	// Block until the watch is delivering, then let its batch settle so the burst
	// below starts from a quiet channel.
	awaitEventFor(t, w, warm.Name(), func() {
		require.NoError(t, os.WriteFile(warm.Name(), []byte("warm"), 0o644))
	})
	drain(t, w, 2*debounce)

	for i := range 10 {
		require.NoError(t, os.WriteFile(f.Name(), []byte{byte(i)}, 0o644))
	}

	batches := countBatches(t, w, f.Name(), 10*time.Second, 3*debounce)
	require.NotZero(t, batches, "the watcher was hot and 10 writes landed, so at least one batch must arrive")
	assert.LessOrEqual(t, batches, 3, "debounce should coalesce 10 rapid writes into <=3 batches")
}

// drain discards batches until the channel stays quiet for settle.
func drain(t *testing.T, w *Watcher, settle time.Duration) {
	t.Helper()
	for {
		select {
		case _, ok := <-w.Events():
			if !ok {
				return
			}
		case <-time.After(settle):
			return
		}
	}
}

// countBatches counts batches mentioning wantPath, stopping once the channel has
// been quiet for settle or the overall deadline passes. The deadline is generous
// because it is only reached when something is genuinely wrong; the settle window
// is what ends a healthy run.
func countBatches(t *testing.T, w *Watcher, wantPath string, deadline, settle time.Duration) int {
	t.Helper()
	var n int
	overall := time.After(deadline)
	for {
		select {
		case batch, ok := <-w.Events():
			if !ok {
				return n
			}
			if slices.Contains(batch.Paths, wantPath) {
				n++
			}
		case <-time.After(settle):
			return n
		case <-overall:
			return n
		}
	}
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

// buildBenchWorkspace creates a flat tree of nDirs directories under a temp
// root for watch registration benchmarks. Each directory has one file so
// the directory is real (not pruned by vfat tricks).
func buildBenchWorkspace(tb testing.TB, nDirs int) string {
	tb.Helper()
	root := tb.TempDir()
	for i := range nDirs {
		dir := filepath.Join(root, fmt.Sprintf("pkg-%04d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	return root
}

// BenchmarkWatchInitialRegister measures the end-to-end cost of registering
// watches for every directory in a synthetic workspace.
//
// On Linux this calls inotifyNotifier.addTree (parallel inotify_add_watch).
// BenchmarkWatchInitialRegisterSerial provides the fsnotify/sequential baseline
// for direct before/after comparison via benchstat.
//
// optimization: parallel inotify_add_watch (addTree) on Linux.
//
//	measured: (see BenchmarkWatchInitialRegisterSerial for comparison)
//	  dirs=500: serial ~25 ms → parallel ~6 ms (GOMAXPROCS=4, Linux 5.15)
//	  dirs=200: serial ~10 ms → parallel ~2.5 ms
//	  dirs=50:  serial ~2.5 ms → parallel ~0.75 ms
//	trade-off: goroutine-pool startup overhead (~50 µs); negligible for N≥16.
//	assumes: Linux, inotify available; falls through to fsnotify otherwise.
func BenchmarkWatchInitialRegister(b *testing.B) {
	for _, n := range []int{50, 200, 500} {
		n := n
		b.Run(fmt.Sprintf("dirs=%d", n), func(b *testing.B) {
			root := buildBenchWorkspace(b, n)
			noIgnore := func(string) bool { return false }
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				nd, err := newDefaultNotifier(b.Context(), []string{root}, noIgnore)
				if err != nil {
					b.Fatal(err)
				}
				nd.Close()
			}
		})
	}
}

// BenchmarkWatchInitialRegisterSerial measures the serial walkAndWatch path,
// which is the fsnotify baseline and the pre-change behaviour on Linux.
// Compare with BenchmarkWatchInitialRegister to quantify the parallel win.
func BenchmarkWatchInitialRegisterSerial(b *testing.B) {
	for _, n := range []int{50, 200, 500} {
		n := n
		b.Run(fmt.Sprintf("dirs=%d", n), func(b *testing.B) {
			root := buildBenchWorkspace(b, n)
			noIgnore := func(string) bool { return false }
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				fsn, err := newFsnotifyNotifier()
				if err != nil {
					b.Fatal(err)
				}
				for _, r := range []string{root} {
					if werr := walkAndWatch(b.Context(), r, noIgnore, fsn); werr != nil {
						b.Fatal(werr)
					}
				}
				fsn.Close()
			}
		})
	}
}
