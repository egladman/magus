package bindings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/internal/service/identity"
	"github.com/egladman/magus/internal/spellruntime"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// commandOpts carries the per-invocation options a magusfile passes to a command
// spell target (e.g. go.build({...})). cwd is the directory to run in (empty means
// the process cwd, "."). args append as raw tokens after the target's base command -
// so a build the op's bare base can't express (custom -ldflags, -o, a single package)
// is reachable through the spell rather than os.exec. env overlays the subprocess
// environment (KEY=value; later entries win per Go's exec duplicate-key rule). hasArgs
// distinguishes "no args key" (fall back to project.ExtraArgs) from an explicit empty
// list; consulted only on the Buzz path.
type commandOpts struct {
	cwd     string
	args    []string
	env     map[string]string
	stdin   string
	hasArgs bool
	// pmBin is the package-manager bin the op's spell declared via
	// mgs_getPackageManagerBin ("" = no substitution). Carried per invocation
	// because runCommand receives only the op, and the opt-in is a fact about
	// the SPELL, not the op.
	pmBin string
	// installHints maps a bin to the spell's declared install command
	// (mgs_listInstallHints), carried per invocation for the same reason as
	// pmBin. Surfaced when forking the op's bin fails because it is absent.
	installHints map[string]string
}

// runCommand runs tgt.Bin as a subprocess with the base argv as reshaped by the
// active charms (see resolveCharmArgs). Empty Cmd is a no-op. opts.cwd defaults to
// "." (the process cwd). Write-mode rides along as the "rw" charm on ctx, so no
// separate write flag is needed.
func runCommand(ctx context.Context, tgt spells.Op, opts commandOpts) (run.ExecResult, error) {
	if tgt.Bin == "" {
		return run.ExecResult{}, nil
	}
	dir := opts.cwd
	if dir == "" {
		dir = "."
	}
	// Resolve a relative dir against the context working directory (the project dir
	// the magusfile runner set via std.WithCwd), matching os.exec's resolvePath. Without
	// this, a spell op invoked from a subproject target (e.g. go["go-run"] from docs/)
	// would run in the process cwd, not the project dir, so its relative paths would
	// miss. Absolute dirs pass through unchanged (the scip op passes one).
	if !filepath.IsAbs(dir) {
		base, err := std.EffectiveCwd(ctx)
		if err != nil {
			return run.ExecResult{}, err
		}
		dir = filepath.Join(base, dir)
	}
	args, err := resolveCharmArgs(ctx, tgt.Args, tgt.Charms)
	if err != nil {
		return run.ExecResult{}, err
	}
	// Warn on the real execution path only (not the describe/render path, which
	// resolveCharmArgs also serves and which surfaces conflicts in its own output).
	warnCharmConflicts(ctx, tgt.Args, tgt.Charms)
	args = append(args, opts.args...)
	// Package-manager substitution happens HERE, at fork time, because an op's
	// recorded bin is static data (recordOp reduces every handler to {cmd,args})
	// while the manager is a per-project fact. The cache key is unaffected by
	// construction: hashStep never hashes the forked argv (only extra args and
	// the target name), and every substitution determinant - the magusfile's
	// package_manager option, package.json, the lockfiles - is already a hashed
	// source of the project, so changing the manager misses on its own.
	bin := tgt.Bin
	if opts.pmBin != "" && spellruntime.KnownPackageManager(bin) {
		pm := spellruntime.ResolvePackageManager(explicitPackageManager(ctx, dir), dir, bin)
		bin, args = spellruntime.SubstitutePackageManager(bin, args, pm)
	}
	// A service op reached as a dependency is supervised in the background (started,
	// readiness-gated, deduped by fingerprint) instead of forked to completion, which
	// would block the run forever. A directly-run service (no supervisor active) falls
	// through and foregrounds, blocking as intended (Ctrl-C stops it).
	if tgt.IsService() && tgt.Service != nil {
		svc := spells.Service{
			Command:   spells.Command{Bin: bin, Args: args},
			Readiness: tgt.Service.Readiness,
			Stop:      tgt.Service.Stop,
			Idle:      tgt.Service.Idle,
			Distinct:  tgt.Service.Distinct,
		}
		// Scoped to the workspace: the daemon hosts services for every workspace on
		// the machine, so a bare fingerprint would share one instance across them.
		var root string
		if ws := types.WorkspaceFromContext(ctx); ws != nil {
			root = ws.Root()
		}
		if handled, serr := service.TrySupervise(ctx, identity.InstanceKey(root, svc), svc); handled {
			return run.ExecResult{}, serr
		}
	}
	return execCommand(ctx, dir, bin, args, opts.env, opts.stdin, tgt.Capture, opts.installHints[bin])
}

