package knowledge

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/require"
)

func TestFileFacts(t *testing.T) {
	g := NewGraph()
	// A changed file with file-level coverage and two defined symbols.
	g.AddNode(types.KnowledgeNode{
		ID: fileID("a.go"), Kind: types.KindFile, Label: "a.go",
		Attrs: map[string]string{AttrCoverage: "0.50", AttrCoveredStmts: "5", AttrTotalStmts: "10"},
	})
	g.AddNode(types.KnowledgeNode{ID: symbolID("a.Foo"), Kind: types.KindSymbol, Label: "Foo", Source: "a.go:3"})
	g.AddNode(types.KnowledgeNode{
		ID: symbolID("a.Bar"), Kind: types.KindSymbol, Label: "Bar", Source: "a.go:20",
		Attrs: map[string]string{AttrCoverage: "1.00", AttrCoveredStmts: "4", AttrTotalStmts: "4"},
	})
	g.AddEdge(types.KnowledgeEdge{Source: fileID("a.go"), Target: symbolID("a.Foo"), Relation: types.RelationDefines})
	g.AddEdge(types.KnowledgeEdge{Source: fileID("a.go"), Target: symbolID("a.Bar"), Relation: types.RelationDefines})
	// Foo is referenced from two files (3 occurrences total); Bar from one (7 occurrences).
	g.AddEdge(types.KnowledgeEdge{Source: fileID("x.go"), Target: symbolID("a.Foo"), Relation: types.RelationReferences, Provenance: "scip count=2 lines=10,20"})
	g.AddEdge(types.KnowledgeEdge{Source: fileID("y.go"), Target: symbolID("a.Foo"), Relation: types.RelationReferences, Provenance: "scip count=1 lines=5"})
	g.AddEdge(types.KnowledgeEdge{Source: fileID("z.go"), Target: symbolID("a.Bar"), Relation: types.RelationReferences, Provenance: "scip count=7"})
	// A non-SCIP references edge into Bar (no count provenance) must not inflate the tally.
	g.AddEdge(types.KnowledgeEdge{Source: charmID("rw"), Target: symbolID("a.Bar"), Relation: types.RelationReferences, Provenance: "charm"})

	got := g.FileFacts("a.go")
	want := FileFacts{
		Coverage: &CoverageFacts{Ratio: 0.5, Covered: 5, Total: 10},
		Symbols: []SymbolFacts{
			// Sorted by descending reference count: Bar (7) leads Foo (3).
			{ID: symbolID("a.Bar"), Label: "Bar", RefCount: 7, FileCount: 1, Coverage: &CoverageFacts{Ratio: 1, Covered: 4, Total: 4}},
			{ID: symbolID("a.Foo"), Label: "Foo", RefCount: 3, FileCount: 2},
		},
	}
	require.Equal(t, want, got)
}

func TestFileFactsAbsent(t *testing.T) {
	g := NewGraph()
	// A file node with no defines edges and no coverage attrs yields the zero value; a
	// file entirely absent from the graph does too.
	g.AddNode(types.KnowledgeNode{ID: fileID("bare.go"), Kind: types.KindFile, Label: "bare.go"})
	require.Equal(t, FileFacts{}, g.FileFacts("bare.go"))
	require.Equal(t, FileFacts{}, g.FileFacts("missing.go"))
}
