package knowledge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/readlog"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// buildFixture returns a cache dir and the inputs for a two-project workspace.
// Shards are fingerprinted by assembled content, so the fixture needs no
// magusfiles on disk: a change is modeled by mutating the Inputs (exactly what
// re-parsing changed sources produces in production).
func buildFixture(t *testing.T) (cacheDir string, in Inputs) {
	cacheDir = filepath.Join(t.TempDir(), ".magus")
	in = Inputs{
		Graph: types.TargetGraphOutput{Projects: []types.TargetGraphProject{
			{Path: "pkg/a", Engine: "buzz", Nodes: []types.TargetGraphNode{{Name: "build"}}},
			{Path: "pkg/b", Engine: "buzz", Nodes: []types.TargetGraphNode{{Name: "build"}}},
		}},
		Spells:      []types.Spell{{Name: "go", Targets: []string{"go-build"}}},
		Diagnostics: []types.DiagnosticCode{types.SandboxWeakened},
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
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

func TestBuildPersistsAndReloads(t *testing.T) {
	cacheDir, in := buildFixture(t)
	g1 := build(t, cacheDir, BuildOptions{}, in)

	man := readManifest(t, cacheDir)
	assert.Equal(t, types.KnowledgeSchemaVersion, man.SchemaVersion)
	for _, name := range []string{registryShardName, "pkg/a", "pkg/b"} {
		_, ok := man.Shards[name]
		assert.Truef(t, ok, "manifest missing shard %q", name)
	}

	// A pure disk load (no assembly) reproduces the built graph byte-for-byte.
	loaded, err := NewStore(cacheDir, false, 0, nil, nil).Load(context.Background())
	require.NoError(t, err)
	built, _ := json.Marshal(g1.Output())
	fromDisk, _ := json.Marshal(loaded.Output())
	assert.Equal(t, string(built), string(fromDisk))
}

// TestSymbolShardsLazyExcludeAndMerge: a declared symbol shard is persisted but
// kept OUT of the default graph (Sync and Load), and pulled in on demand by
// MergeSymbolShards, the lazy-loading contract that keeps symbols off the hot path.
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
	assert.Equal(t, before.Shards[registryShardName].Fingerprint, after.Shards[registryShardName].Fingerprint, "registry fingerprint should be stable")
}

// TestCrossProjectInvalidation: content fingerprinting catches a change that a
// per-project source hash would miss: a cross-project edge from pkg/a to a
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
	a, _ := json.Marshal(g1.Output())
	b, _ := json.Marshal(g2.Output())
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

	// Load restores each shard from remote by fingerprint: full graph, no rebuild.
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
	runtimeFile := filepath.Join(StoreDir(dir), "shards", shardSlug(runtimeShardName)+".json")
	require.FileExists(t, runtimeFile)
	evictAllShardFiles(t, dir)

	// Load now fails to restore the @runtime shard (it was never pushed), proving
	// the exclusion; a deterministic-only graph would have loaded fine.
	_, err = NewStore(dir, false, 0, rem, nil).Load(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), runtimeShardName)
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

// Load must merge shards in a stable order. AddNode and AddEdge are both
// first-writer-wins, so when two shards describe the same node with a different
// Source, the winner is decided purely by merge order, and Load used to take Go's
// randomized map order over the manifest.
//
// The failure this guards against is nasty precisely because it is quiet: the export
// sorts nodes and edges by ID afterward, so counts never move and the diff is a
// handful of "source" lines. That is exactly how it presented: `magus run generate`
// regenerated gen/*.json, the drift gate saw a changed file, and the change was
// provenance only.
//
// Many shards and many iterations because map order is random per range: two shards
// would coin-flip and could pass a short run by luck.
func TestLoadMergesShardsInStableOrder(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), ".magus")
	store := NewStore(cacheDir, false, 0, nil, nil)
	ctx := context.Background()

	// Every shard claims the same node and the same edge, each with its own Source.
	// Whichever merges first supplies both, so the merged result names the winner.
	const shardCount = 12
	shards := make([]Shard, 0, shardCount)
	fps := map[string]string{}
	for i := range shardCount {
		name := fmt.Sprintf("@s%02d", i)
		sh := Shard{
			Name: name,
			Nodes: []types.KnowledgeNode{
				{ID: "file:shared.go", Kind: types.KindFile, Source: fmt.Sprintf("shard-%02d.go", i)},
				{ID: fmt.Sprintf("file:own%02d.go", i), Kind: types.KindFile, Source: "own.go"},
			},
			Edges: []types.KnowledgeEdge{{
				Source:     "file:shared.go",
				Target:     fmt.Sprintf("file:own%02d.go", i),
				Relation:   types.RelationReferences,
				Confidence: types.ConfidenceExtracted,
				Provenance: fmt.Sprintf("shard-%02d.go", i),
			}},
		}
		shards = append(shards, sh)
		fps[name] = fingerprintShardContent(sh)
	}
	_, err := store.Sync(ctx, shards, fps, false)
	require.NoError(t, err)

	// Load repeatedly from the SAME on-disk store; every read must agree.
	var want string
	for i := range 25 {
		g, err := NewStore(cacheDir, false, 0, nil, nil).Load(ctx)
		require.NoError(t, err)
		n, ok := g.node("file:shared.go")
		require.True(t, ok, "shared node missing on iteration %d", i)
		if i == 0 {
			want = n.Source
			continue
		}
		assert.Equal(t, want, n.Source,
			"iteration %d disagreed on provenance: shard merge order is not stable", i)
		if t.Failed() {
			break
		}
	}

	// The sorted order is the contract, not merely "some fixed order": the first
	// shard by name wins, which is what makes the result predictable to a reader
	// rather than an artifact of whatever the map happened to yield.
	assert.Equal(t, "shard-00.go", want, "the lowest-named shard should win a first-writer-wins merge")
}

