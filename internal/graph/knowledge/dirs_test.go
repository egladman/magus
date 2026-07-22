package knowledge

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// edgeTriples reduces edges to (source,target,relation) for order-independent assertion.
func edgeTriples(edges []types.KnowledgeEdge) map[[3]string]bool {
	m := map[[3]string]bool{}
	for _, e := range edges {
		m[[3]string{e.Source, e.Target, e.Relation}] = true
	}
	return m
}

// TestContainsChain covers the directory containment tree a file's node hangs from: a
// nested file threads project -> dir -> ... -> file, a root-level file keeps the single
// project -> file edge, and two files in the SAME directory resolve to the SAME dir node
// ID so the merge unifies them (identity, not duplication).
func TestContainsChain(t *testing.T) {
	t.Run("nested file threads the dir chain", func(t *testing.T) {
		nodes, edges := containsChain(".", "internal/interp/discovery.go", "file:internal/interp/discovery.go")
		require.Len(t, nodes, 2)
		assert.Equal(t, "dir:internal", nodes[0].ID)
		assert.Equal(t, types.KindDir, nodes[0].Kind)
		assert.Equal(t, "internal", nodes[0].Label)
		assert.Equal(t, "dir:internal/interp", nodes[1].ID)

		tr := edgeTriples(edges)
		assert.True(t, tr[[3]string{"project:.", "dir:internal", types.RelationContains}])
		assert.True(t, tr[[3]string{"dir:internal", "dir:internal/interp", types.RelationContains}])
		assert.True(t, tr[[3]string{"dir:internal/interp", "file:internal/interp/discovery.go", types.RelationContains}])
	})

	t.Run("root-level file keeps the direct project edge", func(t *testing.T) {
		nodes, edges := containsChain(".", "magusfile.buzz", "file:magusfile.buzz")
		assert.Empty(t, nodes, "no intermediate directories")
		require.Len(t, edges, 1)
		assert.Equal(t, "project:.", edges[0].Source)
		assert.Equal(t, "file:magusfile.buzz", edges[0].Target)
	})

	t.Run("sub-project chain starts at the project, not the workspace root", func(t *testing.T) {
		nodes, edges := containsChain("libs/diag", "libs/diag/sub/foo.go", "file:libs/diag/sub/foo.go")
		require.Len(t, nodes, 1)
		assert.Equal(t, "dir:libs/diag/sub", nodes[0].ID)
		tr := edgeTriples(edges)
		assert.True(t, tr[[3]string{"project:libs/diag", "dir:libs/diag/sub", types.RelationContains}])
		assert.True(t, tr[[3]string{"dir:libs/diag/sub", "file:libs/diag/sub/foo.go", types.RelationContains}])
	})

	t.Run("two files in one directory share the dir node ID (dedup by identity)", func(t *testing.T) {
		a, _ := containsChain(".", "internal/interp/a.go", "file:internal/interp/a.go")
		b, _ := containsChain(".", "internal/interp/b.buzz", "file:internal/interp/b.buzz")
		assert.Equal(t, a[len(a)-1].ID, b[len(b)-1].ID, "same directory -> one dir node the merge unifies")
	})
}

// TestAssembleDirs covers the aggregate rollup: transitive file count, summed git churn,
// and the language set, all onto the dir node keyed by the same ID containsChain emits.
func TestAssembleDirs(t *testing.T) {
	projects := []types.TargetGraphProject{{Path: "."}}
	leafPaths := []string{"internal/interp/a.go", "internal/interp/b.buzz", "docs/x.md"}
	churn := map[string]int{"internal/interp/a.go": 3, "internal/interp/b.buzz": 2, "docs/x.md": 5}

	shard := assembleDirs(projects, leafPaths, churn)
	by := map[string]types.KnowledgeNode{}
	for _, n := range shard.Nodes {
		by[n.ID] = n
	}

	// internal/interp holds both source files: 2 files, 3+2 churn, go+buzz.
	leaf := by["dir:internal/interp"]
	assert.Equal(t, types.KindDir, leaf.Kind)
	assert.Equal(t, "2", leaf.Attrs[AttrDirFiles])
	assert.Equal(t, "5", leaf.Attrs[AttrDirCommits])
	assert.Equal(t, "buzz,go", leaf.Attrs[AttrLanguages], "languages sorted")

	// internal aggregates its child transitively.
	assert.Equal(t, "2", by["dir:internal"].Attrs[AttrDirFiles])
	assert.Equal(t, "5", by["dir:internal"].Attrs[AttrDirCommits])

	// docs holds the one markdown file.
	assert.Equal(t, "1", by["dir:docs"].Attrs[AttrDirFiles])
	assert.Equal(t, "markdown", by["dir:docs"].Attrs[AttrLanguages])
}

// TestDirNodeStructuralAndAggregateMerge proves the structural dir node (from
// containsChain, carrying Label/Source) and the aggregate dir node (from assembleDirs,
// carrying attrs) fold into ONE node on merge - the same partial-node pattern the
// runtime shard uses for targets.
func TestDirNodeStructuralAndAggregateMerge(t *testing.T) {
	structuralNodes, structuralEdges := containsChain(".", "internal/interp/a.go", "file:internal/interp/a.go")
	dirs := assembleDirs([]types.TargetGraphProject{{Path: "."}}, []string{"internal/interp/a.go"}, map[string]int{"internal/interp/a.go": 3})

	out := mergeAll([]Shard{
		{Nodes: structuralNodes, Edges: structuralEdges},
		dirs,
	}).Output()

	n, ok := nodeByID(out, "dir:internal/interp")
	require.True(t, ok, "one merged dir node")
	assert.Equal(t, types.KindDir, n.Kind)
	assert.Equal(t, "internal/interp", n.Label, "structural label survives")
	assert.Equal(t, "1", n.Attrs[AttrDirFiles], "aggregate attr folded in")
	assert.Equal(t, "go", n.Attrs[AttrLanguages])
	assert.True(t, hasEdge(out, "dir:internal", "dir:internal/interp", types.RelationContains))
}
