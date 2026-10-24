package magus

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// newStampedWorkspace is newIndexedWorkspace with a tool whose version probe counts its
// calls, so a test can tell a read the stamp answered from one that forked.
func newStampedWorkspace(t *testing.T) (m *Magus, src, index string, probes *atomic.Int32) {
	t.Helper()
	probes = &atomic.Int32{}
	spellName := register.Spell(t, "zzz-scip-stamp-test-spell",
		spells.WithTargets(spells.DefaultSymbolIndexOp),
		spells.WithSymbolIndexer(&spells.SymbolIndexer{Format: spells.SymbolFormatSCIP}),
		spells.WithSources("**/*.go"),
		spells.WithTools(map[string]spells.Tool{
			"counted": {Probe: spells.Command{Bin: "counted", Args: []string{"--version"}}},
		}),
		spells.WithVersionProber(func(context.Context, spells.Command, string) (string, error) {
			probes.Add(1)
			return "counted 1.2.3", nil
		}),
		spells.WithInvoker(writesTheIndex),
	)

	w := testkit.NewWorkspace(t)
	w.Magusfile("")
	root := w.Root()
	src = filepath.Join(root, "main.go")
	require.NoError(t, os.WriteFile(src, []byte("package main\n"), 0o644))

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg), WithSpawnRule(TierCeiling))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	require.NoError(t, m.Run(context.Background(), []types.Target{{Path: ".", Name: spells.DefaultSymbolIndexOp}}), "scip run")
	index = symbols.IndexPath(resolveCacheDir(m.Root(), m.cfg), ".", spells.DefaultSymbolIndexOp)
	probes.Store(0)
	return m, src, index, probes
}

func stampedFreshness(t *testing.T, m *Magus) types.SymbolIndexFreshness {
	t.Helper()
	for _, s := range m.SymbolIndexStatusByStamp(context.Background()) {
		if s.Project.Path == "." {
			return s.Freshness
		}
	}
	t.Fatal("the root project is missing from the status")
	return ""
}

func TestSymbolStampSkipsTheProbeOnceTheProbeFoundTheIndexFresh(t *testing.T) {
	m, _, _, probes := newStampedWorkspace(t)

	require.Equal(t, types.SymbolIndexFresh, stampedFreshness(t, m))
	require.Positive(t, probes.Load(), "the first read has no stamp, so it asks the cache the authoritative way")

	probes.Store(0)
	assert.Equal(t, types.SymbolIndexFresh, stampedFreshness(t, m))
	assert.Zero(t, probes.Load(), "a matching stamp answers without forking a tool")
}

func TestSymbolStampIgnoresAnIdenticalRewrite(t *testing.T) {
	m, src, _, probes := newStampedWorkspace(t)
	require.Equal(t, types.SymbolIndexFresh, stampedFreshness(t, m))

	body, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(src, body, 0o644))
	later := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(src, later, later))

	probes.Store(0)
	assert.Equal(t, types.SymbolIndexFresh, stampedFreshness(t, m))
	assert.Zero(t, probes.Load(), "the stamp keys sources by content, so a format pass that changes no bytes still matches")
}

func TestSymbolStampNeverOutlivesChangedBytes(t *testing.T) {
	m, src, _, probes := newStampedWorkspace(t)
	require.Equal(t, types.SymbolIndexFresh, stampedFreshness(t, m))

	require.NoError(t, os.WriteFile(src, []byte("package main\n\nfunc Added() {}\n"), 0o644))

	probes.Store(0)
	assert.Equal(t, types.SymbolIndexStale, stampedFreshness(t, m),
		"a definition added since the index was built is not in it, stamp or no stamp")
	assert.Positive(t, probes.Load(), "a stamp that no longer matches hands the question back to the probe")
	assert.Equal(t, types.SymbolIndexStale, freshness(t, m), "and the authoritative probe agrees")
}

func TestSymbolStampMovesWithTheIndex(t *testing.T) {
	m, _, index, probes := newStampedWorkspace(t)
	require.Equal(t, types.SymbolIndexFresh, stampedFreshness(t, m))

	require.NoError(t, os.WriteFile(index, []byte("scip, rebuilt"), 0o644))

	probes.Store(0)
	assert.Equal(t, types.SymbolIndexStale, stampedFreshness(t, m), "no run wrote these bytes, so no entry vouches for them")
	assert.Positive(t, probes.Load(), "a rewritten index is a different file, so the stamp vouches for it no longer")
}

func TestChangedSinceIndexListsSourcesNewerThanTheIndexSorted(t *testing.T) {
	m, src, index, _ := newStampedWorkspace(t)
	built := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(index, built, built))
	before, after := built.Add(-time.Minute), built.Add(time.Minute)
	require.NoError(t, os.Chtimes(src, before, before))
	require.NoError(t, os.Chtimes(filepath.Join(m.Root(), "magusfile.buzz"), before, before))
	for _, name := range []string{"zeta.go", "alpha.go"} {
		path := filepath.Join(m.Root(), name)
		require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))
		require.NoError(t, os.Chtimes(path, after, after))
	}

	changed, err := m.changedSinceIndex(t.Context(), m.Get("."), spells.DefaultSymbolIndexOp, index, cache.NewSourceMemo())
	require.NoError(t, err)
	assert.Equal(t, []string{"alpha.go", "zeta.go"}, changed, "newer sources, sorted; main.go and the magusfile predate the index")
}

func TestChangedSinceIndexWithoutAnIndexFails(t *testing.T) {
	m, _, index, _ := newStampedWorkspace(t)
	require.NoError(t, os.Remove(index))

	_, err := m.changedSinceIndex(t.Context(), m.Get("."), spells.DefaultSymbolIndexOp, index, cache.NewSourceMemo())
	assert.Error(t, err, "no index file is no build time to compare against, never an empty list")
}

func TestChangedSinceIndexReadsAKeyedSourcePathFromTheRight(t *testing.T) {
	for line, want := range map[string]string{
		"src:main.go:abc:0":   "main.go",
		"src:a:b/c.go:abc:1":  "a:b/c.go",
		"env:PATH=/bin":       "",
		"src:broken":          "",
		"srcdir:x.go:abc:0":   "",
		"spell:src:x.go:ab:0": "",
	} {
		got, ok := keyedSource(line)
		assert.Equal(t, want, got, line)
		assert.Equal(t, want != "", ok, line)
	}
}

func TestSymbolIndexStatusByStampNamesTheChangedSources(t *testing.T) {
	m, src, _, _ := newStampedWorkspace(t)
	require.Equal(t, types.SymbolIndexFresh, stampedFreshness(t, m))

	require.NoError(t, os.WriteFile(src, []byte("package main\n\nfunc Added() {}\n"), 0o644))
	later := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(src, later, later))

	for _, s := range m.SymbolIndexStatusByStamp(t.Context()) {
		if s.Project.Path != "." {
			continue
		}
		assert.Equal(t, types.SymbolIndexStale, s.Freshness)
		assert.Equal(t, []string{"main.go"}, s.Changed)
		assert.Equal(t, 1, s.ChangedCount)
		return
	}
	t.Fatal("the root project is missing from the status")
}

func TestSymbolStampWithoutAnIndexIsNotBuilt(t *testing.T) {
	m, _, index, probes := newStampedWorkspace(t)
	require.Equal(t, types.SymbolIndexFresh, stampedFreshness(t, m))

	require.NoError(t, os.Remove(index))

	probes.Store(0)
	assert.Equal(t, types.SymbolIndexNotBuilt, stampedFreshness(t, m), "a stamp file left behind says nothing about an index that is gone")
	assert.Zero(t, probes.Load())
}
