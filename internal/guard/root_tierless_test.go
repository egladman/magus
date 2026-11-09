package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// tierlessFableRows lists claude-fable-5-1 with no tier.
func tierlessFableRows() []types.ModelPricing {
	return []types.ModelPricing{{ID: "claude-fable-5-1", Alias: "fable"}}
}

func TestARowNamingFableSpawnsOnFable(t *testing.T) {
	set := testModelTiers(t, "claude-code")
	economy, ok := set.At(0)
	require.True(t, ok)
	f := forkedTier{job: "j", record: types.JobModelTier{Name: "economy", Model: "claude-fable-5-1"}, tier: economy, set: set, rows: tierlessFableRows()}

	assert.Empty(t, f.refusal("claude-code", "fable"), "the row names Fable by id, the spawn by alias")
}

func TestATieredRowStillRefusesFable(t *testing.T) {
	set := testModelTiers(t, "claude-code")
	economy, ok := set.At(0)
	require.True(t, ok)
	f := forkedTier{job: "j", record: types.JobModelTier{Name: "economy"}, tier: economy, set: set, rows: tierlessFableRows()}

	assert.Contains(t, f.refusal("claude-code", "claude-fable-5-1"), "maps to no model tier")
}
