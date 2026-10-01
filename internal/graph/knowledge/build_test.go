package knowledge

import (
	"context"
	"log/slog"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildFailsOnUnknownMarkerFamily(t *testing.T) {
	in := markerTree(t, map[string]string{"x.go": "package x\n\n// magus:bogus x\n"})

	g, err := Build(context.Background(), t.TempDir(), BuildOptions{}, in, slog.New(slog.DiscardHandler))

	require.ErrorIs(t, err, &types.DiagnosticError{Code: types.UnknownMarkerFamily})
	assert.ErrorContains(t, err, "x.go:3: magus:bogus")
	assert.Nil(t, g)
}

// BenchmarkBuildNoop is the steady-state cost every query pays: assemble +
// fingerprint every shard + reconcile against an up-to-date store (nothing to
// write except, today, the manifest).
func BenchmarkBuildNoop(b *testing.B) {
	in := syntheticInputs(benchProjects, benchTargets)
	cacheDir := b.TempDir()
	ctx := context.Background()
	if _, err := Build(ctx, cacheDir, BuildOptions{}, in, nil); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Build(ctx, cacheDir, BuildOptions{}, in, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkBuildCold pays the full first-build cost (assemble + fingerprint +
// write every shard + manifest) into a fresh store each iteration.
func BenchmarkBuildCold(b *testing.B) {
	in := syntheticInputs(benchProjects, benchTargets)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cacheDir := b.TempDir()
		b.StartTimer()
		if _, err := Build(ctx, cacheDir, BuildOptions{}, in, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func TestWarmLoadMatchesColdBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cold-build scale test under -short")
	}
	in := syntheticInputs(benchProjects, benchTargets)
	dir := t.TempDir()
	ctx := context.Background()

	cold, err := Build(ctx, dir, BuildOptions{}, in, nil)
	require.NoError(t, err)

	// A warm Load reads only the persisted shards (no assembly) and must reproduce
	// the same graph the cold build merged in memory, the cache-first contract.
	warm, err := NewStore(dir, false, 0, nil, nil).Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, len(cold.Nodes()), len(warm.Nodes()))
	assert.Equal(t, len(cold.Edges()), len(warm.Edges()))
}
