package knowledge

import (
	"context"
	"os"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// symbolQueryFixture spreads symbols over three directories of the root project, with a
// reference and a call crossing between two of them, a symbol defined nowhere here, and
// both overlays, so a neighborhood can cross shards and pick up overlay attrs.
func symbolQueryFixture(t *testing.T) (string, Inputs) {
	cacheDir, in := buildFixture(t)
	in.Graph.Projects = append(in.Graph.Projects, types.TargetGraphProject{Path: "."})
	in.Symbols = map[string][]types.KnowledgeSymbol{
		".": {
			{Key: "x A().", Label: "Alpha", Language: "go", Source: "a/a.go:3", Defs: []string{"a/a.go"},
				Refs: []types.KnowledgeSymbolRef{{Path: "b/b.go", Count: 2, Lines: []int{4, 9}}}},
			{Key: "x B().", Label: "Beta", Language: "go", Source: "b/b.go:1", Defs: []string{"b/b.go"},
				Calls: []types.KnowledgeSymbolCall{{Key: "x A().", Count: 1}}},
			{Key: "x C().", Label: "Gamma", Language: "go", Source: "c/c.go:1", Defs: []string{"c/c.go"}},
			{Key: "dep Ext().", Label: "Ext", Language: "go", Refs: []types.KnowledgeSymbolRef{{Path: "a/a.go", Count: 1, Lines: []int{7}}}},
		},
	}
	in.Coverage = []FileCoverage{{Path: "a/a.go", Covered: 1, Total: 2}}
	in.AgentContacts = []AgentContact{{Session: "s1", Path: "b/b.go", Read: true}}
	return cacheDir, in
}

func queryJSON(t *testing.T, out types.KnowledgeQueryOutput) string {
	t.Helper()
	b, err := json.Marshal(out)
	require.NoError(t, err)
	return string(b)
}

// fullQuery is the answer QuerySymbols must reproduce: the default graph with every symbol
// shard and overlay merged in, then queried.
func fullQuery(t *testing.T, cacheDir string, in Inputs, input string, budget int) string {
	t.Helper()
	g := build(t, cacheDir, BuildOptions{}, in)
	require.NoError(t, NewStore(cacheDir, false, 0, nil, nil).MergeSymbolShards(context.Background(), g))
	return queryJSON(t, g.Query(input, budget))
}

func TestQuerySymbolsMatchesTheFullyMergedQuery(t *testing.T) {
	cacheDir, in := symbolQueryFixture(t)
	for _, tc := range []struct {
		input  string
		budget int
	}{
		{"Alpha", 0},
		{"kind=symbol", 0},
		{"kind=symbol", 2},
		{"Beta kind=symbol", 0},
		{"language=go", 3},
		{"a.go", 0},
		{"kind=file", 0},
		{"relation=calls", 0},
		{"absentname kind=symbol", 0},
	} {
		want := fullQuery(t, cacheDir, in, tc.input, tc.budget)
		g := build(t, cacheDir, BuildOptions{}, in)
		got, ranked, err := NewStore(cacheDir, false, 0, nil, nil).QuerySymbols(context.Background(), g, tc.input, tc.budget)
		require.NoError(t, err)
		assert.Equalf(t, want, queryJSON(t, got), "query %q budget %d", tc.input, tc.budget)
		assert.NotNil(t, ranked)
	}
}

// A query whose neighborhood never reaches a directory's symbols never decodes that
// directory's shard: deleting it changes nothing.
func TestQuerySymbolsDecodesOnlyTheShardsItsAnswerTouches(t *testing.T) {
	cacheDir, in := symbolQueryFixture(t)
	want := fullQuery(t, cacheDir, in, "Gamma kind=symbol", 2)
	g := build(t, cacheDir, BuildOptions{}, in)
	for _, name := range []string{".@symbols:a", ".@symbols:b"} {
		require.NoError(t, os.Remove(NewStore(cacheDir, false, 0, nil, nil).shardPath(name)))
	}

	got, _, err := NewStore(cacheDir, false, 0, nil, nil).QuerySymbols(context.Background(), g, "Gamma kind=symbol", 2)

	require.NoError(t, err)
	assert.Equal(t, want, queryJSON(t, got))
}

// Without a current symbol names index the query merges every shard, as before.
func TestQuerySymbolsFallsBackWithoutNamesIndex(t *testing.T) {
	cacheDir, in := symbolQueryFixture(t)
	want := fullQuery(t, cacheDir, in, "Alpha", 0)
	g := build(t, cacheDir, BuildOptions{}, in)
	store := NewStore(cacheDir, false, 0, nil, nil)
	require.NoError(t, os.Remove(store.namesPath()))

	got, _, err := store.QuerySymbols(context.Background(), g, "Alpha", 0)

	require.NoError(t, err)
	assert.Equal(t, want, queryJSON(t, got))
}
