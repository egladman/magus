package cache

import (
	"testing"

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
	d, err := DeriveTargetOrder(steps, nodes)
	require.NoError(t, err)
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
	d, err := DeriveTargetOrder(steps, nodes)
	require.NoError(t, err)
	require.Equal(t, []DerivedEdge{{Writer: 0, Reader: 2, Ordered: true}}, d.Edges)
}

func TestDeriveTargetOrderStrongCycleNamed(t *testing.T) {
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
	_, err := DeriveTargetOrder(steps, nodes)
	require.Error(t, err)
	// The error must name both targets: it is the author's declarations that
	// conflict, and the fix is in one of those two bodies.
	assert.Contains(t, err.Error(), ". changelog")
	assert.Contains(t, err.Error(), "docs content")
}

func TestDeriveTargetOrderWeakCycleDropped(t *testing.T) {
	steps := orderSteps()
	root, docs := stepKeys(steps)
	nodes := []TargetNode{
		// Declares no reads, so its whole-tree fallback overlaps everything: the
		// resulting cycle is an artifact of the over-approximation and must drop the
		// weak edge rather than fail the load.
		{Project: ".", Target: "index", Steps: []string{root},
			Reads:  []string{"**/*.md"},
			Writes: []string{"MAGUS.md"}, DeclaredWrites: true},
		{Project: "docs", Target: "index", Steps: []string{docs},
			Reads: []string{"**/MAGUS.md"}, DeclaredReads: true,
			Writes: []string{"docs/MAGUS.md"}, DeclaredWrites: true},
	}
	d, err := DeriveTargetOrder(steps, nodes)
	require.NoError(t, err)
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
	d, err := DeriveTargetOrder(steps, nodes)
	require.NoError(t, err)
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
	d, err := DeriveTargetOrder(steps, nodes)
	require.NoError(t, err)
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
	d, err := DeriveTargetOrder(steps, nodes)
	require.NoError(t, err)
	require.Len(t, d.Edges, 1)
	assert.Equal(t, []int{1, 0}, d.TopoNodes())
}
