package watch_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/file/watch"
)

func TestWatcherDetectsFileWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Pre-create the file so the watcher is set up before the write.
	f, err := os.CreateTemp(dir, "*.go")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	w, err := watch.New(
		context.Background(),
		watch.WithRoot(dir),
		watch.WithDebounce(50*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Write to the file to trigger an event.
	if err := os.WriteFile(f.Name(), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case batch := <-w.Events():
		found := false
		for _, p := range batch.Paths {
			if p == f.Name() {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("batch.Paths = %v, want to contain %s", batch.Paths, f.Name())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: no event received after file write")
	}
}

func TestWatcherDebounceCoalesces(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, "*.go")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	w, err := watch.New(
		context.Background(),
		watch.WithRoot(dir),
		watch.WithDebounce(100*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Write 10 times in rapid succession.
	for i := range 10 {
		if err := os.WriteFile(f.Name(), []byte{byte(i)}, 0o644); err != nil {
			t.Fatal(err)
		}
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
	if batches == 0 {
		t.Fatal("no batches received")
	}
	// Tolerate at most 3 batches (debounce may fire between writes on slow CI).
	if batches > 3 {
		t.Errorf("got %d batches for 10 rapid writes; want ≤3 (debounce should coalesce)", batches)
	}
}

func TestWatcherIgnoresBuiltinPaths(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create a .git directory and a file inside it.
	gitDir := filepath.Join(dir, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}

	w, err := watch.New(
		context.Background(),
		watch.WithRoot(dir),
		watch.WithDebounce(50*time.Millisecond),
		watch.WithIgnore(watch.BuiltinIgnore),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Write inside .git — should not produce an event.
	gitFile := filepath.Join(gitDir, "HEAD")
	if err := os.WriteFile(gitFile, []byte("ref: refs/heads/main"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write a legitimate file — should produce an event.
	legit := filepath.Join(dir, "main.go")
	if err := os.WriteFile(legit, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case batch := <-w.Events():
		for _, p := range batch.Paths {
			if p == gitFile {
				t.Errorf("received event for .git file %s; should have been ignored", gitFile)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for legitimate event")
	}
}

func TestWatcherDetectsNewSubdir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	w, err := watch.New(
		context.Background(),
		watch.WithRoot(dir),
		watch.WithDebounce(50*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	sub := filepath.Join(dir, "newpkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	newFile := filepath.Join(sub, "foo.go")

	// Re-write loop instead of a Sleep: keep recreating the target
	// file on a short ticker until the watcher reports it, or the
	// deadline trips. fsnotify backends register newly-created
	// directories asynchronously; a single write that landed before
	// the watch was attached would silently drop on slow CI. Each
	// WriteFile generates a fresh CREATE/WRITE the watcher will see
	// once its watch on `sub` is registered.
	deadline := time.After(4 * time.Second)
	retick := time.NewTicker(50 * time.Millisecond)
	defer retick.Stop()
	write := func() {
		if err := os.WriteFile(newFile, []byte("package newpkg"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write()
	for {
		select {
		case batch := <-w.Events():
			for _, p := range batch.Paths {
				if p == newFile {
					return // success
				}
			}
		case <-retick.C:
			write()
		case <-deadline:
			t.Fatal("timeout: did not see event for file in new subdirectory")
		}
	}
}

// TestWatcherContextCancellation verifies that cancelling the context
// closes the watcher (Events channel closes) without an explicit Close call.
func TestWatcherContextCancellation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	w, err := watch.New(
		ctx,
		watch.WithRoot(dir),
		watch.WithDebounce(50*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}

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
	ignore := watch.OutputsIgnore(wsRoot, []string{"dist/**", "build/output/**"})

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
		got := ignore(tc.path)
		if got != tc.ignored {
			t.Errorf("OutputsIgnore(%q) = %v, want %v", tc.path, got, tc.ignored)
		}
	}
}

// TestWatcherCloseNoGoroutineLeak verifies that calling Close on a Watcher
// with a non-cancellable context doesn't leave the ctx goroutine running.
// It checks that the Events channel is closed promptly after Close returns.
func TestWatcherCloseNoGoroutineLeak(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Use a background (non-cancellable) context to exercise the Close path.
	w, err := watch.New(
		context.Background(),
		watch.WithRoot(dir),
		watch.WithDebounce(50*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

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
		got := watch.BuiltinIgnore(tc.path)
		if got != tc.ignored {
			t.Errorf("BuiltinIgnore(%q) = %v, want %v", tc.path, got, tc.ignored)
		}
	}
}
