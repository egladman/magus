package knowledge

import (
	"fmt"
	"sync"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	assert.Equal(t, "web//spell:go", out.Links[0].Source)
	assert.Equal(t, "web//op:go:go-build", out.Links[0].Target)

	// The original graph is untouched: its IDs stay unqualified.
	for _, n := range g.Nodes() {
		assert.NotContains(t, n.ID, QualifierSep, "Qualified must not mutate the input graph")
	}
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
// its lazy indices (out/in/projPaths) populated yet - the state right after a
// daemon rebuild, before any query has touched the graph.
func buildConcurrencyFixture() *Graph {
	g := NewGraph()
	const projects = 10
	const filesPerProject = 20
	for i := 0; i < projects; i++ {
		proj := fmt.Sprintf("proj%d", i)
		g.AddNode(types.KnowledgeNode{ID: types.KindProject + ":" + proj, Kind: types.KindProject, Label: proj})

		var prev string
		for j := 0; j < filesPerProject; j++ {
			src := fmt.Sprintf("%s/file%d.go", proj, j)
			id := types.KindFile + ":" + src
			g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindFile, Label: src, Source: src})
			g.AddEdge(types.KnowledgeEdge{
				Source: types.KindProject + ":" + proj, Target: id, Relation: types.RelationContains,
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
			Source: types.KindFile + ":" + src0, Target: sym, Relation: types.RelationDefines,
			Confidence: types.ConfidenceExtracted,
		})
	}
	return g
}

// TestConcurrentQueriesDoNotRaceOnLazyIndices reproduces the daemon's warm-graph
// scenario: one *Graph, published with no lazy indices built yet, handed to many
// concurrent readers at once (concurrent HTTP/MCP requests hitting the SAME graph
// right after a rebuild). ensureAdj (out/in) and projectPaths (projPaths) used to
// build those indices behind a bare nil check, so two goroutines racing the first
// query after a rebuild could write g.out/g.in/g.projPaths at the same time - a
// concurrent map write, which is an unrecoverable Go runtime fatal (crashes the
// whole daemon process), not a recoverable panic. Run with -race: before the fix
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
			_ = g.Neighborhood([]string{types.KindProject + ":" + proj}, 50, nil)
			_, _ = g.Explain(types.KindProject + ":" + proj)

			// Refs resolves a symbol then calls ensureAdj.
			_, _ = g.Refs(sym)

			// A project: filter drives projectOf -> g.projectPaths() for every node
			// scanned by Resolve.
			_ = g.Query("project:"+proj, 50)

			// A relation-only query drives touchesRelation, which also calls
			// ensureAdj.
			_ = g.Query("relation:contains", 50)

			// Path drives shortestPath, another ensureAdj caller.
			_, _ = g.Path(types.KindProject+":"+proj, sym)
		}()
	}
	wg.Wait()
}
