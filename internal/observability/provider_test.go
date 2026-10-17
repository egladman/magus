package observability

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCacheRunOptions_NilProviderReturnsNil ensures callers can pass a
// nil Provider through without crashing — a common pattern when telemetry
// init failed earlier in the boot path.
func TestCacheRunOptions_NilProviderReturnsNil(t *testing.T) {
	t.Parallel()
	assert.Nil(t, CacheRunOptions(context.Background(), nil))
}

// TestConfigFromTelemetry_AppliesFallbacks verifies that an empty
// Telemetry is filled in with sensible defaults so callers don't
// need to re-implement that logic at every wiring point.
func TestConfigFromTelemetry_AppliesFallbacks(t *testing.T) {
	t.Parallel()
	got := ConfigFromTelemetry(config.Telemetry{}, "v1.2.3", "")
	assert.Equal(t, Config{Protocol: "grpc", ServiceName: "magus", SampleRatio: 1.0, ServiceVersion: "v1.2.3"}, got)
}

// recorder captures the calls the cache options, remote backend and span tests assert;
// everything else falls through to Nop.
type recorder struct {
	Nop
	reads     []recRead
	durs      []recDur
	remoteOps []RemoteOp
	saved     []float64 // seconds passed to RecordCacheSaved, in order
	spans     []string  // names of spans started, in order
	stops     int
}

type recRead struct {
	result CacheResult
	attrs  []Attr
}

type recDur struct {
	secs   float64
	result CacheResult
}

func (r *recorder) Enabled() bool { return true }

func (r *recorder) RecordCacheRead(_ context.Context, result CacheResult, attrs ...Attr) {
	r.reads = append(r.reads, recRead{result, attrs})
}

func (r *recorder) RecordCacheDuration(_ context.Context, secs float64, result CacheResult) {
	r.durs = append(r.durs, recDur{secs, result})
}

func (r *recorder) RecordCacheSaved(_ context.Context, secs float64) {
	r.saved = append(r.saved, secs)
}

func (r *recorder) RecordRemoteOp(_ context.Context, op RemoteOp) {
	r.remoteOps = append(r.remoteOps, op)
}

func (r *recorder) StartSpan(ctx context.Context, name string, _ ...Attr) (context.Context, func(error)) {
	r.spans = append(r.spans, name)
	return ctx, func(error) {}
}

func (r *recorder) Shutdown(_ context.Context) error { r.stops++; return nil }

func (r *recorder) results() []CacheResult {
	out := make([]CacheResult, len(r.reads))
	for i, read := range r.reads {
		out[i] = read.result
	}
	return out
}

// newCache opens a fresh write-mode cache rooted in t.TempDir.
func newCache(t *testing.T) (root string, c *cache.Cache) {
	t.Helper()
	root = t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	// Mutable EXPLICITLY, never inherited: MAGUS_CACHE_WRITE_ENABLED is ambient in CI
	// (ci.yaml sets it false on every pull_request event), and a write-disabled cache writes no
	// entry, so a test that runs a step twice and asserts the second is a hit fails on
	// a PR run while passing everywhere else. WithLocalWrite is how internal/cache's own
	// newMutableCache fixture pins this.
	c, err := cache.Open(t.Context(), cdir, cache.WithLocalWrite(true))
	require.NoError(t, err, "cache.Open")
	return root, c
}

// TestCacheRunOptions_RecordsEachResult drives the option CacheRunOptions returns through a
// real cache.Cache: a miss, a hit, then a failing run, each recorded as one cache read with
// its result. Only the hit and the miss carry a duration.
func TestCacheRunOptions_RecordsEachResult(t *testing.T) {
	root, c := newCache(t)
	srcDir := filepath.Join(root, "p")
	require.NoError(t, os.MkdirAll(srcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package p"), 0o644))
	outPath := filepath.Join(srcDir, "out.txt")
	spec := cache.Step{
		ProjectPath:   "p",
		Sources:       types.MustParseGlobs("p/*.go"),
		Outputs:       types.MustParseGlobs("p/out.txt"),
		WorkspaceRoot: root,
	}

	rec := &recorder{}
	opts := CacheRunOptions(context.Background(), rec)
	require.Len(t, opts, 1, "one option reports every result")

	r1, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(outPath, []byte("ok"), 0o644)
	}, opts...)
	require.NoError(t, err, "Run(miss)")
	assert.False(t, r1.Hit, "first Run should miss")
	assert.Equal(t, []CacheResult{CacheMiss}, rec.results())
	assert.Empty(t, rec.saved, "a miss ran the work, so it saved nothing")

	r2, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		t.Error("fn should not run on a hit")
		return nil
	}, opts...)
	require.NoError(t, err, "Run(hit)")
	require.True(t, r2.Hit, "second Run should hit; r2 = %+v", r2)
	assert.Equal(t, []CacheResult{CacheMiss, CacheHit}, rec.results())

	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package q"), 0o644))
	_, err = c.Run(context.Background(), spec, func(_ context.Context) error {
		return assert.AnError
	}, opts...)
	require.Error(t, err, "Run(error)")
	assert.Equal(t, []CacheResult{CacheMiss, CacheHit, CacheError}, rec.results())

	require.Len(t, rec.durs, 2, "a failed run records no duration")
	assert.Equal(t, CacheMiss, rec.durs[0].result)
	assert.Equal(t, CacheHit, rec.durs[1].result)
	for _, read := range rec.reads {
		assert.Empty(t, read.attrs, "the result is the read's only attribute")
	}
}