// explicitPackageManager returns the "package_manager" option of the project at
// dir (absolute), or "" when no workspace is on ctx or no project matches.
// Matched by directory rather than project.Where because Where deliberately
// never answers for the root project, and a root-level TS project's explicit
// choice must still win.
func explicitPackageManager(ctx context.Context, dir string) string {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return ""
	}
	for _, p := range ws.All() {
		if p.Dir == dir {
			return p.PackageManager
		}
	}
	return ""
}

// resolveCharmArgs reshapes base by the charms active on ctx. Each active charm
// contributes an RFC 6902 JSON Patch over the argv; the patches are concatenated in
// sorted-name order (so the result is deterministic and immune to CLI activation order
// or duplicate charms) and applied as one sequential patch. Because charms are
// element-level (no root replace), disjoint edits compose freely; overlapping
// positions resolve by name order. The result is always a fresh slice (callers may
// mutate it; base is the shared cached spec).
func resolveCharmArgs(ctx context.Context, base []string, charms map[string]spells.Charm) ([]string, error) {
	var activeNames []string
	for name := range charms {
		if types.HasCharm(ctx, name) {
			activeNames = append(activeNames, name)
		}
	}
	return spellruntime.ApplyCharms(base, charms, activeNames)
}

// charmConflictWarned dedups the run-time conflict warning: the same overridden
// charm on the same command recurs across every project a target fans out to, so one
// warning per unique conflict per process is enough.
var charmConflictWarned sync.Map // signature string -> struct{}

// warnCharmConflicts emits a one-time soft warning when two active charms edit the
// same argv position and one silently overrides the other (the winner decided by
// alphabetical name, so the loser has no effect). It never blocks the run - the
// command still resolves deterministically - but an author almost never means to
// declare a charm whose edit is thrown away, so magus says so.
func warnCharmConflicts(ctx context.Context, base []string, charms map[string]spells.Charm) {
	var activeNames []string
	for name := range charms {
		if types.HasCharm(ctx, name) {
			activeNames = append(activeNames, name)
		}
	}
	conflicts, err := spellruntime.Conflicts(base, charms, activeNames)
	if err != nil {
		return
	}
	for _, c := range conflicts {
		winner := c.OverriddenBy
		if winner == "" {
			winner = "another active charm"
		}
		sig := strings.Join(base, "\x00") + "|" + c.Name + ">" + winner
		if _, seen := charmConflictWarned.LoadOrStore(sig, struct{}{}); seen {
			continue
		}
		slog.WarnContext(ctx, "magus: an active charm is overridden by another and has no effect",
			"charm", c.Name,
			"overridden_by", winner,
			"hint", fmt.Sprintf("charms %q and %q both edit the same argument; %q wins by name order, so %q does nothing here. Drop one, or make their edits disjoint.", c.Name, winner, winner, c.Name))
	}
}

// directMagusBinaryWarnOnce is process-global so the "use magus.cmd/..." hint fires
// at most once per process, not once per command - avoids log spam in a wide run.
var directMagusBinaryWarnOnce sync.Once

// execCommand runs cmd with args in dir, inheriting stdio and sandbox policy. When
// env is non-empty it overlays the base environment (the sandbox baseline when
// present, else the process env); later entries win per Go's exec duplicate-key rule.
// installHint, when non-empty, is the spell's declared install command for cmd,
// appended to the error when the fork fails because cmd is absent from PATH.
func execCommand(ctx context.Context, dir, cmd string, args []string, env map[string]string, stdin string, capture bool, installHint string) (run.ExecResult, error) {
	if filepath.Base(cmd) == "magus" {
		directMagusBinaryWarnOnce.Do(func() {
			slog.WarnContext(ctx, "magus: command spell target called with 'magus' binary",
				"hint", "use magus.cmd(...) or a typed magus.run/describe/insight/doctor(...) instead; contextual cwd, version-pinned, no arg-quoting issues")
		})
	}
	// Sort the per-call env overrides so the subprocess environment is deterministic
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
	res, err := run.Exec(ctx, cmd, args, run.ExecOptions{Dir: dir, Env: overrides, Stdin: stdin, Capture: capture})
	if err != nil && errors.Is(err, types.ExecDenied) {
		return res, err // sandbox exec denial: surface the diagnostic verbatim
	}
	if !res.Started || res.Code != 0 {
		code := res.Code
		if code <= 0 {
			code = 1 // a start failure (binary not found) reports exit 1, as before
		}
		// cmd is the executable that was exec'd (markdownlint, go, tsc), not a spell:
		// a spell is the adapter that chose to run it. Calling it one taught readers
		// a wrong mapping and sent them looking for a spell by the tool's name.
		//
		// A start failure caused by the bin being absent gets the spell's install
		// hint appended, when it declared one: "shellcheck exited 1" tells a reader
		// nothing about the fix, and the spell already knows it.
		if !res.Started && errors.Is(err, exec.ErrNotFound) && installHint != "" {
			return res, fmt.Errorf("%s exited %d: %s not found on PATH; install: %s", cmd, code, cmd, installHint)
		}
		return res, fmt.Errorf("%s exited %d", cmd, code)
	}
	return res, nil
}
