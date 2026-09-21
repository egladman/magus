package interp

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/egladman/magus/types"
)

// levelTrace mirrors config.LevelTrace (slog.LevelDebug-4), duplicated for the reason
// internal/cache duplicates it: config imports this tree, so it cannot be imported back.
const levelTrace slog.Level = slog.LevelDebug - 4

// declaredTimeout reports the timeout a magusfile declared for one target body, and
// zero for a target that declares none.
//
// A lookup, not a constructor and not a context derivation: it reads what the author
// wrote and nothing else. Applying it is the caller's, and so is the dependency-wait
// accumulator every body gets (types.WithDependencyWait), which used to be bundled in
// here and made one function answer two unrelated questions.
//
// That split is the point. runTargetBody applies it per RESUME, so a declared timeout
// bounds the time the body itself executes and not the time its dependencies take. A
// ceiling used to cover the ctx.needs waits inside a body, so a target could exceed one
// having done almost none of its own work; four targets once reported an identical
// 15m52s timeout that one serialization upstream had caused, and each blamed itself.
//
// Only a PARKED body can have this. A blocking ctx.needs sits inside Exec under one
// fixed context whose deadline cannot be changed; a parked one re-enters Exec, and the
// driver hands it a fresh deadline each time.
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
func declaredTimeout(ctx context.Context, dir, target string) time.Duration {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return 0
	}
	p := projectAt(ws, dir)
	if p == nil {
		return 0
	}
	return p.TargetPolicies[target].TimeoutDuration()
}

// projectAt is the project whose magusfile dir IS dir.
//
// Not WorkspaceReader.Where, which answers a different question: it walks up to find
// the project CONTAINING a path and deliberately never returns the root, because a
// file under the root that no nested project claims is not the root's. Here the dir
// is a magusfile's own directory, and the root declares targets like any other
// project; skipping it would leave every root-declared ceiling inert.
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

// logCeiling reports where a ceiling-bearing body's time went, at trace level.
//
// Emitted on every such body, not only the ones that expire. A ceiling that fires already
// reports this split in its error, but the number worth having comes from the runs that
// PASS: whether a declared timeout is measuring the target or measuring the queue is a
// question about the steady state, and by the time one expires the answer arrives too late
// to be a measurement.
func logCeiling(ctx context.Context, target string, ceiling, elapsed time.Duration) {
	if ceiling <= 0 || !slog.Default().Enabled(ctx, levelTrace) {
		return
	}
	waited := types.DependencyWaitFromContext(ctx).Elapsed()
	slog.LogAttrs(ctx, levelTrace, "target.ceiling",
		slog.String("target", target), slog.Duration("ceiling", ceiling),
		slog.Duration("elapsed", elapsed), slog.Duration("composed", waited),
		slog.Duration("own", max(elapsed-waited, 0)))
}
