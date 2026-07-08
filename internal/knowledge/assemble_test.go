package knowledge

import (
	"os"
	"path/filepath"
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

// findEdge returns the edge matching (source, target, relation), or ok=false.
func findEdge(out types.KnowledgeGraphOutput, source, target, relation string) (types.KnowledgeEdge, bool) {
	for _, e := range out.Links {
		if e.Source == source && e.Target == target && e.Relation == relation {
			return e, true
		}
	}
	return types.KnowledgeEdge{}, false
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
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

	// Static metadata enrichment: the project node carries its engine and target
	// count, and each target inherits the engine, so an explain card answers "what
	// toolchain / how big" without a second describe or a hop to the project.
	projA, _ := nodeByID(out, "project:pkg/a")
	assert.Equal(t, "buzz", projA.Attrs[AttrEngine])
	assert.Equal(t, "2", projA.Attrs[AttrTargetCount], "pkg/a declares build+gen")
	assert.Equal(t, "buzz", build.Attrs[AttrEngine], "target inherits project engine")

	// The registry op carries a doc; the project's minimal op node dedups into it
	// without clobbering the richer description, regardless of merge order.
	op, _ := nodeByID(out, "op:go:go-build")
	assert.Equal(t, "Compile.", op.Doc)

	method, _ := nodeByID(out, "method:magus.target.glob")
	assert.Equal(t, "magus.target.glob(pattern: str)", method.Attrs["buzz"])

	diag, _ := nodeByID(out, "diagnostic:MGS2010")
	assert.Equal(t, types.SandboxPolicyMismatch.URL(), diag.Attrs["url"])
}

// TestProjectAttrsWithoutEngine: a project that declares no engine still reports
// its target count, but omits the engine attr entirely (absent, not empty).
func TestProjectAttrsWithoutEngine(t *testing.T) {
	in := Inputs{Graph: types.TargetGraphOutput{Projects: []types.TargetGraphProject{
		{Path: "pkg/c", Nodes: []types.TargetGraphNode{{Name: "build"}, {Name: "test"}, {Name: "lint"}}},
	}}}
	out := mergeAll(AssembleShards(in)).Output()

	projC, _ := nodeByID(out, "project:pkg/c")
	assert.Equal(t, "3", projC.Attrs[AttrTargetCount])
	_, hasEngine := projC.Attrs[AttrEngine]
	assert.False(t, hasEngine, "no engine declared, no engine attr")

	tgt, _ := nodeByID(out, "target:pkg/c:build")
	assert.Nil(t, tgt.Attrs, "engine-less target carries no attrs")
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

func TestAssembleRuntimeEmitsEdges(t *testing.T) {
	events := []types.DiagnosticEvent{
		{Unit: "pkg/foo:build", Code: types.ExecDenied},
		{Unit: "pkg/foo:build", Code: types.ExecDenied}, // dup -> one edge
		{Unit: "pkg/bar", Code: types.SandboxPolicyMismatch},
		{Unit: "", Code: types.ExecDenied}, // no unit -> skipped
	}
	s := assembleRuntime(events)
	require.Equal(t, RuntimeShardName, s.Name)
	require.Len(t, s.Edges, 2)

	// A target-scoped event becomes a target->diagnostic emits edge.
	assert.Contains(t, s.Edges, types.KnowledgeEdge{
		Source: "target:pkg/foo:build", Target: "diagnostic:MGS2007",
		Relation: types.RelationEmits, Confidence: types.ConfidenceExtracted, Score: 1.0, Provenance: "runtime",
	})
	// A project-scoped event becomes a project->diagnostic edge.
	assert.Contains(t, s.Edges, types.KnowledgeEdge{
		Source: "project:pkg/bar", Target: "diagnostic:MGS2010",
		Relation: types.RelationEmits, Confidence: types.ConfidenceExtracted, Score: 1.0, Provenance: "runtime",
	})
}

func TestRuntimeShardBuildsIntoGraph(t *testing.T) {
	in := sampleInputs()
	in.Runtime = []types.DiagnosticEvent{{Unit: "pkg/a:build", Code: types.ExecDenied}}
	out := mergeAll(AssembleShards(in)).Output()
	// The emits edge connects the existing target node to the existing diagnostic node.
	assert.True(t, hasEdge(out, "target:pkg/a:build", "diagnostic:MGS2007", types.RelationEmits),
		"runtime emits edge present in the merged graph")
}

func TestIsRuntimeShard(t *testing.T) {
	assert.True(t, IsRuntimeShard(RuntimeShardName))
	assert.False(t, IsRuntimeShard(RegistryShardName))
	assert.False(t, IsRuntimeShard("pkg/foo"))
}
