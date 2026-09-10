package cache

import (
	"os"
	"path/filepath"
	"strings"
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

func stepKeys(steps []Step) (root, docs string) {
	return stepKey(steps[0]), stepKey(steps[1])
}

func TestDeriveTargetOrderWriterBeforeReader(t *testing.T) {
	steps := []Step{
		{ProjectPath: "a", Target: "gen"},
		{ProjectPath: "b", Target: "check"},
	}
	nodes := []TargetNode{
		{Project: "a", Target: "gen", Steps: []string{stepKey(steps[0])},
			Writes: []string{"a/out/report.md"}, DeclaredWrites: true,
			Reads: []string{"a/src/**"}, DeclaredReads: true},
		{Project: "b", Target: "check", Steps: []string{stepKey(steps[1])},
			Reads: []string{"a/out/*.md"}, DeclaredReads: true},
	}
	d := DeriveTargetOrder(steps, nodes, nil)
	require.Equal(t, []DerivedEdge{{Writer: 0, Reader: 1, Ordered: true}}, d.Edges)
	assert.Equal(t, map[string][]string{stepKey(steps[1]): {stepKey(steps[0])}}, d.RunAfter)
}

func TestDeriveTargetOrderNoSelfEdge(t *testing.T) {
	steps := orderSteps()
	root, docs := stepKeys(steps)
	nodes := []TargetNode{
		// Reads what it writes: not an edge, or every regenerator would cycle on
		// itself.
		{Project: ".", Target: "index", Steps: []string{root},
			Reads: []string{"MAGUS.md"}, DeclaredReads: true,
			Writes: []string{"MAGUS.md"}, DeclaredWrites: true},
		// Same-step overlap is the body's own ctx.needs ordering, not derivation's.
		{Project: ".", Target: "sibling", Steps: []string{root},
			Reads: []string{"MAGUS.md"}, DeclaredReads: true},
		// A cross-step reader still derives, proving the guards above are the only
		// thing suppressing the first two pairs.
		{Project: "docs", Target: "reader", Steps: []string{docs},
			Reads: []string{"MAGUS.md"}, DeclaredReads: true},
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
		{Project: ".", Target: "changelog", Steps: []string{root},
			Reads: []string{"docs/changelog.md"}, DeclaredReads: true,
			Writes: []string{"CHANGELOG.md"}, DeclaredWrites: true},
		{Project: "docs", Target: "content", Steps: []string{docs},
			Reads: []string{"CHANGELOG.md"}, DeclaredReads: true,
			Writes: []string{"docs/changelog.md"}, DeclaredWrites: true},
	}
	d := DeriveTargetOrder(steps, nodes, nil)
	// "docs content" sorts after ". changelog", so the docs-writes-root-reads
	// direction is the one that yields.
	require.Equal(t, []DerivedEdge{{Writer: 0, Reader: 1, Ordered: true}}, d.Edges)
	require.Equal(t, []DroppedEdge{{DerivedEdge: DerivedEdge{Writer: 1, Reader: 0}, Reason: "cycle"}}, d.Dropped)
	assert.Equal(t, map[string][]string{docs: {root}}, d.RunAfter)
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
		return TargetNode{Project: p, Target: "index-generate", Steps: []string{stepKey(steps[i])},
			Reads: []string{"libs/**/MAGUS.md"}, DeclaredReads: true,
			Writes: []string{p + "/MAGUS.md"}, DeclaredWrites: true}
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
		{Project: ".", Target: "index", Steps: []string{root},
			Reads:  []string{"**/*.md"},
			Writes: []string{"MAGUS.md"}, DeclaredWrites: true},
		{Project: "docs", Target: "index", Steps: []string{docs},
			Reads: []string{"**/MAGUS.md"}, DeclaredReads: true,
			Writes: []string{"docs/MAGUS.md"}, DeclaredWrites: true},
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
		{Project: ".", Target: "changelog", Steps: []string{root},
			Reads: []string{"releases/*.yaml"}, DeclaredReads: true,
			Writes: []string{"CHANGELOG.md"}, DeclaredWrites: true},
		{Project: "docs", Target: "content", Steps: []string{docs},
			Reads: []string{"CHANGELOG.md"}, DeclaredReads: true,
			Writes: []string{"docs/changelog.md"}, DeclaredWrites: true},
		{Project: ".", Target: "graph", Steps: []string{root},
			Reads:  []string{"**/*.md"},
			Writes: []string{"gen/*.json"}, DeclaredWrites: true},
	}
	d := DeriveTargetOrder(steps, nodes, nil)
	require.Equal(t, []DerivedEdge{
		// Writer's step already precedes the reader's via the coarse edge.
		{Writer: 0, Reader: 1, Ordered: true},
		// The reader's step ran first and nothing can reorder it: settle after.
		{Writer: 1, Reader: 2, weak: true, Ordered: false},
	}, d.Edges)
	assert.Equal(t, map[string][]string{docs: {root}}, d.RunAfter,
		"only the with-the-grain direction is admitted; the against-the-grain edge induces nothing")
}

