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
	d := DeriveTargetOrder(steps, nodes)
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
	d := DeriveTargetOrder(steps, nodes)
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
	d := DeriveTargetOrder(steps, nodes)
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
		d := DeriveTargetOrder(steps, nodes)
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
	d := DeriveTargetOrder(steps, nodes)
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
	d := DeriveTargetOrder(steps, nodes)
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
	d := DeriveTargetOrder(steps, nodes)
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
	d := DeriveTargetOrder(steps, nodes)
	require.Len(t, d.Edges, 1)
	assert.Equal(t, []int{1, 0}, d.TopoNodes())
}
