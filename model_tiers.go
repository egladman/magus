package magus

import (
	"context"
	"sync"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/workspace"
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

// ModelRows is the lazy reader of the model rows this workspace's wired harnesses
// publish. The first call loads each harness spell.
func (m *Magus) ModelRows(ctx context.Context) func() []types.ModelPricing {
	return sync.OnceValue(func() []types.ModelPricing {
		wired, err := workspace.WiredHarnesses(m)
		if err != nil {
			return nil
		}
		return agent.HarnessModelRows(types.WithWorkspace(ctx, m), m.Root(), wired)
	})
}
