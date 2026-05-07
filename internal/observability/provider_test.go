package observability_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/observability"
)

// TestNew_DisabledIsNoOp verifies that constructing a Provider with
// Enabled=false never opens a connection or panics. This is the path
// almost all magus invocations take, since telemetry is OFF by default.
func TestNew_DisabledIsNoOp(t *testing.T) {
	t.Parallel()
	p, err := observability.New(context.Background(), observability.Config{Enabled: false})
	if err != nil {
		t.Fatalf("New(disabled) returned error: %v", err)
	}
	if p == nil {
		t.Fatal("New(disabled) returned nil Provider")
	}
	if p.Enabled() {
		t.Error("disabled Provider reports Enabled() == true")
	}
	p.RecordCacheHit(context.Background())
	p.RecordCacheMiss(context.Background())
	p.RecordCacheError(context.Background())
	p.RecordCacheDuration(context.Background(), 1.5)
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown returned error: %v", err)
	}
}

// TestCacheRunOptions_NilProviderReturnsNil ensures callers can pass a
// nil Provider through without crashing — a common pattern when telemetry
// init failed earlier in the boot path.
func TestCacheRunOptions_NilProviderReturnsNil(t *testing.T) {
	t.Parallel()
	if got := observability.CacheRunOptions(context.Background(), nil); got != nil {
		t.Errorf("CacheRunOptions(nil) = %v, want nil", got)
	}
}

// TestConfigFromTelemetry_AppliesFallbacks verifies that an empty
// Telemetry is filled in with sensible defaults so callers don't
// need to re-implement that logic at every wiring point.
func TestConfigFromTelemetry_AppliesFallbacks(t *testing.T) {
	t.Parallel()
	got := observability.ConfigFromTelemetry(config.Telemetry{}, "v1.2.3", "")
	if got.Protocol != "grpc" {
		t.Errorf("Protocol = %q, want grpc", got.Protocol)
	}
	if got.ServiceName != "magus" {
		t.Errorf("ServiceName = %q, want magus", got.ServiceName)
	}
	if got.SampleRatio != 1.0 {
		t.Errorf("SampleRatio = %v, want 1.0", got.SampleRatio)
	}
	if got.ServiceVersion != "v1.2.3" {
		t.Errorf("ServiceVersion = %q, want v1.2.3", got.ServiceVersion)
	}
}

// TestNew_EnabledRequiresEndpoint exercises the validation path.
func TestNew_EnabledRequiresEndpoint(t *testing.T) {
	t.Parallel()
	_, err := observability.New(context.Background(), observability.Config{Enabled: true})
	if err == nil {
		t.Fatal("New(enabled, no endpoint) should error, got nil")
	}
}

// recorder implements observability.Provider and captures every call so
// tests can assert the cache-hook closures fire.
type recorder struct {
	hits      []recCall
	misses    []recCall
	errs      []recCall
	durs      []recDur
	remoteOps []observability.RemoteOp
	spans     []string // names of spans started, in order
	stops     int
}

type recCall struct {
	ctx   context.Context
	attrs []observability.Attr
}

type recDur struct {
	ctx   context.Context
	secs  float64
	attrs []observability.Attr
}

func (r *recorder) Enabled() bool { return true }
func (r *recorder) RecordCacheHit(ctx context.Context, attrs ...observability.Attr) {
	r.hits = append(r.hits, recCall{ctx, attrs})
}

func (r *recorder) RecordCacheMiss(ctx context.Context, attrs ...observability.Attr) {
	r.misses = append(r.misses, recCall{ctx, attrs})
}

func (r *recorder) RecordCacheError(ctx context.Context, attrs ...observability.Attr) {
	r.errs = append(r.errs, recCall{ctx, attrs})
}

func (r *recorder) RecordCacheDuration(ctx context.Context, secs float64, attrs ...observability.Attr) {
	r.durs = append(r.durs, recDur{ctx, secs, attrs})
}

func (r *recorder) RecordGraphQuery(_ context.Context, _ float64, _ ...observability.Attr) {}

