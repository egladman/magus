package knowledge

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egladman/magus/internal/json"
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
		Spells: []types.Spell{
			{Name: "go", Targets: []string{"go-build", "go-test"}, TargetDocs: map[string]string{"go-build": "Compile."}},
		},
		Modules: []types.ModuleEntry{{
			Name:    "vcs",
			Doc:     "Version control.",
			Methods: []types.ModuleMethodEntry{{Name: "shortHash", Doc: "Short commit hash.", Buzz: "vcs.shortHash() str"}},
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

func hasEdge(out types.KnowledgeGraphOutput, source, target string, relation types.RelationID) bool {
	for _, e := range out.Links {
		if e.Source == source && e.Target == target && e.Relation == relation {
			return true
		}
	}
	return false
}

// findEdge returns the edge matching (source, target, relation), or ok=false.
func findEdge(out types.KnowledgeGraphOutput, source, target string, relation types.RelationID) (types.KnowledgeEdge, bool) {
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
		{"module:vcs", types.KindModule},
		{"method:vcs.shortHash", types.KindMethod},
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

	method, _ := nodeByID(out, "method:vcs.shortHash")
	assert.Equal(t, "vcs.shortHash() str", method.Attrs["buzz"])

	diag, _ := nodeByID(out, "diagnostic:MGS2010")
	assert.Equal(t, types.CodeURL(types.SandboxPolicyMismatch), diag.Attrs["url"])
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
	assert.Contains(t, out.Links, types.KnowledgeEdge{
		Source: "target:pkg/a:build", Target: "spell:go", Relation: types.RelationUses,
		Confidence: types.ConfidenceExtracted, Score: 1, Provenance: "pkg/a",
	}, "target dispatches spell")
	assert.True(t, hasEdge(out, "target:pkg/a:build", "op:go:go-build", types.RelationUses), "target uses op")
	assert.True(t, hasEdge(out, "charm:rw", "target:pkg/a:build", types.RelationReferences), "charm references target")
	assert.True(t, hasEdge(out, "spell:go", "op:go:go-build", types.RelationContains), "spell contains op")
	assert.True(t, hasEdge(out, "module:vcs", "method:vcs.shortHash", types.RelationContains), "module contains method")

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
	a, err := json.Marshal(mergeAll(AssembleShards(sampleInputs())).Output())
	require.NoError(t, err)
	b, err := json.Marshal(mergeAll(AssembleShards(sampleInputs())).Output())
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

	fwd, _ := json.Marshal(forward)
	bwd, _ := json.Marshal(backward)
	assert.Equal(t, string(fwd), string(bwd))
}

func TestAssembleRuntimeEmitsEdges(t *testing.T) {
	events := []types.DiagnosticEvent{
		{Unit: "pkg/foo:build", Code: types.ExecDenied},
		{Unit: "pkg/foo:build", Code: types.ExecDenied}, // dup -> one edge
		{Unit: "pkg/bar", Code: types.SandboxPolicyMismatch},
		{Unit: "", Code: types.ExecDenied}, // no unit -> skipped
	}
	s := assembleRuntime(events, nil, nil, nil)
	require.Equal(t, runtimeShardName, s.Name)
	require.Len(t, s.Edges, 2)

	// A target-scoped event becomes a target->diagnostic emits edge.
	assert.Contains(t, s.Edges, types.KnowledgeEdge{
		Source: "target:pkg/foo:build", Target: "diagnostic:MGS2007",
		Relation: types.RelationEmits, Confidence: types.ConfidenceExtracted, Score: 1.0, Provenance: ProvenanceRuntime,
	})
	// A project-scoped event becomes a project->diagnostic edge.
	assert.Contains(t, s.Edges, types.KnowledgeEdge{
		Source: "project:pkg/bar", Target: "diagnostic:MGS2010",
		Relation: types.RelationEmits, Confidence: types.ConfidenceExtracted, Score: 1.0, Provenance: ProvenanceRuntime,
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

func TestAssembleRuntimeTimingAttrs(t *testing.T) {
	known := map[string]bool{"target:pkg/a:build": true}
	timings := []types.KnowledgeTiming{
		{Project: "pkg/a", Target: "build", P75Ms: 4200, Samples: 9, HitRate: 0.75, HitRateSamples: 12},
		{Project: "pkg/a", Target: "ghost", P75Ms: 100, Samples: 3, HitRateSamples: 3}, // unknown target -> dropped
		{Project: "pkg/a", Target: "cold", HitRateSamples: 0},                          // no signal at all -> no node
	}
	s := assembleRuntime(nil, timings, nil, known)

	require.Len(t, s.Nodes, 1, "only the known, signal-bearing target yields a node")
	n := s.Nodes[0]
	assert.Equal(t, "target:pkg/a:build", n.ID)
	assert.Equal(t, types.KindTarget, n.Kind, "typed so the merge is order-independent")
	assert.Equal(t, "4200", n.Attrs[AttrDurationP75Ms])
	assert.Equal(t, "9", n.Attrs[attrRunSamples])
	assert.Equal(t, "0.75", n.Attrs[attrCacheHitRate])
}

// TestRuntimeTimingMergesOntoTarget: a timing node merges its attrs onto the
// project shard's target node regardless of shard load order, without clobbering
// the static engine attr.
func TestRuntimeTimingMergesOntoTarget(t *testing.T) {
	in := sampleInputs()
	in.Timings = []types.KnowledgeTiming{{Project: "pkg/a", Target: "build", P75Ms: 500, Samples: 5, HitRate: 0.5, HitRateSamples: 8}}
	out := mergeAll(AssembleShards(in)).Output()

	build, ok := nodeByID(out, "target:pkg/a:build")
	require.True(t, ok)
	assert.Equal(t, "500", build.Attrs[AttrDurationP75Ms])
	assert.Equal(t, "0.50", build.Attrs[attrCacheHitRate])
	assert.Equal(t, "buzz", build.Attrs[AttrEngine], "static engine attr survives the timing merge")
}

func TestAssembleRuntimeOutputRefAttrs(t *testing.T) {
	known := map[string]bool{"target:pkg/a:build": true}
	refs := []types.KnowledgeOutputRef{
		{Project: "pkg/a", Target: "build", Ref: "out1a2b3c", OK: true},
		{Project: "pkg/a", Target: "test", Ref: "outdeadbe", OK: false}, // unknown target -> dropped
		{Project: "pkg/a", Target: "gen", Ref: ""},                      // no ref -> no node, no empty attr
	}
	s := assembleRuntime(nil, nil, refs, known)

	require.Len(t, s.Nodes, 1, "only the known target with a ref yields a node")
	n := s.Nodes[0]
	assert.Equal(t, "target:pkg/a:build", n.ID)
	assert.Equal(t, types.KindTarget, n.Kind, "typed so the merge is order-independent")
	assert.Equal(t, "out1a2b3c", n.Attrs[AttrLastOutputRef])
	assert.Equal(t, "true", n.Attrs[AttrLastRunOK])
}

// TestRuntimeOutputRefMergesOntoTarget: a failing run's ref merges its attrs onto the
// project shard's target node alongside the static engine attr, and a timing ref for
// the same target folds together with the timing attrs (both partial nodes coalesce).
func TestRuntimeOutputRefMergesOntoTarget(t *testing.T) {
	in := sampleInputs()
	in.Timings = []types.KnowledgeTiming{{Project: "pkg/a", Target: "build", P75Ms: 500, Samples: 5, HitRate: 0.5, HitRateSamples: 8}}
	in.OutputRefs = []types.KnowledgeOutputRef{{Project: "pkg/a", Target: "build", Ref: "outf00dfa", OK: false}}
	out := mergeAll(AssembleShards(in)).Output()

	build, ok := nodeByID(out, "target:pkg/a:build")
	require.True(t, ok)
	assert.Equal(t, "outf00dfa", build.Attrs[AttrLastOutputRef])
	assert.Equal(t, "false", build.Attrs[AttrLastRunOK])
	assert.Equal(t, "500", build.Attrs[AttrDurationP75Ms], "timing attrs coexist with ref attrs")
	assert.Equal(t, "buzz", build.Attrs[AttrEngine], "static engine attr survives the ref merge")
}

// TestAssembleOpTools: an op with a static base command carries argv + tool attrs and a
// uses edge to the tool it runs (argv[0] basename); its spell uses that tool too, deduped
// to a single edge; a function-op (no OpCommands entry) carries no argv and links to no
// tool; and two ops sharing a tool link to a SINGLE tool node. Model B: what a target runs
// is reached via target->op->tool, not a per-target command node.
func TestAssembleOpTools(t *testing.T) {
	in := sampleInputs()
	in.Spells[0].Language = "go" // the "go" spell declares a language
	in.Spells[0].Targets = []string{"go-build", "go-test", "noop"}
	in.Spells[0].OpCommands = map[string][]string{
		"go-build": {"/usr/bin/go", "build", "./..."},
		"go-test":  {"go", "test", "./..."}, // same tool -> shared tool node
		// "noop" has no entry: a function-op, no static argv, no tool edge.
	}
	out := mergeAll(AssembleShards(in)).Output()

	opID := "op:go:go-build"
	n, ok := nodeByID(out, opID)
	require.True(t, ok, "the op node exists")
	assert.Equal(t, types.KindOp, n.Kind)
	assert.Equal(t, "/usr/bin/go build ./...", n.Attrs[attrArgv], "the op carries its base argv")
	assert.Equal(t, "go", n.Attrs[attrTool], "the op's tool is argv[0]'s basename")

	// Exactly one tool node for the shared tool, workspace-scoped.
	tID := "tool:go"
	toolNode, ok := nodeByID(out, tID)
	require.True(t, ok, "the tool node exists")
	assert.Equal(t, types.KindTool, toolNode.Kind, "a program is its own kind")
	assert.Equal(t, "go", toolNode.Label, "tool label is the basename (filepath.Base)")
	assert.Empty(t, toolNode.Source, "the tool node is workspace-scoped, not project-owned")

	assert.True(t, hasEdge(out, opID, tID, types.RelationUses), "go-build op uses the go tool")
	assert.True(t, hasEdge(out, "op:go:go-test", tID, types.RelationUses), "go-test op uses the SAME tool")
	// The spell that owns the ops uses the tool too: the spell<->tool link, deduped.
	assert.True(t, hasEdge(out, "spell:go", tID, types.RelationUses), "the go spell uses the go tool")

	// The function-op carries no argv and links to no tool.
	noop, ok := nodeByID(out, "op:go:noop")
	require.True(t, ok, "the function-op still mints an op node")
	assert.Empty(t, noop.Attrs[attrArgv], "a function-op has no static argv")
	assert.False(t, hasEdge(out, "op:go:noop", tID, types.RelationUses), "a function-op links to no tool")

	var tools, spellToolEdges int
	for _, node := range out.Nodes {
		if node.ID == tID {
			tools++
		}
	}
	for _, e := range out.Links {
		if e.Source == "spell:go" && e.Target == tID && e.Relation == types.RelationUses {
			spellToolEdges++
		}
	}
	assert.Equal(t, 1, tools, "exactly one tool node for the shared tool")
	assert.Equal(t, 1, spellToolEdges, "the spell->tool edge is deduped to one despite two ops")
}

// TestRuntimeAttrsCoversAssembled derives the attr set from behaviour: whatever
// assembleRuntime emits with every input populated must be exactly runtimeAttrs. An attr
// added to timingAttrs and forgotten there would leak into the committed export.
func TestRuntimeAttrsCoversAssembled(t *testing.T) {
	known := map[string]bool{"target:pkg/a:build": true}
	s := assembleRuntime(nil,
		[]types.KnowledgeTiming{{Project: "pkg/a", Target: "build", P75Ms: 4200, Samples: 9, HitRate: 0.75, HitRateSamples: 12}},
		[]types.KnowledgeOutputRef{{Project: "pkg/a", Target: "build", Ref: "out1a2b3c", OK: true}},
		known)

	emitted := map[string]bool{}
	for _, n := range s.Nodes {
		for k := range n.Attrs {
			emitted[k] = true
		}
	}
	require.NotEmpty(t, emitted, "guards against a vacuous pass if the fixture stops emitting")
	assert.ElementsMatch(t, runtimeAttrs, slices.Collect(maps.Keys(emitted)),
		"runtimeAttrs must match what the runtime shard actually emits")
}

func TestIsRuntimeShard(t *testing.T) {
	assert.True(t, isRuntimeShard(runtimeShardName))
	assert.False(t, isRuntimeShard(registryShardName))
	assert.False(t, isRuntimeShard("pkg/foo"))
}

// TestDeclaredAsAttr checks a target whose raw name the normalizer rewrote carries
// the declared_as attr on its knowledge node, while the node ID/label stay normalized.
func TestDeclaredAsAttr(t *testing.T) {
	in := Inputs{Graph: types.TargetGraphOutput{Projects: []types.TargetGraphProject{
		{Path: ".", Engine: "buzz", Nodes: []types.TargetGraphNode{
			{Name: "go-build", Declared: "goBuild"},
			{Name: "build"},
		}},
	}}}
	out := mergeAll(AssembleShards(in)).Output()
	n, ok := nodeByID(out, "target:.:go-build")
	require.True(t, ok)
	assert.Equal(t, "go-build", n.Label, "node identity stays normalized")
	assert.Equal(t, "goBuild", n.Attrs[attrDeclaredAs], "raw spelling surfaced as declared_as")
	plain, _ := nodeByID(out, "target:.:build")
	assert.Empty(t, plain.Attrs[attrDeclaredAs], "no declared_as when the name matches")
}

func TestOwningProjectPath(t *testing.T) {
	projects := []types.TargetGraphProject{
		{Path: "."},
		{Path: "docs"},
		{Path: "foo"},
		{Path: "foo/bar"},
	}

	cases := []struct {
		file string
		want string
	}{
		{"docs/render.buzz", "docs"},  // nested project wins over root
		{"magusfile.buzz", "."},       // root-level file falls to the root project
		{"foo/bar/x.buzz", "foo/bar"}, // longest matching project wins
		{"foo/x.buzz", "foo"},         // not deep enough for foo/bar
		{"foobar/x.buzz", "."},        // "foo" must not claim "foobar" (path-prefix guard)
	}
	for _, c := range cases {
		got, ok := owningProjectPath(c.file, projects)
		assert.True(t, ok, "file %q should be owned", c.file)
		assert.Equal(t, c.want, got, "owner of %q", c.file)
	}
}

func TestOwningProjectPathNoRootUnowned(t *testing.T) {
	// With no root project, a top-level file belongs to nobody rather than being
	// force-fit into an unrelated nested project.
	_, ok := owningProjectPath("top.buzz", []types.TargetGraphProject{{Path: "docs"}})
	assert.False(t, ok)
}

// TestWorkspaceContainsPath pins the containment rule every path-bearing node is held to.
func TestWorkspaceContainsPath(t *testing.T) {
	inside := []string{"a.go", "cmd/magus/vcs.go", "docs/gen/index.html", "a..b/c.go", "..hidden/x"}
	for _, p := range inside {
		assert.True(t, workspaceContainsPath(p), "%q is inside the workspace", p)
	}

	outside := []string{
		"",
		"..",
		"../sibling",
		"../../../../../Library/Caches/go-build/01",
		"cmd/../../escape.go",
		"/etc/passwd",
		"/Users/someone/Library/Caches/go-build",
	}
	for _, p := range outside {
		assert.False(t, workspaceContainsPath(p), "%q is outside the workspace", p)
	}
}

// TestProjectContainsFileRefusesEscape covers the hole that put a developer's
// ~/Library/Caches/go-build shards into a committed graph: the root project claimed every
// path unconditionally, so a leaf climbing out was adopted by "." and the directory walk
// minted a node for each ".." on the way.
func TestProjectContainsFileRefusesEscape(t *testing.T) {
	assert.True(t, projectContainsFile(".", "cmd/magus/vcs.go"))
	assert.True(t, projectContainsFile("libs/gopherbuzz", "libs/gopherbuzz/pool.go"))

	assert.False(t, projectContainsFile(".", "../../../../../Library/Caches/go-build/01"),
		"the root project owns the workspace, not the machine")
	assert.False(t, projectContainsFile(".", "/etc/passwd"))
	assert.False(t, projectContainsFile("libs/gopherbuzz", "libs/gopherbuzz/../../escape.go"))

	_, ok := owningProjectPath("../outside.go", []types.TargetGraphProject{{Path: "."}})
	assert.False(t, ok, "no project claims a path outside the workspace")
}

// TestContainsChainRefusesEscape checks the walk itself, not just its caller. The walk
// climbs until it reaches ".", so an escaping leaf is what produces the chain.
func TestContainsChainRefusesEscape(t *testing.T) {
	nodes, edges := containsChain(".", "../../../../../Library/Caches/go-build/01/x", "file:x")
	assert.Empty(t, nodes)
	assert.Empty(t, edges)

	nodes, edges = containsChain(".", "cmd/magus/vcs.go", "file:cmd/magus/vcs.go")
	require.NotEmpty(t, nodes, "an ordinary path still builds its chain")
	for _, n := range nodes {
		assert.True(t, workspaceContainsPath(n.Source), "minted dir %q stays inside the workspace", n.Source)
	}
	assert.NotEmpty(t, edges)
}

// fullInputs is sampleInputs plus every assembly input it deliberately omits, over a
// real workspace on disk. It exists for the two vocabulary tests below and nothing else:
// sampleInputs stays minimal because every other test in this file asserts against
// exactly what it produces, and a Root alone switches on the docs and buzz extractors
// under all of them.
//
// Each addition here is the smallest thing that makes one declared relation reachable.
// The comments say which, because otherwise the next person trimming this fixture cannot
// tell the load-bearing parts from the scenery.
func fullInputs(t *testing.T) Inputs {
	t.Helper()
	in := sampleInputs()
	root := t.TempDir()
	in.Root = root

	write := func(rel, body string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	}

	// A Buzz source under pkg/a gives the file node the rest of the fixture hangs off, and
	// carries three relations itself: rationale_for (the WHY marker), imports (the import
	// line), and calls (helper, reached from build's body).
	write("pkg/a/magusfile.buzz", `import "std";

// WHY: b generates what a compiles against, so a builds second.
export fun build(ctx: magus\Context, args: [str]) > void !> any {
    helper();
}

fun helper() > void {
}
`)
	// documents: a spell page under a "spells" segment, stemmed to a spell the registry
	// knows (see spellFromPath).
	write("docs/spells/go.md", "# go\n\nThe go spell.\n")
	// owns: CODEOWNERS is read from the root and matched against the path-bearing nodes.
	write("CODEOWNERS", "pkg/a @platform\n")

	// produces and consumes: both resolve a target's declared globs against the file and
	// doc nodes already minted, so each glob here names a path written above.
	in.Graph.Projects[0].Nodes[0].WritesFiles = []types.OutputRef{{Glob: "../../docs/spells/go.md"}}
	in.Graph.Projects[0].Nodes[0].ReadsFiles = []types.InputRef{{Project: "pkg/a", Glob: "magusfile.buzz"}}

	// defines and calls, from a SCIP index rather than the Buzz walk: the two relations
	// have producers on both sides, and this is the one a foreign indexer drives.
	in.Symbols = map[string][]types.KnowledgeSymbol{"pkg/a": {
		{Key: "go/pkg-a/Build", Label: "Build", Language: "go", SymbolKind: "function",
			Source: "pkg/a/build.go:10", Defs: []string{"pkg/a/build.go"},
			Calls: []types.KnowledgeSymbolCall{{Key: "go/pkg-a/helper", Count: 1}}},
		{Key: "go/pkg-a/helper", Label: "helper", Language: "go", SymbolKind: "function",
			Source: "pkg/a/build.go:20", Defs: []string{"pkg/a/build.go"}},
	}}
	// depends_on, in its project -> package shape, which no other input produces.
	in.Packages = map[string][]types.KnowledgePackage{"pkg/a": {
		{Manager: "go", Name: "github.com/example/dep", Version: "v1.2.3"},
	}}
	// authored: history folds onto the buzz file node, and the author nodes and edges are
	// gated on VCSAuthorship.
	in.VCS = []types.KnowledgeVCS{{
		Path: "pkg/a/magusfile.buzz", LastCommit: "abc123", LastAuthor: "dev",
		Authors: []string{"dev"}, Commits: 2,
	}}
	in.VCSAuthorship = true
	// emits: a diagnostic observed in run history, which is the only producer of the edge.
	in.Runtime = []types.DiagnosticEvent{{Code: types.SandboxPolicyMismatch, Unit: "pkg/a:build"}}
	// annotates: anchored to a project, which knownNodeIDs always contains. An anchor the
	// graph does not model is dropped rather than made into an edge, so an unknown one
	// would fail the coverage assertion for a reason that is not about the vocabulary.
	in.Notes = []types.KnowledgeNote{{
		Name: "why-pkg-a", Title: "Why pkg/a exists", Path: "notes/why-pkg-a.md",
		Anchors: []string{"project:pkg/a"},
	}}
	return in
}

// TestAssembledEdgesAreAllDeclared is the enforcement point for the closed relation
// vocabulary. types.KnowledgeRelationDefinitions declares which endpoint kinds each
// predicate may connect; nothing checks that at write time, because shards load lazily
// and an edge can arrive before the node that gives its endpoint a kind.
//
// So the check lives here, over a fully assembled graph. A producer that starts emitting
// a shape nobody declared fails at this line, which is the moment worth catching: the
// vocabulary is closed precisely so widening it is a decision someone makes on purpose
// rather than a side effect of a new extractor.
func TestAssembledEdgesAreAllDeclared(t *testing.T) {
	g := mergeAll(AssembleShards(fullInputs(t)))

	undeclared := g.UndeclaredEdges()

	for _, e := range undeclared {
		source, _ := g.node(e.Source)
		target, _ := g.node(e.Target)
		t.Errorf("undeclared edge shape: %s(%s) --%s--> %s(%s); declare it in types.KnowledgeRelationDefinitions or stop emitting it",
			e.Source, source.Kind, e.Relation, e.Target, target.Kind)
	}
}

// TestEveryDeclaredRelationIsExercised is what makes the check above worth anything. It
// reads only the shapes the fixture happens to produce, so a declared relation nothing
// emits is a vocabulary entry no test has ever seen, and the two that were missing when
// this was written, annotates and rationale_for, are exactly the two whose inputs the
// fixture did not supply.
//
// A relation that genuinely cannot be produced from an assembly input does not belong in
// the vocabulary, so there is no exemption list here on purpose: the way to satisfy this
// is to feed the extractor, or to stop declaring the relation.
func TestEveryDeclaredRelationIsExercised(t *testing.T) {
	g := mergeAll(AssembleShards(fullInputs(t)))

	seen := make(map[types.RelationID]bool)
	for _, e := range g.Edges() {
		seen[e.Relation] = true
	}

	for _, d := range types.KnowledgeRelationDefinitions() {
		assert.Truef(t, seen[d.ID], "no assembly input produces %s, so nothing checks its declared shapes", d.ID)
	}
}

// A relation nobody declared permits no shape at all: the set is closed, so an unknown
// predicate is rejected rather than waved through for lack of a rule to break.
func TestUndeclaredRelationPermitsNothing(t *testing.T) {
	assert.False(t, types.KnowledgeRelationAllows("teleports_to", types.KindProject, types.KindTarget))
	assert.True(t, types.KnowledgeRelationAllows(types.RelationContains, types.KindProject, types.KindTarget))
	assert.False(t, types.KnowledgeRelationAllows(types.RelationContains, types.KindTarget, types.KindProject),
		"contains is directed; the reverse shape is not declared")
}

// An edge whose endpoints are not both present is unknown, not wrong. Reporting it here
// would turn a dangling-reference question into a vocabulary one.
func TestUndeclaredEdgesSkipsADanglingEdge(t *testing.T) {
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "project:a", Kind: types.KindProject})
	g.AddEdge(types.KnowledgeEdge{
		Source: "project:a", Target: "project:gone", Relation: types.RelationDependsOn,
		Confidence: types.ConfidenceExtracted, Score: 1,
	})

	assert.Empty(t, g.UndeclaredEdges())
}

// The export carries the vocabulary it was built against, so a consumer meeting an
// unfamiliar predicate can resolve it without a matching binary.
func TestOutputCarriesTheRelationVocabulary(t *testing.T) {
	out := mergeAll(AssembleShards(sampleInputs())).Output()

	assert.Equal(t, types.KnowledgeRelationDefinitions(), out.Relations)
	assert.Equal(t, types.KnowledgeRelationFingerprint(), out.RelationFingerprint)
	assert.NotEmpty(t, out.RelationFingerprint)
}
