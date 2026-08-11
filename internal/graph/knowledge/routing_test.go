package knowledge

import (
	"slices"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func routingKind(r types.KnowledgeRouting, kind string) (types.KnowledgeRoutingKind, bool) {
	for _, k := range r.Kinds {
		if k.Kind == kind {
			return k, true
		}
	}
	return types.KnowledgeRoutingKind{}, false
}

func TestRoutingCountsAndKinds(t *testing.T) {
	g := sampleGraph()
	r := g.Routing()

	assert.Equal(t, types.KnowledgeSchemaVersion, r.SchemaVersion)
	assert.Equal(t, len(g.Nodes()), r.NodeCount)
	// Equal only because sampleInputs sets no Runtime events; EdgeCount counts
	// non-runtime edges, which TestRoutingIgnoresRuntimeShard pins.
	assert.Equal(t, len(g.Edges()), r.EdgeCount)

	tgt, ok := routingKind(r, types.KindTarget)
	require.True(t, ok, "target kind row present")
	assert.Equal(t, 3, tgt.Count) // pkg/a:build, pkg/a:gen, pkg/b:build
	assert.Contains(t, tgt.Anchors, "build", "the most-connected target is an anchor")
	assert.LessOrEqual(t, len(tgt.Anchors), maxAnchors)

	// Kinds appear in the fixed routingKindOrder (project before target before spell).
	order := map[string]int{}
	for i, k := range routingKindOrder {
		order[k] = i
	}
	got := make([]int, len(r.Kinds))
	for i, k := range r.Kinds {
		got[i] = order[k.Kind]
	}
	assert.True(t, slices.IsSorted(got), "kinds render in routingKindOrder")
}

func TestRoutingProjects(t *testing.T) {
	r := sampleGraph().Routing()

	byPath := map[string]types.KnowledgeRoutingProject{}
	for _, p := range r.Projects {
		byPath[p.Path] = p
	}
	a, ok := byPath["pkg/a"]
	require.True(t, ok, "pkg/a routing row present")
	assert.Equal(t, 2, a.TargetCount) // build, gen
	assert.Contains(t, a.KeyTargets, "build")

	b, ok := byPath["pkg/b"]
	require.True(t, ok)
	assert.Equal(t, 1, b.TargetCount)

	// Projects are sorted by path (deterministic output).
	paths := make([]string, len(r.Projects))
	for i, p := range r.Projects {
		paths[i] = p.Path
	}
	assert.True(t, slices.IsSorted(paths), "projects sorted by path")
}

// TestRoutingIncludesOwnerKind pins that owner nodes - merged into the default graph by
// store.go (it excludes only symbol/coverage shards) - actually surface in the routing
// table, not just get loaded and then dropped by an incomplete kind allowlist.
func TestRoutingIncludesOwnerKind(t *testing.T) {
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "owner:@alice", Kind: types.KindOwner, Label: "@alice"})
	g.AddNode(types.KnowledgeNode{ID: "project:pkg/a", Kind: types.KindProject, Label: "pkg/a"})
	g.AddEdge(types.KnowledgeEdge{
		Source: "owner:@alice", Target: "project:pkg/a",
		Relation: types.RelationOwns, Confidence: types.ConfidenceExtracted, Score: 1,
	})

	r := g.Routing()
	row, ok := routingKind(r, types.KindOwner)
	require.True(t, ok, "owner kind row present in Routing")
	assert.Equal(t, 1, row.Count)
	assert.Contains(t, row.Anchors, "@alice")
}

// TestRoutingIgnoresRuntimeShard pins the summary against local run history. All three
// runtime inputs are populated, so the partial nodes are covered along with the edges.
func TestRoutingIgnoresRuntimeShard(t *testing.T) {
	// Which codes these are does not matter, only that two targets document the first and
	// one documents the second: degree decides the order, so the assertion below holds
	// whatever the constants say.
	const documented, tripped = types.MagusfileOnlyMember, types.ToolTooNew
	sourceGraph := func() *Graph {
		g := NewGraph()
		for _, code := range []types.DiagnosticCode{documented, tripped} {
			g.AddNode(types.KnowledgeNode{ID: diagnosticID(string(code)), Kind: types.KindDiagnostic, Label: string(code)})
		}
		for _, name := range []string{"build", "lint"} {
			id := targetID("pkg/a", name)
			g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindTarget, Label: name})
			g.AddEdge(extractedEdge(id, diagnosticID(string(documented)), types.RelationDocuments, "magusfile.buzz"))
		}
		g.AddEdge(extractedEdge(targetID("pkg/a", "build"), diagnosticID(string(tripped)), types.RelationDocuments, "magusfile.buzz"))
		return g
	}

	want := sourceGraph().Routing()
	diag, ok := routingKind(want, types.KindDiagnostic)
	require.True(t, ok, "diagnostic kind row present")
	require.Equal(t, []string{string(documented), string(tripped)}, diag.Anchors,
		"sources alone rank the twice-documented code first")

	// Three hits on the less-documented code, enough to invert the ranking if runtime
	// edges counted. pkg/b:test is undefined by any shard, so a dangling edge rides along.
	local := assembleRuntime(
		[]types.DiagnosticEvent{
			{Unit: "pkg/a:build", Code: tripped},
			{Unit: "pkg/a:lint", Code: tripped},
			{Unit: "pkg/b:test", Code: tripped},
		},
		[]types.KnowledgeTiming{{Project: "pkg/a", Target: "build", P75Ms: 4200, Samples: 9, HitRate: 0.75, HitRateSamples: 12}},
		[]types.KnowledgeOutputRef{{Project: "pkg/a", Target: "build", Ref: "out1a2b3c", OK: true}},
		map[string]bool{targetID("pkg/a", "build"): true})
	require.NotEmpty(t, local.Edges, "fixture must produce runtime edges")
	require.NotEmpty(t, local.Nodes, "and partial target nodes")

	g := sourceGraph()
	g.Merge(local.Nodes, local.Edges)
	assert.Equal(t, want, g.Routing(), "routing is identical with and without the runtime shard")
}

func TestProjectOfTargetID(t *testing.T) {
	for _, tc := range []struct{ id, want string }{
		{"target:pkg/a:build", "pkg/a"},
		{"target:.:build", "."},
		{"target:cmd/magus/starter:ci", "cmd/magus/starter"},
	} {
		got, ok := projectOfTargetID(tc.id)
		require.Truef(t, ok, "%s should parse", tc.id)
		assert.Equal(t, tc.want, got)
	}
	_, ok := projectOfTargetID("spell:go")
	assert.False(t, ok, "non-target id does not parse as a project")
}
