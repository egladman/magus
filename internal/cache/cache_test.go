package cache_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/egladman/magus/internal/cache"
)

// newMutableCache opens a mutable cache at <tmp>/.magus and
// returns a fresh workspace root, the cache directory, and an open
// cache. The caller may re-open the same cdir with different options.
func newMutableCache(t *testing.T) (root, cdir string, c *cache.Cache) {
	t.Helper()
	root = t.TempDir()
	cdir = filepath.Join(t.TempDir(), ".magus")
	c, err := cache.Open(cdir, cache.WithMutable(true))
	if err != nil {
		t.Fatalf("cache.Open: %v", err)
	}
	return root, cdir, c
}

// writeMain writes the test project's main.go with the given body.
// Centralised so a future change to the project layout updates one
// place.
func writeMain(t *testing.T, root, body string) {
	t.Helper()
	abs := filepath.Join(root, "test", "pkg", "main.go")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// makeSpec returns the canonical Spec used across these tests:
// project test/pkg, sources "test/pkg/*.go", rooted at the given
// workspace.
func makeSpec(root string) cache.Spec {
	return cache.Spec{
		ProjectPath:   "test/pkg",
		Sources:       []string{"test/pkg/*.go"},
		WorkspaceRoot: root,
	}
}

// touchOut creates an empty file at <root>/test/pkg/out.txt and
// returns its absolute path. Tests use it as the declared output.
func touchOut(t *testing.T, root string) string {
	t.Helper()
	out := filepath.Join(root, "test", "pkg", "out.txt")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestMissThenHit verifies that the first Run is a miss (fn called)
// and a subsequent Run with the same inputs is a hit (fn not called).
func TestMissThenHit(t *testing.T) {
	root, cdir, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)

	spec := makeSpec(root)
	spec.Outputs = []string{"test/pkg/out.txt"}

	calls := 0
	fn := func(_ context.Context) error {
		calls++
		return os.WriteFile(out, []byte("built"), 0o644)
	}

	r1, err := c.Run(context.Background(), spec, fn)
	if err != nil {
		t.Fatalf("Run(miss): %v", err)
	}
	if r1.Hit {
		t.Fatal("first Run must miss")
	}
	if calls != 1 {
		t.Fatalf("fn called %d times, want 1", calls)
	}
	if r1.Hash == "" {
		t.Fatal("Hash must not be empty after a successful run")
	}

	// Re-open in read-only mode so the second call can hit.
	c2, err := cache.Open(cdir, cache.WithMutable(false))
	if err != nil {
		t.Fatalf("cache.Open(read): %v", err)
	}
	r2, err := c2.Run(context.Background(), spec, fn)
	if err != nil {
		t.Fatalf("Run(hit): %v", err)
	}
	if !r2.Hit {
		t.Fatal("second Run must hit")
	}
	if calls != 1 {
		t.Fatalf("fn called %d times after hit, want 1", calls)
	}
	if r2.Hash != r1.Hash {
		t.Fatalf("hit hash %q != miss hash %q", r2.Hash, r1.Hash)
	}
}

// TestNoCacheAlwaysRuns verifies that a Spec with NoCache=true never replays:
// fn runs on every Run with identical inputs (no snapshot, no hit), so a
// long-running target re-executes instead of replaying a cached completion.
func TestNoCacheAlwaysRuns(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)

	spec := makeSpec(root)
	spec.Outputs = []string{"test/pkg/out.txt"}
	spec.NoCache = true

	calls := 0
	fn := func(_ context.Context) error {
		calls++
		return os.WriteFile(out, []byte("built"), 0o644)
	}

	r1, err := c.Run(context.Background(), spec, fn)
	if err != nil || r1.Hit {
		t.Fatalf("first Run: hit=%v err=%v; want miss", r1.Hit, err)
	}
	r2, err := c.Run(context.Background(), spec, fn)
	if err != nil || r2.Hit {
		t.Fatalf("second Run: hit=%v err=%v; want miss (NoCache must never replay)", r2.Hit, err)
	}
	if calls != 2 {
		t.Fatalf("fn calls = %d, want 2 (NoCache must run every time)", calls)
	}
}

