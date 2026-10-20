package magus

import (
	"context"

	"github.com/egladman/magus/types"
)

// ModelTiers returns the workspace's model tiers: what the root magusfile declared with
// magus\job.modelTiers, else the shipped declaration, and nil when it turned them off.
func (m *Magus) ModelTiers(ctx context.Context) *types.ModelTierSet {
	if m.wsReg == nil {
		return nil
	}
	return m.wsReg.ModelTiers(ctx)
}

// ModelTierMatrix returns the magus\job.modelTier function the root magusfile registered,
// or nil.
func (m *Magus) ModelTierMatrix() types.ModelTierMatrix {
	if m.wsReg == nil {
		return nil
	}
	return m.wsReg.ModelTierMatrix()
}
