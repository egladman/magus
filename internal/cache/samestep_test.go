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
		badge.Needs = []string{DepKey(".", "generate")}
	}
	return []TargetNode{
		{Project: ".", Target: "ci", Steps: []string{step},
			Needs: []string{DepKey(".", "generate"), DepKey(".", "coverage-badge")}},
		{Project: ".", Target: "generate", Steps: []string{step},
			Needs: []string{DepKey(".", "mocks-generate")}},
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
			Needs: []string{DepKey(".", "read")}},
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
	assert.True(t, witness("**/gen/mocks/*.go", "**/*.go"), "a file on disk matches both")
	assert.False(t, witness("cmd/magus/completions/*", "**/MAGUS.md"), "the globs intersect, no path does")
	assert.False(t, witness("**/gen/mocks/*.go", "cmd/magus-termcast/*.go"), "the writer's files sit elsewhere")
}

// With a witness the same fixture is refused only when its overlap is real on disk.
func TestFindSameStepConflictsHonorsTheWitness(t *testing.T) {
	t.Parallel()
	never := OverlapWitness(func(write, read string) bool { return false })
	assert.Empty(t, FindSameStepConflicts(badgeFixture(false), never), "no witness, no refusal")
	always := OverlapWitness(func(write, read string) bool { return true })
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

// TestStageNeedsOrdersLaterCallsAfterEarlierOnes: the docs lint shape, `ctx.needs(format);
// ctx.needs(conventions);`, where format reaches the generators conventions reads. The
// second call is ordered after the first by the body, and that has to reach the
// predicate as edges or every staged composer reads as unordered.
func TestStageNeedsOrdersLaterCallsAfterEarlierOnes(t *testing.T) {
	t.Parallel()
	chain := []types.ChainStep{
		{Target: "format"}, {Project: "gone", Target: "lint"},
		{Target: "conventions", Stage: 1}, {Target: "spelling", Stage: 1},
		{Target: "links", Stage: 2},
	}
	keyOf := func(cs types.ChainStep) (string, bool) {
		if cs.Project == "gone" {
			return "", false
		}
		return DepKey("docs", cs.Target), true
	}
	assert.Equal(t, map[string][]string{
		DepKey("docs", "conventions"): {DepKey("docs", "format")},
		DepKey("docs", "spelling"):    {DepKey("docs", "format")},
		DepKey("docs", "links"):       {DepKey("docs", "format"), DepKey("docs", "conventions"), DepKey("docs", "spelling")},
	}, StageNeeds(chain, keyOf), "a step that resolves to nothing neither orders nor is ordered")
	assert.Empty(t, StageNeeds([]types.ChainStep{{Target: "a"}, {Target: "b"}}, keyOf), "one call, no stages")
}

func TestDeclaredNodesHonorsStages(t *testing.T) {
	t.Parallel()
	p := ciFixtureProject(false)
	// The badge stays unaware of the generator; ci's body runs it in a second call.
	p.TargetChains["ci"] = []types.ChainStep{{Target: "generate"}, {Target: "coverage-badge", Stage: 1}}
	assert.Empty(t, FindSameStepConflicts(DeclaredNodes(p, "ci", nil), nil),
		"a later ctx.needs call is ordered after the earlier one")
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
