package bindings

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/interp"
	bindinggen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/spellruntime"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
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
func loadBuzzSpell(ctx context.Context, path string) (spells.Descriptor, *spells.Spell, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return spells.Descriptor{}, nil, fmt.Errorf("load spell %q: %w", path, err)
	}
	src := string(data)
	spec, err := extractDescriptorWithModules(ctx, src, filepath.Dir(path))
	if err != nil {
		return spells.Descriptor{}, nil, fmt.Errorf("load spell %q: %w", path, err)
	}
	sp := spells.NewSpell(spec.Name,
		spells.WithSources(spec.Needs...),
		spells.WithClaims(spec.Claims...),
		spells.WithIgnoreDirs(spec.IgnoreDirs...),
		spells.WithManifests(spec.Manifests...),
		spells.WithOutputs(spec.Provides...),
		spells.WithTargets(spec.OpNames()...),
		spells.WithServiceTargets(spec.ServiceOpNames()...),
		spells.WithInvoker(newBuzzSpellInvoker(spec, src)),
		spells.WithCommandRenderer(newCommandRenderer(spec.Ops)),
		spells.WithCommandExplainer(newCommandExplainer(spec.Ops)),
		spells.WithCommandConflicts(newCommandConflictChecker(spec.Ops)),
		spells.WithServiceView(newServiceViewer(spec.Ops)),
		spells.WithTargetCharms(charmNamesByTarget(spec.Ops)),
		spells.WithTargetDocs(docsByTarget(spec.Ops)),
		// A workspace-local Buzz spell: doctor enforces a doc comment on each
		// function-handler target (record-style {cmd,args} ops are exempt).
		spells.WithDocRequiredTargets(spec.DocOps...),
	)
	// The rest of OptionalContract. These are applied here rather than in the
	// NewSpell call above because each is conditional on the spell having declared
	// it, and NewSpell takes options rather than a descriptor.
	//
	// Omitting them was a silent, three-way capability loss for every
	// workspace-local spell: the descriptor carried mgs_getVersionProbe,
	// mgs_getLanguage and mgs_isOpaque faithfully, and none of them reached the
	// registered spell. A declared version probe therefore never ran and never
	// entered the cache key, so a local spell's toolchain could drift with nothing
	// invalidating; the spell reported no language; and an opaque spell was treated
	// as transparent. The two sibling construction paths in spell.go already wired
	// all of this, which is why built-in spells were unaffected and the gap stayed
	// invisible.
	var extra []spells.Option
	extra = append(extra, spells.WithTools(spec.Tools), spells.WithVersionProber(versionProber))
	if spec.Language != "" {
		extra = append(extra, spells.WithLanguage(spec.Language))
	}
	if spec.Opaque {
		extra = append(extra, spells.WithOpaque())
	}
	for _, o := range extra {
		o(sp)
	}
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
func extractDescriptorWithModules(ctx context.Context, src, dir string) (spells.Descriptor, error) {
	roots := []string{dir}
	if source := interp.SourceFromContext(ctx); source != nil && source.Dir != dir {
		roots = append(roots, source.Dir)
	}
	sess := buzz.NewSession(ctx, buzz.WithEmbedded(), buzz.WithSearchPaths(spellSearchPaths(roots...)...))
	defer sess.Close()
	interp.AttachSessionObservers(ctx, sess, interp.ModeSpell)
	registerMagusModules(ctx, sess)
	if err := interp.TimeExec(ctx, interp.ModeSpell, func() error { return sess.Exec(ctx, src) }); err != nil {
		return spells.Descriptor{}, err
	}
	return spellruntime.Resolve(ctx, sess)
}

// spellSearchPaths resolves imports from the candidate and its magusfile's directory.
func spellSearchPaths(roots ...string) []string {
	templates := []string{
		"?.buzz",
		filepath.Join("?", "main.buzz"),
		filepath.Join("?", "src", "main.buzz"),
		filepath.Join("?", "src", "?.buzz"),
	}
	paths := make([]string, 0, len(roots)*len(templates))
	for _, root := range roots {
		for _, t := range templates {
			paths = append(paths, filepath.Join(root, t))
		}
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
func newBuzzSpellInvoker(spec spells.Descriptor, src string) func(context.Context, spells.InvokeRequest) (any, error) {
	return func(ctx context.Context, req spells.InvokeRequest) (any, error) {
		if _, ok := spec.Ops[req.Target]; ok {
			return dispatchOp(ctx, spec.Ops, readinessOf(spec), req)
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
func callBuzzSpellFunc(ctx context.Context, src, fn string, req spells.InvokeRequest) (any, error) {
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
	params := bindinggen.AnyToValue(req.Params)
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
	return bindinggen.ValueToAny(rv), nil
}

// targetValue builds the Buzz Target value a spell handler receives as its first
// argument. A plain map suffices — member access (target.name) reads a map key —
// and most handlers ignore it; it carries the invocation's identity for those
// that don't. The active charms ride on ctx, so a handler that inspects
// target.charms sees the run's real charms rather than an always-empty list.
func targetValue(ctx context.Context, req spells.InvokeRequest) vm.Value {
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
