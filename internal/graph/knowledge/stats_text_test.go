package knowledge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestStatsTextNamesTheShapeAndTheOrphanTotal(t *testing.T) {
	out := types.KnowledgeStats{
		Definition: "the shape", NodeCount: 5, EdgeCount: 3,
		ComponentCount: 2, LargestComponentSize: 4, IsolatedCount: 9,
		Gods:     []types.KnowledgeGodNode{{ID: "a", Label: "a", Kind: "file", Degree: 3, In: 1, Out: 2}},
		Orphans:  []types.KnowledgeOrphan{{Kind: "doc", Label: "lonely", Reason: "no links"}},
		Coverage: []types.KnowledgeDocCoverage{{Kind: "target", Documented: 1, Total: 2, Percent: 50, Undocumented: []string{"x"}}},
	}

	var buf strings.Builder
	require.NoError(t, StatsText(&buf, out))

	got := buf.String()
	assert.Contains(t, got, "definition: the shape")
	assert.Contains(t, got, "graph: 5 nodes, 3 edges")
	assert.Contains(t, got, "connectivity: 2 component(s), largest holds 4, 9 isolated node(s)")
	assert.Contains(t, got, "orphans (showing 1 of 9 isolated):")
	assert.Contains(t, got, "1/2 (50%)  missing: x")
}
