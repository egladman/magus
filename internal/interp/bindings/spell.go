package bindings

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	ispell "github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

func init() {
	// Lazy-register so fast subcommands (help, version) skip the registration loop entirely.
	project.DefaultSpellRegistry().SetEnsureHook(ensureSpellsRegistered)
}

var ensureSpellsRegistered = sync.OnceFunc(func() {
	for _, spec := range ispell.Builtins() {
		opts := []types.SpellOption{
			types.WithSources(spec.Needs...),
			types.WithClaims(spec.Claims...),
			types.WithSpellOutputs(spec.Provides...),
			types.WithTargets(spec.OpNames()...),
			types.WithInvoker(newSpellInvoker(spec.Ops)),
			types.WithCommandRenderer(newCommandRenderer(spec.Ops)),
			types.WithTargetSources(spec.TargetNeeds),
			types.WithTargetCharms(charmNamesByTarget(spec.Ops)),
			types.WithTargetDocs(docsByTarget(spec.Ops)),
		}
		if spec.Opaque {
			opts = append(opts, types.WithOpaque())
		}
		if len(spec.VersionCmd) > 0 {
			opts = append(opts, types.WithVersionProbe(newVersionProbe(spec.VersionCmd)))
		}
		project.DefaultSpellRegistry().RegisterSpell(types.NewSpell(spec.Name, opts...))
	}
})

// charmNamesByTarget extracts the sorted charm names each target declares, for
// discovery surfaces like `magus describe`.
func charmNamesByTarget(targets map[string]types.SpellOp) map[string][]string {
	out := make(map[string][]string, len(targets))
	for name, t := range targets {
		if len(t.Charms) == 0 {
			continue
		}
		names := make([]string, 0, len(t.Charms))
		for c := range t.Charms {
			names = append(names, c)
		}
		slices.Sort(names)
		out[name] = names
	}
	return out
}

// docsByTarget extracts each target handler's doc comment, for discovery surfaces
// like `magus describe`. Targets with no comment are omitted.
func docsByTarget(targets map[string]types.SpellOp) map[string]string {
	out := make(map[string]string, len(targets))
	for name, t := range targets {
		if t.Doc != "" {
			out[name] = t.Doc
		}
	}
	return out
}

// newVersionProbe runs argv in the project dir and returns trimmed stdout.
func newVersionProbe(argv []string) func(context.Context, string) (string, error) {
	return func(ctx context.Context, dir string) (string, error) {
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("version probe %v in %s: %w", argv, dir, err)
		}
		return strings.TrimSpace(string(out)), nil
	}
}

// newCommandRenderer returns the fork-command preview used by `magus describe`:
// for a fork target it reports cmd plus the argv as reshaped by the active charms
// (the same resolveCharmArgs the runtime uses), executing nothing. A function-op
// target (Func set) or a no-op (empty Cmd) returns ok=false — there is no static
// command to show.
func newCommandRenderer(targets map[string]types.SpellOp) func(string, []string) (string, []string, bool) {
	return func(target string, charms []string) (string, []string, bool) {
		tgt, ok := targets[target]
		if !ok || tgt.Func != "" || tgt.Cmd == "" {
			return "", nil, false
		}
		args, err := resolveCharmArgs(types.WithCharms(context.Background(), charms), tgt.Args, tgt.Charms)
		if err != nil {
			return "", nil, false
		}
		return tgt.Cmd, args, true
	}
}

// dispatchOp is the single op-dispatch bridge between the magus host and the Buzz
// interpreter. The priority is fixed and the same everywhere: prefer an op's
// NATIVE in-VM function (Func) when a script body exists to run it, otherwise fall
// back to FORKING its command (Cmd). runFn runs a named function-op against the
// spell's script body and is nil for built-in spells — they have no body, so their
// function-ops are a graceful no-op. An unknown target is also a no-op, matching
// the fan-out-and-skip dispatch model.
func dispatchOp(ctx context.Context, ops map[string]types.SpellOp, req types.InvokeRequest,
	runFn func(ctx context.Context, fn string, req types.InvokeRequest) (any, error),
) (any, error) {
	tgt, ok := ops[req.Target]
	if !ok {
		//nolint:nilnil // invoker no-op: an unknown target produces no Data and no error (fan-out-and-skip)
		return nil, nil
	}
	if tgt.Func != "" {
		if runFn == nil {
			//nolint:nilnil // invoker no-op: built-in has no script body for a function-op
			return nil, nil
		}
		return runFn(ctx, tgt.Func, req)
	}
	_, err := runForkTarget(ctx, tgt, forkOpts{cwd: req.Dir, args: project.ExtraArgs(ctx)})
	return nil, err
}