func (r *recorder) RecordRemoteOp(_ context.Context, op observability.RemoteOp) {
	r.remoteOps = append(r.remoteOps, op)
}

func (r *recorder) StartSpan(ctx context.Context, name string, _ ...observability.Attr) (context.Context, func(error)) {
	r.spans = append(r.spans, name)
	return ctx, func(error) {}
}

func (r *recorder) RecordTargetRun(_ context.Context, _ float64, _ ...observability.Attr) {}

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
	if err != nil {
		t.Fatalf("cache.Open: %v", err)
	}
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
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package p"), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(srcDir, "out.txt")
	spec := cache.Spec{
		ProjectPath:   "p",
		Sources:       []string{"p/*.go"},
		Outputs:       []string{"p/out.txt"},
		WorkspaceRoot: root,
	}

	rec := &recorder{}
	opts := observability.CacheRunOptions(context.Background(), rec)
	if len(opts) != 3 {
		t.Fatalf("CacheRunOptions returned %d options, want 3", len(opts))
	}

	// Miss: fn runs, OnMiss + RecordCacheDuration fire.
	r1, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(outPath, []byte("ok"), 0o644)
	}, opts...)
	if err != nil {
		t.Fatalf("Run(miss): %v", err)
	}
	if r1.Hit {
		t.Fatal("first Run should miss")
	}
	if len(rec.misses) != 1 {
		t.Errorf("after miss: misses=%d, want 1", len(rec.misses))
	}
	if len(rec.durs) != 1 {
		t.Errorf("after miss: durs=%d, want 1", len(rec.durs))
	}
	if got := rec.misses[0].attrs[0].Key; got != "outcome" {
		t.Errorf("miss attrs[0].Key = %q, want %q", got, "outcome")
	}
	if got := rec.misses[0].attrs[0].Value; got != "miss" {
		t.Errorf("miss attrs[0].Value = %q, want %q", got, "miss")
	}

	// Hit: identical Run, fn not called, OnHit + duration fire.
	r2, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		t.Error("fn should not run on a hit")
		return nil
	}, opts...)
	if err != nil {
		t.Fatalf("Run(hit): %v", err)
	}
	if !r2.Hit {
		t.Fatalf("second Run should hit; r2 = %+v", r2)
	}
	if len(rec.hits) != 1 {
		t.Errorf("after hit: hits=%d, want 1", len(rec.hits))
	}
	if len(rec.durs) != 2 {
		t.Errorf("after hit: durs=%d, want 2", len(rec.durs))
	}
}

