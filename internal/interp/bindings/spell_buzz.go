package bindings

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egladman/magus/host"
	"github.com/egladman/magus/internal/interp"
	ispell "github.com/egladman/magus/internal/spell"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// loadBuzzSpell reads, extracts (with host modules registered), and idempotently
// registers a Buzz spell with handler op support, returning its spec and the
// registered driver. This is the single place a Buzz spell becomes a registered
// spell — whether reached by `import "spells/<name>"` or the remote-cache
// resolver — so every imported Buzz spell carries handler ops uniformly, not
// only those wired through the cache.
//
// Registering at load time (rather than deferring to project bind) is what lets
// the handler op invoker capture the spell source; the spec-only handle
// can't. Op bodies re-read their inputs each invocation, so a fixed captured
// source is correct, and the registration is idempotent for re-imports.
func loadBuzzSpell(ctx context.Context, path string) (ispell.Descriptor, *types.Spell, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ispell.Descriptor{}, nil, fmt.Errorf("load spell %q: %w", path, err)
	}
	src := string(data)
	spec, err := extractDescriptorWithModules(ctx, src, filepath.Dir(path))
	if err != nil {
		return ispell.Descriptor{}, nil, fmt.Errorf("load spell %q: %w", path, err)
	}
	sp := types.NewSpell(spec.Name,
		types.WithSources(spec.Needs...),
		types.WithClaims(spec.Claims...),
		types.WithIgnoreDirs(spec.IgnoreDirs...),
		types.WithSpellOutputs(spec.Provides...),
		types.WithTargets(spec.OpNames()...),
		types.WithServiceTargets(spec.ServiceOpNames()...),
		types.WithInvoker(newBuzzSpellInvoker(spec, src)),
		types.WithCommandRenderer(newCommandRenderer(spec.Ops)),
		types.WithCommandExplainer(newCommandExplainer(spec.Ops)),
		types.WithCommandConflicts(newCommandConflictChecker(spec.Ops)),
		types.WithServiceView(newServiceViewer(spec.Ops)),
		types.WithTargetCharms(charmNamesByTarget(spec.Ops)),
		types.WithTargetDocs(docsByTarget(spec.Ops)),
		// A workspace-local Buzz spell: doctor enforces a doc comment on each
		// function-handler target (record-style {cmd,args} ops are exempt).
		types.WithDocRequiredTargets(spec.DocOps...),
	)
	// Register-if-absent (not Lookup-then-Register): two imports of the same spell
	// racing here must not both reach RegisterSpell's duplicate panic. First wins;
	// a re-import gets the existing handle. Op bodies re-read inputs per call, so a
	// fixed captured source is correct.
	return spec, project.DefaultSpellRegistry().RegisterIfAbsent(sp), nil
}

// extractDescriptorWithModules runs the spell module in a session that has the std
// and extra host modules registered, then resolves its mgs_ functions. This is the
// load-time twin of callBuzzSpellFunc's session setup, so a spell that imports
// host modules at top level loads as well as it runs. dir is the spell file's own
// directory, added to the import search path so a spell that imports sibling helper
// modules (e.g. render.buzz's `import "render_text"`) resolves during discovery.
func extractDescriptorWithModules(ctx context.Context, src, dir string) (ispell.Descriptor, error) {
	sess := buzz.NewSession(ctx, buzz.WithEmbedded(), buzz.WithSearchPaths(spellSearchPaths(dir)...))
	defer sess.Close()
	interp.AttachSessionObservers(ctx, sess, interp.ModeSpell)
	registerMagusModules(ctx, sess)
	if err := interp.TimeExec(ctx, interp.ModeSpell, func() error { return sess.Exec(ctx, src) }); err != nil {
		return ispell.Descriptor{}, err
	}
	return ispell.Resolve(ctx, sess)
}

// spellSearchPaths returns the import search paths the discovery probe uses for a
// workspace-local spell: the project-relative layouts rooted at the spell's own
// directory, so a plain sibling import (e.g. render.buzz's `import "render_text"`)
// resolves no matter the process cwd. This is deliberately narrower than the run-time
// magusSearchPaths (runtime.go): it omits the cwd, workspace-root, and magusfiles/
// roots and any system/$BUZZ_PATH fallback. The probe only needs to classify a file
// as spell-or-library, and a spell that imports outside its own dir still resolves at
// run time via the fuller set. It is not a port of upstream Buzz resolution (the
// shipped binary is cwd-relative); rooting at the file's own dir is a magus choice
// that keeps discovery cwd-independent. `buzz:` stdlib and registered host modules
// resolve ahead of this via the module resolver.
func spellSearchPaths(dir string) []string {
	templates := []string{
		"?.buzz",
		filepath.Join("?", "main.buzz"),
		filepath.Join("?", "src", "main.buzz"),
		filepath.Join("?", "src", "?.buzz"),
	}
	paths := make([]string, 0, len(templates))
	for _, t := range templates {
		paths = append(paths, filepath.Join(dir, t))
	}
	return paths
}

