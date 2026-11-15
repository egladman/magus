package evidence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// Explain resolves a bare name fuzzily, which is right for a person typing
// `magus explain build` and wrong for evidence: asked for "cmd/magus" it once answered
// target:.:release-sign, and a blast radius from an unrelated node is worse than silence.
func TestLeaseGraphEvidenceTakesOnlyAnExactNode(t *testing.T) {
	t.Parallel()

	g := knowledge.NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "dir:cmd/magus", Kind: types.KindDir, Label: "cmd/magus"})
	g.AddNode(types.KnowledgeNode{ID: "file:internal/ledger/brief.go", Kind: types.KindFile, Label: "brief.go"})

	got, ok := pathEvidence(g, "cmd/magus")
	require.True(t, ok)
	assert.Equal(t, job.TermsEvidence{Path: "cmd/magus", Node: "dir:cmd/magus"}, got)
	// "brief.go" resolves to file:internal/ledger/brief.go by name. It is a match and not
	// evidence: the lease declared a path at the workspace root, and this node is not it.
	_, ok = pathEvidence(g, "brief.go")
	assert.False(t, ok, "a fuzzy match is not evidence")
}

// describe reads the graph evidence as it reads the symbol goals: from the graph as built,
// and from none while a symbol index is stale, which it reports cold.
func TestLeaseGraphEvidenceBuildsNothingOverAStaleIndex(t *testing.T) {
	t.Parallel()

	paths := []string{"code.go"}
	evidence := func(rebuild bool, stale []types.KnowledgeStaleIndex) (cold bool, loads int) {
		graph := &Graph{
			rebuild: rebuild,
			stale:   func(context.Context) []types.KnowledgeStaleIndex { return stale },
			domain: func(context.Context) (*knowledge.Graph, error) {
				loads++
				if !rebuild && len(stale) > 0 {
					t.Error("describe built the graph over a stale index")
				}
				return deltaGraph(nil), nil
			},
		}
		_, cold = LeaseGraphEvidence(t.Context(), graph, paths)
		_, again := LeaseGraphEvidence(t.Context(), graph, paths)
		assert.Equal(t, cold, again, "a second ask answers alike")
		return cold, loads
	}

	cold, loads := evidence(false, nil)
	assert.False(t, cold)
	assert.Equal(t, 1, loads, "one load serves every ask")

	stale := []types.KnowledgeStaleIndex{{Project: "pkg", Language: "go"}}
	cold, loads = evidence(false, stale)
	assert.True(t, cold, "a stale index is reported, not read as nothing depending on the paths")
	assert.Zero(t, loads)

	cold, loads = evidence(true, stale)
	assert.False(t, cold)
	assert.Equal(t, 1, loads)
}