// TestModeAutoWritesOnMiss verifies that the default ModeAuto writes
// a manifest on miss so the next run can hit.
func TestModeAutoWritesOnMiss(t *testing.T) {
	root := t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	// Do NOT set MAGUS_CACHE_MODE — default (ModeAuto) must write.
	c, err := cache.Open(cdir)
	if err != nil {
		t.Fatalf("cache.Open: %v", err)
	}
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	spec := makeSpec(root)
	spec.Outputs = []string{"test/pkg/out.txt"}
	calls := 0
	fn := func(_ context.Context) error { calls++; return os.WriteFile(out, []byte("built"), 0o644) }

	r1, err := c.Run(context.Background(), spec, fn)
	if err != nil {
		t.Fatalf("Run(miss): %v", err)
	}
	if r1.Hit {
		t.Fatal("first Run must miss")
	}

	// Re-open (same dir, same default mode) — must hit.
	c2, err := cache.Open(cdir)
	if err != nil {
		t.Fatalf("cache.Open(auto, second): %v", err)
	}
	r2, err := c2.Run(context.Background(), spec, fn)
	if err != nil {
		t.Fatalf("Run(hit): %v", err)
	}
	if !r2.Hit {
		t.Fatal("ModeAuto must hit on second run")
	}
	if calls != 1 {
		t.Fatalf("fn called %d times, want 1", calls)
	}
}

// TestModeAutoReplaysOnHit verifies that ModeAuto does not call fn
// when the cache already has a valid manifest for the spec.
func TestModeAutoReplaysOnHit(t *testing.T) {
	root, cdir, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	spec := makeSpec(root)
	spec.Outputs = []string{"test/pkg/out.txt"}
	fn := func(_ context.Context) error { return os.WriteFile(out, []byte("built"), 0o644) }

	if _, err := c.Run(context.Background(), spec, fn); err != nil {
		t.Fatalf("prime: %v", err)
	}

	// Re-open (default mutable) — must hit without calling fn.
	c2, err := cache.Open(cdir)
	if err != nil {
		t.Fatalf("cache.Open: %v", err)
	}
	calls := 0
	r, err := c2.Run(context.Background(), spec, func(_ context.Context) error { calls++; return fn(context.Background()) })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !r.Hit {
		t.Fatal("must replay on hit")
	}
	if calls != 0 {
		t.Fatalf("fn called %d times, want 0 on hit", calls)
	}
}

// TestImmutableDoesNotWriteOnMiss verifies that a read-only cache never writes
// a manifest — a subsequent run still misses.
func TestImmutableDoesNotWriteOnMiss(t *testing.T) {
	root := t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	spec := makeSpec(root)
	spec.Outputs = []string{"test/pkg/out.txt"}
	fn := func(_ context.Context) error { return os.WriteFile(out, []byte("built"), 0o644) }

	c, err := cache.Open(cdir, cache.WithMutable(false))
	if err != nil {
		t.Fatalf("cache.Open(immutable): %v", err)
	}
	if r, err := c.Run(context.Background(), spec, fn); err != nil || r.Hit {
		t.Fatalf("first Run: hit=%v err=%v; want miss", r.Hit, err)
	}

	// Re-open mutable — must still miss (nothing was written).
	c2, err := cache.Open(cdir)
	if err != nil {
		t.Fatalf("cache.Open: %v", err)
	}
	if r, err := c2.Run(context.Background(), spec, fn); err != nil || r.Hit {
		t.Fatalf("second Run after immutable miss: hit=%v err=%v; want miss", r.Hit, err)
	}
}

