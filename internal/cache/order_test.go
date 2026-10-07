package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// orderSteps builds a two-step batch: a "gen" step per project, with docs
// depending on root the way the workspace declares it.
func orderSteps() []Step {
	return []Step{
		{ProjectPath: ".", Target: "gen"},
		{ProjectPath: "docs", Target: "gen", DependsOn: []string{"."}},
	}
}

func stepKeys(steps []Step) (root, docs types.TargetRef) {
	return stepRef(steps[0]), stepRef(steps[1])
}

func TestDeriveTargetOrderWriterBeforeReader(t *testing.T) {
	steps := []Step{
		{ProjectPath: "a", Target: "gen"},
		{ProjectPath: "b", Target: "check"},
	}
	nodes := []TargetNode{
		{Project: "a", Target: "gen", Steps: []types.TargetRef{stepRef(steps[0])},
			Writes: types.MustParseGlobs("a/out/report.md"), DeclaredWrites: true,
			Reads: types.MustParseGlobs("a/src/**"), DeclaredReads: true},
		{Project: "b", Target: "check", Steps: []types.TargetRef{stepRef(steps[1])},
			Reads: types.MustParseGlobs("a/out/*.md"), DeclaredReads: true},
	}
	d := DeriveTargetOrder(steps, nodes, nil)
	assert.Equal(t, &DerivedOrder{
		Nodes:    nodes,
		Edges:    []DerivedEdge{{Writer: 0, Reader: 1, Ordered: true}},
		RunAfter: map[types.TargetRef][]types.TargetRef{stepRef(steps[1]): {stepRef(steps[0])}},
		SameStep: d.SameStep, // the two targets run in different steps
	}, d)
}

func TestDeriveTargetOrderNoSelfEdge(t *testing.T) {
	steps := orderSteps()
	root, docs := stepKeys(steps)
	nodes := []TargetNode{
		// Reads what it writes: not an edge, or every regenerator would cycle on
		// itself.
		{Project: ".", Target: "index", Steps: []types.TargetRef{root},
			Reads: types.MustParseGlobs("MAGUS.md"), DeclaredReads: true,
			Writes: types.MustParseGlobs("MAGUS.md"), DeclaredWrites: true},
		// Same-step overlap is the body's own ctx.needs ordering, not derivation's.
		{Project: ".", Target: "sibling", Steps: []types.TargetRef{root},
			Reads: types.MustParseGlobs("MAGUS.md"), DeclaredReads: true},
		// A cross-step reader still derives, proving the guards above are the only
		// thing suppressing the first two pairs.
		{Project: "docs", Target: "reader", Steps: []types.TargetRef{docs},
			Reads: types.MustParseGlobs("MAGUS.md"), DeclaredReads: true},
	}
	d := DeriveTargetOrder(steps, nodes, nil)
	require.Equal(t, []DerivedEdge{{Writer: 0, Reader: 2, Ordered: true}}, d.Edges)
}

// TestDeriveTargetOrderMutualDeclarationsSettle pins the legitimate cycle: two
// targets each declaring writes that the other declares it reads. Every
// project's index-generate is this shape, writing its own MAGUS.md and reading
// its siblings', so it must schedule, with the unschedulable direction recorded
// for settling rather than reported as an authoring error.
func TestDeriveTargetOrderMutualDeclarationsSettle(t *testing.T) {
	steps := orderSteps()
	root, docs := stepKeys(steps)
	nodes := []TargetNode{
		{Project: ".", Target: "changelog", Steps: []types.TargetRef{root},
			Reads: types.MustParseGlobs("docs/changelog.md"), DeclaredReads: true,
			Writes: types.MustParseGlobs("CHANGELOG.md"), DeclaredWrites: true},
		{Project: "docs", Target: "content", Steps: []types.TargetRef{docs},
			Reads: types.MustParseGlobs("CHANGELOG.md"), DeclaredReads: true,
			Writes: types.MustParseGlobs("docs/changelog.md"), DeclaredWrites: true},
	}
	d := DeriveTargetOrder(steps, nodes, nil)
	// "docs content" sorts after ". changelog", so the docs-writes-root-reads
	// direction is the one that yields.
	require.Equal(t, &DerivedOrder{
		Nodes:    nodes,
		Edges:    []DerivedEdge{{Writer: 0, Reader: 1, Ordered: true}},
		Dropped:  []DroppedEdge{{DerivedEdge: DerivedEdge{Writer: 1, Reader: 0}, Reason: "cycle"}},
		RunAfter: map[types.TargetRef][]types.TargetRef{docs: {root}},
		SameStep: d.SameStep, // the two targets run in different steps
	}, d)
}

