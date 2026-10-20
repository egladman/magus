package bindings

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/bindings/ffi"
	bindgen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/parsecache"
	"github.com/egladman/magus/internal/spell"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
)

// CheckSpellFile compiles the Buzz spell at path without registering it and returns
// why it fails to load. Import resolution swallows that error (see
// loadLocalBuzzSpell), so a caller that must see every broken spell asks here. A
// module that is not a spell is not a failure.
func CheckSpellFile(ctx context.Context, path string) error {
	_, _, err := compileBuzzSpell(ctx, path)
	if errors.Is(err, spell.ErrNotASpell) {
		return nil
	}
	return err
}

func compileBuzzSpell(ctx context.Context, path string) (spells.Descriptor, string, error) {
	// Every import statement reaches the resolver, even of a module already loaded, so
	// a library imported by ten files would otherwise be read ten times to learn the
	// same answer.
	probes := interp.ImportProbesFromContext(ctx)
	if probes.NotSpell(path) {
		return spells.Descriptor{}, "", spell.ErrNotASpell
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return spells.Descriptor{}, "", fmt.Errorf("load spell %q: %w", path, err)
	}
	src := string(data)
	// A spell exports mgs_getName, and only the file's own source can export a name, so
	// without the text it is a plain module. Executing one to find that out doubled the
	// cost of every bare-path library import, since the file search runs it again.
	if !strings.Contains(src, spell.NameFunc) {
		probes.MarkNotSpell(path)
		return spells.Descriptor{}, "", spell.ErrNotASpell
	}
	resolved, err := resolveWithModules(ctx, src, filepath.Dir(path))
	if err == nil {
		var spec spells.Descriptor
		if spec, err = resolved.Descriptor(); err == nil {
			return spec, src, nil
		}
	}
	return spells.Descriptor{}, "", fmt.Errorf("load spell %q: %w", path, hint.ExplainImplicitMagus(err))
}

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
	spec, sp, err := newBuzzSpell(ctx, path)
	if err != nil {
		return spells.Descriptor{}, nil, err
	}
	// Register-if-absent (not Lookup-then-Register): two imports of the same spell
	// racing here must not both reach RegisterSpell's duplicate panic. First wins;
	// a re-import gets the existing handle. Op bodies re-read inputs per call, so a
	// fixed captured source is correct.
	return spec, project.DefaultSpellRegistry().RegisterIfAbsent(sp), nil
}

// newBuzzSpell compiles the Buzz spell at path into a spell that is not yet
// registered, so an override of an embedded spell can replace the entry by name where
// loadBuzzSpell would keep the first one.
func newBuzzSpell(ctx context.Context, path string) (spells.Descriptor, *spells.Spell, error) {
	spec, src, err := compileBuzzSpell(ctx, path)
	if err != nil {
		return spells.Descriptor{}, nil, err
	}
	sp := spells.NewSpell(spec.Name, append(localSpellBaseOptions(spec),
		spells.WithInvoker(newBuzzSpellInvoker(spec, src)), spells.WithFuncSource(src))...)
	return spec, sp, nil
}

// resolveWithModules runs the spell module in a session with the host modules registered,
// the load-time twin of execSpellSource's session, then resolves its mgs_ functions.
// dir is the spell's own directory, on the import search path so sibling helper modules
// resolve during discovery.
func resolveWithModules(ctx context.Context, src, dir string) (spell.Resolved, error) {
	roots := []string{dir}
	if source := interp.SourceFromContext(ctx); source != nil && source.Dir != dir {
		roots = append(roots, source.Dir)
	}
	sess := buzz.NewSession(ctx, buzz.WithEmbedded(), buzz.WithParseCache(parsecache.Shared()), buzz.WithSearchPaths(spellSearchPaths(roots...)...))
	defer sess.Close()
	interp.AttachSessionObservers(ctx, sess, interp.ModeSpell)
	sess.AddCompileObserver(buzz.ProfileFromContext(ctx))
	// A spell gets SCRIPT mode, the same one `magus buzz` sees: the members that
	// declare into a workspace being loaded raise MGS1022, the rest work. Without this
	// `import "magus"` fails outright with BZZ2001, which reads as "the module does not
	// exist": the failure mode buildMagus explicitly rejects for script mode.
	// Note the in-process readers (ls, targets, graph) still raise here: a spell has no
	// workspace on its context, so it must reach for the forking members (cmd, describe).
	// It registers before the modules, which set the magus enums on this namespace.
	RegisterMagusNamespace(ctx, sess)
	registerMagusModules(ctx, sess)
	if err := interp.TimeExec(ctx, interp.ModeSpell, func() error { return sess.Exec(ctx, src) }); err != nil {
		return spell.Resolved{}, err
	}
	return spell.Resolve(ctx, sess, src)
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
// op (from mgs_listTargets) runs its command through the shared bridge. Otherwise, if
// the spell exports a function by that name, callBuzzSpellFunc calls it with req.Params.
// An unknown name is a no-op (a fan-out skip). Provider ops do not come through here:
// they go through CallOp.
func newBuzzSpellInvoker(spec spells.Descriptor, src string) func(context.Context, spells.InvokeRequest) (any, error) {
	return func(ctx context.Context, req spells.InvokeRequest) (any, error) {
		if _, ok := spec.Ops[req.Target]; ok {
			return dispatchOp(ctx, spec, req)
		}
		return callBuzzSpellFunc(ctx, src, req.Target, req)
	}
}

// callBuzzSpellFunc calls the exported function fn of src as
// fun(target: Target, cb: fun(any)): the function calls cb(io) with an empty map and
// reads the inputs magus copies into it from req.Params, then returns its result,
// marshaled back to a Go value.
//
// TODO: only the harness, review and workspace-provider ops still use this; once they
// move onto CallOp, delete it and newBuzzSpellInvoker's fallback with it.
func callBuzzSpellFunc(ctx context.Context, src, fn string, req spells.InvokeRequest) (any, error) {
	sess, err := execSpellSource(ctx, src, fmt.Sprintf("spell handler op %q", fn))
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	f, ok := sess.Exports()[fn]
	if !ok {
		// No such exported function: a no-op, not an error. This is the fan-out skip
		// for an unknown target, and how an op a spell does not implement reports it:
		// nil Data.
		return nil, nil //nolint:nilnil // documented no-op: unknown function, nil Data
	}
	// cb delivers the op's inputs by copying req.Params into the map the handler
	// hands it. Buzz maps are pointer-backed, so the handler sees the writes after
	// cb(io) returns. A handler that needs no inputs simply never calls cb.
	params := ffi.AnyToValue(req.Params)
	tgt := bindgen.ObjectTarget(providerTarget(ctx, req.Target, req.Dir))
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
	return ffi.ValueToAny(rv), nil
}
