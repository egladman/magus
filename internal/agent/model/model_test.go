package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// The shipped rows are the prices read on AsOf. A change to one is a price change, which
// must move AsOf with it.
func TestModelShippedRows(t *testing.T) {
	haiku, ok := Lookup("claude-haiku-5-5")
	require.True(t, ok)
	assert.Equal(t, int64(1_000_000), haiku.Window)
	assert.Equal(t, int64(100_000), haiku.PriceStep)
	assert.InDelta(t, 5.0, haiku.StepFactor, 0)
	assert.InDelta(t, 0.01, haiku.CacheRead, 0)

	for _, id := range []string{"claude-fable-5-1", "claude-opus-5-5", "claude-sonnet-5-5"} {
		f, ok := Lookup(id)
		require.True(t, ok, id)
		assert.Equal(t, int64(1_000_000), f.Window, id)
		assert.Zero(t, f.PriceStep, "%s bills its whole window at one price", id)
	}
	old, ok := Lookup("claude-haiku-4-5")
	require.True(t, ok)
	assert.Equal(t, int64(200_000), old.Window)

	assert.Equal(t, int64(1_000_000), Shipped().LargestWindow())
}

func TestModelLookupMatchesHostSpellings(t *testing.T) {
	for _, id := range []string{
		"claude-opus-5-5",
		"CLAUDE-OPUS-5-5",
		"claude-opus-5-5-20260401",
		"claude-opus-5-5[1m]",
		"us.anthropic.claude-opus-5-5-v1:0",
		"anthropic/claude-opus-5-5",
		"claude-opus-5-5@20260401",
	} {
		f, ok := Lookup(id)
		require.True(t, ok, id)
		assert.Equal(t, "claude-opus-5-5", f.ID, id)
	}
	for _, id := range []string{"", "claude-opus-5", "claude-opus-5-55", "claude-opus-5-5.1", "xclaude-opus-5-5", "gpt-5", "opus"} {
		_, ok := Lookup(id)
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
	merged := Shipped().Merge([]types.ModelFacts{
		{ID: "Claude-Haiku-5-5", Window: 2_000_000, CacheRead: 0.02},
		{ID: "claude-next-1", Window: 3_000_000, PriceStep: 50_000, StepFactor: 2},
	})
	haiku, ok := merged.Lookup("claude-haiku-5-5")
	require.True(t, ok)
	assert.Equal(t, int64(2_000_000), haiku.Window)
	assert.InDelta(t, 0.02, haiku.CacheRead, 0)
	assert.InDelta(t, 0.50, haiku.Output, 0, "a field the override leaves 0 keeps the shipped value")
	assert.Equal(t, int64(100_000), haiku.PriceStep)

	next, ok := merged.Lookup("claude-next-1")
	require.True(t, ok)
	assert.Equal(t, int64(3_000_000), merged.LargestWindow())
	assert.InDelta(t, 2.0, next.Factor(60_000), 0)

	original, _ := Lookup("claude-haiku-5-5")
	assert.Equal(t, int64(1_000_000), original.Window, "Merge leaves the receiver alone")
	assert.Len(t, merged.Rows(), len(shipped)+1)
}

func TestModelTableStale(t *testing.T) {
	assert.False(t, Stale(AsOf.Add(MaxAge)))
	assert.True(t, Stale(AsOf.Add(MaxAge+time.Hour)))
	assert.Zero(t, Table{}.LargestWindow())
}