// TestDeriveTargetOrderMutualTrioTieBreak is the workspace's own sibling-index
// shape at three projects: each writes its MAGUS.md and reads every sibling's,
// so all six edges derive. The tie-break must leave the same acyclic subset (the
// key-ascending direction) whatever order the batch happens to list nodes in.
func TestDeriveTargetOrderMutualTrioTieBreak(t *testing.T) {
	steps := []Step{
		{ProjectPath: "libs/a", Target: "index-generate"},
		{ProjectPath: "libs/b", Target: "index-generate"},
		{ProjectPath: "libs/c", Target: "index-generate"},
	}
	node := func(i int) TargetNode {
		p := steps[i].ProjectPath
		return TargetNode{Project: p, Target: "index-generate", Steps: []types.TargetRef{stepRef(steps[i])},
			Reads: types.MustParseGlobs("libs/**/MAGUS.md"), DeclaredReads: true,
			Writes: types.MustParseGlobs(p + "/MAGUS.md"), DeclaredWrites: true}
	}
	perms := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	for _, perm := range perms {
		nodes := []TargetNode{node(perm[0]), node(perm[1]), node(perm[2])}
		d := DeriveTargetOrder(steps, nodes, nil)
		var kept, dropped [][2]string
		for _, e := range d.Edges {
			kept = append(kept, [2]string{d.Nodes[e.Writer].Project, d.Nodes[e.Reader].Project})
		}
		for _, e := range d.Dropped {
			dropped = append(dropped, [2]string{d.Nodes[e.Writer].Project, d.Nodes[e.Reader].Project})
		}
		assert.ElementsMatch(t, [][2]string{{"libs/a", "libs/b"}, {"libs/a", "libs/c"}, {"libs/b", "libs/c"}}, kept,
			"permutation %v keeps only the key-ascending direction", perm)
		assert.ElementsMatch(t, [][2]string{{"libs/b", "libs/a"}, {"libs/c", "libs/a"}, {"libs/c", "libs/b"}}, dropped,
			"permutation %v drops the writer-after-reader direction, and keeps it for settling", perm)
	}
}

func TestDeriveTargetOrderWeakCycleDropped(t *testing.T) {
	steps := orderSteps()
	root, docs := stepKeys(steps)
	nodes := []TargetNode{
		// Declares no reads, so its whole-tree fallback overlaps everything: the
		// resulting cycle is an artifact of the over-approximation, so the weak edge
		// is the one that yields.
		{Project: ".", Target: "index", Steps: []types.TargetRef{root},
			Reads:  types.MustParseGlobs("**/*.md"),
			Writes: types.MustParseGlobs("MAGUS.md"), DeclaredWrites: true},
		{Project: "docs", Target: "index", Steps: []types.TargetRef{docs},
			Reads: types.MustParseGlobs("**/MAGUS.md"), DeclaredReads: true,
			Writes: types.MustParseGlobs("docs/MAGUS.md"), DeclaredWrites: true},
	}
	d := DeriveTargetOrder(steps, nodes, nil)
	require.Equal(t, []DerivedEdge{{Writer: 0, Reader: 1, Ordered: true}}, d.Edges,
		"the strong (declared) direction survives; the weak fallback direction is dropped")
}

// TestDeriveTargetOrderEntangledUnordered pins the motivating shape: chains on
// both sides of a coarse DependsOn edge write into each other's read sets. No
// step order can honor the against-the-grain edge, so it must come back
// unordered (a settling candidate), never a deadlocking wait and never a
// dropped coarse edge.
func TestDeriveTargetOrderEntangledUnordered(t *testing.T) {
	steps := orderSteps()
	root, docs := stepKeys(steps)
	nodes := []TargetNode{
		{Project: ".", Target: "changelog", Steps: []types.TargetRef{root},
			Reads: types.MustParseGlobs("releases/*.yaml"), DeclaredReads: true,
			Writes: types.MustParseGlobs("CHANGELOG.md"), DeclaredWrites: true},
		{Project: "docs", Target: "content", Steps: []types.TargetRef{docs},
			Reads: types.MustParseGlobs("CHANGELOG.md"), DeclaredReads: true,
			Writes: types.MustParseGlobs("docs/changelog.md"), DeclaredWrites: true},
		{Project: ".", Target: "graph", Steps: []types.TargetRef{root},
			Reads:  types.MustParseGlobs("**/*.md"),
			Writes: types.MustParseGlobs("gen/*.json"), DeclaredWrites: true},
	}
	d := DeriveTargetOrder(steps, nodes, nil)
	// Only the with-the-grain direction is admitted to RunAfter; the against-the-grain edge
	// induces nothing.
	assert.Equal(t, &DerivedOrder{
		Nodes: nodes,
		Edges: []DerivedEdge{
			// Writer's step already precedes the reader's via the coarse edge.
			{Writer: 0, Reader: 1, Ordered: true},
			// The reader's step ran first and nothing can reorder it: settle after.
			{Writer: 1, Reader: 2, weak: true, Ordered: false},
		},
		RunAfter: map[types.TargetRef][]types.TargetRef{docs: {root}},
		SameStep: d.SameStep, // two of the three targets share a step, which this test is not about
	}, d)
}

