package cache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	runPkg "github.com/egladman/magus/internal/proc/run"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMutableCache opens a mutable cache at <tmp>/.magus and
// returns a fresh workspace root, the cache directory, and an open
// cache. The caller may re-open the same cdir with different options.
func newMutableCache(t *testing.T) (root, cdir string, c *Cache) {
	t.Helper()
	root = t.TempDir()
	cdir = filepath.Join(t.TempDir(), ".magus")
	c, err := Open(cdir, WithMutable(true))
	require.NoError(t, err, "cache.Open")
	return root, cdir, c
}

// writeMain writes the test project's main.go with the given body.
// Centralised so a future change to the project layout updates one
// place.
func writeMain(t *testing.T, root, body string) {
	t.Helper()
	abs := filepath.Join(root, "test", "pkg", "main.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755), "mkdir")
	require.NoError(t, os.WriteFile(abs, []byte(body), 0o644), "write")
}

// makeStep returns the canonical Step used across these tests:
// project test/pkg, sources "test/pkg/*.go", rooted at the given
// workspace.
func makeStep(root string) Step {
	return Step{
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
	require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o755))
	return out
}

// TestMissThenHit verifies that the first Run is a miss (fn called)
// and a subsequent Run with the same inputs is a hit (fn not called).
func TestMissThenHit(t *testing.T) {
	root, cdir, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)

	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}

	calls := 0
	fn := func(_ context.Context) error {
		calls++
		return os.WriteFile(out, []byte("built"), 0o644)
	}

	r1, err := c.Run(context.Background(), step, fn)
	require.NoError(t, err, "Run(miss)")
	require.False(t, r1.Hit, "first Run must miss")
	require.Equal(t, 1, calls, "fn called once")
	require.NotEmpty(t, r1.Hash, "Hash must not be empty after a successful run")

	// Re-open in read-only mode so the second call can hit.
	c2, err := Open(cdir, WithMutable(false))
	require.NoError(t, err, "cache.Open(read)")
	r2, err := c2.Run(context.Background(), step, fn)
	require.NoError(t, err, "Run(hit)")
	assert.True(t, r2.Hit, "second Run must hit")
	assert.Equal(t, 1, calls, "fn must not run again after hit")
	assert.Equal(t, r1.Hash, r2.Hash, "hit hash must equal miss hash")
}

// TestNoCacheAlwaysRuns verifies that a Step with NoCache=true never replays:
// fn runs on every Run with identical inputs (no snapshot, no hit), so a
// long-running target re-executes instead of replaying a cached completion.
func TestNoCacheAlwaysRuns(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)

	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}
	step.NoCache = true

	calls := 0
	fn := func(_ context.Context) error {
		calls++
		return os.WriteFile(out, []byte("built"), 0o644)
	}

	r1, err := c.Run(context.Background(), step, fn)
	require.NoError(t, err, "first Run")
	require.False(t, r1.Hit, "first Run want miss")
	r2, err := c.Run(context.Background(), step, fn)
	require.NoError(t, err, "second Run")
	require.False(t, r2.Hit, "second Run want miss (NoCache must never replay)")
	assert.Equal(t, 2, calls, "NoCache must run every time")
}

// TestSkipReplayForcesRerunButStillSnapshots verifies the magus run --no-cache
// contract: a Step with SkipReplay=true never replays a hit (fn runs every
// time), but - unlike NoCache - it still snapshots on success, so a later
// ordinary run (SkipReplay=false) hits the refreshed entry instead of missing
// or replaying something stale.
func TestSkipReplayForcesRerunButStillSnapshots(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)

	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}
	step.SkipReplay = true

	calls := 0
	fn := func(_ context.Context) error {
		calls++
		return os.WriteFile(out, []byte("built"), 0o644)
	}

	r1, err := c.Run(context.Background(), step, fn)
	require.NoError(t, err, "first Run")
	require.False(t, r1.Hit, "first Run want miss")
	require.NotEmpty(t, r1.Hash, "SkipReplay must still snapshot: expected a hash")

	r2, err := c.Run(context.Background(), step, fn)
	require.NoError(t, err, "second Run (still --no-cache)")
	require.False(t, r2.Hit, "second Run want miss (SkipReplay must never replay)")
	assert.Equal(t, 2, calls, "SkipReplay must run every time")

	// Drop SkipReplay: an ordinary run must now hit the entry SkipReplay refreshed.
	step.SkipReplay = false
	r3, err := c.Run(context.Background(), step, fn)
	require.NoError(t, err, "third Run (ordinary)")
	assert.True(t, r3.Hit, "ordinary run must hit the entry a --no-cache run refreshed")
	assert.Equal(t, 2, calls, "hit must not call fn again")
}