// TestCacheRunOptions_HitRecordsSavedTime is the end-to-end guard on the savings lens: the
// value has to travel manifest -> cache.Result.Saved -> RecordCacheSaved, and a break anywhere
// on that path leaves an instrument that registers and can never fire.
//
// The producing run sleeps past a millisecond deliberately. Manifest.DurationMs has millisecond
// resolution, so a target that finishes inside one records zero saved and CacheRunOptions skips
// it, which would make this assertion pass or fail on timing rather than on the wiring.
func TestCacheRunOptions_HitRecordsSavedTime(t *testing.T) {
	root, c := newCache(t)
	srcDir := filepath.Join(root, "p")
	require.NoError(t, os.MkdirAll(srcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package p"), 0o644))
	outPath := filepath.Join(srcDir, "out.txt")
	spec := cache.Step{
		ProjectPath:   "p",
		Sources:       types.MustParseGlobs("p/*.go"),
		Outputs:       types.MustParseGlobs("p/out.txt"),
		WorkspaceRoot: root,
	}

	rec := &recorder{}
	opts := CacheRunOptions(context.Background(), rec)

	_, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		time.Sleep(5 * time.Millisecond)
		return os.WriteFile(outPath, []byte("ok"), 0o644)
	}, opts...)
	require.NoError(t, err, "Run(miss)")

	r2, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		t.Error("fn should not run on a hit")
		return nil
	}, opts...)
	require.NoError(t, err, "Run(hit)")
	require.True(t, r2.Hit)
	require.Positive(t, r2.Saved, "the hit must carry what the entry recorded")

	require.Len(t, rec.saved, 1, "one observation per hit")
	assert.InDelta(t, r2.Saved.Seconds(), rec.saved[0], 1e-9)
}

// recordingTargetProvider extends recorder to capture
// RecordTargetRun calls for TargetRunOptions tests.
type recordingTargetProvider struct {
	recorder
	targetRuns []recTargetRun
}

type recTargetRun struct {
	secs  float64
	attrs []Attr
}

func (r *recordingTargetProvider) RecordTargetRun(ctx context.Context, secs float64, attrs ...Attr) {
	r.targetRuns = append(r.targetRuns, recTargetRun{secs, attrs})
}

func attrVal(attrs []Attr, key string) string {
	for _, a := range attrs {
		if a.Key == key {
			return a.Value
		}
	}
	return ""
}

