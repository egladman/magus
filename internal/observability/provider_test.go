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
	assert.Equal(t, "grpc", got.Protocol)
	assert.Equal(t, "magus", got.ServiceName)
	assert.Equal(t, 1.0, got.SampleRatio)
	assert.Equal(t, "v1.2.3", got.ServiceVersion)
}

// recorder implements observability.Provider and captures every call so
// tests can assert the cache-hook closures fire.
type recorder struct {
	hits      []recCall
	misses    []recCall
	errs      []recCall
	durs      []recDur
	remoteOps []RemoteOp
	spans     []string // names of spans started, in order
	stops     int
}

type recCall struct {
	ctx   context.Context
	attrs []Attr
}

type recDur struct {
	ctx   context.Context
	secs  float64
	attrs []Attr
}

func (r *recorder) Enabled() bool { return true }
func (r *recorder) RecordCacheHit(ctx context.Context, attrs ...Attr) {
	r.hits = append(r.hits, recCall{ctx, attrs})
}

func (r *recorder) RecordCacheMiss(ctx context.Context, attrs ...Attr) {
	r.misses = append(r.misses, recCall{ctx, attrs})
}

func (r *recorder) RecordCacheError(ctx context.Context, attrs ...Attr) {
	r.errs = append(r.errs, recCall{ctx, attrs})
}

func (r *recorder) RecordCacheDuration(ctx context.Context, secs float64, attrs ...Attr) {
	r.durs = append(r.durs, recDur{ctx, secs, attrs})
}

func (r *recorder) RecordGraphQuery(_ context.Context, _ float64, _ ...Attr) {}

func (r *recorder) RecordRemoteOp(_ context.Context, op RemoteOp) {
	r.remoteOps = append(r.remoteOps, op)
}

func (r *recorder) StartSpan(ctx context.Context, name string, _ ...Attr) (context.Context, func(error)) {
	r.spans = append(r.spans, name)
	return ctx, func(error) {}
}

func (r *recorder) RecordTargetRun(_ context.Context, _ float64, _ ...Attr) {}

func (r *recorder) RecordPoolAcquire(_ context.Context, _ float64, _ int64) {}

func (r *recorder) RecordPoolRelease(_ context.Context, _ int64) {}

func (r *recorder) RecordPoolWaiting(_ context.Context, _ int64) {}

func (r *recorder) RecordMCPCall(_ context.Context, _ MCPCall) {}

func (r *recorder) RecordSandboxApply(_ context.Context, _ float64, _, _ string) {}

func (r *recorder) RecordSandboxRules(_ context.Context, _ SandboxRules) {}

func (r *recorder) RecordSandboxCheck(_ context.Context, _, _, _ string) {}

func (r *recorder) RecordSandboxEnvDropped(_ context.Context, _ string, _ int64) {}

func (r *recorder) RecordBuzzExec(_ context.Context, _ float64, _, _ string) {}

func (r *recorder) RecordBuzzCompile(_ context.Context, _ float64, _, _ string) {}

func (r *recorder) RecordBuzzHostCall(_ context.Context, _ BuzzHostCall) {}

func (r *recorder) RecordBuzzSessionReuse(_ context.Context, _ string) {}

func (r *recorder) RecordBuzzSessionIdle(_ context.Context, _ int64) {}

func (r *recorder) RecordBuzzSessionEviction(_ context.Context, _ string) {}

func (r *recorder) RecordBuzzSessionWarm(_ context.Context, _ float64, _ string) {}

func (r *recorder) RecordBuzzImport(_ context.Context, _ float64, _, _ string) {}

func (r *recorder) RecordBuzzSpellResolve(_ context.Context, _ float64, _, _ string) {}

func (r *recorder) RecordBuzzSpellBuiltinsWarm(_ context.Context, _ float64, _ string) {}

func (r *recorder) RecordBuzzJITRun(_ context.Context) {}

func (r *recorder) RecordBuzzVMFault(_ context.Context, _ string) {}

func (r *recorder) Snapshot(_ context.Context) ([]byte, error) { return nil, nil }

func (r *recorder) Shutdown(_ context.Context) error { r.stops++; return nil }

// newCache opens a fresh write-mode cache rooted in t.TempDir.
func newCache(t *testing.T) (root string, c *cache.Cache) {
	t.Helper()
	root = t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	t.Setenv("MAGUS_CACHE_MODE", "auto")
	c, err := cache.Open(cdir)
	require.NoError(t, err, "cache.Open")
	return root, c
}