func TestDeriveTargetOrderIgnoredDirInvisibleToFallbackReader(t *testing.T) {
	steps := orderSteps()
	root, docs := stepKeys(steps)
	nodes := []TargetNode{
		{Project: "docs", Target: "site", Steps: []string{docs},
			Writes: []string{"docs/gen/**"}, DeclaredWrites: true},
		{Project: ".", Target: "graph", Steps: []string{root},
			Reads:      []string{"**/*.md"},
			IgnoreDirs: []string{"gen"}},
	}
	d := DeriveTargetOrder(steps, nodes, nil)
	assert.Empty(t, d.Edges, "a fallback reader never walks its ignored dirs, so writes confined there derive nothing")
}

func TestGlobsOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"docs/changelog.md", "**/*.md", true},
		{"docs/changelog.md", "docs/changelog.md", true},
		{"docs/changelog.md", "CHANGELOG.md", false},
		{"proto/gen/descriptor.binpb", "gen/*.json", false},
		{"gen/*.json", "**/*.md", false},
		{"gen/*.json", "gen/**", true},
		{"reference/buzz/*.md", "**/*.md", true},
		{"docs/**", "docs/gen/site/index.html", true},
		{"a/*.md", "b/*.md", false},
		{"**", "anything/at/all.txt", true},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, globsOverlap(tc.a, tc.b), "globsOverlap(%q, %q)", tc.a, tc.b)
		assert.Equal(t, tc.want, globsOverlap(tc.b, tc.a), "globsOverlap(%q, %q)", tc.b, tc.a)
	}
}

func TestTopoNodesWritersFirst(t *testing.T) {
	steps := orderSteps()
	root, docs := stepKeys(steps)
	nodes := []TargetNode{
		{Project: ".", Target: "graph", Steps: []string{root}, Reads: []string{"**/*.md"}},
		{Project: "docs", Target: "content", Steps: []string{docs},
			Reads: []string{"src/*.txt"}, DeclaredReads: true,
			Writes: []string{"docs/changelog.md"}, DeclaredWrites: true},
	}
	d := DeriveTargetOrder(steps, nodes, nil)
	require.Len(t, d.Edges, 1)
	assert.Equal(t, []int{1, 0}, d.TopoNodes())
}

// badgeFixture is the 2026-09-10 incident in miniature: one `ci` step whose chain runs a
// generator writing Go files and a badge reading every Go file, with nothing between them.
// order says whether the badge declares the ctx.needs edge that was the fix.
func badgeFixture(ordered bool) []TargetNode {
	step := DepKey(".", "ci")
	badge := TargetNode{
		Project: ".", Target: "coverage-badge", Steps: []string{step},
		Reads: []string{"**/*.go"}, DeclaredReads: true,
		Writes: []string{"assets/coverage.svg"}, DeclaredWrites: true,
	}
	if ordered {
		badge.Needs = []Need{{Key: DepKey(".", "generate")}}
	}
	return []TargetNode{
		{Project: ".", Target: "ci", Steps: []string{step},
			Needs: []Need{{Key: DepKey(".", "generate")}, {Key: DepKey(".", "coverage-badge")}}},
		{Project: ".", Target: "generate", Steps: []string{step},
			Needs: []Need{{Key: DepKey(".", "mocks-generate")}}},
		{Project: ".", Target: "mocks-generate", Steps: []string{step},
			Writes: []string{"**/gen/mocks/*.go"}, DeclaredWrites: true},
		badge,
	}
}