// TestModeAutoWritesOnMiss verifies that the default ModeAuto writes
// a manifest on miss so the next run can hit.
func TestModeAutoWritesOnMiss(t *testing.T) {
	root := t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	// Do NOT set MAGUS_CACHE_MODE — default (ModeAuto) must write.
	c, err := Open(cdir)
	require.NoError(t, err, "cache.Open")
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}
	calls := 0
	fn := func(_ context.Context) error { calls++; return os.WriteFile(out, []byte("built"), 0o644) }

	r1, err := c.Run(context.Background(), step, fn)
	require.NoError(t, err, "Run(miss)")
	require.False(t, r1.Hit, "first Run must miss")

	// Re-open (same dir, same default mode) — must hit.
	c2, err := Open(cdir)
	require.NoError(t, err, "cache.Open(auto, second)")
	r2, err := c2.Run(context.Background(), step, fn)
	require.NoError(t, err, "Run(hit)")
	assert.True(t, r2.Hit, "ModeAuto must hit on second run")
	assert.Equal(t, 1, calls, "fn called once")
}

// TestModeAutoReplaysOnHit verifies that ModeAuto does not call fn
// when the cache already has a valid manifest for the step.
func TestModeAutoReplaysOnHit(t *testing.T) {
	root, cdir, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}
	fn := func(_ context.Context) error { return os.WriteFile(out, []byte("built"), 0o644) }

	_, err := c.Run(context.Background(), step, fn)
	require.NoError(t, err, "prime")

	// Re-open (default mutable) — must hit without calling fn.
	c2, err := Open(cdir)
	require.NoError(t, err, "cache.Open")
	calls := 0
	r, err := c2.Run(context.Background(), step, func(_ context.Context) error { calls++; return fn(context.Background()) })
	require.NoError(t, err, "Run")
	assert.True(t, r.Hit, "must replay on hit")
	assert.Equal(t, 0, calls, "fn must not run on hit")
}

// TestImmutableDoesNotWriteOnMiss verifies that a read-only cache never writes
// a manifest — a subsequent run still misses.
func TestImmutableDoesNotWriteOnMiss(t *testing.T) {
	root := t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}
	fn := func(_ context.Context) error { return os.WriteFile(out, []byte("built"), 0o644) }

	c, err := Open(cdir, WithMutable(false))
	require.NoError(t, err, "cache.Open(immutable)")
	r, err := c.Run(context.Background(), step, fn)
	require.NoError(t, err, "first Run")
	require.False(t, r.Hit, "first Run want miss")

	// Re-open mutable — must still miss (nothing was written).
	c2, err := Open(cdir)
	require.NoError(t, err, "cache.Open")
	r, err = c2.Run(context.Background(), step, fn)
	require.NoError(t, err, "second Run after immutable miss")
	assert.False(t, r.Hit, "second Run after immutable miss want miss")
}

// TestHashChangesOnSourceEdit verifies that editing a source file
// changes the hash.
func TestHashChangesOnSourceEdit(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main // v1")
	out := touchOut(t, root)
	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}
	fn := func(_ context.Context) error { return os.WriteFile(out, []byte("out"), 0o644) }

	r1, err := c.Run(context.Background(), step, fn)
	require.NoError(t, err)
	writeMain(t, root, "package main // v2")
	r2, err := c.Run(context.Background(), step, fn)
	require.NoError(t, err)
	assert.NotEqual(t, r1.Hash, r2.Hash, "hash must change after source edit")
}

