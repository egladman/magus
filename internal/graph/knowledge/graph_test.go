package knowledge

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func TestMain(m *testing.M) {
	testkit.Main(m, "MAGUS_CONFORMANCE_GRAPH", "MAGUS_CONFORMANCE_HISTORY")
}

func TestQualified(t *testing.T) {
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "spell:go", Kind: types.KindSpell, Label: "go"})
	g.AddNode(types.KnowledgeNode{ID: "op:go:go-build", Kind: types.KindOp, Label: "go-build"})
	g.AddEdge(types.KnowledgeEdge{Source: "spell:go", Target: "op:go:go-build", Relation: types.RelationContains})

	q := Qualified(g, "web")
	out := q.Output()

	ids := make([]string, len(out.Nodes))
	for i, n := range out.Nodes {
		ids[i] = n.ID
	}
	assert.ElementsMatch(t, []string{"web//spell:go", "web//op:go:go-build"}, ids)
	require.Len(t, out.Links, 1)
	assert.Equal(t, types.KnowledgeEdge{
		Source: "web//spell:go", Target: "web//op:go:go-build", Relation: types.RelationContains,
	}, out.Links[0])

	// The original graph is untouched: its IDs stay unqualified.
	for _, n := range g.Nodes() {
		assert.NotContains(t, n.ID, QualifierSep, "Qualified must not mutate the input graph")
	}
}

// Edge attrs get the control-char strip node attrs get, never alias the caller's map, and
// fold across copies of one edge the way node attrs do: the kept edge's values win and it
// takes the keys only the other carries.
func TestDirEdgeAttrsAreSanitizedAndFilledOnMerge(t *testing.T) {
	g := NewGraph()
	in := map[string]string{types.AttrTransport: "ht\x1btp"}
	declared := types.KnowledgeEdge{Source: "dir:a", Target: "dir:b", Relation: types.RelationImports,
		Confidence: types.ConfidenceInferred, Score: 0.5, Attrs: in}
	g.AddEdge(declared)
	in[types.AttrTransport] = "mutated"
	require.Len(t, g.Edges(), 1)
	assert.Equal(t, map[string]string{types.AttrTransport: "http"}, g.Edges()[0].Attrs)

	g.AddEdge(types.KnowledgeEdge{Source: "dir:a", Target: "dir:b", Relation: types.RelationImports,
		Confidence: types.ConfidenceExtracted, Score: 1, Attrs: map[string]string{types.AttrLanguage: "go", types.AttrTransport: "grpc"}})
	assert.Equal(t, types.KnowledgeEdge{Source: "dir:a", Target: "dir:b", Relation: types.RelationImports,
		Confidence: types.ConfidenceExtracted, Score: 1,
		Attrs: map[string]string{types.AttrLanguage: "go", types.AttrTransport: "grpc"}}, g.Edges()[0])

	g.ensureAdj()
	g.AddEdge(declared)
	assert.Equal(t, map[string]string{types.AttrLanguage: "go", types.AttrTransport: "grpc"}, g.Edges()[0].Attrs,
		"a weaker copy fills nothing the kept edge already holds")
	g.AddEdge(types.KnowledgeEdge{Source: "dir:a", Target: "dir:b", Relation: types.RelationImports,
		Confidence: types.ConfidenceInferred, Score: 0.1, Attrs: map[string]string{types.AttrBuildTags: "js,wasm"}})
	want := map[string]string{types.AttrLanguage: "go", types.AttrTransport: "grpc", types.AttrBuildTags: "js,wasm"}
	assert.Equal(t, want, g.Edges()[0].Attrs)
	g.ensureAdj()
	assert.Equal(t, want, g.out["dir:a"][0].Attrs, "the adjacency index sees the filled attrs")

	g.AddEdge(types.KnowledgeEdge{Source: "dir:a", Target: "dir:c", Relation: types.RelationImports,
		Confidence: types.ConfidenceExtracted, Score: 1, Attrs: map[string]string{}})
	assert.Nil(t, g.Edges()[1].Attrs, "an empty map normalizes to absent")
}