// TestTargetRunOptions verifies that TargetRunOptions fires once per spell
// per run, with the correct outcome and cache.hit attributes.
func TestTargetRunOptions(t *testing.T) {
	root, c := newCache(t)
	srcDir := filepath.Join(root, "p")
	require.NoError(t, os.MkdirAll(srcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package p"), 0o644))
	outPath := filepath.Join(srcDir, "out.txt")
	spec := cache.Step{
		ProjectPath:   "p",
		Sources:       types.MustParseGlobs("p/*.go"),
		Outputs:       types.MustParseGlobs("p/out.txt"),
		WorkspaceRoot: root,
		Target:        "test",
	}

	rec := &recordingTargetProvider{}
	spellsOf := func(projectPath string) []string { return []string{"go"} }
	opts := TargetRunOptions(context.Background(), rec, spellsOf)

	// Miss: outcome=ok, cache.hit=false.
	_, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(outPath, []byte("ok"), 0o644)
	}, opts...)
	require.NoError(t, err, "Run(miss)")
	require.Len(t, rec.targetRuns, 1, "after miss")
	r0 := rec.targetRuns[0]
	assert.Equal(t, "ok", attrVal(r0.attrs, "outcome"), "miss outcome")
	assert.Equal(t, "false", attrVal(r0.attrs, "cache.hit"), "miss cache.hit")
	assert.Equal(t, "test", attrVal(r0.attrs, "magus.target"), "miss target")
	assert.Equal(t, "go", attrVal(r0.attrs, "magus.spell"), "miss spell")

	// Hit: outcome=ok, cache.hit=true.
	_, err = c.Run(context.Background(), spec, func(_ context.Context) error {
		t.Error("fn must not run on hit")
		return nil
	}, opts...)
	require.NoError(t, err, "Run(hit)")
	require.Len(t, rec.targetRuns, 2, "after hit")
	r1 := rec.targetRuns[1]
	assert.Equal(t, "true", attrVal(r1.attrs, "cache.hit"), "hit cache.hit")
	assert.Equal(t, "ok", attrVal(r1.attrs, "outcome"), "hit outcome")

	// Multi-spell: two spells → two rows per run.
	multiSpellsOf := func(string) []string { return []string{"go", "typescript"} }
	multiOpts := TargetRunOptions(context.Background(), rec, multiSpellsOf)
	multiStep := cache.Step{
		ProjectPath:   "q",
		Sources:       types.MustParseGlobs("p/*.go"),
		WorkspaceRoot: root,
		Target:        "build",
	}
	before := len(rec.targetRuns)
	_, err = c.Run(context.Background(), multiStep, func(_ context.Context) error {
		return nil
	}, multiOpts...)
	require.NoError(t, err, "Run(multi-spell)")
	assert.Equal(t, 2, len(rec.targetRuns)-before, "multi-spell should emit 2 rows")
}

// graphRecorder captures RecordGraphQuery calls so the otel graph observer's attribute
// mapping can be asserted.
type graphRecorder struct {
	Nop
	enabled bool
	calls   []graphCall
}

type graphCall struct {
	secs  float64
	attrs []Attr
}

func (g *graphRecorder) Enabled() bool { return g.enabled }
func (g *graphRecorder) RecordGraphQuery(_ context.Context, secs float64, attrs ...Attr) {
	g.calls = append(g.calls, graphCall{secs, attrs})
}

func TestGraphObserver_NilOrDisabledIsNoop(t *testing.T) {
	t.Parallel()
	assert.IsType(t, types.NoopObserver{}, GraphObserver(context.Background(), nil))
	assert.IsType(t, types.NoopObserver{}, GraphObserver(context.Background(), &graphRecorder{enabled: false}))
	// An enabled provider yields the real otel-backed observer.
	assert.IsType(t, &otelGraphObserver{}, GraphObserver(context.Background(), &graphRecorder{enabled: true}))
}

func TestOtelGraphObserver_Observe(t *testing.T) {
	t.Parallel()
	rec := &graphRecorder{enabled: true}
	obs := GraphObserver(context.Background(), rec)

	obs.Observe(types.GraphEvent{Op: types.GraphOpBuild, Duration: 2 * time.Second})
	obs.Observe(types.GraphEvent{Op: "path", Strategy: "bfs", Duration: 500 * time.Millisecond})
	// No strategy: the attr is omitted.
	obs.Observe(types.GraphEvent{Op: "stats", Duration: time.Second})
	// A failure records no point.
	obs.Observe(types.GraphEvent{Op: types.GraphOpBuild, Err: assert.AnError})

	require.Len(t, rec.calls, 3)

	assert.InDelta(t, 2.0, rec.calls[0].secs, 1e-9)
	assert.Equal(t, []Attr{{Key: "op", Value: "build"}}, rec.calls[0].attrs)

	assert.InDelta(t, 0.5, rec.calls[1].secs, 1e-9)
	assert.Equal(t, []Attr{{Key: "op", Value: "path"}, {Key: "strategy", Value: "bfs"}}, rec.calls[1].attrs)

	assert.Equal(t, []Attr{{Key: "op", Value: "stats"}}, rec.calls[2].attrs) // strategy dropped when empty
}

func TestWithProviderAndFromContext(t *testing.T) {
	t.Parallel()
	assert.Nil(t, FromContext(context.Background())) // nothing stored

	rec := &graphRecorder{enabled: true}
	ctx := WithProvider(context.Background(), rec)
	assert.Same(t, rec, FromContext(ctx))
}