// TestActionDiscriminant verifies that two steps differing only in
// Action produce different hashes.
func TestActionDiscriminant(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)

	base := makeStep(root)
	base.Outputs = []string{"test/pkg/out.txt"}
	fn := func(_ context.Context) error { return os.WriteFile(out, []byte("out"), 0o644) }

	build := base
	build.Target = "build"
	rBuild, err := c.Run(context.Background(), build, fn)
	require.NoError(t, err)
	test := base
	test.Target = "test"
	rTest, err := c.Run(context.Background(), test, fn)
	require.NoError(t, err)
	assert.NotEqual(t, rBuild.Hash, rTest.Hash, "different Target must yield different hash")
}

// TestClean verifies that Clean removes manifests, making a
// subsequent Run a miss.
func TestClean(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}
	fn := func(_ context.Context) error { return os.WriteFile(out, []byte("out"), 0o644) }

	_, err := c.Run(context.Background(), step, fn)
	require.NoError(t, err)
	require.NoError(t, c.Delete(context.Background(), "test/pkg"), "Clean")

	calls := 0
	wrapped := func(_ context.Context) error { calls++; return fn(context.Background()) }
	r, err := c.Run(context.Background(), step, wrapped)
	require.NoError(t, err)
	assert.False(t, r.Hit, "Run after Clean must miss")
	assert.Equal(t, 1, calls, "fn called once after Clean")
}

// TestStats verifies that the per-cache miss counter is bumped after
// a write-mode miss.
func TestStats(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}

	_, err := c.Run(context.Background(), step, func(_ context.Context) error {
		return os.WriteFile(out, []byte("out"), 0o644)
	})
	require.NoError(t, err)
	want := Stats{Hit: 0, Miss: 1, Error: 0}
	assert.Equal(t, want, c.Stats())
}

// TestCacheSizeCapAccepted exercises MAGUS_CACHE_SIZE parsing: any
// recognised value (or unrecognised, which falls back to "no cap")
// must let Open succeed.
func TestCacheSizeCapAccepted(t *testing.T) {
	for _, env := range []string{"", "0", "1000", "500KB", "2MB", "1GB", "bad"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv("MAGUS_CACHE_SIZE", env)
			_, err := Open(filepath.Join(t.TempDir(), ".cap"))
			assert.NoErrorf(t, err, "Open with MAGUS_CACHE_SIZE=%q", env)
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
	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}

	// The panic from fn must propagate out of Run rather than deadlock.
	assert.Panics(t, func() {
		_, _ = c.Run(context.Background(), step, func(_ context.Context) error {
			panic(errors.New("boom"))
		})
	}, "expected panic to propagate from fn")
}

// TestExportImportRoundTrip verifies that a cache snapshot exported to a
// gzip-tar and imported into a fresh cache directory produces hits on the
// same step.
func TestExportImportRoundTrip(t *testing.T) {
	root, _, src := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}

	// Populate the source cache.
	_, err := src.Run(context.Background(), step, func(_ context.Context) error {
		return os.WriteFile(out, []byte("built"), 0o644)
	})
	require.NoError(t, err, "populate")

	// Export the source cache to an in-memory buffer.
	var rawBuf bytes.Buffer
	require.NoError(t, src.Export(context.Background(), &rawBuf), "Export")

	// Import into a new cache directory (immutable so we test read-after-import).
	dstDir := filepath.Join(t.TempDir(), ".magus-dst")
	dst, err := Open(dstDir, WithMutable(false))
	require.NoError(t, err, "Open dst")
	require.NoError(t, dst.Import(context.Background(), &rawBuf), "Import")

	// Verify that the step hits in the destination cache.
	calls := 0
	r, err := dst.Run(context.Background(), step, func(_ context.Context) error { calls++; return nil })
	require.NoError(t, err, "Run after import")
	assert.True(t, r.Hit, "Run after Import must hit")
	assert.Equal(t, 0, calls, "fn must not run after Import hit")
}

