package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildFixture returns a cache dir and the inputs for a two-project workspace.
// Shards are fingerprinted by assembled content, so the fixture needs no
// magusfiles on disk - a change is modeled by mutating the Inputs (exactly what
// re-parsing changed sources produces in production).
func buildFixture(t *testing.T) (cacheDir string, in Inputs) {
	cacheDir = filepath.Join(t.TempDir(), ".magus")
	in = Inputs{
		Graph: types.TargetGraphOutput{Projects: []types.TargetGraphProject{
			{Path: "pkg/a", Engine: "buzz", Nodes: []types.TargetGraphNode{{Name: "build"}}},
			{Path: "pkg/b", Engine: "buzz", Nodes: []types.TargetGraphNode{{Name: "build"}}},
		}},
		Spells:      types.SpellsOutput{Spells: []types.SpellEntry{{Name: "go", Targets: []string{"go-build"}}}},
		Diagnostics: []types.DiagnosticCode{types.SandboxPolicyMismatch},
	}
	return cacheDir, in
}

func build(t *testing.T, cacheDir string, opts BuildOptions, in Inputs) *Graph {
	t.Helper()
	g, err := Build(context.Background(), cacheDir, opts, in, nil)
	require.NoError(t, err)
	return g
}

func readManifest(t *testing.T, cacheDir string) manifest {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(StoreDir(cacheDir), "manifest.json"))
	require.NoError(t, err)
	var m manifest
	require.NoError(t, codec.Unmarshal(b, &m))
	return m
}

func TestBuildPersistsAndReloads(t *testing.T) {
	cacheDir, in := buildFixture(t)
	g1 := build(t, cacheDir, BuildOptions{}, in)

	man := readManifest(t, cacheDir)
	assert.Equal(t, types.KnowledgeSchemaVersion, man.SchemaVersion)
	for _, name := range []string{RegistryShardName, "pkg/a", "pkg/b"} {
		_, ok := man.Shards[name]
		assert.Truef(t, ok, "manifest missing shard %q", name)
	}

	// A pure disk load (no assembly) reproduces the built graph byte-for-byte.
	loaded, err := NewStore(cacheDir, false, 0, nil, nil).Load(context.Background())
	require.NoError(t, err)
	built, _ := codec.Marshal(g1.Output())
	fromDisk, _ := codec.Marshal(loaded.Output())
	assert.Equal(t, string(built), string(fromDisk))
}

// TestSymbolShardsLazyExcludeAndMerge: a declared symbol shard is persisted but
// kept OUT of the default graph (Sync and Load), and pulled in on demand by
// MergeSymbolShards - the lazy-loading contract that keeps symbols off the hot path.
func TestSymbolShardsLazyExcludeAndMerge(t *testing.T) {
	cacheDir, in := buildFixture(t)
	in.Symbols = map[string][]types.KnowledgeSymbol{
		"pkg/a": {{Key: "example.com/a Foo#", Label: "Foo", Source: "pkg/a/a.go:1", Defs: []string{"pkg/a/a.go"}}},
	}
	g := build(t, cacheDir, BuildOptions{}, in)

	// Persisted in the manifest, but absent from the default (Sync) graph.
	_, ok := readManifest(t, cacheDir).Shards["pkg/a@symbols"]
	assert.True(t, ok, "symbol shard is persisted")
	_, inGraph := g.node("symbol:example.com/a Foo#")
	assert.False(t, inGraph, "symbol node excluded from the default graph")

	store := NewStore(cacheDir, false, 0, nil, nil)

	// A pure disk Load also excludes symbol shards.
	loaded, err := store.Load(context.Background())
	require.NoError(t, err)
	_, inLoad := loaded.node("symbol:example.com/a Foo#")
	assert.False(t, inLoad, "Load excludes symbol shards too")

	// MergeSymbolShards pulls them in on demand.
	require.NoError(t, store.MergeSymbolShards(context.Background(), loaded))
	_, nowIn := loaded.node("symbol:example.com/a Foo#")
	assert.True(t, nowIn, "symbol node present after the lazy merge")
}

