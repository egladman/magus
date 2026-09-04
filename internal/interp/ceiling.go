package interp

import (
	"context"
	"path/filepath"
	"time"

	"github.com/egladman/magus/types"
)

// withDeclaredCeiling bounds one magusfile target body by the timeout its magusfile
// declared, and is a pass-through for a target that declares none.
//
// This is the seam because it is the only place a magusfile target body runs: the
// scheduled target reaches it through runBuzz, and every ctx.needs-composed one
// reaches the same closure through the Buzz pool. Bounding only the scheduled target
// would leave a declaration on a composed target inert for the command people
// actually type, which is the shape ChainMemoryMB exists to avoid on the memory side.
//
// Nesting is deliberate and needs no coordination: a composed target's ceiling is a
// second, tighter deadline inside its parent's, and whichever expires first cancels.
//
// No workspace in ctx is the bare `magus buzz` / REPL case. It reads as undeclared
// rather than an error: there is no magusfile policy to consult, and refusing to run
// a script because nobody declared a ceiling would be a strange thing to do.
func withDeclaredCeiling(ctx context.Context, dir, target string) (context.Context, context.CancelFunc, time.Duration) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return ctx, func() {}, 0
	}
	p := projectAt(ws, dir)
	if p == nil {
		return ctx, func() {}, 0
	}
	d := p.TargetPolicies[target].TimeoutDuration()
	if d <= 0 {
		return ctx, func() {}, 0
	}
	c, cancel := context.WithTimeout(ctx, d)
	return c, cancel, d
}

// projectAt is the project whose magusfile dir IS dir.
//
// Not WorkspaceReader.Where, which answers a different question: it walks up to find
// the project CONTAINING a path and deliberately never returns the root, because a
// file under the root that no nested project claims is not the root's. Here the dir
// is a magusfile's own directory, and the root declares targets like any other
// project - skipping it would leave every root-declared ceiling inert.
//
// The symlink pass is the fallback rather than the rule so the common case costs no
// syscalls; it exists because a temp dir reaches this resolved on some hosts and
// unresolved on others (/var vs /private/var on darwin), and a ceiling that works
// only on the developer's machine is not a guard.
func projectAt(ws types.WorkspaceReader, dir string) *types.Project {
	all := ws.All()
	for _, p := range all {
		if p.Dir == dir {
			return p
		}
	}
	abs, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil
	}
	for _, p := range all {
		if pd, err := filepath.EvalSymlinks(p.Dir); err == nil && pd == abs {
			return p
		}
	}
	return nil
}