// TestOnResult verifies that the OnResult callback fires exactly once
// after every Cache.Run regardless of whether the result is a hit, miss,
// or error, and that the Step and Result passed to it are consistent.
func TestOnResult(t *testing.T) {
	t.Parallel()
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}

	type call struct {
		projectPath string
		hit         bool
		err         error
	}
	var calls []call
	onResult := OnResult(func(s *Step, r *Result, err error) {
		calls = append(calls, call{s.ProjectPath, r.Hit, err})
	})

	// Miss: fn runs and writes out.txt.
	_, err := c.Run(context.Background(), step, func(_ context.Context) error {
		return os.WriteFile(out, []byte("ok"), 0o644)
	}, onResult)
	require.NoError(t, err, "Run(miss)")
	require.Len(t, calls, 1, "after miss")
	assert.Equal(t, call{"test/pkg", false, nil}, calls[0], "after miss")

	// Hit: same step, fn must not run.
	_, err = c.Run(context.Background(), step, func(_ context.Context) error {
		t.Error("fn must not run on hit")
		return nil
	}, onResult)
	require.NoError(t, err, "Run(hit)")
	require.Len(t, calls, 2, "after hit")
	assert.Equal(t, call{"test/pkg", true, nil}, calls[1], "after hit")

	// Error: fn fails; OnResult fires with the error.
	errStep := Step{
		ProjectPath:   "test/pkg/err",
		Sources:       []string{"test/pkg/*.go"},
		WorkspaceRoot: root,
	}
	wantErr := errors.New("build failed")
	_, err = c.Run(context.Background(), errStep, func(_ context.Context) error {
		return wantErr
	}, onResult)
	require.Error(t, err, "Run(error): expected error")
	require.Len(t, calls, 3, "after error")
	assert.False(t, calls[2].hit, "error result must not be a hit")
	assert.Error(t, calls[2].err, "OnResult must fire with the error")
}

// TestOnResultMultiple verifies that multiple OnResult callbacks accumulate and
// all fire, rather than the last registration clobbering the earlier ones. This
// guards the coexistence of independent result observers (report, telemetry,
// diagnostic capture), which each register their own OnResult on the same run.
func TestOnResultMultiple(t *testing.T) {
	t.Parallel()
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}

	var first, second int
	_, err := c.Run(context.Background(), step, func(_ context.Context) error {
		return os.WriteFile(out, []byte("ok"), 0o644)
	},
		OnResult(func(*Step, *Result, error) { first++ }),
		OnResult(func(*Step, *Result, error) { second++ }),
	)
	require.NoError(t, err, "Run")
	assert.Equal(t, 1, first, "first OnResult must fire")
	assert.Equal(t, 1, second, "second OnResult must fire (not clobbered by the first)")
}

// TestExportImportUnsafePath verifies that Import rejects tar entries
// that would escape the cache directory via path traversal.
func TestExportImportUnsafePath(t *testing.T) {
	dir := t.TempDir()
	c, err := Open(filepath.Join(dir, ".magus"), WithMutable(false))
	require.NoError(t, err)

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

	assert.Error(t, c.Import(context.Background(), &buf), "Import with path traversal must return error")
}

// collapseCache builds a Cache in collapse-on-success mode at default verbosity.
func collapseCache(t *testing.T) *Cache {
	t.Helper()
	return &Cache{dir: t.TempDir(), log: slog.Default(), logLevel: slog.LevelInfo, collapse: true}
}

// TestCaptureRunCollapseSuppressesOutputOnSuccess verifies a passing project's
// subprocess output is withheld rather than streamed to stderr.
func TestCaptureRunCollapseSuppressesOutputOnSuccess(t *testing.T) {
	c := collapseCache(t)
	lp := c.logPath("svc/api", "deadbeef")

	out := captureStderr(t, func() {
		_, err := c.captureRun(context.Background(), lp, "svc/api", "test", func(ctx context.Context) error {
			stdout, _ := runPkg.OutputWriters(ctx)
			fmt.Fprintln(stdout, "compiling lots of noisy output...")
			return nil
		})
		require.NoError(t, err)
	})

	assert.Empty(t, out, "collapse mode should withhold subprocess output on success")
}

