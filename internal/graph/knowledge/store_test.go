package knowledge

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
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
		Spells:      []types.SpellEntry{{Name: "go", Targets: []string{"go-build"}}},
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

// TestInputFingerprintRetainsSkippedShard: a shard produced from an expensive input carries
// an input fingerprint. When the caller SKIPS producing it (absent from the build) but the
// input is unchanged, Sync reuses it from disk instead of pruning it - the mechanism that
// lets the git scan be skipped without a bespoke cache file. A changed input drops it.
func TestInputFingerprintRetainsSkippedShard(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), ".magus")
	store := NewStore(cacheDir, false, 0, nil, nil)
	ctx := context.Background()

	vcs := Shard{Name: "@vcs", Nodes: []types.KnowledgeNode{
		{ID: "file:a.go", Kind: types.KindFile, Source: "a.go", Attrs: map[string]string{"vcs_last_author": "Ada"}},
	}}
	fp := fingerprintShardContent(vcs)

	// Build 1: produce @vcs, keyed by input fingerprint "head1".
	g1, err := store.Sync(ctx, []Shard{vcs}, map[string]string{"@vcs": fp}, map[string]string{"@vcs": "head1"}, false)
	require.NoError(t, err)
	n, ok := g1.node("file:a.go")
	require.True(t, ok)
	assert.Equal(t, "Ada", n.Attrs["vcs_last_author"])
	assert.Equal(t, "head1", readManifest(t, cacheDir).Shards["@vcs"].InputFingerprint)

	// Build 2: @vcs SKIPPED (absent from the build), input unchanged -> reused from disk.
	g2, err := store.Sync(ctx, nil, nil, map[string]string{"@vcs": "head1"}, false)
	require.NoError(t, err)
	n2, ok := g2.node("file:a.go")
	require.True(t, ok, "the skipped-but-fresh @vcs shard is reused from disk")
	assert.Equal(t, "Ada", n2.Attrs["vcs_last_author"])
	assert.FileExists(t, store.shardPath("@vcs"))

	// Build 3: @vcs SKIPPED, but the input CHANGED -> not reused (the caller was expected
	// to rebuild it, so a stale shard is dropped rather than served).
	g3, err := store.Sync(ctx, nil, nil, map[string]string{"@vcs": "head2"}, false)
	require.NoError(t, err)
	_, ok = g3.node("file:a.go")
	assert.False(t, ok, "a changed input drops the stale shard")
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

// fakeRemote is an in-memory RemoteShards for tests: content-addressed by key.
type fakeRemote struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func newFakeRemote() *fakeRemote { return &fakeRemote{blobs: map[string][]byte{}} }

func (f *fakeRemote) PutShard(_ context.Context, key string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.blobs[key] = b
	f.mu.Unlock()
	return nil
}

func (f *fakeRemote) GetShard(_ context.Context, key string) (io.ReadCloser, error) {
	f.mu.Lock()
	b, ok := f.blobs[key]
	f.mu.Unlock()
	if !ok {
		return nil, ErrShardMiss // honor the interface contract: a miss is ErrShardMiss, not a nil reader
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func evictAllShardFiles(t *testing.T, cacheDir string) {
	t.Helper()
	dir := filepath.Join(StoreDir(cacheDir), "shards")
	ents, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, ents)
	for _, e := range ents {
		require.NoError(t, os.Remove(filepath.Join(dir, e.Name())))
	}
}

func TestRemoteShardPushPullRoundTrip(t *testing.T) {
	in := sampleInputs() // deterministic shards only (no runtime)
	dir := t.TempDir()
	rem := newFakeRemote()
	ctx := context.Background()

	built, err := Build(ctx, dir, BuildOptions{Remote: rem}, in, nil)
	require.NoError(t, err)
	require.NotEmpty(t, rem.blobs, "deterministic shards pushed to remote")
	wantNodes := len(built.Nodes())

	// Simulate LRU eviction: every shard file gone, manifest intact.
	evictAllShardFiles(t, dir)

	// Load restores each shard from remote by fingerprint - full graph, no rebuild.
	g, err := NewStore(dir, false, 0, rem, nil).Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, wantNodes, len(g.Nodes()), "graph fully restored from remote")
}

