package guard

import (
	"context"
	"sync"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/types"
)

// onceInspected is the lazy reader of what read takes from the workspace inspect opens. The
// first call opens it and the answer is kept for the rest, so a judgment that never asks
// pays for nothing. A workspace that does not open yields T's zero value.
func onceInspected[T any](ctx context.Context, inspect func(context.Context) (types.WorkspaceRepository, error), read func(context.Context, types.WorkspaceRepository) T) func() T {
	return sync.OnceValue(func() T {
		ws, err := inspect(ctx)
		if err != nil {
			var zero T
			return zero
		}
		return read(ctx, ws)
	})
}

// HarnessModelRows is the lazy reader of the model rows the harnesses wired in the
// workspace at root publish through mgs_listModels; the first call loads each harness
// spell. A workspace that does not load or carries no wiring, wires no harness or wires none
// that publishes rows yields no rows, which the guard reads as no facts about a model
// rather than an error.
func HarnessModelRows(ctx context.Context, root string, inspect func(context.Context) (types.WorkspaceRepository, error)) func() []model.Row {
	return onceInspected(ctx, inspect, func(ctx context.Context, ws types.WorkspaceRepository) []model.Row {
		wired, err := workspace.WiredHarnesses(ws)
		if err != nil {
			return nil
		}
		return agent.HarnessModelRows(types.WithWorkspace(ctx, ws), root, wired)
	})
}

// modelRows is HarnessModelRows for the workspace found from rootOverride, opened as
// the guard's other rules open it, so one load serves them all. nil outside a workspace.
func (e HookEnv) modelRows(ctx context.Context, rootOverride string) func() []model.Row {
	root, err := workspace.FindRoot(rootOverride)
	if err != nil {
		return nil
	}
	return HarnessModelRows(ctx, root, func(ctx context.Context) (types.WorkspaceRepository, error) {
		return e.Inspect(ctx, rootOverride, "")
	})
}

// modelTiers lazily reads the model tiers of rootOverride's workspace.
// It opens the workspace whole, as modelRows does.
// The guard's rule load keeps only magus\guard registrations.
// So it holds no magus\modelTiers call and no harness aliases.
func (e HookEnv) modelTiers(ctx context.Context, rootOverride string) func() *tier.Set {
	if _, err := workspace.FindRoot(rootOverride); err != nil {
		return nil
	}
	return workspaceModelTiers(ctx, func(ctx context.Context) (types.WorkspaceRepository, error) {
		return e.Inspect(ctx, rootOverride, "")
	})
}

// workspaceModelTiers is the lazy reader of the model tiers the workspace inspect opens
// declares. A workspace that does not load or declares no tiers yields nil.
func workspaceModelTiers(ctx context.Context, inspect func(context.Context) (types.WorkspaceRepository, error)) func() *tier.Set {
	return onceInspected(ctx, inspect, func(ctx context.Context, ws types.WorkspaceRepository) *tier.Set {
		tiered, ok := ws.(interface {
			ModelTiers(ctx context.Context) *tier.Set
		})
		if !ok {
			return nil
		}
		return tiered.ModelTiers(ctx)
	})
}