func TestDeriveTargetOrderIgnoredDirInvisibleToFallbackReader(t *testing.T) {
	steps := orderSteps()
	root, docs := stepKeys(steps)
	nodes := []TargetNode{
		{Project: "docs", Target: "site", Steps: []types.TargetRef{docs},
			Writes: types.MustParseGlobs("docs/gen/**"), DeclaredWrites: true},
		{Project: ".", Target: "graph", Steps: []types.TargetRef{root},
			Reads:      types.MustParseGlobs("**/*.md"),
			IgnoreDirs: []string{"gen"}},
	}
	d := DeriveTargetOrder(steps, nodes, nil)
	assert.Empty(t, d.Edges, "a fallback reader never walks its ignored dirs, so writes confined there derive nothing")
}

func TestTopoNodesWritersFirst(t *testing.T) {
	steps := orderSteps()
	root, docs := stepKeys(steps)
	nodes := []TargetNode{
		{Project: ".", Target: "graph", Steps: []types.TargetRef{root}, Reads: types.MustParseGlobs("**/*.md")},
		{Project: "docs", Target: "content", Steps: []types.TargetRef{docs},
			Reads: types.MustParseGlobs("src/*.txt"), DeclaredReads: true,
			Writes: types.MustParseGlobs("docs/changelog.md"), DeclaredWrites: true},
	}
	d := DeriveTargetOrder(steps, nodes, nil)
	require.Len(t, d.Edges, 1)
	assert.Equal(t, []int{1, 0}, d.TopoNodes())
}

// badgeFixture is the 2026-09-10 incident in miniature: one `ci` step whose chain runs a
// generator writing Go files and a badge reading every Go file, with nothing between them.
// order says whether the badge declares the ctx.needs edge that was the fix.
func badgeFixture(ordered bool) []TargetNode {
	step := ref(".", "ci")
	badge := TargetNode{
		Project: ".", Target: "coverage-badge", Steps: []types.TargetRef{step},
		Reads: types.MustParseGlobs("**/*.go"), DeclaredReads: true,
		Writes: types.MustParseGlobs("assets/coverage.svg"), DeclaredWrites: true,
	}
	if ordered {
		badge.Needs = Needs(ref(".", "generate"))
	}
	return []TargetNode{
		{Project: ".", Target: "ci", Steps: []types.TargetRef{step},
			Needs: Needs(ref(".", "generate"), ref(".", "coverage-badge"))},
		{Project: ".", Target: "generate", Steps: []types.TargetRef{step},
			Needs: Needs(ref(".", "mocks-generate"))},
		{Project: ".", Target: "mocks-generate", Steps: []types.TargetRef{step},
			Writes: types.MustParseGlobs("**/gen/mocks/*.go"), DeclaredWrites: true},
		badge,
	}
}

func TestFindSameStepConflictsRefusesTheUnorderedReader(t *testing.T) {
	t.Parallel()
	got := FindSameStepConflicts(badgeFixture(false), nil)
	require.Len(t, got, 1)
	assert.Equal(t, SameStepConflict{
		Step:      ref(".", "ci"),
		Writer:    ref(".", "mocks-generate"),
		Reader:    ref(".", "coverage-badge"),
		WriteGlob: "**/gen/mocks/*.go",
		ReadGlob:  "**/*.go",
	}, got[0])
	assert.True(t, got[0].SameProject(), "one project on both sides, so the run refuses rather than advises")
}

func TestFindSameStepConflictsClearedByANeedsPath(t *testing.T) {
	t.Parallel()
	// ctx.needs(generate) is transitive: the badge reaches the writer through the
	// composer, which is how the fix was actually written.
	assert.Empty(t, FindSameStepConflicts(badgeFixture(true), nil))
}

func TestFindSameStepConflictsIgnoresBaselineFallbacks(t *testing.T) {
	t.Parallel()
	nodes := badgeFixture(false)
	for i := range nodes {
		if nodes[i].Target == "coverage-badge" {
			// Same globs, no ctx.readsFiles behind them: the project baseline is a
			// whole-project over-approximation, and refusing a run over a guess costs
			// more than the stale read it would prevent.
			nodes[i].DeclaredReads = false
		}
	}
	assert.Empty(t, FindSameStepConflicts(nodes, nil))

	nodes = badgeFixture(false)
	for i := range nodes {
		if nodes[i].Target == "mocks-generate" {
			nodes[i].DeclaredWrites = false
		}
	}
	assert.Empty(t, FindSameStepConflicts(nodes, nil), "a fallback WRITER is a guess too")
}