func TestRuntimeShardNotPushed(t *testing.T) {
	in := sampleInputs()
	in.Runtime = []types.DiagnosticEvent{{Unit: "pkg/a:build", Code: types.ExecDenied}}
	dir := t.TempDir()
	rem := newFakeRemote()
	ctx := context.Background()

	_, err := Build(ctx, dir, BuildOptions{Remote: rem}, in, nil)
	require.NoError(t, err)

	// The runtime shard's blob must not be on the remote (local history, not shared).
	runtimeFile := filepath.Join(StoreDir(dir), "shards", shardSlug(RuntimeShardName)+".json")
	require.FileExists(t, runtimeFile)
	evictAllShardFiles(t, dir)

	// Load now fails to restore the @runtime shard (it was never pushed), proving
	// the exclusion; a deterministic-only graph would have loaded fine.
	_, err = NewStore(dir, false, 0, rem, nil).Load(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), RuntimeShardName)
}

func TestPruneToSizeEvictsOverCap(t *testing.T) {
	in := sampleInputs()
	dir := t.TempDir()
	ctx := context.Background()

	// Build once uncapped to learn the store's natural size.
	_, err := Build(ctx, dir, BuildOptions{}, in, nil)
	require.NoError(t, err)
	total := shardsDirSize(t, dir)
	require.Positive(t, total)

	// Rebuild with a cap at half; the prune must bring the dir under it.
	cap := total / 2
	_, err = Build(ctx, dir, BuildOptions{MaxBytes: cap, Refresh: true}, in, nil)
	require.NoError(t, err)
	assert.LessOrEqual(t, shardsDirSize(t, dir), cap, "shards dir pruned to the cap")
}

func shardsDirSize(t *testing.T, cacheDir string) int64 {
	t.Helper()
	dir := filepath.Join(StoreDir(cacheDir), "shards")
	ents, err := os.ReadDir(dir)
	require.NoError(t, err)
	var total int64
	for _, e := range ents {
		info, err := e.Info()
		require.NoError(t, err)
		total += info.Size()
	}
	return total
}

// benchStore builds a graph on disk once and returns a Store pointed at it, so a
// read benchmark measures reading rather than the build that produced it.
func benchStore(b *testing.B, nProjects int) (*Store, int64) {
	b.Helper()
	cacheDir := filepath.Join(b.TempDir(), ".magus")
	if _, err := Build(context.Background(), cacheDir, BuildOptions{}, syntheticInputs(nProjects, 8), nil); err != nil {
		b.Fatal(err)
	}
	var onDisk int64
	dir := StoreDir(cacheDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		b.Fatal(err)
	}
	for _, e := range entries {
		if fi, err := e.Info(); err == nil {
			onDisk += fi.Size()
		}
	}
	return NewStore(cacheDir, false, 0, nil, nil), onDisk
}

// BenchmarkStoreLoad measures a warm read of an already-built graph.
//
// Its absence is why a superlinear cost stayed invisible: Load allocates a Go map
// per node and edge to materialize the graph, so throughput FALLS as the workspace
// grows (134 MB/s at 2k projects, 79 MB/s at 50k) and the wall time reaches ~5 s.
// SetBytes is on the shard bytes so the report is throughput, which is the number
// that exposes the degradation - a plain ns/op just looks bigger for a bigger input.
//
// Note for anyone reading a profile from this: Store.Load has no production caller
// today. Every command goes through Build, which re-assembles from source. That
// makes this benchmark a measure of the READ path a future
// skip-assembly-when-unchanged change would start exercising, not of current
// per-command cost.
func BenchmarkStoreLoad(b *testing.B) {
	for _, n := range []int{200, 2000} {
		b.Run(fmt.Sprintf("p%d", n), func(b *testing.B) {
			store, onDisk := benchStore(b, n)
			b.SetBytes(onDisk)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				g, err := store.Load(context.Background())
				if err != nil {
					b.Fatal(err)
				}
				if len(g.Nodes()) == 0 {
					b.Fatal("loaded an empty graph")
				}
			}
		})
	}
}

// BenchmarkStoreSync measures the write side: fingerprint, compare, and persist the
// shards that changed. This is the path every magus command actually pays, unlike
// Load, so it is the one to watch when judging a format or fingerprint change.
func BenchmarkStoreSync(b *testing.B) {
	in := syntheticInputs(200, 8)
	shards := AssembleShards(in)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cacheDir := filepath.Join(b.TempDir(), ".magus")
		store := NewStore(cacheDir, false, 0, nil, nil)
		b.StartTimer()
		if _, err := store.Sync(context.Background(), shards, nil, nil, false); err != nil {
			b.Fatal(err)
		}
	}
}