func TestUnionIntoDistinctWorkspaces(t *testing.T) {
	a := NewGraph()
	a.AddNode(types.KnowledgeNode{ID: "spell:go", Kind: types.KindSpell})
	b := NewGraph()
	b.AddNode(types.KnowledgeNode{ID: "spell:go", Kind: types.KindSpell}) // same ID in another repo

	merged := NewGraph()
	UnionInto(merged, Qualified(a, "api"))
	UnionInto(merged, Qualified(b, "web"))

	out := merged.Output()
	// Qualification keeps the same-named spell from two repos as distinct nodes.
	assert.Equal(t, 2, out.NodeCount)
	_, okA := nodeByID(out, "api//spell:go")
	_, okB := nodeByID(out, "web//spell:go")
	assert.True(t, okA && okB, "both workspaces' nodes present and distinct")
}

// buildConcurrencyFixture returns a Graph with several hundred edges and none of
// its lazy indices (out/in and the search index) populated yet, the state right after a
// server rebuild, before any query has touched the graph.
func buildConcurrencyFixture() *Graph {
	g := NewGraph()
	const projects = 10
	const filesPerProject = 20
	for i := 0; i < projects; i++ {
		proj := fmt.Sprintf("proj%d", i)
		g.AddNode(types.KnowledgeNode{ID: fmt.Sprintf("%s:%s", types.KindProject, proj), Kind: types.KindProject, Label: proj})

		var prev string
		for j := 0; j < filesPerProject; j++ {
			src := fmt.Sprintf("%s/file%d.go", proj, j)
			id := fmt.Sprintf("%s:%s", types.KindFile, src)
			g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindFile, Label: src, Source: src})
			g.AddEdge(types.KnowledgeEdge{
				Source: fmt.Sprintf("%s:%s", types.KindProject, proj), Target: id, Relation: types.RelationContains,
				Confidence: types.ConfidenceExtracted,
			})
			if prev != "" {
				g.AddEdge(types.KnowledgeEdge{
					Source: prev, Target: id, Relation: types.RelationReferences,
					Confidence: types.ConfidenceExtracted,
				})
			}
			prev = id
		}

		// One symbol per project, defined by its first file, so Refs has something to
		// resolve and ensureAdj has out/in entries to serve for it.
		sym := fmt.Sprintf("symbol:%s.Func", proj)
		src0 := fmt.Sprintf("%s/file0.go", proj)
		g.AddNode(types.KnowledgeNode{ID: sym, Kind: types.KindSymbol, Label: "Func", Source: src0 + ":1"})
		g.AddEdge(types.KnowledgeEdge{
			Source: fmt.Sprintf("%s:%s", types.KindFile, src0), Target: sym, Relation: types.RelationDefines,
			Confidence: types.ConfidenceExtracted,
		})
	}
	return g
}

// TestConcurrentQueriesDoNotRaceOnLazyIndices reproduces the server's warm-graph
// scenario: one *Graph, published with no lazy indices built yet, handed to many
// concurrent readers at once (concurrent HTTP/MCP requests hitting the SAME graph
// right after a rebuild). ensureAdj (out/in) and the project-path index (now part of
// ensureSearchIndex) used to build those indices behind a bare nil check, so two goroutines
// racing the first query after a rebuild could write g.out/g.in and the index at the same time, a
// concurrent map write, which is an unrecoverable Go runtime fatal (crashes the
// whole server process), not a recoverable panic. Run with -race: before the fix
// this trips the race detector; after, it is clean.
func TestConcurrentQueriesDoNotRaceOnLazyIndices(t *testing.T) {
	g := buildConcurrencyFixture()

	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			proj := fmt.Sprintf("proj%d", i%10)
			sym := fmt.Sprintf("symbol:%s.Func", proj)

			// Neighborhood and Explain both call ensureAdj (out/in).
			_ = g.Neighborhood([]string{fmt.Sprintf("%s:%s", types.KindProject, proj)}, 50, nil)
			_, _ = g.Explain(fmt.Sprintf("%s:%s", types.KindProject, proj))

			// Refs resolves a symbol then calls ensureAdj.
			_, _ = g.Refs(sym)

			// A project: filter drives the search index and its lazily built project
			// owners, which every concurrent Resolve shares.
			_ = g.Query("project:"+proj, 50)

			// A relation-only query drives touchesRelation, which also calls
			// ensureAdj.
			_ = g.Query("relation:contains", 50)

			// Path drives shortestPath, another ensureAdj caller.
			_, _ = g.Path(fmt.Sprintf("%s:%s", types.KindProject, proj), sym)
		}()
	}
	wg.Wait()
}

