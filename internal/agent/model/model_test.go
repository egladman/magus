package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

var fixtureRows = []types.ModelFacts{
	{ID: "claude-opus-5-5", Window: 1_000_000, Input: 4, Output: 20},
	{ID: "claude-haiku-5-5", Window: 1_000_000, Input: 0.1, CacheRead: 0.01, Output: 0.5, PriceStep: 100_000, StepFactor: 5},
	{ID: "claude-haiku-4-5", Window: 200_000, Input: 1, Output: 5},
}

func fixtureTable() Table { return Table{}.Merge(fixtureRows) }

func TestZeroTableHasNoFacts(t *testing.T) {
	_, ok := Table{}.Lookup("claude-opus-5-5")

	assert.False(t, ok, "no rows is no facts, never a free or zero-sized model")
	assert.Empty(t, Table{}.Rows())
}

func TestModelLookupMatchesHostSpellings(t *testing.T) {
	table := fixtureTable()
	for _, id := range []string{
		"claude-opus-5-5",
		"CLAUDE-OPUS-5-5",
		"claude-opus-5-5-20260401",
		"claude-opus-5-5[1m]",
		"us.anthropic.claude-opus-5-5-v1:0",
		"anthropic/claude-opus-5-5",
		"claude-opus-5-5@20260401",
	} {
		f, ok := table.Lookup(id)
		require.True(t, ok, id)
		assert.Equal(t, "claude-opus-5-5", f.ID, id)
	}
	for _, id := range []string{"", "claude-opus-5", "claude-opus-5-55", "claude-opus-5-5.1", "xclaude-opus-5-5", "gpt-5", "opus"} {
		_, ok := table.Lookup(id)
		assert.False(t, ok, id)
	}
}

// The longest matching row wins, so a row for a model family never shadows one for a
// release inside it.
func TestModelLookupPrefersLongestRow(t *testing.T) {
	table := Table{}.Merge([]types.ModelFacts{
		{ID: "claude-opus-5", Window: 500_000},
		{ID: "claude-opus-5-5", Window: 1_000_000},
	})
	f, ok := table.Lookup("claude-opus-5-5-20260401")
	require.True(t, ok)
	assert.Equal(t, "claude-opus-5-5", f.ID)
	f, ok = table.Lookup("claude-opus-5-20250101")
	require.True(t, ok)
	assert.Equal(t, "claude-opus-5", f.ID)
}

func TestModelMergeOverlaysAndAdds(t *testing.T) {
	base := fixtureTable()
	merged := base.Merge([]types.ModelFacts{
		{ID: "Claude-Haiku-5-5", Window: 2_000_000, CacheRead: 0.02},
		{ID: "claude-next-1", Window: 3_000_000, PriceStep: 50_000, StepFactor: 2},
	})
	haiku, ok := merged.Lookup("claude-haiku-5-5")
	require.True(t, ok)
	assert.Equal(t, int64(2_000_000), haiku.Window)
	assert.InDelta(t, 0.02, haiku.CacheRead, 0)
	assert.InDelta(t, 0.50, haiku.Output, 0, "a field the override leaves 0 keeps the earlier value")
	assert.Equal(t, int64(100_000), haiku.PriceStep)

	next, ok := merged.Lookup("claude-next-1")
	require.True(t, ok)
	assert.InDelta(t, 2.0, next.Factor(60_000), 0)

	original, _ := base.Lookup("claude-haiku-5-5")
	assert.Equal(t, int64(1_000_000), original.Window, "Merge leaves the receiver alone")
	assert.Len(t, merged.Rows(), len(fixtureRows)+1)
}

// A harness row is merged before agents.models, so a person's row wins field by field.
func TestModelMergeLayersHarnessRowsUnderAgentsModels(t *testing.T) {
	table := Table{}.
		Merge([]types.ModelFacts{{ID: "claude-opus-5-5", Window: 1_000_000, Input: 4, Output: 20}}).
		Merge([]types.ModelFacts{{ID: "claude-opus-5-5", Input: 3}})

	f, ok := table.Lookup("claude-opus-5-5")
	require.True(t, ok)
	assert.Equal(t, types.ModelFacts{ID: "claude-opus-5-5", Window: 1_000_000, Input: 3, Output: 20}, f)
}