// TestCaptureRunCollapseShowsFailureExcerpt verifies that on failure collapse
// displays a bounded diagnostic excerpt on stderr, while retaining the full log.
func TestCaptureRunCollapseShowsFailureExcerpt(t *testing.T) {
	c := collapseCache(t)
	lp := c.logPath("svc/api", "cafef00d")
	want := errors.New("boom")

	stderrOut := captureStderr(t, func() {
		_, err := c.captureRun(context.Background(), lp, "svc/api", "test", func(ctx context.Context) error {
			stdout, _ := runPkg.OutputWriters(ctx)
			fmt.Fprintln(stdout, "lint: undefined symbol foo")
			return want
		})
		require.ErrorIs(t, err, want)
	})

	// Header and concise diagnostic are both on the human-facing stderr stream.
	assert.Contains(t, stderrOut, "-- svc/api (failed) --")
	assert.Contains(t, stderrOut, "lint: undefined symbol foo")
	// The failure log is retained (Run persists it to the output store under a ref
	// so `magus query ref...` can replay a failing target's exact output).
	data, statErr := os.ReadFile(lp)
	require.NoError(t, statErr, "collapse failure log should be retained after replay")
	assert.Contains(t, string(data), "lint: undefined symbol foo")
}

// captureStderr redirects os.Stderr for the duration of fn and returns what was written.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

// silentCache builds a Cache in silent mode with a log dir under t.TempDir.
func silentCache(t *testing.T) *Cache {
	t.Helper()
	c := &Cache{dir: t.TempDir(), log: slog.Default(), logLevel: slog.LevelError, silent: true}
	return c
}

func TestCaptureRunSilentBubblesNotices(t *testing.T) {
	c := silentCache(t)
	lp := c.logPath("svc/api", "deadbeef")

	out := captureStderr(t, func() {
		_, err := c.captureRun(context.Background(), lp, "svc/api", "test", func(ctx context.Context) error {
			stdout, _ := runPkg.OutputWriters(ctx)
			fmt.Fprintln(stdout, "compiling...")
			fmt.Fprintln(stdout, "magus:notice: deployed api v1.2.3")
			return nil
		})
		require.NoError(t, err)
	})

	assert.Equal(t, "notice: svc/api: deployed api v1.2.3\n", out)
	// Successful-run log is retained (replayable).
	_, statErr := os.Stat(lp)
	assert.NoError(t, statErr)
}

func TestCaptureRunSilentBoundsFailureAndKeepsLog(t *testing.T) {
	c := silentCache(t)
	lp := c.logPath("svc/api", "cafef00d")
	want := errors.New("boom")

	out := captureStderr(t, func() {
		_, err := c.captureRun(context.Background(), lp, "svc/api", "test", func(ctx context.Context) error {
			stdout, _ := runPkg.OutputWriters(ctx)
			for i := 0; i < maxFailTailLines+10; i++ {
				fmt.Fprintf(stdout, "line %d\n", i)
			}
			return want
		})
		require.ErrorIs(t, err, want)
	})

	assert.Contains(t, out, "-- svc/api (failed) --")
	// Bounded AND diagnostic-focused: a plain tail of a test log is mostly
	// successful lines, so silent mode runs the same excerpt filter the default
	// display uses. The full-log path keeps everything it drops reachable.
	assert.Contains(t, out, "showing likely diagnostics; full log: "+lp)
	assert.Contains(t, out, fmt.Sprintf("line %d", maxFailTailLines+9)) // last line present
	assert.NotContains(t, out, "line 0\n")                              // earliest line trimmed
	// Failure log is retained in silent mode so the printed path resolves.
	_, statErr := os.Stat(lp)
	assert.NoError(t, statErr)
}

// In quiet-but-not-silent mode the failure output is fully dumped to stderr; the
// log is now retained (not removed) so Run can persist it under a target-output ref.
func TestCaptureRunQuietRetainsFailureLog(t *testing.T) {
	c := &Cache{dir: t.TempDir(), log: slog.Default(), logLevel: slog.LevelError}
	lp := c.logPath("svc/api", "0badf00d")
	want := errors.New("boom")

	_ = captureStderr(t, func() {
		_, err := c.captureRun(context.Background(), lp, "svc/api", "test", func(ctx context.Context) error {
			stdout, _ := runPkg.OutputWriters(ctx)
			fmt.Fprintln(stdout, "line 0")
			return want
		})
		require.ErrorIs(t, err, want)
	})

	data, statErr := os.ReadFile(lp)
	require.NoError(t, statErr, "quiet-mode failure log should be retained for the output store")
	assert.Contains(t, string(data), "line 0")
}

