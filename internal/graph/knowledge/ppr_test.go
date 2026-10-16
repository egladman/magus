package knowledge

import (
	"fmt"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/egladman/magus/internal/textrank"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pprTestWeights() textrank.PPRWeights {
	return textrank.PPRWeights{
		Restart:    0.15,
		Iterations: 50,
		Tolerance:  1e-9,
		MaxNodes:   100,
		Edges:      map[string]float64{"calls": 1, "references": 1},
	}
}

func pprTestGraph(nodes []string, edges []types.KnowledgeEdge) *Graph {
	g := NewGraph()
	for _, id := range nodes {
		g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindSymbol, Label: id})
	}
	for _, e := range edges {
		g.AddEdge(e)
	}
	return g
}

func pprEdge(source, target string, rel types.RelationID) types.KnowledgeEdge {
	return types.KnowledgeEdge{Source: source, Target: target, Relation: rel}
}

func pprScoreByID(res PPRResult) map[string]PPRScore {
	out := map[string]PPRScore{}
	for _, s := range res.Scores {
		out[s.ID] = s
	}
	return out
}

func TestPersonalizedPageRankRanksNeighborsOfTheSeed(t *testing.T) {
	g := pprTestGraph([]string{"seed", "a", "b", "c", "other", "lonely"}, []types.KnowledgeEdge{
		pprEdge("seed", "a", types.RelationCalls),
		pprEdge("b", "a", types.RelationCalls),
		pprEdge("b", "c", types.RelationReferences),
		pprEdge("other", "c", types.RelationCalls),
	})

	res, err := g.PersonalizedPageRank(map[string]float64{"seed": 1}, pprTestWeights())
	require.NoError(t, err)

	ids := make([]string, len(res.Scores))
	total := 0.0
	for i, s := range res.Scores {
		ids[i] = s.ID
		total += s.Score
		assert.Equal(t, []string{"seed"}, s.Seeds, s.ID)
	}
	// The seed's only neighbor takes all of its outflow and edges past it; past that, rank
	// falls with distance, and an unconnected node is never walked.
	assert.Equal(t, []string{"a", "seed", "b", "c", "other"}, ids)
	assert.InDelta(t, 1, total, 1e-9, "the walk keeps all the restart mass")
	assert.Equal(t, 5, res.Walked)
	assert.False(t, res.Truncated)
	assert.Empty(t, res.Unplaced)
	assert.Empty(t, res.Skipped)
}

func TestPersonalizedPageRankNamesTheSeedsThatContributedMost(t *testing.T) {
	g := pprTestGraph([]string{"s1", "s2", "s3", "s4", "hub"}, []types.KnowledgeEdge{
		pprEdge("s1", "hub", types.RelationCalls),
		pprEdge("s2", "hub", types.RelationCalls),
		pprEdge("s3", "hub", types.RelationCalls),
		pprEdge("s4", "hub", types.RelationCalls),
	})

	res, err := g.PersonalizedPageRank(map[string]float64{"s1": 1, "s2": 4, "s3": 2, "s4": 2}, pprTestWeights())
	require.NoError(t, err)

	byID := pprScoreByID(res)
	assert.Equal(t, []string{"s2", "s3", "s4"}, byID["hub"].Seeds, "by contribution, then id among equals")
	assert.Equal(t, "s2", byID["s2"].Seeds[0], "a seed is its own largest contributor")
}