// BenchmarkStoreLoad measures a warm read of an already-built graph.
//
// Its absence is why a superlinear cost stayed invisible: Load allocates a Go map
// per node and edge to materialize the graph, so throughput FALLS as the workspace
// grows (134 MB/s at 2k projects, 79 MB/s at 50k) and the wall time reaches ~5 s.
// SetBytes is on the shard bytes so the report is throughput, which is the number
// that exposes the degradation: a plain ns/op just looks bigger for a bigger input.
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

// writeManifestBytes plants a raw manifest on disk, so a case can express a
// schema bump or a truncated file that json.Marshal would never produce.
func writeManifestBytes(t *testing.T, b []byte) string {
	t.Helper()
	cacheDir := filepath.Join(t.TempDir(), ".magus")
	require.NoError(t, os.MkdirAll(StoreDir(cacheDir), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(StoreDir(cacheDir), "manifest.json"), b, 0o644))
	return cacheDir
}

func marshalManifest(t *testing.T, m manifest) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return b
}

func TestProjectPaths(t *testing.T) {
	cases := []struct {
		name string
		man  []byte // nil plants no manifest at all
		want []string
	}{
		{
			name: "project shards only, sorted; singletons and symbol shards filtered",
			man: marshalManifest(t, manifest{SchemaVersion: types.KnowledgeSchemaVersion, Shards: map[string]shardMeta{
				"pkg/b":         {},
				"pkg/a":         {},
				".":             {},
				"@runtime":      {},
				"@registry":     {},
				"pkg/a@symbols": {},
			}}),
			want: []string{".", "pkg/a", "pkg/b"},
		},
		{name: "no manifest reads as no projects"},
		{
			// A schema bump invalidates the whole store, so the paths are
			// unknown rather than empty: a caller must not scope on them.
			name: "schema mismatch reads as no projects",
			man: marshalManifest(t, manifest{SchemaVersion: types.KnowledgeSchemaVersion + 1, Shards: map[string]shardMeta{
				"pkg/a": {},
			}}),
		},
		{name: "truncated json reads as no projects", man: []byte(`{"schema_version":10,"shards":{"pkg/a":`)},
		{name: "non-json reads as no projects", man: []byte("not json at all")},
		{
			name: "a manifest of nothing but filtered shards",
			man: marshalManifest(t, manifest{SchemaVersion: types.KnowledgeSchemaVersion, Shards: map[string]shardMeta{
				"@runtime":      {},
				"@registry":     {},
				"pkg/a@symbols": {},
			}}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cacheDir := filepath.Join(t.TempDir(), "absent")
			if tc.man != nil {
				cacheDir = writeManifestBytes(t, tc.man)
			}
			assert.Equal(t, tc.want, ProjectPaths(cacheDir))
		})
	}
}

func TestStoreSymbolIndexDigest(t *testing.T) {
	digest := func(t *testing.T, cacheDir string) types.SymbolIndexDigest {
		t.Helper()
		d, err := NewStore(cacheDir, false, 0, nil, nil).SymbolIndexDigest()
		require.NoError(t, err)
		return d
	}
	symbol := func(label string) []types.KnowledgeSymbol {
		return []types.KnowledgeSymbol{{Key: "example.com/a Foo#", Label: label, Source: "pkg/a/a.go:1", Defs: []string{"pkg/a/a.go"}}}
	}

	t.Run("no store is not indexed", func(t *testing.T) {
		assert.Equal(t, types.SymbolIndexDigest{Projects: []string{}}, digest(t, filepath.Join(t.TempDir(), "absent")))
	})

	t.Run("a store without symbol shards is not indexed", func(t *testing.T) {
		cacheDir, in := buildFixture(t)
		build(t, cacheDir, BuildOptions{}, in)
		assert.Equal(t, types.SymbolIndexDigest{Projects: []string{}}, digest(t, cacheDir))
	})

	t.Run("hashes each symbol shard's fingerprint in project order", func(t *testing.T) {
		cacheDir, in := buildFixture(t)
		in.Symbols = map[string][]types.KnowledgeSymbol{"pkg/b": symbol("Bar"), "pkg/a": symbol("Foo")}
		build(t, cacheDir, BuildOptions{}, in)
		man := readManifest(t, cacheDir)
		// pkg/b's index defines its symbol under pkg/a, outside pkg/b's own directory, so
		// that symbol has a directory shard of its own and pkg/b contributes two lines.
		h := sha256.New()
		fmt.Fprintf(h, "pkg/a\x00%s\npkg/b\x00%s\npkg/b:pkg/a\x00%s\n", man.Shards["pkg/a@symbols"].Fingerprint,
			man.Shards["pkg/b@symbols"].Fingerprint, man.Shards["pkg/b@symbols:pkg/a"].Fingerprint)
		assert.Equal(t, types.SymbolIndexDigest{
			Digest:   hex.EncodeToString(h.Sum(nil)),
			Indexed:  true,
			Projects: []string{"pkg/a", "pkg/b"},
		}, digest(t, cacheDir))
	})

	t.Run("moves with symbol content and only with it", func(t *testing.T) {
		cacheDir, in := buildFixture(t)
		in.Symbols = map[string][]types.KnowledgeSymbol{"pkg/a": symbol("Foo")}
		build(t, cacheDir, BuildOptions{}, in)
		first := digest(t, cacheDir)
		require.True(t, first.Indexed)

		in.Graph.Projects[0].Nodes = append(in.Graph.Projects[0].Nodes, types.TargetGraphNode{Name: "lint"})
		build(t, cacheDir, BuildOptions{}, in)
		assert.Equal(t, first, digest(t, cacheDir), "a domain shard change leaves the digest alone")

		in.Symbols["pkg/a"] = symbol("Renamed")
		build(t, cacheDir, BuildOptions{}, in)
		assert.NotEqual(t, first.Digest, digest(t, cacheDir).Digest, "a symbol shard change moves the digest")
	})

	t.Run("a blank fingerprint refuses", func(t *testing.T) {
		cacheDir := writeManifestBytes(t, marshalManifest(t, manifest{SchemaVersion: types.KnowledgeSchemaVersion, Shards: map[string]shardMeta{
			"pkg/a@symbols": {},
		}}))
		_, err := NewStore(cacheDir, false, 0, nil, nil).SymbolIndexDigest()
		require.ErrorContains(t, err, `symbol shard "pkg/a@symbols" has no fingerprint`)
	})
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
		if _, err := store.Sync(context.Background(), shards, nil, false); err != nil {
			b.Fatal(err)
		}
	}
}