// TestCacheRunOptions_HitAndMissFireProviderHooks drives the closures
// returned by CacheRunOptions through a real cache.Cache: the first
// Run is a miss, the second is a hit, and we assert the
// recorder observed both with the right attributes. Also
// covers OnError via a third Run whose fn fails.
func TestCacheRunOptions_HitAndMissFireProviderHooks(t *testing.T) {
	root, c := newCache(t)
	srcDir := filepath.Join(root, "p")
	require.NoError(t, os.MkdirAll(srcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package p"), 0o644))
	outPath := filepath.Join(srcDir, "out.txt")
	spec := cache.Step{
		ProjectPath:   "p",
		Sources:       []string{"p/*.go"},
		Outputs:       []string{"p/out.txt"},
		WorkspaceRoot: root,
	}

	rec := &recorder{}
	opts := CacheRunOptions(context.Background(), rec)
	require.Len(t, opts, 3, "CacheRunOptions returned wrong number of options")

	// Miss: fn runs, OnMiss + RecordCacheDuration fire.
	r1, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(outPath, []byte("ok"), 0o644)
	}, opts...)
	require.NoError(t, err, "Run(miss)")
	assert.False(t, r1.Hit, "first Run should miss")
	assert.Len(t, rec.misses, 1, "after miss")
	assert.Len(t, rec.durs, 1, "after miss")
	require.NotEmpty(t, rec.misses[0].attrs)
	assert.Equal(t, "outcome", rec.misses[0].attrs[0].Key)
	assert.Equal(t, "miss", rec.misses[0].attrs[0].Value)

	// Hit: identical Run, fn not called, OnHit + duration fire.
	r2, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		t.Error("fn should not run on a hit")
		return nil
	}, opts...)
	require.NoError(t, err, "Run(hit)")
	require.True(t, r2.Hit, "second Run should hit; r2 = %+v", r2)
	assert.Len(t, rec.hits, 1, "after hit")
	assert.Len(t, rec.durs, 2, "after hit")
}

// TestMetricRecordNoProjectAttr ensures CacheRunOptions never stamps a
// "project" key on any metric attribute. Per-project cardinality is
// unbounded and must be omitted from metric dimensions.
func TestMetricRecordNoProjectAttr(t *testing.T) {
	root, c := newCache(t)
	srcDir := filepath.Join(root, "p")
	require.NoError(t, os.MkdirAll(srcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package p"), 0o644))
	outPath := filepath.Join(srcDir, "out.txt")
	spec := cache.Step{
		ProjectPath:   "p",
		Sources:       []string{"p/*.go"},
		Outputs:       []string{"p/out.txt"},
		WorkspaceRoot: root,
	}

	rec := &recorder{}
	opts := CacheRunOptions(context.Background(), rec)

	// Miss
	_, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(outPath, []byte("ok"), 0o644)
	}, opts...)
	require.NoError(t, err, "Run(miss)")
	// Hit
	_, err = c.Run(context.Background(), spec, func(_ context.Context) error {
		return nil
	}, opts...)
	require.NoError(t, err, "Run(hit)")

	for _, call := range rec.hits {
		for _, attr := range call.attrs {
			assert.NotEqual(t, "project", attr.Key, "hit metric attrs must not contain per-project cardinality")
		}
	}
	for _, call := range rec.misses {
		for _, attr := range call.attrs {
			assert.NotEqual(t, "project", attr.Key, "miss metric attrs must not contain per-project cardinality")
		}
	}
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
		Sources:       []string{"p/*.go"},
		Outputs:       []string{"p/out.txt"},
		WorkspaceRoot: root,
		Target:        "test",
	}

	rec := &recordingTargetProvider{}
	spellsOf := func(projectPath string) []string { return []string{"go"} }
	opts := TargetRunOptions(context.Background(), rec, spellsOf)

	// Miss: outcome=success, cache.hit=false.
	_, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(outPath, []byte("ok"), 0o644)
	}, opts...)
	require.NoError(t, err, "Run(miss)")
	require.Len(t, rec.targetRuns, 1, "after miss")
	r0 := rec.targetRuns[0]
	assert.Equal(t, "success", attrVal(r0.attrs, "outcome"), "miss outcome")
	assert.Equal(t, "false", attrVal(r0.attrs, "cache.hit"), "miss cache.hit")
	assert.Equal(t, "test", attrVal(r0.attrs, "magus.target"), "miss target")
	assert.Equal(t, "go", attrVal(r0.attrs, "magus.spell"), "miss spell")

	// Hit: outcome=success, cache.hit=true.
	_, err = c.Run(context.Background(), spec, func(_ context.Context) error {
		t.Error("fn must not run on hit")
		return nil
	}, opts...)
	require.NoError(t, err, "Run(hit)")
	require.Len(t, rec.targetRuns, 2, "after hit")
	r1 := rec.targetRuns[1]
	assert.Equal(t, "true", attrVal(r1.attrs, "cache.hit"), "hit cache.hit")
	assert.Equal(t, "success", attrVal(r1.attrs, "outcome"), "hit outcome")

	// Multi-spell: two spells → two rows per run.
	multiSpellsOf := func(string) []string { return []string{"go", "typescript"} }
	multiOpts := TargetRunOptions(context.Background(), rec, multiSpellsOf)
	multiStep := cache.Step{
		ProjectPath:   "q",
		Sources:       []string{"p/*.go"},
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

// graphRecorder captures RecordGraphQuery calls so the otel graph observer's
// attribute mapping can be asserted. It embeds Provider so only the two methods
// the observer touches need bodies.
type graphRecorder struct {
	Provider
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

func TestOtelGraphObserver_OnBuildOnQueryOnError(t *testing.T) {
	t.Parallel()
	rec := &graphRecorder{enabled: true}
	obs := GraphObserver(context.Background(), rec)

	obs.OnBuild(types.BuildStats{Duration: 2 * time.Second})
	obs.OnQuery(types.QueryEvent{Op: "path", Strategy: "bfs", Duration: 500 * time.Millisecond})
	obs.OnQuery(types.QueryEvent{Op: "stats", Duration: time.Second}) // no strategy: attr omitted
	obs.OnError(assert.AnError)                                       // no-op, must not panic or record

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