func TestFindSameStepConflictsRefusesTheUnorderedReader(t *testing.T) {
	t.Parallel()
	got := FindSameStepConflicts(badgeFixture(false), nil)
	require.Len(t, got, 1)
	assert.Equal(t, SameStepConflict{
		Step:   DepKey(".", "ci"),
		Writer: DepKey(".", "mocks-generate"),
		Reader: DepKey(".", "coverage-badge"),
		Write:  "**/gen/mocks/*.go",
		Read:   "**/*.go",
		// One project on both sides, so the run refuses rather than advises.
		SameProject: true,
	}, got[0])
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
	ci, gate := DepKey(".", "ci"), DepKey(".", "gate")
	// Same overlap, different steps: that is an edge DeriveTargetOrder derives, not a
	// plan to refuse.
	cross := []TargetNode{
		{Project: ".", Target: "gen", Steps: []string{ci},
			Writes: []string{"gen/**/*.go"}, DeclaredWrites: true},
		{Project: ".", Target: "read", Steps: []string{gate},
			Reads: []string{"**/*.go"}, DeclaredReads: true},
	}
	assert.Empty(t, FindSameStepConflicts(cross, nil))

	// The writer needs the reader, so the reader runs first by the body's own choice.
	// Reading what was there beforehand is a staleness question, not an unschedulable
	// plan.
	writerFirst := []TargetNode{
		{Project: ".", Target: "gen", Steps: []string{ci},
			Writes: []string{"gen/**/*.go"}, DeclaredWrites: true,
			Needs: []Need{{Key: DepKey(".", "read")}}},
		{Project: ".", Target: "read", Steps: []string{ci},
			Reads: []string{"**/*.go"}, DeclaredReads: true},
	}
	assert.Empty(t, FindSameStepConflicts(writerFirst, nil))
}

// A refusal has to stand on a file. Two globs that intersect as globs and never as
// paths (this tree's "**/MAGUS.md" against "cmd/magus/completions/*") must not refuse a
// run, and two that meet on a real file must.
func TestWorkspaceOverlapWitnessNeedsAFileMatchingBothGlobs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gen", "mocks"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "gen", "mocks", "store.go"), []byte("package mocks\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "MAGUS.md"), []byte("# index\n"), 0o644))

	witness := WorkspaceOverlapWitness(root)
	assert.True(t, witness("**/gen/mocks/*.go", "**/*.go", nil), "a file on disk matches both")
	assert.False(t, witness("cmd/magus/completions/*", "**/MAGUS.md", nil), "the globs intersect, no path does")
	assert.False(t, witness("**/gen/mocks/*.go", "cmd/magus-termcast/*.go", nil), "the writer's files sit elsewhere")

	// The key never hashes a pattern's hits under a pruned dir, so they witness nothing
	// for a pattern; an exact path is hashed by stat and still counts.
	assert.False(t, witness("**/gen/mocks/*.go", "**/*.go", []string{"gen"}), "pruned for a pattern read")
	assert.True(t, witness("**/gen/mocks/*.go", "gen/mocks/store.go", []string{"gen"}), "an exact read reaches in")
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
		assert.False(t, c.SameProject)
	}
	assert.NoError(t, SameStepConflictError(conflicts), "cross-project pairs do not refuse")
	assert.Contains(t, SameStepAdvice(conflicts), "MGS4008")
	assert.Empty(t, SameStepAdvice(FindSameStepConflicts(badgeFixture(false), nil)),
		"a same-project pair is refused, not advised")
}