func TestFindSameStepConflictsIgnoresCrossStepAndWriterFirst(t *testing.T) {
	t.Parallel()
	ci, gate := ref(".", "ci"), ref(".", "gate")
	// Same overlap, different steps: that is an edge DeriveTargetOrder derives, not a
	// plan to refuse.
	cross := []TargetNode{
		{Project: ".", Target: "gen", Steps: []types.TargetRef{ci},
			Writes: types.MustParseGlobs("gen/**/*.go"), DeclaredWrites: true},
		{Project: ".", Target: "read", Steps: []types.TargetRef{gate},
			Reads: types.MustParseGlobs("**/*.go"), DeclaredReads: true},
	}
	assert.Empty(t, FindSameStepConflicts(cross, nil))

	// The writer needs the reader, so the reader runs first by the body's own choice.
	// Reading what was there beforehand is a staleness question, not an unschedulable
	// plan.
	writerFirst := []TargetNode{
		{Project: ".", Target: "gen", Steps: []types.TargetRef{ci},
			Writes: types.MustParseGlobs("gen/**/*.go"), DeclaredWrites: true,
			Needs: Needs(ref(".", "read"))},
		{Project: ".", Target: "read", Steps: []types.TargetRef{ci},
			Reads: types.MustParseGlobs("**/*.go"), DeclaredReads: true},
	}
	assert.Empty(t, FindSameStepConflicts(writerFirst, nil))
}

// A refusal has to stand on a file. Two globs that intersect as globs and never as
// paths (this tree's "**/MAGUS.md" against "cmd/magus/completions/*") must not refuse a
// run, and two that meet on a real file must.
func TestWorkspaceOverlapWitnessNeedsAFileMatchingBothGlobs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, f := range []string{"internal/mocks/store.go", "gen/mocks/store.go", "dist/app.go", "MAGUS.md"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, f)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, f), []byte("x\n"), 0o644))
	}

	witness := WorkspaceOverlapWitness(root)
	assert.True(t, witness("**/mocks/*.go", "**/*.go", nil), "a file on disk matches both")
	assert.False(t, witness("cmd/magus/completions/*", "**/MAGUS.md", nil), "the globs intersect, no path does")
	assert.False(t, witness("**/mocks/*.go", "cmd/magus-termcast/*.go", nil), "the writer's files sit elsewhere")

	// The key never hashes a pattern's hits under a pruned dir, so they witness nothing
	// for a pattern: the core names every walk skips, and the reader's own spell dirs.
	// An exact path is hashed by stat and still counts.
	assert.False(t, witness("**/gen/mocks/*.go", "**/*.go", nil), "gen is pruned for every pattern read")
	assert.True(t, witness("**/gen/mocks/*.go", "gen/mocks/store.go", nil), "an exact read reaches in")
	assert.True(t, witness("dist/*.go", "**/*.go", nil))
	assert.False(t, witness("dist/*.go", "**/*.go", []string{"dist"}), "pruned by the reader's own spell dirs")

	// A pattern that names the pruned dir is hashed from there, so it witnesses too.
	assert.True(t, witness("**/gen/mocks/*.go", "gen/mocks/*.go", nil), "the read names gen/")
	assert.False(t, witness("dist/*.go", "gen/mocks/*.go", nil), "named, but the writer writes elsewhere")
}

