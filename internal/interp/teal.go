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
		types.WithSources("magusfile.tl", "magusfile.bzz"),
		types.WithInvoker(func(ctx context.Context, req types.InvokeRequest) (any, error) {
			return nil, runTarget(ctx, req.Dir, req.Target)
		}),
		types.WithDeclarationFiles("magusfile.tl", "magusfile.bzz"),
		types.WithDeclarationDirGlobs("magusfiles/*.tl", "magusfiles/*.bzz"),
	))
}

// runTarget dispatches target via RunDir; returns nil on ErrNoMagusfile or ErrUnknownTarget.
func runTarget(ctx context.Context, dir, target string) error {
	err := RunDir(ctx, dir, target, project.ExtraArgs(ctx))
	if errors.Is(err, ErrNoMagusfile) || errors.Is(err, ErrUnknownTarget) {
		return nil // no magusfile, or target absent in every engine; skip
	}
	return err
}