// TestHashChangesOnSourceEdit verifies that editing a source file
// changes the hash.
func TestHashChangesOnSourceEdit(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main // v1")
	out := touchOut(t, root)
	spec := makeSpec(root)
	spec.Outputs = []string{"test/pkg/out.txt"}
	fn := func(_ context.Context) error { return os.WriteFile(out, []byte("out"), 0o644) }

	r1, err := c.Run(context.Background(), spec, fn)
	if err != nil {
		t.Fatal(err)
	}
	writeMain(t, root, "package main // v2")
	r2, err := c.Run(context.Background(), spec, fn)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Hash == r2.Hash {
		t.Fatal("hash must change after source edit")
	}
}

// TestActionDiscriminant verifies that two specs differing only in
// Action produce different hashes.
func TestActionDiscriminant(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)

	base := makeSpec(root)
	base.Outputs = []string{"test/pkg/out.txt"}
	fn := func(_ context.Context) error { return os.WriteFile(out, []byte("out"), 0o644) }

	build := base
	build.Target = "build"
	rBuild, err := c.Run(context.Background(), build, fn)
	if err != nil {
		t.Fatal(err)
	}
	test := base
	test.Target = "test"
	rTest, err := c.Run(context.Background(), test, fn)
	if err != nil {
		t.Fatal(err)
	}
	if rBuild.Hash == rTest.Hash {
		t.Fatal("different Target must yield different hash")
	}
}

// TestClean verifies that Clean removes manifests, making a
// subsequent Run a miss.
func TestClean(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	spec := makeSpec(root)
	spec.Outputs = []string{"test/pkg/out.txt"}
	fn := func(_ context.Context) error { return os.WriteFile(out, []byte("out"), 0o644) }

	if _, err := c.Run(context.Background(), spec, fn); err != nil {
		t.Fatal(err)
	}
	if err := c.Clean(context.Background(), "test/pkg"); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	calls := 0
	wrapped := func(_ context.Context) error { calls++; return fn(context.Background()) }
	r, err := c.Run(context.Background(), spec, wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if r.Hit {
		t.Fatal("Run after Clean must miss")
	}
	if calls != 1 {
		t.Fatalf("fn called %d times after Clean, want 1", calls)
	}
}

// TestStats verifies that the per-cache miss counter is bumped after
// a write-mode miss.
func TestStats(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	spec := makeSpec(root)
	spec.Outputs = []string{"test/pkg/out.txt"}

	if _, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(out, []byte("out"), 0o644)
	}); err != nil {
		t.Fatal(err)
	}
	want := cache.Stats{Hit: 0, Miss: 1, Error: 0}
	if got := c.Stats(); !reflect.DeepEqual(want, got) {
		t.Fatalf("Stats: got %+v, want %+v", got, want)
	}
}

// TestCacheSizeCapAccepted exercises MAGUS_CACHE_SIZE parsing: any
// recognised value (or unrecognised, which falls back to "no cap")
// must let Open succeed.
func TestCacheSizeCapAccepted(t *testing.T) {
	for _, env := range []string{"", "0", "1000", "500KB", "2MB", "1GB", "bad"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv("MAGUS_CACHE_SIZE", env)
			if _, err := cache.Open(filepath.Join(t.TempDir(), ".cap")); err != nil {
				t.Fatalf("Open with MAGUS_CACHE_SIZE=%q: %v", env, err)
			}
		})
	}
}

// TestPanicInFnDoesNotDeadlock guards the captureRun fix: if fn
// panics (e.g. a buggy build function), the cache must clean up its
// pipe readers and unwind without deadlocking. Without the
// defer-based cleanup, this test would hang.
func TestPanicInFnDoesNotDeadlock(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	spec := makeSpec(root)
	spec.Outputs = []string{"test/pkg/out.txt"}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic to propagate from fn")
		}
	}()

	_, _ = c.Run(context.Background(), spec, func(_ context.Context) error {
		panic(errors.New("boom"))
	})
}