// The race that failed `magus affected generate:rw`: mcp-tools-generate compiled
// internal/spell, which go:embeds internal/spell/gen/types, while spells-generate
// replayed that tree beside it in generate's one ctx.needs call. Declared as a read, the
// embedded tree has to make the pair a refusal; the pruned walk used to hide it.
func TestFindSameStepConflictsSeesAReadOfAGeneratedTree(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	f := filepath.Join(root, "internal/spell/gen/types/versionkey.buzz")
	require.NoError(t, os.MkdirAll(filepath.Dir(f), 0o755))
	require.NoError(t, os.WriteFile(f, []byte("x\n"), 0o644))

	gen := ref(".", "generate")
	nodes := func(ordered bool) []TargetNode {
		reader := TargetNode{Project: ".", Target: "mcp-tools-generate", Steps: []types.TargetRef{gen},
			Reads: types.MustParseGlobs("**/*.go", "internal/spell/gen/types/*.buzz"), DeclaredReads: true,
			Writes: types.MustParseGlobs("internal/handler/mcp/gen/registry.go"), DeclaredWrites: true}
		if ordered {
			reader.Needs = Needs(ref(".", "spells-generate"))
		}
		return []TargetNode{
			{Project: ".", Target: "generate", Steps: []types.TargetRef{gen},
				Needs: Needs(ref(".", "spells-generate"), ref(".", "mcp-tools-generate"))},
			{Project: ".", Target: "spells-generate", Steps: []types.TargetRef{gen},
				Writes: types.MustParseGlobs("internal/spell/gen/types/*.buzz"), DeclaredWrites: true},
			reader,
		}
	}

	witness := WorkspaceOverlapWitness(root)
	got := FindSameStepConflicts(nodes(false), witness)
	assert.Equal(t, SameStepConflicts{{
		Step: gen, Writer: ref(".", "spells-generate"), Reader: ref(".", "mcp-tools-generate"),
		WriteGlob: "internal/spell/gen/types/*.buzz", ReadGlob: "internal/spell/gen/types/*.buzz",
	}}, got)
	require.Error(t, got.Refusal())
	assert.Empty(t, FindSameStepConflicts(nodes(true), witness), "ctx.needs(spells_generate) orders the pair")
}

// With a witness the same fixture is refused only when its overlap is real on disk.
func TestFindSameStepConflictsHonorsTheWitness(t *testing.T) {
	t.Parallel()
	never := OverlapWitness(func(write, read string, ignore []string) bool { return false })
	assert.Empty(t, FindSameStepConflicts(badgeFixture(false), never), "no witness, no refusal")
	always := OverlapWitness(func(write, read string, ignore []string) bool { return true })
	assert.NotEmpty(t, FindSameStepConflicts(badgeFixture(false), always))
}

// A cross-project pair is real and reported, but the refusal is reserved for a pair
// whose fix is one ctx.needs in the file that composes both.
func TestCrossProjectConflictAdvisesInsteadOfRefusing(t *testing.T) {
	t.Parallel()
	nodes := badgeFixture(false)
	for i := range nodes {
		if nodes[i].Target == "mocks-generate" {
			nodes[i].Project = "libs/other"
		}
	}
	conflicts := FindSameStepConflicts(nodes, nil)
	require.NotEmpty(t, conflicts)
	for _, c := range conflicts {
		assert.False(t, c.SameProject())
	}
	require.NoError(t, conflicts.Refusal(), "cross-project pairs do not refuse")
	assert.Contains(t, conflicts.Advice(), "MGS4008")
	assert.Empty(t, FindSameStepConflicts(badgeFixture(false), nil).Advice(),
		"a same-project pair is refused, not advised")
}

// TestChainCallsGroupByCall: the docs lint shape, `ctx.needs(format);
// ctx.needs(conventions);`, where format reaches the generators conventions reads. The
// second call is ordered after the first by the body, and that has to survive on the
// node or every composer with two calls reads as unordered.
func TestChainCallsGroupByCall(t *testing.T) {
	t.Parallel()
	p := &types.Project{
		Path: "docs",
		TargetChains: map[string][]types.ChainStep{
			"lint":   types.Needs("format", "gone:lint").Needs("conventions", "spelling").Needs("links"),
			"orphan": types.Needs("gone:a"),
		},
	}
	none := func(string) *types.Project { return nil }
	calls := ChainCalls(p, "lint", none)
	assert.Equal(t,
		Needs(ref("docs", "format")).Needs(ref("docs", "conventions"), ref("docs", "spelling")).Needs(ref("docs", "links")),
		calls, "a step that resolves to nothing neither orders nor is ordered")

	assert.Equal(t, []types.TargetRef{ref("docs", "format"), ref("docs", "conventions"), ref("docs", "spelling")},
		calls.before(ref("docs", "links")))
	assert.Equal(t, []types.TargetRef{ref("docs", "format")}, calls.before(ref("docs", "spelling")),
		"a sibling in the same call is not before")
	assert.Nil(t, calls.before(ref("docs", "format")), "nothing precedes the first call")
	assert.Nil(t, calls.before(ref("docs", "nobody")), "not a member")
	assert.Empty(t, ChainCalls(p, "orphan", none), "no empty call is kept")
}

func TestDeclaredNodesHonorsTheCallOrder(t *testing.T) {
	t.Parallel()
	p := ciFixtureProject(false)
	// The badge stays unaware of the generator; ci's body runs it in a second call.
	p.TargetChains["ci"] = types.Needs("generate").Needs("coverage-badge")
	assert.Empty(t, FindSameStepConflicts(DeclaredNodes(p, "ci", nil), nil),
		"a later ctx.needs call is ordered after the earlier one")
}

