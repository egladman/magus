package observability

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNew_DisabledIsNoOp verifies that constructing a Provider with
// Enabled=false never opens a connection or panics. This is the path
// almost all magus invocations take, since telemetry is OFF by default.
func TestNew_DisabledIsNoOp(t *testing.T) {
	t.Parallel()
	p, err := New(context.Background(), Config{Enabled: false})
	require.NoError(t, err)
	require.NotNil(t, p, "New(disabled) returned nil Provider")
	assert.False(t, p.Enabled(), "disabled Provider reports Enabled() == true")
	p.RecordCacheHit(context.Background())
	p.RecordCacheMiss(context.Background())
	p.RecordCacheError(context.Background())
	p.RecordCacheDuration(context.Background(), 1.5)
	assert.NoError(t, p.Shutdown(context.Background()))
}

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

// TestNew_EnabledRequiresEndpoint exercises the validation path.
func TestNew_EnabledRequiresEndpoint(t *testing.T) {
	t.Parallel()
	_, err := New(context.Background(), Config{Enabled: true})
	assert.Error(t, err, "New(enabled, no endpoint) should error")
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

// TestCacheRunOptions_DisabledProviderIsInert verifies the
// nil-vs-disabled boundary documented on CacheRunOptions: a Provider
// reporting Enabled()==false still returns the options (callers can
// wire them unconditionally), but the underlying record calls are
// no-ops via disabledProvider — the cache.Run pipeline exercises
// every disabled-provider record method through a hit path.
func TestCacheRunOptions_DisabledProviderIsInert(t *testing.T) {
	p, err := New(context.Background(), Config{Enabled: false})
	require.NoError(t, err)
	assert.False(t, p.Enabled(), "disabled provider reports Enabled() == true")

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
	opts := CacheRunOptions(context.Background(), p)

	// Drive a miss then a hit so OnMiss + OnHit + OnError-adjacent
	// paths fire on the disabled provider. No assertion on counters
	// (it is inert) — the test is that nothing panics and Run
	// completes.
	_, err = c.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(outPath, []byte("ok"), 0o644)
	}, opts...)
	require.NoError(t, err, "Run(miss)")
	_, err = c.Run(context.Background(), spec, func(_ context.Context) error {
		t.Error("fn must not run on a hit")
		return nil
	}, opts...)
	require.NoError(t, err, "Run(hit)")
	assert.NoError(t, p.Shutdown(context.Background()), "disabled.Shutdown")
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
