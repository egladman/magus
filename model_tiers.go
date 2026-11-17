package magus

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/types"
)

// ModelRow is one model a harness or agents.models lists, as [Magus.Models] reads it.
type ModelRow = model.Row

// CheckGrader judges a job row's check the way the guard will judge its holder running
// it, as [Magus.CheckGrader] returns it.
type CheckGrader = job.CheckGrader

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
func (m *Magus) Models(ctx context.Context) func() []ModelRow {
	return sync.OnceValue(func() []ModelRow {
		wired, err := workspace.WiredHarnesses(m)
		if err != nil {
			return nil
		}
		return agent.HarnessModelRows(types.WithWorkspace(ctx, m), m.Root(), wired)
	})
}

// CheckGrader grades a declared check the way the guard will judge a job's holder running
// it in the checkout at the root it is handed, against that workspace's rules. A door
// opening a job store hands it over as job.Location.Grader. Each root's rules load on the
// first check graded there and are reused until a file they were read from changes.
func (m *Magus) CheckGrader() CheckGrader {
	return guard.NewCheckGrader(func(ctx context.Context, root string) (guard.Dependencies, *guard.WorkspaceRules) {
		return guard.NewDependencies(ctx, guard.Wiring{
			Root:          root,
			Config:        m.cfg,
			BinaryVersion: m.version,
			Inspect: func(ctx context.Context, root string) (types.WorkspaceRepository, error) {
				if m.ownRoot(root) {
					return m, nil
				}
				return Inspect(ctx, root, m.processOptions()...)
			},
			CacheDir: m.guardCacheDir,
		})
	})
}