func TestCaptureRunSilentPassNoNoticeIsSilent(t *testing.T) {
	c := silentCache(t)
	lp := c.logPath("svc/api", "feedface")

	out := captureStderr(t, func() {
		_, err := c.captureRun(context.Background(), lp, "svc/api", "test", func(ctx context.Context) error {
			stdout, _ := runPkg.OutputWriters(ctx)
			fmt.Fprintln(stdout, "all good, nothing to report")
			return nil
		})
		require.NoError(t, err)
	})

	assert.Empty(t, out, "a passing run with no notice lines should print nothing")
}

// atomicStoreMax stores v into dst iff it is greater than the current value.
func atomicStoreMax(dst *atomic.Int32, v int32) {
	for {
		cur := dst.Load()
		if v <= cur || dst.CompareAndSwap(cur, v) {
			return
		}
	}
}

// TestRunAllSlotsThrottles verifies that a step whose Slots equals the whole
// slot budget runs alone: while it holds every slot no other step can be in fn,
// yet lighter steps still saturate the budget when the heavy one is idle.
func TestRunAllSlotsThrottles(t *testing.T) {
	root, c := openCache(t)

	var running atomic.Int32
	var duringHeavy atomic.Int32 // peak concurrency observed while the heavy step is in fn
	var lightPeak atomic.Int32   // peak concurrency observed among the light steps

	heavy := depStep(root, "heavy")
	heavy.NoCache = true
	heavy.Slots = 2 // == WithConcurrency below, so it holds every slot

	steps := []Step{heavy}
	for _, p := range []string{"l1", "l2", "l3"} {
		s := depStep(root, p)
		s.NoCache = true
		steps = append(steps, s)
	}

	fn := func(_ context.Context, s Step) error {
		cur := running.Add(1)
		defer running.Add(-1)
		if s.ProjectPath == "heavy" {
			time.Sleep(30 * time.Millisecond)
			atomicStoreMax(&duringHeavy, running.Load())
			return nil
		}
		atomicStoreMax(&lightPeak, cur)
		time.Sleep(15 * time.Millisecond)
		return nil
	}

	_, err := c.RunAll(context.Background(), steps, fn, WithConcurrency(2))
	require.NoError(t, err, "RunAll")

	assert.Equal(t, int32(1), duringHeavy.Load(), "no step may run while a slots==budget step holds every slot")
	assert.Equal(t, int32(2), lightPeak.Load(), "light steps should saturate the 2-slot budget when the heavy step is idle")
}

// TestRunAllSlotsHandbackNoDeadlock guards against a multi-slot step self-deadlocking
// when its fn hands back its build slot to reserve internally-parallel slots
// (the os.with_slots / archive.* pattern). A step holding N slots must hand back
// *all* N, not one, or an AcquireN inside fn blocks forever on slots the step
// itself is pinning.
func TestRunAllSlotsHandbackNoDeadlock(t *testing.T) {
	root, c := openCache(t)

	heavy := depStep(root, "heavy")
	heavy.NoCache = true
	heavy.Slots = 2 // holds the whole 2-slot budget

	fn := func(ctx context.Context, _ Step) error {
		lim := LimiterFromContext(ctx)
		if lim == nil {
			return nil
		}
		// Mirror OsWithSlots: hand back the slots we hold, then reserve `threads`.
		// With the whole budget held, reserving 2 can only succeed if the handback
		// released both of our slots.
		held := SlotsHeld(ctx)
		if held > 0 {
			lim.ReleaseN(held)
			defer func() { _ = lim.AcquireN(context.WithoutCancel(ctx), held) }()
		}
		if err := lim.AcquireN(ctx, 2); err != nil {
			return err
		}
		defer lim.ReleaseN(2)
		return nil
	}

	done := make(chan error, 1)
	go func() {
		_, err := c.RunAll(context.Background(), []Step{heavy}, fn, WithConcurrency(2))
		done <- err
	}()

	select {
	case err := <-done:
		require.NoError(t, err, "RunAll")
	case <-time.After(5 * time.Second):
		t.Fatal("weighted step deadlocked handing back its slot to reserve more")
	}
}
