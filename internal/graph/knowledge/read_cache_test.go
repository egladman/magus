package knowledge

import (
	"context"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func enableReadCache(t *testing.T, limit int64) {
	t.Helper()
	SetReadCacheLimit(limit)
	t.Cleanup(func() { SetReadCacheLimit(0) })
}

// symbolRead is what a server's symbol read does: the default graph from the store, the
// symbol classes brought up to date, then every symbol shard merged in.
func symbolRead(t *testing.T, cacheDir string, stamps Stamps, in Inputs) *Graph {
	t.Helper()
	g, _ := ensure(t, cacheDir, stamps, DefaultClasses, in)
	ensure(t, cacheDir, stamps, SymbolClasses, in)
	require.NoError(t, NewStore(cacheDir, false, 0, nil, nil).MergeSymbolShards(context.Background(), g))
	return g
}

// uncached runs read with the read cache swapped for an empty, disabled one, leaving the
// real one as it was.
func uncached[T any](read func() T) T {
	saved := readCache
	readCache = &cacheState{}
	defer func() { readCache = saved }()
	return read()
}

// assertCacheCurrent checks the cache holds nothing for cacheDir's store that its manifest
// no longer names, and stays within its limit.
func assertCacheCurrent(t *testing.T, cacheDir string) {
	t.Helper()
	man := readManifest(t, cacheDir)
	readCache.mu.Lock()
	defer readCache.mu.Unlock()
	for path, e := range readCache.files {
		if e.name == "" {
			continue
		}
		meta, ok := man.Shards[e.name]
		assert.Truef(t, ok && meta.Fingerprint == e.fingerprint, "%s is cached under a fingerprint the manifest no longer names", path)
	}
	assert.LessOrEqual(t, readCache.used, readCache.limit)
	assert.LessOrEqual(t, len(readCache.merged), 1)
}

func TestReadCacheAnswersAsAnUncachedRead(t *testing.T) {
	cacheDir, in := symbolQueryFixture(t)
	enableReadCache(t, 64<<20)
	stamps := stampsAll("v1")
	ensure(t, cacheDir, stamps, AllClasses, in)
	want := uncached(func() string { return outputJSON(t, symbolRead(t, cacheDir, stamps, in)) })

	first := symbolRead(t, cacheDir, stamps, in)
	second := symbolRead(t, cacheDir, stamps, in)

	assert.Equal(t, want, outputJSON(t, first))
	assert.Equal(t, want, outputJSON(t, second))
	assert.True(t, second.shared, "the second read adopts the merged graph the first one cached")
	assertCacheCurrent(t, cacheDir)
}

// Every input a symbol read depends on moves the read cache's answer with it: a cached
// answer from before the change is never served after it.
func TestReadCacheFollowsEveryInputItCaches(t *testing.T) {
	for _, tc := range []struct {
		name string
		// change edits the inputs and moves the stamps that edit would move, then syncs
		// whatever a build would.
		change func(t *testing.T, cacheDir string, stamps Stamps, in *Inputs)
		// fact is in the read's answer after the change.
		fact string
	}{
		{
			name: "a source edit",
			change: func(t *testing.T, cacheDir string, stamps Stamps, in *Inputs) {
				in.Symbols["."][2].Label = "GammaRenamed"
				stamps[ClassSymbols] = "v2symbols"
			},
			fact: "GammaRenamed",
		},
		{
			name: "an index rebuild",
			change: func(t *testing.T, cacheDir string, stamps Stamps, in *Inputs) {
				syms := in.Symbols["."][:2:2]
				in.Symbols["."] = append(syms, types.KnowledgeSymbol{Key: "x D().", Label: "Delta", Language: "go", Source: "d/d.go:1", Defs: []string{"d/d.go"}})
				stamps[ClassSymbols] = "v2symbols"
			},
			fact: "Delta",
		},
		{
			name: "a session append",
			change: func(t *testing.T, cacheDir string, stamps Stamps, in *Inputs) {
				in.AgentContacts = append(in.AgentContacts, AgentContact{Session: "s2", Path: "c/c.go", Write: true})
				stamps[ClassSession] = "v2session"
				// A symbol read leaves @session as stored; a build rewrites it.
				ensure(t, cacheDir, stamps, AllClasses, *in)
			},
			fact: `"agent_writes":"1"`,
		},
		{
			name: "a domain edit",
			change: func(t *testing.T, cacheDir string, stamps Stamps, in *Inputs) {
				in.Graph.Projects = append(in.Graph.Projects, types.TargetGraphProject{Path: "pkg/new", Engine: "buzz"})
				stamps[ClassDomain] = "v2domain"
			},
			fact: `"project:pkg/new"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cacheDir, in := symbolQueryFixture(t)
			enableReadCache(t, 64<<20)
			stamps := stampsAll("v1")
			ensure(t, cacheDir, stamps, AllClasses, in)
			before := outputJSON(t, symbolRead(t, cacheDir, stamps, in))
			require.NotContains(t, before, tc.fact)

			tc.change(t, cacheDir, stamps, &in)
			got := outputJSON(t, symbolRead(t, cacheDir, stamps, in))

			assert.Contains(t, got, tc.fact)
			assert.Equal(t, uncached(func() string { return outputJSON(t, symbolRead(t, cacheDir, stamps, in)) }), got)
			assertCacheCurrent(t, cacheDir)
		})
	}
}

// A graph that adopted the cached merge is the caller's to change, and changing it leaves
// the cached graph, and so the next read, as they were.
func TestReadCacheCopiesTheMergedGraphOnWrite(t *testing.T) {
	cacheDir, in := symbolQueryFixture(t)
	enableReadCache(t, 64<<20)
	stamps := stampsAll("v1")
	ensure(t, cacheDir, stamps, AllClasses, in)
	want := outputJSON(t, symbolRead(t, cacheDir, stamps, in))

	g := symbolRead(t, cacheDir, stamps, in)
	require.True(t, g.shared)
	for _, n := range g.Nodes() {
		g.AddNode(types.KnowledgeNode{ID: n.ID, Attrs: map[string]string{"written_by_a_caller": "1"}})
	}
	g.AddEdge(extractedEdge("symbol:x A().", "symbol:x C().", types.RelationCalls, "a caller"))
	require.NotEqual(t, want, outputJSON(t, g))

	assert.Equal(t, want, outputJSON(t, symbolRead(t, cacheDir, stamps, in)))
}

func TestReadCacheQuerySymbolsMatchesTheUncachedQuery(t *testing.T) {
	cacheDir, in := symbolQueryFixture(t)
	enableReadCache(t, 64<<20)
	stamps := stampsAll("v1")
	ensure(t, cacheDir, stamps, AllClasses, in)
	query := func(input string) (string, *Graph) {
		g, _ := ensure(t, cacheDir, stamps, DefaultClasses, in)
		out, ranked, err := NewStore(cacheDir, false, 0, nil, nil).QuerySymbols(context.Background(), g, input, 0)
		require.NoError(t, err)
		return queryJSON(t, out), ranked
	}
	for _, input := range []string{"Alpha", "kind=symbol", "Beta kind=symbol", "a.go", "relation=calls", "absentname kind=symbol"} {
		want := uncached(func() string { s, _ := query(input); return s })
		for range 2 {
			got, ranked := query(input)
			assert.Equalf(t, want, got, "query %q", input)
			assert.NotNil(t, ranked)
		}
	}
}

// A limit smaller than any file caches nothing, and the reads are still the uncached ones.
func TestReadCacheHoldsNothingOverItsLimit(t *testing.T) {
	cacheDir, in := symbolQueryFixture(t)
	enableReadCache(t, 1)
	stamps := stampsAll("v1")
	ensure(t, cacheDir, stamps, AllClasses, in)
	want := uncached(func() string { return outputJSON(t, symbolRead(t, cacheDir, stamps, in)) })

	got := outputJSON(t, symbolRead(t, cacheDir, stamps, in))

	assert.Equal(t, want, got)
	assert.Empty(t, readCache.files)
	assert.Empty(t, readCache.merged)
}