func TestPersonalizedPageRankIsDeterministic(t *testing.T) {
	nodes := make([]string, 40)
	for i := range nodes {
		nodes[i] = fmt.Sprintf("n%02d", i)
	}
	var edges []types.KnowledgeEdge
	for i := range nodes {
		edges = append(edges,
			pprEdge(nodes[i], nodes[(i*7+3)%len(nodes)], types.RelationCalls),
			pprEdge(nodes[i], nodes[(i*11+5)%len(nodes)], types.RelationReferences))
	}
	seeds := map[string]float64{"n00": 1, "n13": 0.5, "n27": 2}

	run := func(shuffle uint64) string {
		ns, es := append([]string(nil), nodes...), append([]types.KnowledgeEdge(nil), edges...)
		r := rand.New(rand.NewPCG(shuffle, shuffle))
		r.Shuffle(len(ns), func(i, j int) { ns[i], ns[j] = ns[j], ns[i] })
		r.Shuffle(len(es), func(i, j int) { es[i], es[j] = es[j], es[i] })
		res, err := pprTestGraph(ns, es).PersonalizedPageRank(seeds, pprTestWeights())
		require.NoError(t, err)
		return fmt.Sprintf("%#v", res)
	}

	want := run(1)
	for shuffle := uint64(2); shuffle < 6; shuffle++ {
		assert.Equal(t, want, run(shuffle), "insertion order %d", shuffle)
	}
}

func TestPersonalizedPageRankWalksOnlyWeightedRelations(t *testing.T) {
	g := pprTestGraph([]string{"seed", "called", "owned", "contained"}, []types.KnowledgeEdge{
		pprEdge("seed", "called", types.RelationCalls),
		pprEdge("owned", "seed", types.RelationOwns),
		pprEdge("contained", "seed", types.RelationContains),
	})
	w := pprTestWeights()
	w.Edges = map[string]float64{"calls": 1, "contains": 0}

	res, err := g.PersonalizedPageRank(map[string]float64{"seed": 1}, w)
	require.NoError(t, err)

	byID := pprScoreByID(res)
	assert.Contains(t, byID, "called")
	assert.NotContains(t, byID, "owned", "a relation absent from the weights is not walked")
	assert.NotContains(t, byID, "contained", "a relation weighted 0 is not walked")
	res.Scores = nil
	assert.Equal(t, PPRResult{Walked: 2, Skipped: []string{"contains"}}, res)
}

func TestPersonalizedPageRankReportsUnplacedSeedsAndSkippedRelations(t *testing.T) {
	g := pprTestGraph([]string{"file:a.go", "file:b.go"}, []types.KnowledgeEdge{
		pprEdge("file:a.go", "file:b.go", types.RelationReferences),
	})
	w := pprTestWeights()
	w.Edges = map[string]float64{"references": 1, "co_changes": 0.5, "calls": 1}

	res, err := g.PersonalizedPageRank(map[string]float64{"file:a.go": 1, "file:new.go": 1}, w)
	require.NoError(t, err)

	byID := pprScoreByID(res)
	assert.InDelta(t, 1, byID["file:a.go"].Score+byID["file:b.go"].Score, 1e-9, "an unplaced seed carries no mass")
	res.Scores = nil
	assert.Equal(t, PPRResult{Unplaced: []string{"file:new.go"}, Walked: 2, Skipped: []string{"calls", "co_changes"}}, res)
}