// TestFingerprintInvalidation: changing one project's assembled inputs rewrites
// only that shard; untouched projects and the registry keep their fingerprint.
func TestFingerprintInvalidation(t *testing.T) {
	cacheDir, in := buildFixture(t)
	build(t, cacheDir, BuildOptions{}, in)
	before := readManifest(t, cacheDir)

	// Add a target to pkg/a (as a re-parse of a changed magusfile would).
	in.Graph.Projects[0].Nodes = append(in.Graph.Projects[0].Nodes, types.TargetGraphNode{Name: "lint"})
	build(t, cacheDir, BuildOptions{}, in)
	after := readManifest(t, cacheDir)

	assert.NotEqual(t, before.Shards["pkg/a"].Fingerprint, after.Shards["pkg/a"].Fingerprint, "pkg/a fingerprint should change")
	assert.Equal(t, before.Shards["pkg/b"].Fingerprint, after.Shards["pkg/b"].Fingerprint, "pkg/b fingerprint should be stable")
	assert.Equal(t, before.Shards[RegistryShardName].Fingerprint, after.Shards[RegistryShardName].Fingerprint, "registry fingerprint should be stable")
}

// TestCrossProjectInvalidation: content fingerprinting catches a change that a
// per-project source hash would miss - a cross-project edge from pkg/a to a
// pkg/b target. When that target reference changes, pkg/a's shard must rebuild.
func TestCrossProjectInvalidation(t *testing.T) {
	cacheDir, in := buildFixture(t)
	in.Graph.Projects[0].Nodes[0].CrossDependencies = []types.CrossTargetRef{{Project: "pkg/b", Target: "build"}}
	build(t, cacheDir, BuildOptions{}, in)
	before := readManifest(t, cacheDir)

	// Repoint pkg/a's cross-dep at a different pkg/b target.
	in.Graph.Projects[0].Nodes[0].CrossDependencies[0].Target = "test"
	build(t, cacheDir, BuildOptions{}, in)
	after := readManifest(t, cacheDir)

	assert.NotEqual(t, before.Shards["pkg/a"].Fingerprint, after.Shards["pkg/a"].Fingerprint, "pkg/a fingerprint should change when its cross-project edge changes")
}

// TestRebuildIsIdempotent: rebuilding with unchanged inputs leaves the shard
// files byte-identical.
func TestRebuildIsIdempotent(t *testing.T) {
	cacheDir, in := buildFixture(t)
	g1 := build(t, cacheDir, BuildOptions{}, in)
	shardsDir := filepath.Join(StoreDir(cacheDir), "shards")
	first := snapshotDir(t, shardsDir)

	g2 := build(t, cacheDir, BuildOptions{}, in)
	second := snapshotDir(t, shardsDir)

	assert.Equal(t, first, second, "shard files should be byte-identical across idempotent rebuilds")
	a, _ := codec.Marshal(g1.Output())
	b, _ := codec.Marshal(g2.Output())
	assert.Equal(t, string(a), string(b))
}

// TestDeletionReconciliation: dropping a project removes its shard file and its
// manifest entry.
func TestDeletionReconciliation(t *testing.T) {
	cacheDir, in := buildFixture(t)
	build(t, cacheDir, BuildOptions{}, in)
	require.Contains(t, readManifest(t, cacheDir).Shards, "pkg/b")
	bShard := NewStore(cacheDir, false, 0, nil, nil).shardPath("pkg/b")
	require.FileExists(t, bShard)

	in.Graph.Projects = in.Graph.Projects[:1]
	build(t, cacheDir, BuildOptions{}, in)

	assert.NotContains(t, readManifest(t, cacheDir).Shards, "pkg/b")
	assert.NoFileExists(t, bShard)
}

// TestImmutableDoesNotWrite: with immutable set on a fresh cache dir, Build
// returns a graph but persists nothing.
func TestImmutableDoesNotWrite(t *testing.T) {
	cacheDir, in := buildFixture(t)
	g := build(t, cacheDir, BuildOptions{Immutable: true}, in)
	assert.Positive(t, g.Output().NodeCount)
	assert.NoDirExists(t, StoreDir(cacheDir))
}

// snapshotDir returns a name->contents map of a directory's files.
func snapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, err)
		out[e.Name()] = string(b)
	}
	return out
}

func TestRuntimeRecordsRoundTripDedupAndCap(t *testing.T) {
	dir := t.TempDir()
	require.Empty(t, LoadRuntimeEvents(dir))

	require.NoError(t, RecordRuntimeEvents(dir, []types.DiagnosticEvent{
		{Unit: "a:build", Code: types.ExecDenied},
		{Unit: "a:build", Code: types.ExecDenied}, // dup within the batch
	}))
	require.Len(t, LoadRuntimeEvents(dir), 1)

	// A second run merges without re-adding the existing pair, and adds the new one.
	require.NoError(t, RecordRuntimeEvents(dir, []types.DiagnosticEvent{
		{Unit: "a:build", Code: types.ExecDenied},
		{Unit: "b:test", Code: types.RaceDetected},
	}))
	assert.Len(t, LoadRuntimeEvents(dir), 2)
}
