package bindings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"sync"

	"github.com/egladman/magus/internal/run"
	ispell "github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/types"
)

// forkOpts carries the per-invocation options a magusfile passes to a fork
// spell target (e.g. go.build({...})). cwd is the directory to fork in (empty
// means the process cwd, "."). args append as raw tokens after the target's base
// command — so a build the op's bare base can't express (custom -ldflags, -o, a
// single package) is reachable through the spell rather than os.exec, with the
// linker flags passed as ordinary args. env overlays the forked subprocess
// environment (KEY=value; later entries win per Go's exec duplicate-key rule).
// hasArgs distinguishes "no args key" (fall back to project.ExtraArgs) from an
// explicit empty list; consulted only on the Buzz path.
type forkOpts struct {
	cwd     string
	args    []string
	env     map[string]string
	stdin   string
	hasArgs bool
}

// runForkTarget forks tgt.Cmd with the base argv as reshaped by the active
// charms (see resolveCharmArgs). Empty Cmd is a no-op. opts.cwd defaults to "."
// (the process cwd). Write-mode rides along as the "rw" charm on ctx, so no
// separate write flag is needed.
func runForkTarget(ctx context.Context, tgt ispell.Op, opts forkOpts) (run.ExecResult, error) {
	if tgt.Cmd == "" {
		return run.ExecResult{}, nil
	}
	dir := opts.cwd
	if dir == "" {
		dir = "."
	}
	args, err := resolveCharmArgs(ctx, tgt.Args, tgt.Charms)
	if err != nil {
		return run.ExecResult{}, err
	}
	args = append(args, opts.args...)
	return execFork(ctx, dir, tgt.Cmd, args, opts.env, opts.stdin, tgt.Capture)
}

// resolveCharmArgs reshapes base by the charms active on ctx. Each active charm
// contributes an RFC 6902 JSON Patch over the argv; the patches are concatenated
// in sorted-name order — so the result is deterministic and immune to CLI
// activation order or duplicate charms — and applied as one sequential patch.
// Because charms are element-level (no root replace), disjoint edits compose
// freely; overlapping positions resolve by name order. The result is always a
// fresh slice (callers may mutate it; base is the shared cached spec).
func resolveCharmArgs(ctx context.Context, base []string, charms map[string]ispell.Charm) ([]string, error) {
	if len(charms) == 0 {
		return slices.Clone(base), nil
	}
	names := make([]string, 0, len(charms))
	for name := range charms {
		names = append(names, name)
	}
	slices.Sort(names)

	var ops []ispell.PatchOp
	for _, name := range names {
		if types.HasCharm(ctx, name) {
			ops = append(ops, charms[name].Ops...)
		}
	}
	if len(ops) == 0 {
		return slices.Clone(base), nil
	}
	return ispell.ApplyPatch(base, ops)
}

// directMagusBinaryWarnOnce is process-global so the "use magus.cmd" hint fires
// at most once per process, not once per fork — avoids log spam in a wide run.
var directMagusBinaryWarnOnce sync.Once

// execFork runs cmd with args in dir, inheriting stdio and sandbox policy. When
// env is non-empty it overlays the base environment (the sandbox baseline when
// present, else the process env); later entries win per Go's exec duplicate-key rule.
func execFork(ctx context.Context, dir, cmd string, args []string, env map[string]string, stdin string, capture bool) (run.ExecResult, error) {
	if filepath.Base(cmd) == "magus" {
		directMagusBinaryWarnOnce.Do(func() {
			slog.Warn("magus: fork spell target called with 'magus' binary",
				"hint", "use magus.cmd({...}) instead — in-process, version-pinned, no arg-quoting issues")
		})
	}
	// Sort the per-call env overrides so the forked environment is deterministic
	// regardless of map iteration order; run.Exec applies them over the sandbox
	// BaseEnv (later entries win).
	var overrides []string
	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		overrides = make([]string, 0, len(env))
		for _, k := range keys {
			overrides = append(overrides, k+"="+env[k])
		}
	}
	res, err := run.Exec(ctx, cmd, args, run.ExecSpec{Dir: dir, Env: overrides, Stdin: stdin, Capture: capture})
	if err != nil && errors.Is(err, &types.DiagnosticError{Code: types.ExecDenied}) {
		return res, err // sandbox exec denial: surface the diagnostic verbatim
	}
	if !res.Started || res.Code != 0 {
		code := res.Code
		if code <= 0 {
			code = 1 // a start failure (binary not found) reports exit 1, as before
		}
		return res, fmt.Errorf("spell %s: exit %d", cmd, code)
	}
	return res, nil
}
