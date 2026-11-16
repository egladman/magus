package magus

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/types"
)

// ModelTiers returns what the root magusfile declared with magus\modelTiers.
// Else it returns the shipped declaration, and nil when it turned them off.
// It is nil too when the shipped one does not build, logged as an error.
func (m *Magus) ModelTiers(ctx context.Context) *tier.Set {
	set, err := m.modelTiers.Tiers()
	var off *model.TiersOffError
	switch {
	case errors.As(err, &off):
		return nil
	case err != nil:
		slog.With(attr.Component("magus")).ErrorContext(ctx, "the default model tier declaration does not load", attr.Error(err))
		return nil
	}
	return set
}

// ModelTierMatrix returns the magus\modelTier function the root magusfile
// registered, or nil.
func (m *Magus) ModelTierMatrix() types.ModelTierMatrix {
	if m.wsReg == nil {
		return nil
	}
	return m.wsReg.ModelTierMatrix()
}

// Models is the lazy reader of the model rows this workspace's wired harnesses
// publish. The first call loads each harness spell.
func (m *Magus) Models(ctx context.Context) func() []model.Row {
	return sync.OnceValue(func() []model.Row {
		wired, err := workspace.WiredHarnesses(m)
		if err != nil {
			return nil
		}
		return agent.HarnessModelRows(types.WithWorkspace(ctx, m), m.Root(), wired)
	})
}
