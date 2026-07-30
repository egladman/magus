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
		types.WithInvoker(runTarget),
		types.WithDeclarationFiles("magusfile.buzz"),
		types.WithDeclarationDirGlobs("magusfiles/*.buzz"),
	))
}

// runTarget dispatches target via RunDir, returning the target's return value so
// it reaches InvokeResponse.Data. This invoker hardcoded nil, which meant the
// value was discarded a second time even once the interpreter kept it.
// Returns nil on ErrNoMagusfile or ErrUnknownTarget.
func runTarget(ctx context.Context, req types.InvokeRequest) (any, error) {
	val, err := RunDir(ctx, req.Dir, req.Target, project.ExtraArgs(ctx))
	if errors.Is(err, ErrNoMagusfile) || errors.Is(err, ErrUnknownTarget) {
		// nil value + nil error is precisely right here and is not a sentinel case:
		// a project without this target is SKIPPED, not failed (that is what makes a
		// fan-out tolerant), and it produced no value to report. The (any, error)
		// shape is fixed by types.WithInvoker, so there is no third slot to say it in.
		//nolint:nilnil // absent target is a skip, and a skip has no value to return
		return nil, nil
	}
	return val, err
}
