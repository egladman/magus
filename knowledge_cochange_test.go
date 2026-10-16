package magus

import (
	"fmt"
	"slices"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func commitOf(id string, paths ...string) types.CommitChange {
	c := types.CommitChange{ID: id}
	for _, p := range paths {
		c.Files = append(c.Files, types.FileChange{Path: p})
	}
	return c
}

// commitsOf repeats the same file set as n commits with distinct ids.
func commitsOf(tag string, n int, paths ...string) []types.CommitChange {
	out := make([]types.CommitChange, n)
	for i := range out {
		out[i] = commitOf(fmt.Sprintf("%s%d", tag, i), paths...)
	}
	return out
}

func TestAggregateFileCouplingThresholds(t *testing.T) {
	var changes []types.CommitChange
	// a and b share two commits: the minimum support, and full confidence.
	changes = append(changes, commitsOf("ab", 2, "a.go", "b.go")...)
	// c and d share one commit: coincidence.
	changes = append(changes, commitOf("cd", "c.go", "d.go"))
	// e and f each change 10 times and share 2 of them: confidence 0.2, below the floor.
	changes = append(changes, commitsOf("ef", 2, "e.go", "f.go")...)
	changes = append(changes, commitsOf("e", 8, "e.go")...)
	changes = append(changes, commitsOf("f", 8, "f.go")...)
	// g and h each change 10 times and share 3 of them: confidence 0.3, exactly the floor.
	changes = append(changes, commitsOf("gh", 3, "g.go", "h.go")...)
	changes = append(changes, commitsOf("g", 7, "g.go")...)
	changes = append(changes, commitsOf("h", 7, "h.go")...)

	assert.Equal(t, []types.KnowledgeCoChange{
		{A: "a.go", B: "b.go", Support: 2, Confidence: 1},
		{A: "g.go", B: "h.go", Support: 3, Confidence: 0.3},
	}, aggregateFileCoupling(changes, ""))
}

func TestAggregateFileCouplingConfidenceUsesTheRarerFile(t *testing.T) {
	var changes []types.CommitChange
	changes = append(changes, commitsOf("ab", 2, "hot.go", "rare.go")...)
	changes = append(changes, commitsOf("hot", 18, "hot.go")...)

	assert.Equal(t, []types.KnowledgeCoChange{
		{A: "hot.go", B: "rare.go", Support: 2, Confidence: 1},
	}, aggregateFileCoupling(changes, ""), "2 of rare.go's 2 commits, though only 2 of hot.go's 20")
}

func TestAggregateFileCouplingSkipsBulkCommits(t *testing.T) {
	var bulk []string
	for i := range coChangeMaxFiles + 1 {
		bulk = append(bulk, fmt.Sprintf("gen/f%02d.go", i))
	}
	atLimit := bulk[:coChangeMaxFiles]

	// Two bulk commits would give every pair in them support 2.
	changes := []types.CommitChange{commitOf("b1", bulk...), commitOf("b2", bulk...)}
	assert.Empty(t, aggregateFileCoupling(changes, ""), "a commit over the limit is mechanical")

	changes = []types.CommitChange{commitOf("l1", atLimit...), commitOf("l2", atLimit...)}
	assert.NotEmpty(t, aggregateFileCoupling(changes, ""), "a commit at the limit still counts")
}

func TestAggregateFileCouplingBulkCommitsDoNotDiluteConfidence(t *testing.T) {
	var bulk []string
	for i := range coChangeMaxFiles + 1 {
		bulk = append(bulk, fmt.Sprintf("gen/f%02d.go", i))
	}
	bulk = append(bulk, "a.go")
	changes := commitsOf("ab", 2, "a.go", "b.go")
	changes = append(changes, commitsOf("bulk", 20, bulk...)...)

	assert.Equal(t, []types.KnowledgeCoChange{
		{A: "a.go", B: "b.go", Support: 2, Confidence: 1},
	}, aggregateFileCoupling(changes, ""))
}

func TestAggregateFileCouplingStripsPrefix(t *testing.T) {
	changes := commitsOf("c", 2, "sub/proj/a.go", "sub/proj/b.go", "other/c.go")

	assert.Equal(t, []types.KnowledgeCoChange{
		{A: "a.go", B: "b.go", Support: 2, Confidence: 1},
	}, aggregateFileCoupling(changes, "sub/proj/"), "a path outside the workspace subtree is dropped")
}

func TestAggregateFileCouplingIgnoresOrderAndDuplicates(t *testing.T) {
	changes := []types.CommitChange{
		commitOf("1", "b.go", "a.go", "b.go"),
		commitOf("2", "c.go", "a.go"),
		commitOf("3", "a.go", "c.go", "b.go"),
		commitOf("4", "b.go", "c.go"),
	}
	want := aggregateFileCoupling(changes, "")
	require.Equal(t, []types.KnowledgeCoChange{
		{A: "a.go", B: "b.go", Support: 2, Confidence: 2.0 / 3},
		{A: "a.go", B: "c.go", Support: 2, Confidence: 2.0 / 3},
		{A: "b.go", B: "c.go", Support: 2, Confidence: 2.0 / 3},
	}, want, "a path listed twice in one commit counts once; pairs have A < B and are sorted")

	slices.Reverse(changes)
	assert.Equal(t, want, aggregateFileCoupling(changes, ""))
}

func TestAggregateFileCouplingEmpty(t *testing.T) {
	assert.Empty(t, aggregateFileCoupling(nil, ""))
}
