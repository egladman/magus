package buzzspell

import (
	"context"
	"fmt"

	"github.com/egladman/gopherbuzz"
	ispell "github.com/egladman/magus/internal/spell"
)

// Extract executes a Buzz spell module and decodes the mgs_ functions it exports
// into a [ispell.Spec], so a Buzz spell and a Teal spell read identically.
// A spell is a module that exposes its exported mgs_-prefixed
// functions (mirroring Buzz's bz_-prefixed API): a required mgs_getName plus
// optional mgs_listRequiredGlobs/mgs_listProvidedGlobs/mgs_listClaimedGlobs/
// mgs_getVersionCommand/mgs_isForeignProcess/mgs_listTargets.
func Extract(ctx context.Context, src string) (ispell.Spec, error) {
	sess := buzz.NewSession(ctx)
	defer sess.Close()

	// A fork spell may `import "magus/target"` for the Target type in its handler
	// signatures; register the pure-types module so a bare-session extract resolves
	// it (host-module spells go through the binding path, which registers more).
	sess.SetSourceModule(ispell.TargetModulePath, ispell.TargetModuleSource)
	if err := sess.Exec(ctx, src); err != nil {
		return ispell.Spec{}, fmt.Errorf("buzzspell: exec: %w", err)
	}
	return ispell.Resolve(ctx, sess, ispell.ForkOrFunctionOps(src))
}