// TestReadStoreExportKeysShardsByFingerprint pins the publishable form: every shard the
// returned manifest names has a blob keyed the way GetShard asks for it.
func TestReadStoreExportKeysShardsByFingerprint(t *testing.T) {
	in := sampleInputs()
	in.PrivateNotes = []types.KnowledgeNote{{Path: "/home/me/notes.md", Title: "mine"}}
	in.Runtime = []types.DiagnosticEvent{{Unit: "pkg/a:build", Code: types.ExecDenied}}
	dir := t.TempDir()
	_, err := Build(t.Context(), dir, BuildOptions{}, in, nil)
	require.NoError(t, err)

	export, err := ReadStoreExport(dir)
	require.NoError(t, err)
	require.NotEmpty(t, export.Shards)

	keys, err := ShardKeys(export.Manifest)
	require.NoError(t, err)
	require.Len(t, keys, len(export.Shards), "the manifest names exactly the shards that ship with it")
	for _, sh := range export.Shards {
		assert.Equal(t, keys[sh.Name], sh.Key, "a shard's layer key is the fingerprint the manifest routes by")
		assert.False(t, isMachineLocalShard(sh.Name), "machine-local content must never be published")
	}
	assert.NotContains(t, keys, runtimeShardName)
	assert.NotContains(t, keys, privateNotesShardName)
}

// TestPublishedShardsRebuildTheGraph proves the shards alone are enough: a reader holding
// the export and no store on disk reconstructs what the build produced.
func TestPublishedShardsRebuildTheGraph(t *testing.T) {
	in := sampleInputs()
	dir := t.TempDir()
	built, err := Build(t.Context(), dir, BuildOptions{}, in, nil)
	require.NoError(t, err)

	export, err := ReadStoreExport(dir)
	require.NoError(t, err)

	merged := NewGraph()
	for _, sh := range export.Shards {
		require.NoError(t, MergeShardFile(merged, sh.Bytes))
	}
	assert.Equal(t, len(built.Nodes()), len(merged.Nodes()))
	assert.Equal(t, len(built.Edges()), len(merged.Edges()))
}

// TestShardKeysRefusesAForeignSchema stops a published store written by another schema
// from being read as this one: the shard files behind it carry that schema's shapes.
func TestShardKeysRefusesAForeignSchema(t *testing.T) {
	_, err := ShardKeys([]byte(`{"schema_version":0,"shards":{}}`))
	assert.Error(t, err)
}

// TestReadStoreExportWithoutAStore reports the absence rather than an empty export, so a
// publisher cannot push nothing and call it a publish.
func TestReadStoreExportWithoutAStore(t *testing.T) {
	_, err := ReadStoreExport(t.TempDir())
	assert.ErrorIs(t, err, ErrNoStore)
}

// gatedRemote parks the first PutShard until released, which stops its Sync after the
// shard file is written and before the manifest is.
type gatedRemote struct {
	*fakeRemote
	entered, release chan struct{}
	once             sync.Once
}

func (g *gatedRemote) PutShard(ctx context.Context, key string, r io.Reader) error {
	g.once.Do(func() {
		close(g.entered)
		<-g.release
	})
	return g.fakeRemote.PutShard(ctx, key, r)
}