// TestChainNeedsKeepTheCallIndex: the docs lint shape, `ctx.needs(format);
// ctx.needs(conventions);`, where format reaches the generators conventions reads. The
// second call is ordered after the first by the body, and that has to survive on the
// node or every composer with two calls reads as unordered.
func TestChainNeedsKeepTheCallIndex(t *testing.T) {
	t.Parallel()
	chain := []types.ChainStep{
		{Target: "format"}, {Project: "gone", Target: "lint"},
		{Target: "conventions", CallIndex: 1}, {Target: "spelling", CallIndex: 1},
		{Target: "links", CallIndex: 2},
	}
	keyOf := func(cs types.ChainStep) (string, bool) {
		if cs.Project == "gone" {
			return "", false
		}
		return DepKey("docs", cs.Target), true
	}
	needs := ChainNeeds(chain, keyOf)
	assert.Equal(t, []Need{
		{Key: DepKey("docs", "format")},
		{Key: DepKey("docs", "conventions"), CallIndex: 1},
		{Key: DepKey("docs", "spelling"), CallIndex: 1},
		{Key: DepKey("docs", "links"), CallIndex: 2},
	}, needs, "a step that resolves to nothing neither orders nor is ordered")

	n := TargetNode{Needs: needs}
	assert.Equal(t, []string{DepKey("docs", "format"), DepKey("docs", "conventions"), DepKey("docs", "spelling")},
		n.MembersBefore(DepKey("docs", "links")))
	assert.Equal(t, []string{DepKey("docs", "format")}, n.MembersBefore(DepKey("docs", "spelling")),
		"a sibling in the same call is not before")
	assert.Nil(t, n.MembersBefore(DepKey("docs", "format")), "nothing precedes the first call")
	assert.Nil(t, n.MembersBefore(DepKey("docs", "nobody")), "not a member")
	assert.Empty(t, ChainNeeds([]types.ChainStep{{Project: "gone", Target: "a"}}, keyOf))
}

func TestDeclaredNodesHonorsTheCallOrder(t *testing.T) {
	t.Parallel()
	p := ciFixtureProject(false)
	// The badge stays unaware of the generator; ci's body runs it in a second call.
	p.TargetChains["ci"] = []types.ChainStep{{Target: "generate"}, {Target: "coverage-badge", CallIndex: 1}}
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
			"ci":    {{Target: "generate"}, {Target: "lint"}, {Target: "build", CallIndex: 1}},
			"lint":  {{Target: "format"}},
			"build": {{Target: "generate"}, {Target: "site-generate", CallIndex: 1}},
		},
		TargetInputs:  map[string][]types.InputRef{"site-generate": {{Glob: "**/*.md"}}},
		TargetUpdates: map[string][]types.UpdateRef{"format": {{Glob: "*.md"}}},
		TargetOutputs: map[string][]types.OutputRef{"site-generate": {{Glob: "gen/**"}}},
	}
	assert.Empty(t, FindSameStepConflicts(DeclaredNodes(p, "ci", nil), nil))

	// One call instead: build fans out beside lint, and the render races the rewrite.
	p.TargetChains["ci"] = []types.ChainStep{{Target: "generate"}, {Target: "lint"}, {Target: "build"}}
	got := FindSameStepConflicts(DeclaredNodes(p, "ci", nil), nil)
	require.Len(t, got, 1)
	assert.Equal(t, DepKey("docs", "site-generate"), got[0].Reader)
	assert.Equal(t, DepKey("docs", "format"), got[0].Writer)
}

// TestAComposersOwnNeedsDoNotOrderItsMembers pins the hop the upward walk must not
// take: ci needs both the writer and the reader's composer in ONE call, so reaching the
// writer through ci's needs would read two unordered siblings as sequenced.
func TestAComposersOwnNeedsDoNotOrderItsMembers(t *testing.T) {
	t.Parallel()
	p := &types.Project{
		Path: ".", Name: "root",
		TargetChains: map[string][]types.ChainStep{
			"ci":    {{Target: "mocks-generate"}, {Target: "check"}},
			"check": {{Target: "coverage-badge"}},
		},
		TargetInputs:  map[string][]types.InputRef{"coverage-badge": {{Glob: "**/*.go"}}},
		TargetOutputs: map[string][]types.OutputRef{"mocks-generate": {{Glob: "**/mocks/*.go"}}},
	}
	require.Len(t, FindSameStepConflicts(DeclaredNodes(p, "ci", nil), nil), 1)
}

func TestSameStepConflictErrorNamesBothFixes(t *testing.T) {
	t.Parallel()
	err := SameStepConflictError(FindSameStepConflicts(badgeFixture(false), nil))
	require.Error(t, err)
	require.ErrorIs(t, err, types.UnorderedSameStepWrite)
	msg := err.Error()
	for _, want := range []string{
		". coverage-badge", ". mocks-generate", "**/*.go", "**/gen/mocks/*.go",
		"ctx.needs(mocks-generate)", "ctx.readsFiles",
	} {
		assert.Contains(t, msg, want)
	}
	assert.NotContains(t, msg, nodeKeySep, "a node key's separator is a control byte; never printed raw")

	assert.NoError(t, SameStepConflictError(nil), "no conflicts is not an error")
}