// TestCallOrderReachesUnderTheLaterComposer is the docs ci shape: `ctx.needs(generate,
// lint, test); ctx.needs(build);` with the render under build reading pages that format,
// under lint, rewrites. The render never names format; build's call does, and nothing
// under build starts before build does.
func TestCallOrderReachesUnderTheLaterComposer(t *testing.T) {
	t.Parallel()
	p := &types.Project{
		Path: "docs", Name: "docs",
		TargetChains: map[string][]types.ChainStep{
			"ci":    types.Needs("generate", "lint").Needs("build"),
			"lint":  types.Needs("format"),
			"build": types.Needs("generate").Needs("site-generate"),
		},
		TargetInputs:  map[string][]types.InputRef{"site-generate": {{Project: "docs", Glob: "**/*.md"}}},
		TargetUpdates: map[string][]types.UpdateRef{"format": {{Project: "docs", Glob: "*.md"}}},
		TargetOutputs: map[string][]types.OutputRef{"site-generate": {{Project: "docs", Glob: "gen/**"}}},
	}
	assert.Empty(t, FindSameStepConflicts(DeclaredNodes(p, "ci", nil), nil))

	// One call instead: build fans out beside lint, and the render races the rewrite.
	p.TargetChains["ci"] = types.Needs("generate", "lint", "build")
	got := FindSameStepConflicts(DeclaredNodes(p, "ci", nil), nil)
	require.Len(t, got, 1)
	assert.Equal(t, SameStepConflict{
		Step:      got[0].Step, // the step and the glob pair are not what this test pins
		Writer:    ref("docs", "format"),
		Reader:    ref("docs", "site-generate"),
		WriteGlob: got[0].WriteGlob,
		ReadGlob:  got[0].ReadGlob,
	}, got[0])
}

// TestAComposersOwnNeedsDoNotOrderItsMembers pins the hop the upward walk must not
// take: ci needs both the writer and the reader's composer in ONE call, so reaching the
// writer through ci's needs would read two unordered siblings as sequenced.
func TestAComposersOwnNeedsDoNotOrderItsMembers(t *testing.T) {
	t.Parallel()
	p := &types.Project{
		Path: ".", Name: "root",
		TargetChains: map[string][]types.ChainStep{
			"ci":    types.Needs("mocks-generate", "check"),
			"check": types.Needs("coverage-badge"),
		},
		TargetInputs:  map[string][]types.InputRef{"coverage-badge": {{Project: ".", Glob: "**/*.go"}}},
		TargetOutputs: map[string][]types.OutputRef{"mocks-generate": {{Project: ".", Glob: "**/mocks/*.go"}}},
	}
	require.Len(t, FindSameStepConflicts(DeclaredNodes(p, "ci", nil), nil), 1)
}

func TestSameStepRefusalNamesBothFixes(t *testing.T) {
	t.Parallel()
	err := FindSameStepConflicts(badgeFixture(false), nil).Refusal()
	require.Error(t, err)
	require.ErrorIs(t, err, types.UnorderedSameStepWrite)
	msg := err.Error()
	for _, want := range []string{
		". coverage-badge", ". mocks-generate", "**/*.go", "**/gen/mocks/*.go",
		"ctx.needs(mocks-generate)", "ctx.readsFiles",
	} {
		assert.Contains(t, msg, want)
	}

	require.NoError(t, SameStepConflicts(nil).Refusal(), "no conflicts is not an error")
}

// TestOrderingIsNotInheritedAcrossComposers: composer A fans the reader and the writer
// out together while composer B runs the writer first, and one step composes both. The
// reader runs once, wherever it is reached first, so B's sequencing proves nothing about
// A's fan-out and the pair stays unordered.
func TestOrderingIsNotInheritedAcrossComposers(t *testing.T) {
	t.Parallel()
	p := &types.Project{
		Path: ".", Name: "root",
		TargetChains: map[string][]types.ChainStep{
			"ci": types.Needs("a", "b"),
			"a":  types.Needs("mocks-generate", "coverage-badge"),
			"b":  types.Needs("mocks-generate").Needs("coverage-badge"),
		},
		TargetInputs:  map[string][]types.InputRef{"coverage-badge": {{Project: ".", Glob: "**/*.go"}}},
		TargetOutputs: map[string][]types.OutputRef{"mocks-generate": {{Project: ".", Glob: "**/mocks/*.go"}}},
	}
	got := FindSameStepConflicts(DeclaredNodes(p, "ci", nil), nil)
	require.Len(t, got, 1)
	assert.Equal(t, ref(".", "coverage-badge"), got[0].Reader)

	// With A staged the same way, every composer agrees and the pair is ordered.
	p.TargetChains["a"] = types.Needs("mocks-generate").Needs("coverage-badge")
	assert.Empty(t, FindSameStepConflicts(DeclaredNodes(p, "ci", nil), nil))
}