// Two processes Sync one store with different content for a shard. Unlocked, the
// slower one's manifest lands over the faster one's shard file: the manifest names fp2
// while the file holds fp1, and since later builds compare fingerprints against the
// manifest, the stale file is never rewritten.
func TestStoreConcurrentSyncsLeaveManifestAndShardsAgreeing(t *testing.T) {
	cacheDir := t.TempDir()
	shard := func(label string) []Shard {
		return []Shard{{Name: "project:x", Nodes: []types.KnowledgeNode{{ID: "project:x", Label: label}}}}
	}
	remote := &gatedRemote{fakeRemote: newFakeRemote(), entered: make(chan struct{}), release: make(chan struct{})}

	slow := make(chan error, 1)
	go func() {
		_, err := NewStore(cacheDir, false, 0, remote, nil).Sync(t.Context(), shard("v2"), map[string]string{"project:x": "fp2"}, false)
		slow <- err
	}()
	<-remote.entered

	fast := make(chan error, 1)
	go func() {
		_, err := NewStore(cacheDir, false, 0, nil, nil).Sync(t.Context(), shard("v1"), map[string]string{"project:x": "fp1"}, false)
		fast <- err
	}()
	// Unlocked, the second Sync finishes inside this window; locked, it waits for the first.
	select {
	case err := <-fast:
		fast <- err
	case <-time.After(500 * time.Millisecond):
	}
	close(remote.release)
	require.NoError(t, <-slow)
	require.NoError(t, <-fast)

	man := readManifest(t, cacheDir)
	sf, err := NewStore(cacheDir, false, 0, nil, nil).readShard("project:x")
	require.NoError(t, err)
	assert.Equal(t, man.Shards["project:x"].Fingerprint, sf.Fingerprint, "the manifest names a shard the file does not hold")
}

// A shard is written compact: the indentation was only for a reader of the file, and it
// made a stored graph about twice the size. Nothing about the content or its key changes,
// and a store written indented, by an older build, still loads.
func TestStoreWritesCompactShards(t *testing.T) {
	cacheDir, in := buildFixture(t)
	g1 := build(t, cacheDir, BuildOptions{}, in)
	s := NewStore(cacheDir, false, 0, nil, nil)

	b, err := os.ReadFile(s.shardPath("pkg/b"))
	require.NoError(t, err)
	require.True(t, json.Valid(b))
	assert.NotContains(t, string(b), "\n", "a compact shard is one line")

	sf, err := s.readShard("pkg/b")
	require.NoError(t, err)
	assert.Equal(t, readManifest(t, cacheDir).Shards["pkg/b"].Fingerprint, sf.Fingerprint, "the key is the content's, not the bytes'")

	// The same shard written the way an older build wrote it is read to the same content.
	indented, err := json.MarshalIndent(sf, "", "  ")
	require.NoError(t, err)
	require.Contains(t, string(indented), "\n  ")
	assert.Less(t, len(b), len(indented), "compact is smaller than the indented form of the same shard")
	t.Logf("shard pkg/b: %d bytes compact, %d indented", len(b), len(indented))
	require.NoError(t, os.WriteFile(s.shardPath("pkg/b"), indented, 0o644))
	again, err := s.readShard("pkg/b")
	require.NoError(t, err)
	assert.Equal(t, sf, again)

	g2, err := s.Load(context.Background())
	require.NoError(t, err)
	assert.Equal(t, g1.Fingerprint(), g2.Fingerprint())
}

// mergeShards sizes its maps from the shards' counts; the sum over-counts a node two
// shards both declare, which must cost buckets only, never a node.
func TestMergeShardsPresizedGraphEqualsIncrementalMerge(t *testing.T) {
	shared := types.KnowledgeNode{ID: "spell:go", Kind: types.KindSpell, Label: "go"}
	shards := []Shard{
		{Name: "pkg/b", Nodes: []types.KnowledgeNode{shared, {ID: "project:pkg/b", Kind: types.KindProject, Label: "b"}},
			Edges: []types.KnowledgeEdge{{Source: "project:pkg/b", Target: "spell:go", Relation: types.RelationUses, Confidence: types.ConfidenceExtracted}}},
		{Name: "pkg/a", Nodes: []types.KnowledgeNode{shared, {ID: "project:pkg/a", Kind: types.KindProject, Label: "a"}},
			Edges: []types.KnowledgeEdge{{Source: "project:pkg/a", Target: "spell:go", Relation: types.RelationUses, Confidence: types.ConfidenceExtracted}}},
		{Name: "pkg/a" + symbolsShardSuffix, Nodes: []types.KnowledgeNode{{ID: "symbol:x", Kind: types.KindSymbol, Label: "x"}}},
	}
	want := NewGraph()
	want.Merge(shards[1].Nodes, shards[1].Edges)
	want.Merge(shards[0].Nodes, shards[0].Edges)

	got := mergeShards(shards)
	assert.Equal(t, want.Output(), got.Output())
	assert.Len(t, got.Nodes(), 3, "the shared node is one node")
	assert.NotContains(t, got.nodes, "symbol:x", "a lazily loaded shard is not part of the default merge")
}

// BenchmarkMergeShards is the presizing A/B: the same default-shard merge into a graph
// sized from the shards' counts (what mergeShards does) and into one grown from empty
// (what it did). The 2000-project fixture is the one the other benchmarks use.
func BenchmarkMergeShards(b *testing.B) {
	shards := AssembleShards(syntheticInputs(benchProjects, benchTargets))
	b.Run("presized", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = mergeShards(shards)
		}
	})
	b.Run("grown", func(b *testing.B) {
		picked := make([]Shard, 0, len(shards))
		for _, sh := range shards {
			if !isLazyShard(sh.Name) {
				picked = append(picked, sh)
			}
		}
		b.ReportAllocs()
		for b.Loop() {
			g := NewGraph()
			for _, sh := range picked {
				g.Merge(sh.Nodes, sh.Edges)
			}
		}
	})
}

