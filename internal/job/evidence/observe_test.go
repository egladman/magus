package evidence

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// describe job reads the graph as built and job wait may build it. On a current index the
// two grade a row alike; on a stale one describe builds nothing and leaves the symbol goal
// ungraded, naming the rebuild.
func TestDescribeAndWaitGradeSymbolsAlike(t *testing.T) {
	t.Parallel()

	g := deltaGraph(map[string]string{"Present": "code.go:3"})
	row := jobtypes.Job{ID: "wave/sym", Paths: jobtypes.Paths{Write: []string{"code.go"}}, Goals: []jobtypes.Goal{
		{ID: "here", Kind: jobtypes.GoalKindSymbol, Expect: jobtypes.ExpectPresent, Symbols: []string{"Present"}},
		{ID: "gone", Kind: jobtypes.GoalKindSymbol, Expect: jobtypes.ExpectAbsent, Symbols: []string{"Removed"}},
	}}
	read := func(rebuild bool, stale []types.KnowledgeStaleIndex) *Graph {
		return &Graph{
			rebuild: rebuild,
			stale:   func(context.Context) []types.KnowledgeStaleIndex { return stale },
			build: func(context.Context) (*knowledge.Graph, error) {
				if !rebuild && len(stale) > 0 {
					t.Error("describe built the graph over a stale index")
				}
				return g, nil
			},
		}
	}
	grade := func(graph *Graph) jobtypes.Status {
		seen, err := Observer("", func(string) *Graph { return graph }, Env{})(t.Context(), row)
		require.NoError(t, err)
		return job.VerifyGates(row, jobtypes.Result{Job: row.ID}, jobtypes.CapturedRun{}, nil, []jobtypes.Job{row}, seen)
	}

	described, waited := grade(read(false, nil)), grade(read(true, nil))
	assert.Equal(t, waited, described, "on a current index describe and wait agree")
	require.Len(t, described.Gates, 2)
	for _, gate := range described.Gates {
		assert.True(t, gate.Verified, "%+v", gate)
	}

	stale := []types.KnowledgeStaleIndex{{Project: "pkg", Language: "go"}}
	described = grade(read(false, stale))
	require.Len(t, described.Gates, 2)
	for _, gate := range described.Gates {
		assert.False(t, gate.Verified, gate.ID)
		assert.Contains(t, strings.Join(gate.Violations, " "), hint.GraphBuild.String(), gate.ID)
	}
	assert.Equal(t, waited, grade(read(true, stale)), "wait builds whatever the index says")
}

func TestJobObserverGradesSymbolsInTheJobsOwnTree(t *testing.T) {
	t.Parallel()

	root, checkout := t.TempDir(), t.TempDir()
	row := jobtypes.Job{ID: "wave/sym", CheckoutRoot: checkout, Goals: []jobtypes.Goal{
		{ID: "here", Kind: jobtypes.GoalKindSymbol, Expect: jobtypes.ExpectPresent, Symbols: []string{"Present"}},
	}}
	var trees []string
	graphIn := func(tree string) *Graph {
		trees = append(trees, tree)
		return &Graph{
			rebuild: true,
			stale:   func(context.Context) []types.KnowledgeStaleIndex { return nil },
			build: func(context.Context) (*knowledge.Graph, error) {
				return deltaGraph(map[string]string{"Present": "code.go:3"}), nil
			},
		}
	}

	seen, err := Observer(root, graphIn, Env{})(t.Context(), row)

	require.NoError(t, err)
	assert.Equal(t, []string{checkout}, trees, "the worker's checkout holds the symbols its branch defines, not the waiter's")
	assert.True(t, seen.SymbolsKnown)
}