// TestDeriveTargetOrderPartlySharedStepsStillDerive: a writer in steps S1 and S2 against
// a reader in S2 and S3 meets across S1 and S3, so the edge derives and the projection
// orders those steps; the race inside S2, where both run, is the same-step question and
// is reported there rather than lost.
func TestDeriveTargetOrderPartlySharedStepsStillDerive(t *testing.T) {
	t.Parallel()
	steps := []Step{{ProjectPath: "a", Target: "gen"}, {ProjectPath: "b", Target: "check"}, {ProjectPath: "c", Target: "check"}}
	s1, s2, s3 := stepRef(steps[0]), stepRef(steps[1]), stepRef(steps[2])
	nodes := []TargetNode{
		{Project: "a", Target: "gen", Steps: []types.TargetRef{s1, s2},
			Writes: types.MustParseGlobs("a/out/*.md"), DeclaredWrites: true},
		{Project: "b", Target: "check", Steps: []types.TargetRef{s2, s3},
			Reads: types.MustParseGlobs("a/out/**"), DeclaredReads: true},
	}
	d := DeriveTargetOrder(steps, nodes, nil)
	require.Len(t, d.Edges, 1, "the pair is not confined to one step, so it is an edge")
	assert.True(t, d.Edges[0].Ordered)
	assert.ElementsMatch(t, []types.TargetRef{s1, s2}, d.RunAfter[s3])
	require.Len(t, d.SameStep, 1, "and the S2 race is still reported")
	assert.Equal(t, s2, d.SameStep[0].Step)

	nodes[1].Steps = []types.TargetRef{s1, s2}
	assert.Empty(t, DeriveTargetOrder(steps, nodes, nil).Edges, "identical step sets are the body's own sequencing")
}

// TestOrderingDoesNotDependOnWhichPairAsksFirst is the docs workspace's shape: preflight
// is reached under generate's first call and again under the site render, which build
// runs after generate. Answering "is the render after content-generate" walks through
// preflight's composers back to the render, and a memoized recursion answered that with
// whichever partial set it was building at the time, so the doctor and the run disagreed
// on the same tree. Every node order must give the same answer.
func TestOrderingDoesNotDependOnWhichPairAsksFirst(t *testing.T) {
	t.Parallel()
	p := &types.Project{
		Path: "docs", Name: "docs",
		TargetChains: map[string][]types.ChainStep{
			"ci":            types.Needs("generate").Needs("build"),
			"generate":      types.Needs("preflight").Needs("content-generate"),
			"build":         types.Needs("generate").Needs("site-generate"),
			"site-generate": types.Needs("build-hljs"),
			"build-hljs":    types.Needs("preflight"),
		},
		TargetInputs:  map[string][]types.InputRef{"site-generate": {{Project: "docs", Glob: "**/*.md"}}},
		TargetOutputs: map[string][]types.OutputRef{"content-generate": {{Project: "docs", Glob: "reference/*.md"}}},
	}
	nodes := DeclaredNodes(p, "ci", nil)
	for shift := range nodes {
		rotated := append(slices.Clone(nodes[shift:]), nodes[:shift]...)
		assert.Empty(t, FindSameStepConflicts(rotated, nil), "rotation %d", shift)
		o := newNodeOrder(rotated)
		assert.True(t, o.runsAfter(ref("docs", "site-generate"), ref("docs", "content-generate")), "rotation %d", shift)
		assert.Empty(t, o.preceded[ref("docs", "preflight")], "nothing precedes preflight, whichever composer reaches it first")
	}
}

// BenchmarkFindSameStepConflicts is the doctor's shape at workspace scale: one composer
// over sixty members, every one declared, so every pair reaches the overlap test.
func BenchmarkFindSameStepConflicts(b *testing.B) {
	const n = 60
	names := make([]string, n)
	inputs := map[string][]types.InputRef{}
	outputs := map[string][]types.OutputRef{}
	for i := range n {
		name := fmt.Sprintf("t%d", i)
		names[i] = name
		inputs[name] = []types.InputRef{{Project: ".", Glob: fmt.Sprintf("src/%d/**/*.go", i%7)}, {Project: ".", Glob: "**/*.md"}}
		outputs[name] = []types.OutputRef{{Project: ".", Glob: fmt.Sprintf("gen/%d/*.go", i%5)}, {Project: ".", Glob: fmt.Sprintf("docs/%d.md", i)}}
	}
	// Three calls of twenty: the shape a composer with a couple of stages takes.
	chain := types.Needs(names[:20]...).Needs(names[20:40]...).Needs(names[40:]...)
	p := &types.Project{
		Path: ".", Name: "root",
		TargetChains:  map[string][]types.ChainStep{"ci": chain},
		TargetInputs:  inputs,
		TargetOutputs: outputs,
	}
	nodes := DeclaredNodes(p, "ci", nil)
	b.ResetTimer()
	for range b.N {
		FindSameStepConflicts(nodes, nil)
	}
}