// fastRead is what one Ensure call asked of its callbacks: how often each ran, and the
// classes gather was handed (nil means gather never ran).
type fastRead struct {
	stamps, indexes int
	gathered        []ShardClass
}

// fastReadOptions is how a test describes the read: the cheap stamps it computes, the full
// stamps and declared indexes the deferred callbacks would return, and whether it forces a
// rebuild.
type fastReadOptions struct {
	fast    Stamps
	full    Stamps
	indexes []SymbolIndexDeclaration
	refresh bool
}

// seedFast writes a store whose classes carry the full stamps "v1" and the fast stamps
// "f1", the state a previous read left behind, and returns the cache dir with the inputs
// that built it. It declares no indexes, as a store written before they were recorded.
func seedFast(t *testing.T) (string, Inputs) {
	t.Helper()
	cacheDir, in := stampFixture(t)
	_, err := Ensure(context.Background(), cacheDir, BuildOptions{Stamps: stampsAll("v1"), FastStamps: stampsAll("f1")}, AllClasses,
		func([]ShardClass) (Inputs, error) { return in, nil }, nil)
	require.NoError(t, err)
	return cacheDir, in
}

// ensureFast runs Ensure the way the CLI does: FastStamps as a value, the full stamps and
// the declared indexes behind callbacks that count their calls, so a test can assert what a
// read paid for as well as what it returned.
func ensureFast(t *testing.T, cacheDir string, o fastReadOptions, want []ShardClass, in Inputs) (*Graph, *fastRead) {
	t.Helper()
	calls := &fastRead{}
	opts := BuildOptions{
		FastStamps: o.fast,
		Refresh:    o.refresh,
		StampsFunc: func(context.Context) (Stamps, error) {
			calls.stamps++
			return o.full, nil
		},
		IndexesFunc: func(context.Context) ([]SymbolIndexDeclaration, error) {
			calls.indexes++
			return o.indexes, nil
		},
	}
	g, err := Ensure(context.Background(), cacheDir, opts, want, func(stale []ShardClass) (Inputs, error) {
		calls.gathered = stale
		return in, nil
	}, nil)
	require.NoError(t, err)
	return g, calls
}

func declsFixture() []SymbolIndexDeclaration {
	return []SymbolIndexDeclaration{{Project: "pkg/a", Dir: "/ws/pkg/a", Language: "go", Path: "/ws/pkg/a/index.scip"}}
}

func TestEnsureFastStampsMatchingTheManifestPayForNothing(t *testing.T) {
	cacheDir, in := seedFast(t)
	built, _ := ensure(t, cacheDir, stampsAll("v1"), AllClasses, in)
	before := readManifest(t, cacheDir)

	loaded, calls := ensureFast(t, cacheDir, fastReadOptions{
		fast: stampsAll("f1"), full: stampsAll("never"), indexes: declsFixture(),
	}, DefaultClasses, Inputs{})

	assert.Zero(t, calls.stamps, "the whole point of the fast stamp: the model is never evaluated")
	assert.Zero(t, calls.indexes, "a read the fast stamps settle never evaluates the workspace to record indexes")
	assert.Nil(t, calls.gathered)
	assert.Equal(t, outputJSON(t, built), outputJSON(t, loaded), "the stored graph is the assembled one")
	assert.Equal(t, before, readManifest(t, cacheDir), "a settled read leaves the manifest alone")
}

func TestEnsureFastMissWithMatchingFullStampRecordsFastAndIndexesWithoutRebuilding(t *testing.T) {
	cacheDir, in := seedFast(t)
	before := readManifest(t, cacheDir)
	require.False(t, before.IndexesKnown, "a store written without a declaration never recorded them")

	_, calls := ensureFast(t, cacheDir, fastReadOptions{
		fast: stampsAll("f2"), full: stampsAll("v1"), indexes: declsFixture(),
	}, DefaultClasses, in)

	assert.Equal(t, 1, calls.stamps, "an unsettled class is judged by the full stamps, computed once")
	assert.Equal(t, 1, calls.indexes, "the workspace was paid for, so the declarations ride along")
	assert.Nil(t, calls.gathered, "the full stamp matched: nothing is reassembled")
	after := readManifest(t, cacheDir)
	for _, c := range DefaultClasses {
		assert.Equalf(t, "f2"+string(c), after.FastStamps[c], "class %s records the fast stamp that just missed", c)
	}
	for _, c := range LazyClasses {
		assert.Equalf(t, "f1"+string(c), after.FastStamps[c], "class %s was not wanted, so its record stays", c)
	}
	assert.Equal(t, before.Inputs, after.Inputs, "the full stamps are what settled the read, never rewritten")
	assert.Equal(t, before.Shards, after.Shards, "no shard moves")
	assert.Equal(t, declsFixture(), after.Indexes)
	assert.True(t, after.IndexesKnown)

	_, calls = ensureFast(t, cacheDir, fastReadOptions{
		fast: stampsAll("f2"), full: stampsAll("never"), indexes: declsFixture(),
	}, DefaultClasses, Inputs{})
	assert.Zero(t, calls.stamps, "the repaired store settles the next read cheaply")
}