func TestDeriveTargetOrderCarriesSameStepConflicts(t *testing.T) {
	t.Parallel()
	steps := []Step{{ProjectPath: ".", Target: "ci"}}
	d := DeriveTargetOrder(steps, badgeFixture(false), nil)
	assert.Empty(t, d.Edges, "a same-step pair is never an edge; no schedule can express it")
	require.Len(t, d.SameStep, 1)
	assert.Equal(t, DepKey(".", "coverage-badge"), d.SameStep[0].Reader)
}

// ciFixtureProject is the same shape as a workspace declares it, for the collection that
// has no batch to read: one composer, its chain, and the declarations on each member.
func ciFixtureProject(ordered bool) *types.Project {
	badgeChain := []types.ChainStep{}
	if ordered {
		badgeChain = append(badgeChain, types.ChainStep{Target: "generate"})
	}
	return &types.Project{
		Path: ".", Name: "root",
		TargetChains: map[string][]types.ChainStep{
			"ci":             {{Target: "generate"}, {Target: "coverage-badge"}},
			"generate":       {{Target: "mocks-generate"}},
			"coverage-badge": badgeChain,
		},
		TargetInputs: map[string][]types.InputRef{
			"coverage-badge": {{Glob: "**/*.go"}},
		},
		TargetOutputs: map[string][]types.OutputRef{
			"mocks-generate": {{Glob: "**/gen/mocks/*.go"}},
			"coverage-badge": {{Glob: "assets/coverage.svg"}},
		},
	}
}

func TestDeclaredNodesFeedsTheSamePredicate(t *testing.T) {
	t.Parallel()
	unordered := FindSameStepConflicts(DeclaredNodes(ciFixtureProject(false), "ci", nil), nil)
	require.Len(t, unordered, 1)
	assert.Equal(t, DepKey(".", "coverage-badge"), unordered[0].Reader)
	assert.Equal(t, DepKey(".", "mocks-generate"), unordered[0].Writer)

	assert.Empty(t, FindSameStepConflicts(DeclaredNodes(ciFixtureProject(true), "ci", nil), nil),
		"the ctx.needs edge that fixed the workspace has to silence this too")
}

func TestDeclaredNodesRootsGlobsAtTheWorkspace(t *testing.T) {
	t.Parallel()
	p := &types.Project{
		Path: "docs", Name: "docs",
		TargetChains:  map[string][]types.ChainStep{"gen": {{Target: "pages"}}},
		TargetInputs:  map[string][]types.InputRef{"pages": {{Glob: "src/**/*.md"}}},
		TargetUpdates: map[string][]types.UpdateRef{"pages": {{Project: ".", Glob: "MAGUS.md"}}},
	}
	nodes := DeclaredNodes(p, "gen", nil)
	var pages TargetNode
	for _, n := range nodes {
		if n.Target == "pages" {
			pages = n
		}
	}
	assert.Equal(t, []string{"docs/src/**/*.md", "MAGUS.md"}, pages.Reads,
		"a ref with no project of its own belongs to the project that declared it; an update is read AND written")
	assert.Equal(t, []string{"MAGUS.md"}, pages.Writes)
	assert.True(t, pages.DeclaredWrites, "ctx.modifiesExistingFiles is a declared write")
}

func TestDeclaredNodesSkipsAnUnresolvableCrossProjectStep(t *testing.T) {
	t.Parallel()
	p := &types.Project{
		Path:         ".",
		TargetChains: map[string][]types.ChainStep{"ci": {{Project: "gone", Target: "gen"}}},
	}
	nodes := DeclaredNodes(p, "ci", func(string) *types.Project { return nil })
	require.Len(t, nodes, 1)
	assert.Empty(t, nodes[0].Needs, "a step magus cannot resolve contributes nothing rather than a guess")
}

func TestTargetTailOfANodeKey(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "generate", targetOf(DepKey("docs", "generate")))
	assert.Equal(t, "docs", targetOf("docs"), "a bare project key has no target half")
	assert.False(t, strings.Contains(targetOf(DepKey(".", "ci")), nodeKeySep))
}