func TestPersonalizedPageRankRefusesBadWeights(t *testing.T) {
	g := pprTestGraph([]string{"seed"}, nil)
	seeds := map[string]float64{"seed": 1}
	tests := []struct {
		name  string
		edit  func(*textrank.PPRWeights)
		seeds map[string]float64
		want  string
	}{
		{name: "restart zero", edit: func(w *textrank.PPRWeights) { w.Restart = 0 }, want: "ppr.restart: must be between 0 and 1 exclusive, got 0"},
		{name: "restart one", edit: func(w *textrank.PPRWeights) { w.Restart = 1 }, want: "ppr.restart: must be between 0 and 1 exclusive, got 1"},
		{name: "restart NaN", edit: func(w *textrank.PPRWeights) { w.Restart = math.NaN() }, want: "ppr.restart: must be between 0 and 1 exclusive, got NaN"},
		{name: "no iterations", edit: func(w *textrank.PPRWeights) { w.Iterations = 0 }, want: "ppr.iterations: must be at least 1, got 0"},
		{name: "negative tolerance", edit: func(w *textrank.PPRWeights) { w.Tolerance = -1 }, want: "ppr.tolerance: must not be negative, got -1"},
		{name: "no nodes", edit: func(w *textrank.PPRWeights) { w.MaxNodes = 0 }, want: "ppr.max_nodes: must be at least 1, got 0"},
		{name: "no edges", edit: func(w *textrank.PPRWeights) { w.Edges = nil }, want: "ppr.edges: names no relation, so nothing can be walked"},
		{name: "unknown relation", edit: func(w *textrank.PPRWeights) { w.Edges["co_change"] = 1 }, want: "ppr.edges.co_change: no such relation"},
		{name: "negative edge", edit: func(w *textrank.PPRWeights) { w.Edges["calls"] = -1 }, want: "ppr.edges.calls: must be a finite weight of at least 0, got -1"},
		{name: "infinite edge", edit: func(w *textrank.PPRWeights) { w.Edges["calls"] = math.Inf(1) }, want: "ppr.edges.calls: must be a finite weight of at least 0, got +Inf"},
		{name: "negative seed", seeds: map[string]float64{"seed": -1}, want: "ppr seed seed: must be a finite weight of at least 0, got -1"},
		{name: "no seeds", seeds: map[string]float64{}, want: "ppr seeds: none given"},
		{name: "all seeds unplaced", seeds: map[string]float64{"gone": 1, "new": 1}, want: "ppr seeds: none of the 2 seeds is a node in the graph"},
		{name: "seeds weigh nothing", seeds: map[string]float64{"seed": 0}, want: "ppr seeds: every walked seed weighs 0, so no node can be ranked"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := pprTestWeights()
			if tt.edit != nil {
				tt.edit(&w)
			}
			s := seeds
			if tt.seeds != nil {
				s = tt.seeds
			}
			res, err := g.PersonalizedPageRank(s, w)
			require.EqualError(t, err, tt.want)
			assert.Equal(t, PPRResult{}, res)
		})
	}
}

func TestPersonalizedPageRankTruncatesAtMaxNodes(t *testing.T) {
	nodes := []string{"seed"}
	var edges []types.KnowledgeEdge
	for i := range 10 {
		leaf := fmt.Sprintf("leaf%d", i)
		nodes = append(nodes, leaf)
		edges = append(edges, pprEdge("seed", leaf, types.RelationCalls))
	}
	g := pprTestGraph(nodes, edges)

	w := pprTestWeights()
	w.MaxNodes = 4
	res, err := g.PersonalizedPageRank(map[string]float64{"seed": 1}, w)
	require.NoError(t, err)
	assert.Equal(t, 4, res.Walked)
	assert.True(t, res.Truncated)
	ids := make([]string, len(res.Scores))
	for i, s := range res.Scores {
		ids[i] = s.ID
	}
	assert.Equal(t, []string{"seed", "leaf0", "leaf1", "leaf2"}, ids, "the frontier is admitted in sorted order")

	w.MaxNodes = 11
	res, err = g.PersonalizedPageRank(map[string]float64{"seed": 1}, w)
	require.NoError(t, err)
	assert.Equal(t, 11, res.Walked)
	assert.False(t, res.Truncated, "a walk that fits exactly is not truncated")
}

func BenchmarkPersonalizedPageRank(b *testing.B) {
	const n = 5000
	g := NewGraph()
	for i := range n {
		g.AddNode(types.KnowledgeNode{ID: fmt.Sprintf("sym:%04d", i), Kind: types.KindSymbol})
	}
	for i := range n {
		from := fmt.Sprintf("sym:%04d", i)
		g.AddEdge(pprEdge(from, fmt.Sprintf("sym:%04d", (i*7+1)%n), types.RelationCalls))
		g.AddEdge(pprEdge(from, fmt.Sprintf("sym:%04d", (i*13+5)%n), types.RelationReferences))
		g.AddEdge(pprEdge(from, fmt.Sprintf("sym:%04d", (i+1)%n), types.RelationImplements))
	}
	weights, err := textrank.LoadWeights()
	if err != nil {
		b.Fatal(err)
	}
	seeds := map[string]float64{"sym:0000": 1, "sym:1234": 0.8, "sym:2500": 0.6, "sym:3999": 0.4, "sym:4321": 0.2}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := g.PersonalizedPageRank(seeds, weights.PPR); err != nil {
			b.Fatal(err)
		}
	}
}