func TestEnsureFastMissRecordsAnEmptyIndexListAsKnown(t *testing.T) {
	cacheDir, in := seedFast(t)

	_, calls := ensureFast(t, cacheDir, fastReadOptions{
		fast: stampsAll("f2"), full: stampsAll("v1"), indexes: nil,
	}, DefaultClasses, in)

	assert.Equal(t, 1, calls.indexes)
	after := readManifest(t, cacheDir)
	assert.True(t, after.IndexesKnown, "a workspace that declares no index is an answer, not a gap")
	assert.Empty(t, after.Indexes)
}

func TestEnsureFastMissKeepsIndexesAlreadyRecorded(t *testing.T) {
	cacheDir, in := seedFast(t)
	ensureFast(t, cacheDir, fastReadOptions{fast: stampsAll("f2"), full: stampsAll("v1"), indexes: declsFixture()}, DefaultClasses, in)

	_, calls := ensureFast(t, cacheDir, fastReadOptions{
		fast: stampsAll("f3"), full: stampsAll("v1"), indexes: []SymbolIndexDeclaration{{Project: "other"}},
	}, DefaultClasses, in)

	assert.Equal(t, 1, calls.stamps)
	assert.Zero(t, calls.indexes, "a manifest that knows its indexes does not ask again")
	after := readManifest(t, cacheDir)
	assert.Equal(t, declsFixture(), after.Indexes)
	assert.Equal(t, "f3"+string(ClassDomain), after.FastStamps[ClassDomain], "the fast stamp still moves")
}

func TestEnsureFastMissWithMissingFullStampRebuildsAndRecordsInputsFastAndIndexes(t *testing.T) {
	cacheDir, in := seedFast(t)
	full := stampsAll("v1")
	full[ClassRuntime] = "v2runtime"
	in.Indexes = declsFixture()

	_, calls := ensureFast(t, cacheDir, fastReadOptions{fast: stampsAll("f2"), full: full}, DefaultClasses, in)

	assert.Equal(t, 1, calls.stamps, "the stamps asked for before the verdict are still asked for once")
	assert.Equal(t, []ShardClass{ClassRuntime}, calls.gathered, "only the class whose full stamp moved is reassembled")
	after := readManifest(t, cacheDir)
	assert.Equal(t, "v2runtime", after.Inputs[ClassRuntime])
	assert.Equal(t, "f2runtime", after.FastStamps[ClassRuntime], "a rebuilt class is stamped with both stamps")
	assert.Equal(t, declsFixture(), after.Indexes, "a rebuild records what gather resolved")
	assert.True(t, after.IndexesKnown)
}

func TestEnsureFastMissWithoutAFastStampFallsBackToTheFullStamps(t *testing.T) {
	cacheDir, in := seedFast(t)
	fast := stampsAll("f1")
	fast[ClassDomain] = ""

	_, calls := ensureFast(t, cacheDir, fastReadOptions{fast: fast, full: stampsAll("v1")}, DefaultClasses, in)

	assert.Equal(t, 1, calls.stamps, "a stamp the caller could not compute never counts as a match")
	assert.Nil(t, calls.gathered)
	assert.Equal(t, "f1"+string(ClassDomain), readManifest(t, cacheDir).FastStamps[ClassDomain],
		"and is never recorded as one either")
}

func TestEnsureFastFreshClassWhoseShardWentBadResolvesTheFullStampsToRebuild(t *testing.T) {
	cacheDir, in := seedFast(t)
	path := filepath.Join(StoreDir(cacheDir), "shards", shardSlug("pkg/a")+".json")
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var sf shardFile
	require.NoError(t, json.Unmarshal(b, &sf))
	sf.Fingerprint = "written-by-another-build"
	b, err = json.Marshal(sf)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0o644))

	_, calls := ensureFast(t, cacheDir, fastReadOptions{fast: stampsAll("f1"), full: stampsAll("v1")}, DefaultClasses, in)

	assert.Equal(t, 1, calls.stamps, "a rebuilt class is stamped with the full stamps, so the fast verdict alone is not enough")
	assert.Equal(t, []ShardClass{ClassDomain}, calls.gathered)
}

func TestEnsureRefreshIgnoresFastStamps(t *testing.T) {
	cacheDir, in := seedFast(t)

	_, calls := ensureFast(t, cacheDir, fastReadOptions{
		fast: stampsAll("f1"), full: stampsAll("v1"), refresh: true,
	}, AllClasses, in)

	assert.Equal(t, 1, calls.stamps, "a matching fast stamp must not settle a forced rebuild")
	assert.ElementsMatch(t, AllClasses, calls.gathered)
}

func TestEnsureFastStampsWithNoManifestFallToTheFullStamps(t *testing.T) {
	cacheDir, in := stampFixture(t)

	_, calls := ensureFast(t, cacheDir, fastReadOptions{fast: stampsAll("f1"), full: stampsAll("v1")}, AllClasses, in)

	assert.Equal(t, 1, calls.stamps)
	assert.ElementsMatch(t, AllClasses, calls.gathered)
	assert.Equal(t, "f1"+string(ClassDomain), readManifest(t, cacheDir).FastStamps[ClassDomain], "the first build records the fast stamp")
}

func TestEnsurePropagatesAStampsFuncError(t *testing.T) {
	cacheDir, in := seedFast(t)
	boom := errors.New("evaluating the workspace failed")

	_, err := Ensure(context.Background(), cacheDir, BuildOptions{
		FastStamps: stampsAll("f2"),
		StampsFunc: func(context.Context) (Stamps, error) { return nil, boom },
	}, DefaultClasses, func([]ShardClass) (Inputs, error) { return in, nil }, nil)

	assert.ErrorIs(t, err, boom)
}

