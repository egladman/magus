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
		return HarnessModelRows(ctx, m.Root(), m)
	})
}

// HarnessModelRows is the model rows the harnesses wired in ws publish through
// mgs_listModels. A workspace with no wiring, or none that publishes rows, yields no rows,
// which the guard reads as no facts about a model rather than an error.
func HarnessModelRows(ctx context.Context, root string, ws types.WorkspaceRepository) []types.ModelPricing {
	wired, err := workspace.WiredHarnesses(ws)
	if err != nil {
		return nil
	}
	ctx = agent.ContextWithWiredHarnesses(types.WithWorkspace(ctx, ws), wired)
	var rows []types.ModelPricing
	for _, id := range wired {
		if d, _, err := agent.LoadHarness(ctx, root, id); err == nil {
			rows = append(rows, d.Models...)
		}
	}
	return rows
}