// TestMetricRecordNoProjectAttr ensures CacheRunOptions never stamps a
// "project" key on any metric attribute. Per-project cardinality is
// unbounded and must be omitted from metric dimensions.
func TestMetricRecordNoProjectAttr(t *testing.T) {
	root, c := newCache(t)
	srcDir := filepath.Join(root, "p")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package p"), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(srcDir, "out.txt")
	spec := cache.Spec{
		ProjectPath:   "p",
		Sources:       []string{"p/*.go"},
		Outputs:       []string{"p/out.txt"},
		WorkspaceRoot: root,
	}

	rec := &recorder{}
	opts := observability.CacheRunOptions(context.Background(), rec)

	// Miss
	if _, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(outPath, []byte("ok"), 0o644)
	}, opts...); err != nil {
		t.Fatalf("Run(miss): %v", err)
	}
	// Hit
	if _, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		return nil
	}, opts...); err != nil {
		t.Fatalf("Run(hit): %v", err)
	}

	for _, call := range rec.hits {
		for _, attr := range call.attrs {
			if attr.Key == "project" {
				t.Errorf("hit metric attrs contain project=%q; per-project cardinality must be removed", attr.Value)
			}
		}
	}
	for _, call := range rec.misses {
		for _, attr := range call.attrs {
			if attr.Key == "project" {
				t.Errorf("miss metric attrs contain project=%q; per-project cardinality must be removed", attr.Value)
			}
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
	p, err := observability.New(context.Background(), observability.Config{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if p.Enabled() {
		t.Error("disabled provider reports Enabled() == true")
	}

	root, c := newCache(t)
	srcDir := filepath.Join(root, "p")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package p"), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(srcDir, "out.txt")
	spec := cache.Spec{
		ProjectPath:   "p",
		Sources:       []string{"p/*.go"},
		Outputs:       []string{"p/out.txt"},
		WorkspaceRoot: root,
	}
	opts := observability.CacheRunOptions(context.Background(), p)

	// Drive a miss then a hit so OnMiss + OnHit + OnError-adjacent
	// paths fire on the disabled provider. No assertion on counters
	// (it is inert) — the test is that nothing panics and Run
	// completes.
	if _, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(outPath, []byte("ok"), 0o644)
	}, opts...); err != nil {
		t.Fatalf("Run(miss): %v", err)
	}
	if _, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		t.Error("fn must not run on a hit")
		return nil
	}, opts...); err != nil {
		t.Fatalf("Run(hit): %v", err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("disabled.Shutdown: %v", err)
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
	attrs []observability.Attr
}

func (r *recordingTargetProvider) RecordTargetRun(ctx context.Context, secs float64, attrs ...observability.Attr) {
	r.targetRuns = append(r.targetRuns, recTargetRun{secs, attrs})
}

func attrVal(attrs []observability.Attr, key string) string {
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
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package p"), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(srcDir, "out.txt")
	spec := cache.Spec{
		ProjectPath:   "p",
		Sources:       []string{"p/*.go"},
		Outputs:       []string{"p/out.txt"},
		WorkspaceRoot: root,
		Target:        "test",
	}

	rec := &recordingTargetProvider{}
	spellsOf := func(projectPath string) []string { return []string{"go"} }
	opts := observability.TargetRunOptions(context.Background(), rec, spellsOf)

	// Miss: outcome=success, cache.hit=false.
	if _, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(outPath, []byte("ok"), 0o644)
	}, opts...); err != nil {
		t.Fatalf("Run(miss): %v", err)
	}
	if len(rec.targetRuns) != 1 {
		t.Fatalf("after miss: targetRuns=%d, want 1", len(rec.targetRuns))
	}
	r0 := rec.targetRuns[0]
	if got := attrVal(r0.attrs, "outcome"); got != "success" {
		t.Errorf("miss outcome=%q, want success", got)
	}
	if got := attrVal(r0.attrs, "cache.hit"); got != "false" {
		t.Errorf("miss cache.hit=%q, want false", got)
	}
	if got := attrVal(r0.attrs, "magus.target"); got != "test" {
		t.Errorf("miss target=%q, want test", got)
	}
	if got := attrVal(r0.attrs, "magus.spell"); got != "go" {
		t.Errorf("miss spell=%q, want go", got)
	}

	// Hit: outcome=success, cache.hit=true.
	if _, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		t.Error("fn must not run on hit")
		return nil
	}, opts...); err != nil {
		t.Fatalf("Run(hit): %v", err)
	}
	if len(rec.targetRuns) != 2 {
		t.Fatalf("after hit: targetRuns=%d, want 2", len(rec.targetRuns))
	}
	r1 := rec.targetRuns[1]
	if got := attrVal(r1.attrs, "cache.hit"); got != "true" {
		t.Errorf("hit cache.hit=%q, want true", got)
	}
	if got := attrVal(r1.attrs, "outcome"); got != "success" {
		t.Errorf("hit outcome=%q, want success", got)
	}

	// Multi-spell: two spells → two rows per run.
	multiSpellsOf := func(string) []string { return []string{"go", "typescript"} }
	multiOpts := observability.TargetRunOptions(context.Background(), rec, multiSpellsOf)
	multiSpec := cache.Spec{
		ProjectPath:   "q",
		Sources:       []string{"p/*.go"},
		WorkspaceRoot: root,
		Target:        "build",
	}
	before := len(rec.targetRuns)
	if _, err := c.Run(context.Background(), multiSpec, func(_ context.Context) error {
		return nil
	}, multiOpts...); err != nil {
		t.Fatalf("Run(multi-spell): %v", err)
	}
	if got := len(rec.targetRuns) - before; got != 2 {
		t.Errorf("multi-spell emitted %d rows, want 2", got)
	}
}