func TestSymbolIndexDeclarationsAnswerOnlyForTheRecordedDomainFastStamp(t *testing.T) {
	cacheDir, in := seedFast(t)
	store := NewStore(cacheDir, true, 0, nil, nil)

	_, ok := store.SymbolIndexDeclarations("f1" + string(ClassDomain))
	assert.False(t, ok, "a store that never recorded indexes cannot vouch for them, whatever the stamp")

	ensureFast(t, cacheDir, fastReadOptions{fast: stampsAll("f2"), full: stampsAll("v1"), indexes: declsFixture()}, DefaultClasses, in)
	domain := "f2" + string(ClassDomain)

	got, ok := store.SymbolIndexDeclarations(domain)
	require.True(t, ok)
	assert.Equal(t, declsFixture(), got)

	_, ok = store.SymbolIndexDeclarations("f1" + string(ClassDomain))
	assert.False(t, ok, "the stamp the declarations were recorded under has moved on")
	_, ok = store.SymbolIndexDeclarations("f2" + string(ClassRuntime))
	assert.False(t, ok, "only the domain stamp vouches for a workspace's indexes")
	_, ok = store.SymbolIndexDeclarations("")
	assert.False(t, ok, "a stamp the caller could not compute matches nothing")

	got[0].Path = "mutated"
	again, ok := store.SymbolIndexDeclarations(domain)
	require.True(t, ok)
	assert.Equal(t, declsFixture(), again, "the caller gets a copy, not the store's slice")
}

func TestSymbolIndexDeclarationsAnEmptyListRecordedCountsAsKnown(t *testing.T) {
	cacheDir, in := seedFast(t)
	ensureFast(t, cacheDir, fastReadOptions{fast: stampsAll("f2"), full: stampsAll("v1")}, DefaultClasses, in)

	got, ok := NewStore(cacheDir, true, 0, nil, nil).SymbolIndexDeclarations("f2" + string(ClassDomain))

	assert.True(t, ok, "a workspace that declares no index answers yes, with nothing")
	assert.Empty(t, got)
}

func TestSymbolIndexDeclarationsWithoutAManifestAnswerNothing(t *testing.T) {
	cacheDir, _ := stampFixture(t)

	_, ok := NewStore(cacheDir, true, 0, nil, nil).SymbolIndexDeclarations("f1" + string(ClassDomain))

	assert.False(t, ok)
}

// A sync that rebuilds one class must carry every other class's record forward; Extra is
// what the next stamp folds for a class this sync did not touch, whether or not the sync
// declares indexes.
func TestStoreSyncKeepsUntouchedClassesExtra(t *testing.T) {
	cacheDir, in := stampFixture(t)
	in.Extra = map[ShardClass][]string{ClassDomain: {"/ws/magus.yaml"}}
	_, err := Ensure(context.Background(), cacheDir, BuildOptions{Stamps: stampsAll("v1")}, AllClasses,
		func([]ShardClass) (Inputs, error) { return in, nil }, nil)
	require.NoError(t, err)
	store := NewStore(cacheDir, true, 0, nil, nil)
	require.Equal(t, []string{"/ws/magus.yaml"}, store.ExtraInputs(ClassDomain))

	stamps := stampsAll("v1")
	stamps[ClassRuntime] = "v2runtime"
	in.Extra = nil
	ensure(t, cacheDir, stamps, DefaultClasses, in)

	assert.Equal(t, []string{"/ws/magus.yaml"}, store.ExtraInputs(ClassDomain), "the domain class was not rebuilt")
}

// The CLI's gather always declares indexes (never nil), including on the very first build
// of a store, when there is no manifest to carry anything forward from.
func TestStoreFirstSyncRecordsDeclaredIndexes(t *testing.T) {
	cacheDir, in := stampFixture(t)
	in.Indexes = declsFixture()

	ensure(t, cacheDir, stampsAll("v1"), AllClasses, in)

	man := readManifest(t, cacheDir)
	assert.Equal(t, declsFixture(), man.Indexes)
	assert.True(t, man.IndexesKnown)
}

// readsStamper is a FastStampsFunc whose domain stamp is a function of the reads it is
// handed and of env, a stand-in for the environment: the stamp a real read computes folds
// the current value of every variable the last evaluation looked up.
func readsStamper(env *map[string]string) func(context.Context, readlog.Reads, bool) Stamps {
	return func(_ context.Context, reads readlog.Reads, known bool) Stamps {
		var b strings.Builder
		if !known {
			b.WriteString("reads:unknown")
		}
		for _, name := range reads.Env {
			b.WriteString(name + "=" + (*env)[name] + ";")
		}
		return Stamps{ClassDomain: "domain:" + b.String(), ClassRuntime: "runtime"}
	}
}

// readsRun is one Ensure over the stamp fixture with the reads machinery wired, counting
// what it had to pay for.
type readsRun struct {
	stamps, gathers int
}