// newSpellInvoker returns an invoker closure for a built-in spell. Built-in ops
// are fork-only (cmd/args/charms data, no script body), so it dispatches through
// the shared bridge with a nil function-op runner.
func newSpellInvoker(targets map[string]types.SpellOp) func(context.Context, types.InvokeRequest) (any, error) {
	return func(ctx context.Context, req types.InvokeRequest) (any, error) {
		return dispatchOp(ctx, targets, req, nil)
	}
}

// forkTargetNames returns the spell's fork (forkable) target names, sorted.
func forkTargetNames(targets map[string]types.SpellOp) []string {
	names := make([]string, 0, len(targets))
	for name, tgt := range targets {
		if tgt.Func == "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// loadLocalSpell compiles a workspace-local Buzz spell at path and registers
// it, returning its spec and ok=false on any failure. Errors are logged, not
// raised: discovery paths cannot route an error back to a caller.
func loadLocalSpell(ctx context.Context, path string) (ispell.Descriptor, bool) {
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			slog.Error("load local spell: getwd", "err", err)
			return ispell.Descriptor{}, false
		}
		path = filepath.Join(cwd, path)
	}
	return loadLocalBuzzSpell(ctx, path)
}

// hasFunctionOp reports whether any of m's targets is an in-VM function-op (Func
// set) rather than a fork command.
func hasFunctionOp(m ispell.Descriptor) bool {
	for _, t := range m.Ops {
		if t.Func != "" {
			return true
		}
	}
	return false
}

// loadSpellFile loads a spell file as a function-op-capable SpellDriver and
// registers it — the in-package entry point the remote cache backend uses to
// resolve a backend selected by a file path. A .buzz spell loads through the Buzz
// path (registering a function-op spell eagerly, capturing its source for in-VM
// dispatch).
func loadSpellFile(ctx context.Context, path string) (types.SpellDriver, error) {
	if !strings.HasSuffix(path, ".buzz") {
		return nil, fmt.Errorf("spell file %q: must be a .buzz spell", path)
	}
	_, sp, err := loadBuzzSpell(ctx, path)
	if err != nil {
		return nil, err // explicit nil: don't return a typed-nil *types.Spell as a non-nil interface
	}
	return sp, nil
}

// loadLocalBuzzSpell compiles a workspace-local Buzz spell at path, returning its
// spec and ok=false on any failure. Extract routes through the same
// ispell.Decode a built-in uses, so a .buzz workspace spell and a built-in are
// read and validated identically. Errors are logged, not raised, since discovery
// paths cannot route an error back to the caller. Registration is deferred to
// magus.project.register; the handle the caller builds carries the resolved spec
// so it registers by value there.
func loadLocalBuzzSpell(ctx context.Context, path string) (ispell.Descriptor, bool) {
	// loadBuzzSpell registers the spell with the function-op invoker (capturing its
	// source), so an imported Buzz spell carries function-ops whether it is later
	// bound to a project or wired as the remote cache backend. project bind finds
	// it already registered and binds it by name.
	m, _, err := loadBuzzSpell(ctx, path)
	if err != nil {
		slog.Error("load local spell: buzz", "path", path, "err", err)
		return ispell.Descriptor{}, false
	}
	return m, true
}

// localSpellBaseOptions builds the SpellOptions common to every workspace-local
// spell registration (cache metadata, command renderer, charm/doc discovery),
// minus the invoker — each registration path supplies its own.
func localSpellBaseOptions(m ispell.Descriptor) []types.SpellOption {
	opts := []types.SpellOption{
		types.WithSources(m.Needs...),
		types.WithClaims(m.Claims...),
		types.WithSpellOutputs(m.Provides...),
		types.WithTargets(m.OpNames()...),
		types.WithCommandRenderer(newCommandRenderer(m.Ops)),
		types.WithTargetCharms(charmNamesByTarget(m.Ops)),
		types.WithTargetDocs(docsByTarget(m.Ops)),
		types.WithDocRequiredTargets(m.DocOps...),
	}
	if m.Opaque {
		opts = append(opts, types.WithOpaque())
	}
	if len(m.VersionCmd) > 0 {
		opts = append(opts, types.WithVersionProbe(newVersionProbe(m.VersionCmd)))
	}
	return opts
}

// registerLocalSpell registers a decoded fork-only workspace-local spell into the
// default registry. The shared ispell.Decode produces m for the imported Buzz
// spell by-value path, so this is the single deferred registration point (called at
// magus.project.register bind time). A function-op spell instead registers eagerly
// at load via loadBuzzSpell.
func registerLocalSpell(m ispell.Descriptor) {
	opts := append(localSpellBaseOptions(m), types.WithInvoker(newSpellInvoker(m.Ops)))
	project.DefaultSpellRegistry().RegisterIfAbsent(types.NewSpell(m.Name, opts...))
}
