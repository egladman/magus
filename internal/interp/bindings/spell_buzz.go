package bindings

import (
	"context"
	"fmt"
	"os"

	buzzeng "github.com/egladman/gopherbuzz"
	ispell "github.com/egladman/magus/internal/spell"
	buzzgen "github.com/egladman/magus/internal/std/gen/buzz"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// loadBuzzSpell reads, extracts (with host modules registered), and idempotently
// registers a Buzz spell with function-op support, returning its spec and the
// registered driver. This is the single place a Buzz spell becomes a registered
// spell — whether reached by `import "spells/<name>"`, magus.spell.load, or the
// remote-cache resolver — so every imported Buzz spell carries function-ops
// uniformly, not only those wired through the cache.
//
// Registering at load time (rather than deferring to project bind) is what lets
// the function-op invoker capture the spell source; the spec-only handle
// can't. Op bodies re-read their inputs each invocation, so a fixed captured
// source is correct, and the registration is idempotent for re-imports.
func loadBuzzSpell(ctx context.Context, path string) (ispell.Spec, *types.Spell, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ispell.Spec{}, nil, fmt.Errorf("load spell %q: %w", path, err)
	}
	src := string(data)
	spec, err := extractSpecWithModules(ctx, src)
	if err != nil {
		return ispell.Spec{}, nil, fmt.Errorf("load spell %q: %w", path, err)
	}
	sp := types.NewSpell(spec.Name,
		types.WithSources(spec.Needs...),
		types.WithClaims(spec.Claims...),
		types.WithSpellOutputs(spec.Provides...),
		types.WithTargets(spec.TargetNames()...),
		types.WithInvoker(newBuzzSpellInvoker(spec, src)),
		types.WithCommandRenderer(newCommandRenderer(spec.Targets)),
		types.WithTargetCharms(charmNamesByTarget(spec.Targets)),
		types.WithTargetDocs(docsByTarget(spec.Targets)),
		// A workspace-local Buzz spell: doctor enforces a doc comment on each
		// function-handler target (record-style {cmd,args} ops are exempt).
		types.WithDocRequiredTargets(spec.DocTargets...),
	)
	// Register-if-absent (not Lookup-then-Register): two imports of the same spell
	// racing here must not both reach RegisterSpell's duplicate panic. First wins;
	// a re-import gets the existing handle. Op bodies re-read inputs per call, so a
	// fixed captured source is correct.
	return spec, project.DefaultSpellRegistry().RegisterIfAbsent(sp), nil
}

// extractSpecWithModules runs the spell module in a session that has the std
// and extra host modules registered, then resolves its mgs_ functions. This is the
// load-time twin of callBuzzSpellFunc's session setup, so a spell that imports
// host modules at top level loads as well as it runs.
func extractSpecWithModules(ctx context.Context, src string) (ispell.Spec, error) {
	sess := buzzeng.NewSession(ctx)
	defer sess.Close()
	registerHostModules(ctx, sess)
	if err := sess.Exec(ctx, src); err != nil {
		return ispell.Spec{}, err
	}
	return ispell.Resolve(ctx, sess, ispell.ForkOrFunctionOps(src))
}

// newBuzzSpellInvoker dispatches a Buzz spell's ops: function-ops (Func set) run
// in the VM with req.Params and return their result as Data; fork ops fork as
// usual. Unknown targets are a graceful no-op, matching the built-in invoker.
func newBuzzSpellInvoker(spec ispell.Spec, src string) func(context.Context, types.InvokeRequest) (any, error) {
	return func(ctx context.Context, req types.InvokeRequest) (any, error) {
		tgt, ok := spec.Targets[req.Target]
		if !ok {
			return nil, nil
		}
		if tgt.Func != "" {
			return callBuzzSpellFunc(ctx, src, tgt.Func, req)
		}
		_, err := runForkTarget(ctx, tgt, forkOpts{cwd: req.Dir, args: project.ExtraArgs(ctx)})
		return nil, err
	}
}

// callBuzzSpellFunc executes src in a fresh module-registered session and calls
// the exported handler fn with the invocation's Target and the input callback cb,
// returning its result marshalled back to a Go value. The handler signature is
// fun(target: Target, cb: fun(any)) > bool: the handler calls cb(io) with an empty
// map and reads the op's inputs the host writes into it (the cache passes
// {project, hash, dest/src} via req.Params). Inputs arrive by mutation rather than
// as a data argument because cb is a fun(any) callback, not a payload — the same
// typed contract a fork spell's cb callback uses. A fresh session per call means
// the spell's top-level code re-runs every invocation, so a function-op spell's
// module body must be idempotent (no one-time side effects) — the mgs_ functions
// and op bodies do the work.
func callBuzzSpellFunc(ctx context.Context, src, fn string, req types.InvokeRequest) (any, error) {
	sess := buzzeng.NewSession(ctx)
	defer sess.Close()
	registerHostModules(ctx, sess)
	if err := sess.Exec(ctx, src); err != nil {
		return nil, fmt.Errorf("spell function-op %q: exec: %w", fn, err)
	}
	f, ok := sess.Exports()[fn]
	if !ok {
		return nil, fmt.Errorf("spell function-op %q: not an exported function", fn)
	}
	// cb delivers the op's inputs by copying req.Params into the map the handler
	// hands it. Buzz maps are pointer-backed, so the handler sees the writes after
	// cb(io) returns. A handler that needs no inputs simply never calls cb.
	params := buzzgen.AnyToValue(req.Params)
	cb := buzzeng.DirectValue("magus.cb", func(_ context.Context, args []buzzeng.Value) (buzzeng.Value, error) {
		if len(args) > 0 && args[0].IsMap() && params.IsMap() {
			for _, k := range params.MapKeys() {
				v, _ := params.MapGet(k)
				args[0].MapSet(k, v)
			}
		}
		return buzzeng.Null, nil
	})
	args := []buzzeng.Value{targetValue(req), cb}
	rv, err := sess.CallValue(ctx, f, args)
	if err != nil {
		return nil, fmt.Errorf("spell function-op %q: %w", fn, err)
	}
	return buzzgen.ValueToAny(rv), nil
}

// targetValue builds the Buzz Target value a spell handler receives as its first
// argument. A plain map suffices — member access (target.name) reads a map key —
// and most handlers ignore it; it carries the invocation's identity for those
// that don't.
func targetValue(req types.InvokeRequest) buzzeng.Value {
	t := buzzeng.NewMap()
	t.MapSet("name", buzzeng.StrValue(req.Target))
	t.MapSet("projectPath", buzzeng.StrValue(req.Dir))
	t.MapSet("charms", buzzeng.ListValue(nil))
	t.MapSet("files", buzzeng.ListValue(nil))
	return t
}