func ensureWithReads(t *testing.T, cacheDir string, in Inputs, reads *readlog.Reads, stamper func(context.Context, readlog.Reads, bool) Stamps, withReadsFunc bool) (*Graph, readsRun) {
	t.Helper()
	var run readsRun
	opts := BuildOptions{
		FastStampsFunc: stamper,
		StampsFunc: func(context.Context) (Stamps, error) {
			run.stamps++
			return stampsAll("v1"), nil
		},
	}
	if withReadsFunc {
		opts.ReadsFunc = func(context.Context) (readlog.Reads, error) { return *reads, nil }
	}
	g, err := Ensure(context.Background(), cacheDir, opts, DefaultClasses, func([]ShardClass) (Inputs, error) {
		run.gathers++
		in.Reads = reads
		return in, nil
	}, nil)
	require.NoError(t, err)
	return g, run
}

func TestEnsureSyncRecordsTheEvaluationReadsAndStampsOverThem(t *testing.T) {
	cacheDir, in := stampFixture(t)
	env := map[string]string{"REGION": "eu"}
	reads := &readlog.Reads{Env: []string{"REGION"}}
	stamper := readsStamper(&env)

	_, first := ensureWithReads(t, cacheDir, in, reads, stamper, true)
	require.Equal(t, 1, first.gathers, "a store with no manifest builds")

	man := readManifest(t, cacheDir)
	assert.Equal(t, *reads, man.Reads, "the sync records what the evaluation read")
	assert.True(t, man.ReadsKnown)
	assert.Equal(t, stamper(context.Background(), *reads, true)[ClassDomain], man.FastStamps[ClassDomain],
		"the recorded fast stamp is computed over the reads just recorded, not over the manifest's old ones")

	_, second := ensureWithReads(t, cacheDir, in, reads, stamper, true)
	assert.Equal(t, readsRun{}, second, "the same environment settles fast: no full stamps, no gather")

	env["REGION"] = "us"
	_, third := ensureWithReads(t, cacheDir, in, reads, stamper, true)
	assert.Equal(t, 1, third.stamps, "a moved variable the evaluation read misses the fast stamp and asks the full one")
	assert.Equal(t, 0, third.gathers, "the full stamp still matches, so nothing is rebuilt")
	assert.Equal(t, stamper(context.Background(), *reads, true)[ClassDomain], readManifest(t, cacheDir).FastStamps[ClassDomain],
		"the fast stamp is re-recorded over the new value, so the next read settles fast again")

	_, fourth := ensureWithReads(t, cacheDir, in, reads, stamper, true)
	assert.Equal(t, readsRun{}, fourth)
}

func TestEnsureRecordsReadsForAManifestThatNeverHadThem(t *testing.T) {
	cacheDir, in := stampFixture(t)
	// A store written before reads existed: plain stamps, no reads, no fast stamps.
	ensure(t, cacheDir, stampsAll("v1"), AllClasses, in)
	before := readManifest(t, cacheDir)
	require.False(t, before.ReadsKnown)

	env := map[string]string{"REGION": "eu"}
	reads := &readlog.Reads{Env: []string{"REGION"}, Files: []string{"/etc/magus/site.toml"}}
	stamper := readsStamper(&env)

	_, run := ensureWithReads(t, cacheDir, in, reads, stamper, true)
	assert.Equal(t, 1, run.stamps, "unknown reads match nothing recorded, so the full stamps are asked")
	assert.Equal(t, 0, run.gathers, "and match, so nothing is rebuilt")
	after := readManifest(t, cacheDir)
	assert.Equal(t, *reads, after.Reads, "the repair records the reads the evaluation made")
	assert.True(t, after.ReadsKnown)
	assert.Equal(t, stamper(context.Background(), *reads, true)[ClassDomain], after.FastStamps[ClassDomain],
		"and the fast stamp over them, not the one computed over unknown reads")

	_, again := ensureWithReads(t, cacheDir, in, reads, stamper, true)
	assert.Equal(t, readsRun{}, again, "from here the read settles fast")
}

func TestEnsureWithoutAReadsFuncNeverRecordsReads(t *testing.T) {
	cacheDir, in := stampFixture(t)
	ensure(t, cacheDir, stampsAll("v1"), AllClasses, in)
	env := map[string]string{}
	stamper := readsStamper(&env)

	_, run := ensureWithReads(t, cacheDir, in, &readlog.Reads{}, stamper, false)
	assert.Equal(t, 1, run.stamps)
	man := readManifest(t, cacheDir)
	assert.False(t, man.ReadsKnown, "a caller that cannot say what was read records nothing")

	_, again := ensureWithReads(t, cacheDir, in, &readlog.Reads{}, stamper, false)
	assert.Equal(t, 1, again.stamps, "and keeps paying the full stamps rather than trusting a stamp over unknown reads")
}

func TestStoreEvaluationReadsDistinguishNoneFromUnknown(t *testing.T) {
	cacheDir, in := stampFixture(t)
	store := NewStore(cacheDir, true, 0, nil, nil)
	_, known := store.EvaluationReads()
	assert.False(t, known, "no manifest")

	ensure(t, cacheDir, stampsAll("v1"), AllClasses, in)
	_, known = store.EvaluationReads()
	assert.False(t, known, "a sync that carried no reads")

	env := map[string]string{}
	ensureWithReads(t, cacheDir, in, &readlog.Reads{}, readsStamper(&env), true)
	reads, known := store.EvaluationReads()
	assert.True(t, known, "an evaluation that read nothing is known to have read nothing")
	assert.Equal(t, readlog.Reads{}, reads)
}