// cloneSuite clones the graph a store read returns, as a symbol read
// clones the warm graph before merging into it.
type cloneSuite struct {
	suite.Suite
	cacheDir string
	stamps   Stamps
	in       Inputs
}

func TestCloneSuite(t *testing.T) {
	suite.Run(t, new(cloneSuite))
}

func (s *cloneSuite) SetupTest() {
	s.cacheDir, s.in = symbolQueryFixture(s.T())
	enableReadCache(s.T(), 64<<20)
	s.stamps = stampsAll("v1")
	ensure(s.T(), s.cacheDir, s.stamps, AllClasses, s.in)
}

// published is the default graph as the warm graph publishes it.
func (s *cloneSuite) published() *Graph {
	g, _ := ensure(s.T(), s.cacheDir, s.stamps, DefaultClasses, s.in)
	g.MarkShared()
	return g
}

func (s *cloneSuite) merged(base *Graph) *Graph {
	g := base.Clone()
	s.Require().NoError(NewStore(s.cacheDir, false, 0, nil, nil).MergeSymbolShards(context.Background(), g))
	return g
}

func (s *cloneSuite) TestACloneMergesWhatAFreshReadMerges() {
	want := uncached(func() string { return outputJSON(s.T(), symbolRead(s.T(), s.cacheDir, s.stamps, s.in)) })
	s.Equal(want, outputJSON(s.T(), s.merged(s.published())))
}

func (s *cloneSuite) TestASecondCloneAdoptsTheMergeTheFirstCached() {
	base := s.published()
	s.Require().NotEmpty(base.base, "the store read names its shards")
	first := s.merged(base)
	second := s.merged(base)
	s.True(second.shared, "the second merge adopts the first one's cached graph")
	s.Equal(outputJSON(s.T(), first), outputJSON(s.T(), second))
}

func (s *cloneSuite) TestAMergeLeavesTheSourceUnchanged() {
	base := s.published()
	before := outputJSON(s.T(), base)
	s.merged(base)
	s.Equal(before, outputJSON(s.T(), base))
	s.False(base.HasSymbols())
}

func (s *cloneSuite) TestACloneOfAnUnsharedGraphNeverSharesItsMaps() {
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "dir:a", Kind: types.KindDir})
	c := g.Clone()
	c.AddNode(types.KnowledgeNode{ID: "dir:b", Kind: types.KindDir})
	g.AddNode(types.KnowledgeNode{ID: "dir:c", Kind: types.KindDir})
	s.False(g.shared, "Clone never writes its source")
	s.Equal([]string{"dir:a", "dir:c"}, sortedNodeIDs(g))
	s.Equal([]string{"dir:a", "dir:b"}, sortedNodeIDs(c))
}

// copySource is a published default graph of a mid-size workspace.
func copySource() *Graph {
	g := NewGraph()
	for i := range 20000 {
		id := fmt.Sprintf("file:pkg%d/f%d.go", i%400, i)
		g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindFile, Label: id, Doc: "a file"})
		g.AddEdge(extractedEdge(fmt.Sprintf("dir:pkg%d", i%400), id, types.RelationContains, "fs"))
		g.AddEdge(extractedEdge(id, fmt.Sprintf("file:pkg%d/f%d.go", (i+1)%400, i+1), types.RelationReferences, "scip"))
	}
	g.MarkShared()
	return g
}

func BenchmarkCopyForASymbolMergeByUnion(b *testing.B) {
	src := copySource()
	for b.Loop() {
		g := NewGraph()
		UnionInto(g, src)
		g.AddNode(types.KnowledgeNode{ID: "symbol:x A().", Kind: types.KindSymbol})
	}
}

func BenchmarkCopyForASymbolMergeByClone(b *testing.B) {
	src := copySource()
	for b.Loop() {
		g := src.Clone()
		g.AddNode(types.KnowledgeNode{ID: "symbol:x A().", Kind: types.KindSymbol})
	}
}

func sortedNodeIDs(g *Graph) []string {
	var ids []string
	for _, n := range g.Nodes() {
		ids = append(ids, n.ID)
	}
	return ids
}