// TestExportImportRoundTrip verifies that a cache snapshot exported to a
// gzip-tar and imported into a fresh cache directory produces hits on the
// same spec.
func TestExportImportRoundTrip(t *testing.T) {
	root, _, src := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	spec := makeSpec(root)
	spec.Outputs = []string{"test/pkg/out.txt"}

	// Populate the source cache.
	if _, err := src.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(out, []byte("built"), 0o644)
	}); err != nil {
		t.Fatalf("populate: %v", err)
	}

	// Export the source cache to an in-memory buffer.
	var rawBuf bytes.Buffer
	if err := src.Export(context.Background(), &rawBuf); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Import into a new cache directory (immutable so we test read-after-import).
	dstDir := filepath.Join(t.TempDir(), ".magus-dst")
	dst, err := cache.Open(dstDir, cache.WithMutable(false))
	if err != nil {
		t.Fatalf("Open dst: %v", err)
	}
	if err := dst.Import(context.Background(), &rawBuf); err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Verify that the spec hits in the destination cache.
	calls := 0
	r, err := dst.Run(context.Background(), spec, func(_ context.Context) error { calls++; return nil })
	if err != nil {
		t.Fatalf("Run after import: %v", err)
	}
	if !r.Hit {
		t.Fatal("Run after Import must hit")
	}
	if calls != 0 {
		t.Fatalf("fn called %d times after Import hit, want 0", calls)
	}
}

// TestOnResult verifies that the OnResult callback fires exactly once
// after every Cache.Run regardless of whether the result is a hit, miss,
// or error, and that the Spec and Result passed to it are consistent.
func TestOnResult(t *testing.T) {
	t.Parallel()
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	spec := makeSpec(root)
	spec.Outputs = []string{"test/pkg/out.txt"}

	type call struct {
		projectPath string
		hit         bool
		err         error
	}
	var calls []call
	onResult := cache.OnResult(func(s *cache.Spec, r *cache.Result, err error) {
		calls = append(calls, call{s.ProjectPath, r.Hit, err})
	})

	// Miss: fn runs and writes out.txt.
	if _, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(out, []byte("ok"), 0o644)
	}, onResult); err != nil {
		t.Fatalf("Run(miss): %v", err)
	}
	if len(calls) != 1 || calls[0].hit || calls[0].err != nil {
		t.Fatalf("after miss: calls=%+v", calls)
	}

	// Hit: same spec, fn must not run.
	if _, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		t.Error("fn must not run on hit")
		return nil
	}, onResult); err != nil {
		t.Fatalf("Run(hit): %v", err)
	}
	if len(calls) != 2 || !calls[1].hit || calls[1].err != nil {
		t.Fatalf("after hit: calls=%+v", calls)
	}

	// Error: fn fails; OnResult fires with the error.
	errSpec := cache.Spec{
		ProjectPath:   "test/pkg/err",
		Sources:       []string{"test/pkg/*.go"},
		WorkspaceRoot: root,
	}
	wantErr := errors.New("build failed")
	if _, err := c.Run(context.Background(), errSpec, func(_ context.Context) error {
		return wantErr
	}, onResult); err == nil {
		t.Fatal("Run(error): expected error")
	}
	if len(calls) != 3 || calls[2].hit || calls[2].err == nil {
		t.Fatalf("after error: calls=%+v", calls)
	}
}

// TestExportImportUnsafePath verifies that Import rejects tar entries
// that would escape the cache directory via path traversal.
func TestExportImportUnsafePath(t *testing.T) {
	dir := t.TempDir()
	c, err := cache.Open(filepath.Join(dir, ".magus"), cache.WithMutable(false))
	if err != nil {
		t.Fatal(err)
	}

	// Craft a malicious tar with a path traversal entry.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     "../../../etc/evil",
		Size:     4,
	})
	_, _ = tw.Write([]byte("evil"))
	_ = tw.Close()
	_ = gz.Close()

	if err := c.Import(context.Background(), &buf); err == nil {
		t.Fatal("Import with path traversal must return error")
	}
}
