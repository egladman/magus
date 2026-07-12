package otlp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/observability"
)

// TestNew_DisabledIsNoOp verifies that constructing a Provider with
// Enabled=false never opens a connection or panics. This is the path
// almost all magus invocations take, since telemetry is OFF by default.
func TestNew_DisabledIsNoOp(t *testing.T) {
	t.Parallel()
	p, err := New(context.Background(), observability.Config{Enabled: false})
	require.NoError(t, err)
	require.NotNil(t, p, "New(disabled) returned nil Provider")
	assert.False(t, p.Enabled(), "disabled Provider reports Enabled() == true")
	p.RecordCacheHit(context.Background())
	p.RecordCacheMiss(context.Background())
	p.RecordCacheError(context.Background())
	p.RecordCacheDuration(context.Background(), 1.5)
	assert.NoError(t, p.Shutdown(context.Background()))
}

// TestNew_EnabledRequiresEndpoint exercises the validation path.
func TestNew_EnabledRequiresEndpoint(t *testing.T) {
	t.Parallel()
	_, err := New(context.Background(), observability.Config{Enabled: true})
	assert.Error(t, err, "New(enabled, no endpoint) should error")
}

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

// TestCacheRunOptions_DisabledProviderIsInert verifies the
// nil-vs-disabled boundary documented on CacheRunOptions: a Provider
// reporting Enabled()==false still returns the options (callers can
// wire them unconditionally), but the underlying record calls are
// no-ops via disabledProvider — the cache.Run pipeline exercises
// every disabled-provider record method through a hit path.
func TestCacheRunOptions_DisabledProviderIsInert(t *testing.T) {
	p, err := New(context.Background(), observability.Config{Enabled: false})
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
	opts := observability.CacheRunOptions(context.Background(), p)

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
