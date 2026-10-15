package magus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

func TestModelTiersWithoutARegistry(t *testing.T) {
	m := &Magus{}
	assert.Nil(t, m.ModelTiers(t.Context()))
	assert.Nil(t, m.ModelTierMatrix())
}

func TestModelTiersReadTheRegistry(t *testing.T) {
	set, err := types.NewModelTierSet(types.ModelTierDeclaration{
		Enum:   "Tier",
		Site:   "magusfile.buzz:1",
		Cases:  []string{"low", "high"},
		Bounds: make([]types.ModelTierBounds, 2),
		Skills: []types.SkillVariant{types.SkillVariantFull, types.SkillVariantShort},
	})
	require.NoError(t, err)

	reg := workspace.NewWorkspaceRegistry()
	reg.SetModelTiers(set)
	reg.SetModelTierMatrix(func(context.Context, types.ModelTierRequest) (*types.JobModelTier, error) {
		return &types.JobModelTier{Name: "high", Rank: 1}, nil
	})
	m := &Magus{wsReg: reg}

	assert.Same(t, set, m.ModelTiers(t.Context()))
	got, err := m.ModelTierMatrix()(context.Background(), types.ModelTierRequest{})
	require.NoError(t, err)
	assert.Equal(t, &types.JobModelTier{Name: "high", Rank: 1}, got)
}
