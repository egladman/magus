package knowledge

import (
	"testing"

	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sampleInputs is a small but edge-covering fixture: two projects with a
// project-level dependency, an intra-project target dep, a cross-project target
// dep, a spell op use, a charm reference, plus registry spells/modules/diagnostics.
func sampleInputs() Inputs {
	return Inputs{
		Graph: types.TargetGraphOutput{
			Projects: []types.TargetGraphProject{
				{
					Path:      "pkg/a",
					Engine:    "buzz",
					DependsOn: []string{"pkg/b"},
					Nodes: []types.TargetGraphNode{
						{
							Name:              "build",
							Doc:               "Build A.",
							Dependencies:      []string{"gen"},
							Charms:            []string{"rw"},
							Spells:            []types.TargetSpellUse{{Spell: "go", Ops: []string{"go-build"}}},
							CrossDependencies: []types.CrossTargetRef{{Project: "pkg/b", Target: "build"}},
						},
						{Name: "gen"},
					},
				},
				{
					Path:   "pkg/b",
					Engine: "buzz",
					Nodes:  []types.TargetGraphNode{{Name: "build"}},
				},
			},
		},
		Spells: types.SpellsOutput{Spells: []types.SpellEntry{
			{Name: "go", Targets: []string{"go-build", "go-test"}, TargetDocs: map[string]string{"go-build": "Compile."}},
		}},
		Modules: []types.ModuleEntry{{
			Name:    "magus.target",
			Doc:     "Target selectors.",
			Methods: []types.ModuleMethodEntry{{Name: "glob", Doc: "Glob match.", Buzz: "magus.target.glob(pattern: str)"}},
		}},
		Diagnostics: []types.DiagnosticCode{types.SandboxPolicyMismatch},
	}
}

// mergeAll folds every shard into one graph, the same as a load-time merge.
func mergeAll(shards []Shard) *Graph {
	g := NewGraph()
	for _, sh := range shards {
		g.Merge(sh.Nodes, sh.Edges)
	}
	return g
}

func nodeByID(out types.KnowledgeGraphOutput, id string) (types.KnowledgeNode, bool) {
	for _, n := range out.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return types.KnowledgeNode{}, false
}

func hasEdge(out types.KnowledgeGraphOutput, source, target, relation string) bool {
	for _, e := range out.Links {
		if e.Source == source && e.Target == target && e.Relation == relation {
			return true
		}
	}
	return false
}

func TestAssembleNodes(t *testing.T) {
	out := mergeAll(AssembleShards(sampleInputs())).Output()

	for _, tc := range []struct {
		id, kind string
	}{
		{"project:pkg/a", types.KindProject},
		{"project:pkg/b", types.KindProject},
		{"target:pkg/a:build", types.KindTarget},
		{"target:pkg/a:gen", types.KindTarget},
		{"target:pkg/b:build", types.KindTarget},
		{"spell:go", types.KindSpell},
		{"op:go:go-build", types.KindOp},
		{"op:go:go-test", types.KindOp},
		{"charm:rw", types.KindCharm},
		{"module:magus.target", types.KindModule},
		{"method:magus.target.glob", types.KindMethod},
		{"diagnostic:MGS2010", types.KindDiagnostic},
	} {
		n, ok := nodeByID(out, tc.id)
		require.Truef(t, ok, "missing node %q", tc.id)
		assert.Equalf(t, tc.kind, n.Kind, "kind of %q", tc.id)
	}

	build, _ := nodeByID(out, "target:pkg/a:build")
	assert.Equal(t, "Build A.", build.Doc)

	// The registry op carries a doc; the project's minimal op node dedups into it
	// without clobbering the richer description, regardless of merge order.
	op, _ := nodeByID(out, "op:go:go-build")
	assert.Equal(t, "Compile.", op.Doc)

	method, _ := nodeByID(out, "method:magus.target.glob")
	assert.Equal(t, "magus.target.glob(pattern: str)", method.Attrs["buzz"])

	diag, _ := nodeByID(out, "diagnostic:MGS2010")
	assert.Equal(t, types.SandboxPolicyMismatch.URL(), diag.Attrs["url"])
}

func TestAssembleEdges(t *testing.T) {
	out := mergeAll(AssembleShards(sampleInputs())).Output()

	assert.True(t, hasEdge(out, "project:pkg/a", "target:pkg/a:build", types.RelationContains), "project contains target")
	assert.True(t, hasEdge(out, "project:pkg/a", "project:pkg/b", types.RelationDependsOn), "project depends_on project")
	assert.True(t, hasEdge(out, "target:pkg/a:build", "target:pkg/a:gen", types.RelationDependsOn), "intra-project target dep")
	assert.True(t, hasEdge(out, "target:pkg/a:build", "target:pkg/b:build", types.RelationDependsOn), "cross-project target dep")
	assert.True(t, hasEdge(out, "target:pkg/a:build", "op:go:go-build", types.RelationUses), "target uses op")
	assert.True(t, hasEdge(out, "charm:rw", "target:pkg/a:build", types.RelationReferences), "charm references target")
	assert.True(t, hasEdge(out, "spell:go", "op:go:go-build", types.RelationContains), "spell contains op")
	assert.True(t, hasEdge(out, "module:magus.target", "method:magus.target.glob", types.RelationContains), "module contains method")

	// Every Phase 1 edge is directly extracted.
	for _, e := range out.Links {
		assert.Equalf(t, types.ConfidenceExtracted, e.Confidence, "edge %s->%s", e.Source, e.Target)
		assert.Equalf(t, 1.0, e.Score, "edge %s->%s", e.Source, e.Target)
	}
}

func TestOutputMetadata(t *testing.T) {
	out := mergeAll(AssembleShards(sampleInputs())).Output()
	assert.Equal(t, types.KnowledgeSchemaVersion, out.SchemaVersion)
	assert.True(t, out.Directed)
	assert.False(t, out.Multigraph)
	assert.Equal(t, len(out.Nodes), out.NodeCount)
	assert.Equal(t, len(out.Links), out.EdgeCount)
}

// TestDeterministicSerialization guards the byte-identical-output invariant that
// cache fingerprinting and golden diffs depend on.
func TestDeterministicSerialization(t *testing.T) {
	a, err := codec.Marshal(mergeAll(AssembleShards(sampleInputs())).Output())
	require.NoError(t, err)
	b, err := codec.Marshal(mergeAll(AssembleShards(sampleInputs())).Output())
	require.NoError(t, err)
	assert.Equal(t, string(a), string(b))
}

// TestMergeOrderIndependence: shards merged in reverse order produce the same graph.
func TestMergeOrderIndependence(t *testing.T) {
	shards := AssembleShards(sampleInputs())
	forward := mergeAll(shards).Output()

	reversed := make([]Shard, len(shards))
	for i, sh := range shards {
		reversed[len(shards)-1-i] = sh
	}
	backward := mergeAll(reversed).Output()

	fwd, _ := codec.Marshal(forward)
	bwd, _ := codec.Marshal(backward)
	assert.Equal(t, string(fwd), string(bwd))
}
