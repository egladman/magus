package magus

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// MaybeLaunchSandbox returns at once unless this process was started as the launcher
// of a sandboxed child; then it confines itself and execs that child, never returning.
//
// On Linux, magus confines each process a sandboxed run starts by re-executing the
// running binary as that launcher. A program that runs magus targets with the sandbox
// on must therefore call this first in main, before any configuration, logging or
// connection is set up, or its children start it again in their place.
func MaybeLaunchSandbox() { sandbox.MaybeLaunch() }

// jobPlace places this workspace's job store and checkout record. A user state dir that
// will not resolve is left empty: the store reports it, which the sandbox meets by
// failing open with a warning, and the checkout reads as having no record.
func (m *Magus) jobPlace() job.Location {
	stateBase, _ := xdg.StateDir()
	return job.Location{CacheDir: m.CacheDir(), StateBase: stateBase, Root: m.ws.Root}
}

// SandboxMode is the sandbox mode this workspace's runs are confined under.
func (m *Magus) SandboxMode() types.SandboxMode { return m.cfg.Sandbox.Mode.Resolved() }

// ApplySandbox attaches this workspace's sandbox policy to ctx, or returns ctx unchanged
// when its sandbox mode is off. Any other mode either attaches a policy or returns an
// error: it never runs unconfined. Callers outside a target run (a `magus buzz` script)
// go through here so a script and a target are confined by the same policy; Run calls it
// too, so there is one condition rather than a copy per entry point. The write grant is
// narrowed to the acting lease's row, resolved by job.ActingLease in the order the guard
// hook resolves it. In every mode it installs the edit judge the fs writers consult.
//
// Nothing here confines this process: the policy's children are confined as each
// starts (see sandbox.Command). Required mode is refused (MGS2012) here rather than at
// the first child when the kernel cannot confine them, and best-effort without the
// kernel layer says so once (MGS2005).
func (m *Magus) ApplySandbox(ctx context.Context) (context.Context, error) {
	mode := m.cfg.Sandbox.Mode
	if !mode.Enabled() {
		return std.WithEditJudge(ctx, m.editJudge()), nil
	}
	loc := m.jobPlace()
	// Every spell the workspace loaded, so a script, a magusfile body and a nested magus
	// get every toolchain's grants; runTarget narrows a spell op's child to its project's.
	// A service spell's grants are kept for its own ops alone (sandbox.ForProvider).
	loaded := project.DefaultSpellRegistry().All()
	storeDir, err := config.StoreDir(m.ws.Root, m.cfg)
	if err != nil {
		return ctx, err
	}
	p, err := sandbox.FromConfig(m.ws.Root, loc.CacheDir, storeDir, m.cfg.Sandbox, spells.Sandboxes(loaded), m.serviceSpells(loaded)...)
	if err != nil {
		return ctx, err
	}
	lease, from := job.RunLease(ctx, loc)
	p = sandbox.NarrowToLease(ctx, p, loc, lease, from)
	confines, err := p.KernelConfines()
	if err != nil {
		return ctx, err
	}
	if !confines {
		warnKernelUnavailable(ctx)
	}
	// The binding checks sit below observability in the import graph, so the live
	// provider reaches them as a recorder on the same ctx that carries the policy.
	if prov := observability.FromContext(ctx); prov != nil {
		ctx = sandbox.WithMetrics(ctx, prov)
		recordRules(ctx, prov, p)
	}
	return std.WithEditJudge(sandbox.WithPolicy(ctx, p), m.editJudge()), nil
}

// serviceSpells names the loaded spells that serve magus rather than a target: the
// remote cache backend, and any spell contributing no op a target composes, such as a CI
// or secret provider. Such a spell's sandbox declaration exists for its own handler ops,
// so it must not become a grant every script and step receives: the GitHub Actions
// spell's cache write token would otherwise reach every target it caches for.
func (m *Magus) serviceSpells(loaded []*spells.Spell) []string {
	var names []string
	if name := m.wsReg.RemoteBackend(); name != "" {
		names = append(names, name)
	}
	for _, s := range loaded {
		if len(s.Targets()) == 0 {
			names = append(names, s.Name())
		}
	}
	return names
}

