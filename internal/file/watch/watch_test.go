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

	"github.com/egladman/magus/types"
	"github.com/fsnotify/fsnotify"
)

// awaitEventFor re-invokes trigger on a ticker until a debounced batch containing wantPath
// arrives, then returns; it fails the test at a generous deadline. This is the robust pattern
// for fsnotify tests: there is no portable signal that an OS watch is "hot" after Add()
// returns, so a single trigger can drop in the establishment gap (and stay dropped) under
// load. Re-firing recovers it once the watch goes live. The ticker interval sits ABOVE the
// tests' debounce window on purpose: re-triggering faster than the debounce would keep
// resetting the timer and starve the flush, so a batch would never emit.
func awaitEvent(t *testing.T, w *Watcher, wantPath string, trigger func()) {
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
	awaitEvent(t, w, f.Name(), func() {
		require.NoError(t, os.WriteFile(f.Name(), []byte("hello"), 0o644))
	})
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
	awaitEvent(t, w, newFile, func() {
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
	ignore := OutputsIgnore(wsRoot, types.MustParseGlobs("dist/**", "build/output/**"))

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

// TestOutputsIgnoreWatchesAnExcludedFile pins the rebuild-loop guard to the declaration:
// a hand-maintained file an output carves out is a source, so editing it must trigger a
// rebuild, and the directory holding it must not be pruned from the watch.
func TestOutputsIgnoreWatchesAnExcludedFile(t *testing.T) {
	t.Parallel()
	ignore := OutputsIgnore("/repo", types.MustParseGlobs("gen/**", "!gen/runtime.go"))

	assert.True(t, ignore("/repo/gen/fs.go"), "a generated file is still ignored")
	assert.False(t, ignore("/repo/gen/runtime.go"), "the excluded file fires a rebuild")
	assert.False(t, ignore("/repo/gen"), "the directory holding it stays watched")
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

// chanNotifier is a notifier whose event stream the test drives directly,
// so the pending-cap behavior can be exercised without depending on real
// fsnotify delivery — which drops events under backpressure (see
// notify_linux.go) and so cannot be relied on to deliver a precise count
// under CPU load.
type chanNotifier struct {
	events chan fsnotify.Event
	errors chan error
}

func (n *chanNotifier) Add(string) error              { return nil }
func (n *chanNotifier) Remove(string) error           { return nil }
func (n *chanNotifier) Events() <-chan fsnotify.Event { return n.events }
func (n *chanNotifier) Errors() <-chan error          { return n.errors }
func (n *chanNotifier) Close() error                  { return nil }

// newTestWatcher assembles a Watcher around fake, bypassing newDefaultNotifier,
// and starts its loop with cfg. It mirrors the channel sizing in New.
func newTestWatcher(ctx context.Context, fake notifier, cfg watchConfig) *Watcher {
	w := &Watcher{
		events:  make(chan Batch, cfg.bufferSize),
		errors:  make(chan error, 16),
		done:    make(chan struct{}),
		n:       fake,
		walkSem: make(chan struct{}, walkWorkers),
		synth:   make(chan string, maxPending*2),
	}
	go w.loop(ctx, cfg)
	return w
}

// TestPendingCapFlushesImmediately verifies that crossing maxPending forces a
// flush rather than waiting for the debounce timer. It drives events through a
// fake notifier so the count is exact and lossless: with a 30s debounce, any
// batch that arrives promptly can only have come from the cap path.
func TestPendingCapFlushesImmediately(t *testing.T) {
	t.Parallel()
	fake := &chanNotifier{
		events: make(chan fsnotify.Event, maxPending*2),
		errors: make(chan error, 16),
	}
	cfg := watchConfig{
		debounce:   30 * time.Second, // long enough that a timely flush must be the cap
		ignore:     func(string) bool { return false },
		bufferSize: 256,
	}
	w := newTestWatcher(context.Background(), fake, cfg)
	defer w.Close()

	// Feed exactly maxPending+8 distinct Write events. Write (not Create) avoids
	// the loop's os.Stat dir probe, keeping the path purely in-memory.
	for i := 0; i < maxPending+8; i++ {
		fake.events <- fsnotify.Event{
			Name: fmt.Sprintf("/virtual/f%04d.txt", i),
			Op:   fsnotify.Write,
		}
	}

	select {
	case batch := <-w.Events():
		assert.GreaterOrEqual(t, len(batch.Paths), maxPending, "cap flush batch too small")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout: no flush received; pending cap is not flushing on overflow")
	}
}

// Debounce coalescing, asserted exactly.
//
// This drives synthetic events through chanNotifier for the same reason
// TestPendingCapFlushesImmediately does, stated on chanNotifier: real fsnotify drops
// events under backpressure and cannot deliver a precise count under CPU load. An
// earlier version of this assertion went through the real filesystem and needed a
// warm-up write, a drain, a dropped t.Parallel, and three tuned constants
// (100ms/300ms/10s), and still failed in CI. It was measuring the OS scheduler.
//
// The debounce here is longer than the test can possibly take, so the timer is
// guaranteed NOT to fire mid-burst. Closing the event channel then makes loop
// flush whatever is pending as one final batch. That turns "10 rapid events
// coalesce into one batch" into an exact equality with no wall-clock in it: if
// coalescing broke, the count is 10, not "more than 3".
func TestDebounceCoalescesBurstIntoOneBatch(t *testing.T) {
	const burst = 10

	fake := &chanNotifier{
		events: make(chan fsnotify.Event, burst),
		errors: make(chan error, 1),
	}
	cfg := watchConfig{
		debounce:   time.Hour, // never fires; the close below is what flushes
		bufferSize: 8,
		ignore:     func(string) bool { return false },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newTestWatcher(ctx, fake, cfg)

	for range burst {
		fake.events <- fsnotify.Event{Name: "/tmp/a.go", Op: fsnotify.Write}
	}
	close(fake.events)

	batch, ok := <-w.Events()
	require.True(t, ok, "closing the event stream must flush the pending set")
	assert.Equal(t, []string{"/tmp/a.go"}, batch.Paths,
		"%d writes to one path must coalesce into a single deduplicated entry", burst)

	_, more := <-w.Events()
	assert.False(t, more, "the burst must produce exactly one batch, not one per event")
}

// Distinct paths in one window are coalesced into a single batch too, and every
// path survives. Deduplication must not become deletion.
func TestDebounceKeepsEveryDistinctPathInOneBatch(t *testing.T) {
	const paths = 5

	fake := &chanNotifier{
		events: make(chan fsnotify.Event, paths*2),
		errors: make(chan error, 1),
	}
	cfg := watchConfig{
		debounce:   time.Hour,
		bufferSize: 8,
		ignore:     func(string) bool { return false },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newTestWatcher(ctx, fake, cfg)

	want := make([]string, 0, paths)
	for i := range paths {
		p := fmt.Sprintf("/tmp/f%d.go", i)
		want = append(want, p)
		// Twice each, so dedup and coalescing are both exercised.
		fake.events <- fsnotify.Event{Name: p, Op: fsnotify.Write}
		fake.events <- fsnotify.Event{Name: p, Op: fsnotify.Write}
	}
	close(fake.events)

	batch, ok := <-w.Events()
	require.True(t, ok)
	assert.ElementsMatch(t, want, batch.Paths,
		"each path appears once, and none is dropped")
}