// newBuzzSpellInvoker dispatches a request against a Buzz spell. A declared command
// op (from mgs_listTargets) runs its command through the shared bridge. Otherwise,
// if the spell exports a function by that name, it is called directly in the VM —
// the escape hatch a remote cache backend uses: its enabled/get_artifact/
// put_artifact/prune are plain exported functions, invoked by name with req.Params,
// not operations in the command model. An unknown name is a no-op (fan-out skip, or
// an optional backend op a spell doesn't implement).
func newBuzzSpellInvoker(spec ispell.Descriptor, src string) func(context.Context, types.InvokeRequest) (any, error) {
	return func(ctx context.Context, req types.InvokeRequest) (any, error) {
		if _, ok := spec.Ops[req.Target]; ok {
			return dispatchOp(ctx, spec.Ops, req)
		}
		return callBuzzSpellFunc(ctx, src, req.Target, req)
	}
}

// callBuzzSpellFunc executes src in a fresh module-registered session and calls
// the exported handler fn with the invocation's Target and the input callback cb,
// returning its result marshalled back to a Go value. The handler signature is
// fun(target: Target, cb: fun(any)) > bool: the handler calls cb(io) with an empty
// map and reads the op's inputs the host writes into it (the cache passes
// {project, hash, dest/src} via req.Params). Inputs arrive by mutation rather than
// as a data argument because cb is a fun(any) callback, not a payload — the same
// typed contract a command spell's cb callback uses. A fresh session per call means
// the spell's top-level code re-runs every invocation, so a handler op spell's
// module body must be idempotent (no one-time side effects) — the mgs_ functions
// and op bodies do the work.
func callBuzzSpellFunc(ctx context.Context, src, fn string, req types.InvokeRequest) (any, error) {
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	interp.AttachSessionObservers(ctx, sess, interp.ModeSpell)
	registerMagusModules(ctx, sess)
	if err := interp.TimeExec(ctx, interp.ModeSpell, func() error { return sess.Exec(ctx, src) }); err != nil {
		return nil, fmt.Errorf("spell handler op %q: exec: %w", fn, err)
	}
	f, ok := sess.Exports()[fn]
	if !ok {
		// No such exported function: a no-op, not an error. This is the fan-out-skip
		// for an unknown target, and how an optional backend op (a cache backend that
		// declares no enabled()/prune()) reports "unsupported" — nil Data, which the
		// remote-cache adapter reads as always-active / not-implemented.
		return nil, nil //nolint:nilnil // documented no-op: unknown function, nil Data
	}
	// cb delivers the op's inputs by copying req.Params into the map the handler
	// hands it. Buzz maps are pointer-backed, so the handler sees the writes after
	// cb(io) returns. A handler that needs no inputs simply never calls cb.
	params := host.AnyToValue(req.Params)
	tgt := targetValue(ctx, req)
	cb := vm.DirectValue("magus.cb", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) > 0 && args[0].IsMap() && params.IsMap() {
			for _, k := range params.MapKeys() {
				v, _ := params.MapGet(k)
				args[0].MapSet(k, v)
			}
		}
		return vm.Null, nil
	})
	args := []vm.Value{tgt, cb}
	rv, err := interp.TimeCall(ctx, interp.ModeSpell, func() (vm.Value, error) {
		return sess.CallValue(ctx, f, args)
	})
	if err != nil {
		return nil, fmt.Errorf("spell handler op %q: %w", fn, err)
	}
	return host.ValueToAny(rv), nil
}

// targetValue builds the Buzz Target value a spell handler receives as its first
// argument. A plain map suffices — member access (target.name) reads a map key —
// and most handlers ignore it; it carries the invocation's identity for those
// that don't. The active charms ride on ctx, so a handler that inspects
// target.charms sees the run's real charms rather than an always-empty list.
func targetValue(ctx context.Context, req types.InvokeRequest) vm.Value {
	t := vm.NewMap()
	t.MapSet("name", vm.StrValue(req.Target))
	t.MapSet("projectPath", vm.StrValue(req.Dir))
	charms := types.CharmsFromContext(ctx)
	cv := make([]vm.Value, len(charms))
	for i, c := range charms {
		cv[i] = vm.StrValue(c)
	}
	t.MapSet("charms", vm.ListValue(cv))
	t.MapSet("files", vm.ListValue(nil))
	return t
}