// warnedKernelUnavailable keeps MGS2005 to one line per process.
var warnedKernelUnavailable sync.Once

// warnKernelUnavailable logs MGS2005 for a best-effort sandbox on a host without
// landlock. A nested magus says nothing: the invocation that started it already did,
// and a line per nested run would teach a reader to skip it.
func warnKernelUnavailable(ctx context.Context) {
	if spawn.CurrentLevel() > 0 {
		return
	}
	warnedKernelUnavailable.Do(func() {
		reason := "landlock reports ABI 0"
		if _, err := sandbox.ABI(); err != nil {
			reason = err.Error()
		}
		slog.WarnContext(ctx, types.FormatDiagnostic(types.SandboxUnsupported,
			"kernel landlock unavailable; children run under magus's binding checks and env allowlist only"),
			"reason", reason)
	})
}

// recordRules reports the allow-rules p was built from under the workspace scope.
func recordRules(ctx context.Context, prov observability.Provider, p *sandbox.Policy) {
	access := map[filesystem.Access]int64{}
	for _, r := range p.FS.Rules {
		for _, a := range []filesystem.Access{filesystem.Read, filesystem.Write, filesystem.Exec} {
			if r.Grants(a) {
				access[a]++
			}
		}
	}
	prov.RecordSandboxRules(ctx, observability.SandboxRules{
		Access:   access,
		EnvExact: int64(len(p.Env.Names)),
		EnvGlob:  int64(len(p.Env.Prefixes)),
		Scope:    "workspace",
	})
}

// editJudge grades a Buzz fs write to an existing workspace file
// with guard.GradeEdit, the grader `refs --rename` uses.
// The cache and temp dirs are not graded.
// In a target run a declared output passes: the run makes it.
func (m *Magus) editJudge() std.EditJudge {
	root := canonical(m.Root())
	rel := func(ctx context.Context, path string) (string, bool) {
		real := canonical(path)
		if real == "" || !within(root, real) {
			return "", false
		}
		for _, dir := range []string{m.CacheDir(), os.TempDir(), sandbox.PolicyFromContext(ctx).TempBase()} {
			// A temp dir holding the workspace would skip all of it.
			if d := canonical(dir); d != "" && !within(d, root) && within(d, real) {
				return "", false
			}
		}
		r, err := filepath.Rel(root, real)
		return filepath.ToSlash(r), err == nil
	}
	var once sync.Once
	var deps guard.Dependencies
	var depsErr error
	return std.EditJudge{
		Covers: func(ctx context.Context, path string) bool {
			_, ok := rel(ctx, path)
			return ok
		},
		Grade: func(ctx context.Context, path string, before, after []byte) error {
			r, ok := rel(ctx, path)
			if !ok {
				return fmt.Errorf("fs write %s: refused: it left the workspace while being judged", path)
			}
			once.Do(func() { deps, depsErr = m.guardDependencies(ctx) })
			if depsErr != nil {
				return fmt.Errorf("fs write %s: refused: the guard rules that grade it did not load: %w", r, depsErr)
			}
			inRun := types.ActiveDispatchFromContext(ctx) != nil
			rule, reason := guard.GradeEdit(ctx, deps, m, r, before, after, guard.EditGrade{InRun: inRun})
			switch {
			case reason == "":
				return nil
			case rule == "":
				return fmt.Errorf("fs write %s: refused: %s", r, reason)
			}
			return fmt.Errorf("fs write %s: refused by the guard [%s]: %s", r, rule, reason)
		},
	}
}

// canonical is path absolute with symlinks resolved, "" when unresolvable.
func canonical(path string) string {
	if path == "" {
		return ""
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(real)
	if err != nil {
		return ""
	}
	return abs
}

// within reports whether path is dir or lies under it.
func within(dir, path string) bool {
	r, err := filepath.Rel(dir, path)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}