func TestDeriveTargetOrderCarriesSameStepConflicts(t *testing.T) {
	t.Parallel()
	steps := []Step{{ProjectPath: ".", Target: "ci"}}
	d := DeriveTargetOrder(steps, badgeFixture(false), nil)
	assert.Empty(t, d.Edges, "a same-step pair is never an edge; no schedule can express it")
	require.Len(t, d.SameStep, 1)
	assert.Equal(t, ref(".", "coverage-badge"), d.SameStep[0].Reader)
}

// ciFixtureProject is the same shape as a workspace declares it, for the collection that
// has no batch to read: one composer, its chain, and the declarations on each member.
func ciFixtureProject(ordered bool) *types.Project {
	var badgeChain types.Chain
	if ordered {
		badgeChain = types.Needs("generate")
	}
	return &types.Project{
		Path: ".", Name: "root",
		TargetChains: map[string][]types.ChainStep{
			"ci":             types.Needs("generate", "coverage-badge"),
			"generate":       types.Needs("mocks-generate"),
			"coverage-badge": badgeChain,
		},
		TargetInputs: map[string][]types.InputRef{
			"coverage-badge": {{Project: ".", Glob: "**/*.go"}},
		},
		TargetOutputs: map[string][]types.OutputRef{
			"mocks-generate": {{Project: ".", Glob: "**/gen/mocks/*.go"}},
			"coverage-badge": {{Project: ".", Glob: "assets/coverage.svg"}},
		},
	}
}

func TestDeclaredNodesFeedsTheSamePredicate(t *testing.T) {
	t.Parallel()
	unordered := FindSameStepConflicts(DeclaredNodes(ciFixtureProject(false), "ci", nil), nil)
	require.Len(t, unordered, 1)
	assert.Equal(t, SameStepConflict{
		Step:      unordered[0].Step, // the step and the glob pair are not what this test pins
		Writer:    ref(".", "mocks-generate"),
		Reader:    ref(".", "coverage-badge"),
		WriteGlob: unordered[0].WriteGlob,
		ReadGlob:  unordered[0].ReadGlob,
	}, unordered[0])

	assert.Empty(t, FindSameStepConflicts(DeclaredNodes(ciFixtureProject(true), "ci", nil), nil),
		"the ctx.needs edge that fixed the workspace has to silence this too")
}

func TestDeclaredNodesRootsGlobsAtTheWorkspace(t *testing.T) {
	t.Parallel()
	p := &types.Project{
		Path: "docs", Name: "docs",
		TargetChains:  map[string][]types.ChainStep{"gen": types.Needs("pages")},
		TargetInputs:  map[string][]types.InputRef{"pages": {{Project: "docs", Glob: "src/**/*.md"}}},
		TargetUpdates: map[string][]types.UpdateRef{"pages": {{Project: ".", Glob: "MAGUS.md"}}},
	}
	nodes := DeclaredNodes(p, "gen", nil)
	var pages TargetNode
	for _, n := range nodes {
		if n.Target == "pages" {
			pages = n
		}
	}
	// Every ref carries its owner from resolution, an update is read AND written, and
	// ctx.modifiesExistingFiles is a declared write.
	assert.Equal(t, TargetNode{
		Project:        "docs",
		Target:         "pages",
		Steps:          pages.Steps,
		Reads:          []types.Glob{{Pattern: "MAGUS.md"}, {Pattern: "docs/src/**/*.md"}},
		Writes:         []types.Glob{{Pattern: "MAGUS.md"}},
		DeclaredReads:  pages.DeclaredReads,
		DeclaredWrites: true,
		IgnoreDirs:     pages.IgnoreDirs,
		Needs:          pages.Needs,
	}, pages)
}

func TestDeclaredNodesSkipsAnUnresolvableCrossProjectStep(t *testing.T) {
	t.Parallel()
	p := &types.Project{
		Path:         ".",
		TargetChains: map[string][]types.ChainStep{"ci": types.Needs("gone:gen")},
	}
	nodes := DeclaredNodes(p, "ci", func(string) *types.Project { return nil })
	require.Len(t, nodes, 1)
	assert.Empty(t, nodes[0].Needs, "a step magus cannot resolve contributes nothing rather than a guess")
}
