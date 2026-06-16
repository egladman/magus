package interp

import (
	"context"
	"errors"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

func init() {
	project.DefaultSpellRegistry().RegisterSpell(types.NewSpell(
		"magusfile",
		types.WithSources("magusfile.buzz"),
		types.WithInvoker(func(ctx context.Context, req types.InvokeRequest) (any, error) {
			return nil, runTarget(ctx, req.Dir, req.Target)
		}),
		types.WithDeclarationFiles("magusfile.buzz"),
		types.WithDeclarationDirGlobs("magusfiles/*.buzz"),
	))
}

// runTarget dispatches target via RunDir; returns nil on ErrNoMagusfile or ErrUnknownTarget.
func runTarget(ctx context.Context, dir, target string) error {
	err := RunDir(ctx, dir, target, project.ExtraArgs(ctx))
	if errors.Is(err, ErrNoMagusfile) || errors.Is(err, ErrUnknownTarget) {
		return nil // no magusfile, or target absent
	}
	return err
}
