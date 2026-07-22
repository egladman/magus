package bindings

import (
	"context"
	"sync/atomic"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/workspace"
)

// noopTargets builds a targets map whose callables are never expected to run, so a
// test that only exercises name-matching/resolution can supply a target set without
// caring about dispatch. A nil callable is a valid registered target: dispatchBuzzDeps
// treats it as a no-op (see the `fn == nil` branch), which mirrors targets that exist
// in the graph but have no runnable body.
func noopTargets(names ...string) map[string]vm.Callable {
	m := make(map[string]vm.Callable, len(names))
	for _, n := range names {
		m[n] = nil
	}
	return m
}

// noop is a Buzz callable that does nothing; handy for building function VALUES
// (vm.DirectValue) to feed magus.needs in resolution tests.
func noop(context.Context, []vm.Value) (vm.Value, error) { return vm.Null, nil }

// requireDirect asserts that a namespace exposes name as a DirectValue and returns it.
// A missing key or a non-callable entry is a wiring regression. Direct invocation of
// the returned callable is not possible from outside package vm (the *directObj
// accessor is unexported, and Session.CallValue discards a DirectValue's pushed result
// because no frame is created); value-returning builtins are exercised end to end
// through the interpreter instead.
func requireDirect(t *testing.T, ns vm.Value, name string) vm.Value {
	t.Helper()
	fn, ok := ns.MapGet(name)
	require.Truef(t, ok, "namespace missing %q", name)
	require.Truef(t, fn.IsDirect(), "%q is not a DirectValue", name)
	return fn
}

// callVoidDirect invokes a DirectValue whose result is Null (or an error) through a
// Session - the same CallValue path host code uses. It suits side-effecting builtins
// like magus.cache.remote where the return is Null and the error is what matters; a
// DirectValue that yields a non-Null value cannot be read back this way (see
// requireDirect) and is covered end to end instead.
func callVoidDirect(t *testing.T, fn vm.Value, args ...vm.Value) error {
	t.Helper()
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	_, err := sess.CallValue(ctx, fn, args)
	return err
}

func TestMatchBuzzTargets(t *testing.T) {
	targets := noopTargets("go-build", "go-test", "rust-build", "lint")

	tests := []struct {
		name     string
		patterns []string
		want     []string
	}{
		// Suffix shorthand: "build" is treated as ".*-build", matching the two
		// "-build" targets, sorted, and not the bare "lint".
		{"suffix shorthand", []string{"build"}, []string{"go-build", "rust-build"}},
		// A glob with "*" is anchored and translated: "go-*" matches only the go targets.
		{"glob star prefix", []string{"go-*"}, []string{"go-build", "go-test"}},
		// A target matched by two patterns is returned once (seen-set dedup).
		{"overlapping patterns dedup", []string{"build", "*-build"}, []string{"go-build", "rust-build"}},
		// No match yields nil, not an empty non-nil slice the caller must special-case.
		{"no match", []string{"python-*"}, nil},
		// The full wildcard matches every registered target, sorted.
		{"match all", []string{"*"}, []string{"go-build", "go-test", "lint", "rust-build"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchBuzzTargets(targets, tt.patterns)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestResolveTargetFun pins how magus.needs maps a passed function value to its
// canonical target key: the declared name is normalized like the run path, existence
// is checked against the target set, and - when an export registry is available - the
// value must BE the exported function, so a local helper sharing a target's name can't
// stand in for it.
func TestResolveTargetFun(t *testing.T) {
	t.Run("named exported target resolves and normalizes", func(t *testing.T) {
		// A camelCase-named handle normalizes to the kebab-registered key, matching
		// the CLI's many-spellings forgiveness.
		fn := vm.DirectValue("goBuild", noop)
		targets := noopTargets("go-build")
		exports := map[string]vm.Value{"go-build": fn}
		got, err := resolveTargetFun(targets, exports, fn)
		require.NoError(t, err)
		assert.Equal(t, "go-build", got)
	})

	t.Run("anonymous function is rejected", func(t *testing.T) {
		for _, name := range []string{"", "<fun>"} {
			_, err := resolveTargetFun(noopTargets("go-build"), nil, vm.DirectValue(name, noop))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "anonymous function is not a target")
		}
	})

	t.Run("a named non-target function is rejected", func(t *testing.T) {
		_, err := resolveTargetFun(noopTargets("go-build"), nil, vm.DirectValue("nope", noop))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `function "nope" does not name an exported target`)
	})

	t.Run("a function matching a target name but not its value is rejected", func(t *testing.T) {
		// The identity guard: a local helper named like a target must not silently
		// stand in for the exported target function.
		exported := vm.DirectValue("go-build", noop)
		impostor := vm.DirectValue("go-build", noop)
		exports := map[string]vm.Value{"go-build": exported}
		_, err := resolveTargetFun(noopTargets("go-build"), exports, impostor)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is not the exported target function")
	})

	t.Run("nil exports skips the identity check (REPL)", func(t *testing.T) {
		// The REPL has no export-discovery pass, so identity can't be verified; a
		// name match against the target set is enough.
		fn := vm.DirectValue("go-build", noop)
		got, err := resolveTargetFun(noopTargets("go-build"), nil, fn)
		require.NoError(t, err)
		assert.Equal(t, "go-build", got)
	})
}

// TestExternalHandles pins the cross-project handle registry: a handle is recovered
// by value identity (via the new vm.Value.Equal), and an unregistered value misses.
func TestExternalHandles(t *testing.T) {
	ext := &externalHandles{}
	handle := vm.DirectValue("../b.build", noop)
	dep := externalTarget{Project: "../b", Target: "build"}
	ext.register(handle, dep)

	got, ok := ext.lookup(handle)
	require.True(t, ok, "the registered handle must be recovered")
	assert.Equal(t, dep, got)

	_, ok = ext.lookup(vm.DirectValue("../b.build", noop))
	assert.False(t, ok, "a distinct value with the same name must not match (identity, not name)")
}

func TestBuildBuzzNeeds(t *testing.T) {
	t.Run("an exported target function resolves and dispatches", func(t *testing.T) {
		var runs atomic.Int32
		record := func(context.Context, []vm.Value) (vm.Value, error) { runs.Add(1); return vm.Null, nil }
		buildFn := vm.DirectValue("go-build", record)
		targets := map[string]vm.Callable{"go-build": record}
		exports := map[string]vm.Value{"go-build": buildFn}
		needs := buildBuzzNeeds(targets, exports, &externalHandles{})

		v, err := needs(context.Background(), []vm.Value{buildFn})
		require.NoError(t, err)
		require.Equal(t, vm.Null, v)
		assert.Equal(t, int32(1), runs.Load())
	})

	t.Run("a string argument is rejected", func(t *testing.T) {
		needs := buildBuzzNeeds(noopTargets("go-build"), nil, &externalHandles{})
		_, err := needs(context.Background(), []vm.Value{vm.StrValue("go-build")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be a target function")
		assert.Contains(t, err.Error(), "ctx.glob(...)")
	})

	t.Run("an anonymous function is rejected", func(t *testing.T) {
		needs := buildBuzzNeeds(noopTargets("go-build"), nil, &externalHandles{})
		_, err := needs(context.Background(), []vm.Value{vm.DirectValue("", noop)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "anonymous function is not a target")
	})

	t.Run("a named non-target function is rejected", func(t *testing.T) {
		needs := buildBuzzNeeds(noopTargets("go-build"), nil, &externalHandles{})
		_, err := needs(context.Background(), []vm.Value{vm.DirectValue("nope", noop)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not name an exported target")
	})

	t.Run("a cross-project handle is dispatched separately and no-ops without a coordinator", func(t *testing.T) {
		// dispatchBuzzExternal no-ops when there is no CrossDispatch/Source/Workspace
		// in ctx (the describe/parse path), so magus.needs of a cross handle succeeds
		// without touching the same-project target set.
		ext := &externalHandles{}
		handle := vm.DirectValue("../b.build", noop)
		ext.register(handle, externalTarget{Project: "../b", Target: "build"})
		needs := buildBuzzNeeds(noopTargets("go-build"), nil, ext)

		v, err := needs(context.Background(), []vm.Value{handle})
		require.NoError(t, err)
		require.Equal(t, vm.Null, v)
	})
}

func TestBuildBuzzGlob(t *testing.T) {
	// mkTargets builds a matching targets map plus the parallel exports map of handle
	// values glob returns, both keyed by the same target names.
	mkTargets := func(names ...string) (map[string]vm.Callable, map[string]vm.Value) {
		targets := map[string]vm.Callable{}
		exports := map[string]vm.Value{}
		for _, n := range names {
			targets[n] = noop
			exports[n] = vm.DirectValue(n, noop)
		}
		return targets, exports
	}
	handleNames := func(v vm.Value) []string {
		var out []string
		for _, h := range v.ListItems() {
			out = append(out, h.FunName())
		}
		return out
	}

	t.Run("resolves patterns to the matching target handles", func(t *testing.T) {
		targets, exports := mkTargets("go-build", "go-test", "lint")
		v, err := buildBuzzGlob(targets, exports)(context.Background(), []vm.Value{vm.StrValue("go-*")})
		require.NoError(t, err)
		require.True(t, v.IsList())
		// matchBuzzTargets sorts, so the handles come back in target-name order.
		assert.Equal(t, []string{"go-build", "go-test"}, handleNames(v), "go-* matches go-build and go-test, not lint")
	})

	t.Run("a non-string argument is rejected", func(t *testing.T) {
		targets, exports := mkTargets("go-build")
		_, err := buildBuzzGlob(targets, exports)(context.Background(), []vm.Value{vm.IntValue(7)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be a glob pattern string")
	})

	t.Run("zero arguments is an error", func(t *testing.T) {
		targets, exports := mkTargets("go-build")
		_, err := buildBuzzGlob(targets, exports)(context.Background(), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at least one glob pattern")
	})

	t.Run("a pattern matching nothing yields an empty list", func(t *testing.T) {
		targets, exports := mkTargets("go-build")
		v, err := buildBuzzGlob(targets, exports)(context.Background(), []vm.Value{vm.StrValue("python-*")})
		require.NoError(t, err)
		require.True(t, v.IsList())
		assert.Empty(t, v.ListItems(), "no match yields no handles")
	})

	t.Run("needs flattens a glob list and dispatches each match", func(t *testing.T) {
		var runs atomic.Int32
		record := func(context.Context, []vm.Value) (vm.Value, error) { runs.Add(1); return vm.Null, nil }
		targets := map[string]vm.Callable{"go-build": record, "go-test": record, "lint": record}
		exports := map[string]vm.Value{"go-build": vm.DirectValue("go-build", record), "go-test": vm.DirectValue("go-test", record), "lint": vm.DirectValue("lint", record)}
		globbed, err := buildBuzzGlob(targets, exports)(context.Background(), []vm.Value{vm.StrValue("go-*")})
		require.NoError(t, err)

		needs := buildBuzzNeeds(targets, exports, &externalHandles{})
		v, err := needs(context.Background(), []vm.Value{globbed})
		require.NoError(t, err)
		require.Equal(t, vm.Null, v)
		assert.Equal(t, int32(2), runs.Load(), "needs(glob(go-*)) dispatches go-build and go-test, not lint")
	})
}

func TestBuildCacheNS(t *testing.T) {
	t.Run("valid spell handle records the remote backend", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		// buildCacheNS captures the ctx at construction; remote() records onto that
		// registry regardless of the call-time ctx.
		ns := buildCacheNS(workspace.ContextWithRegistry(context.Background(), reg), nil)

		handle := vm.NewMap()
		handle.MapSet("name", vm.StrValue("s3-cache"))
		require.NoError(t, callVoidDirect(t, requireDirect(t, ns, "remote"), handle))
		assert.Equal(t, "s3-cache", reg.RemoteBackend())
	})

	t.Run("no registry in context is a silent no-op", func(t *testing.T) {
		// describe/parse runs have no per-Open registry; remote() must not panic and
		// simply records nothing.
		ns := buildCacheNS(context.Background(), nil)
		handle := vm.NewMap()
		handle.MapSet("name", vm.StrValue("s3-cache"))
		require.NoError(t, callVoidDirect(t, requireDirect(t, ns, "remote"), handle))
	})

	t.Run("non-map argument is rejected", func(t *testing.T) {
		ns := buildCacheNS(context.Background(), nil)
		err := callVoidDirect(t, requireDirect(t, ns, "remote"), vm.StrValue("s3-cache"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "imported spell handle")
	})

	t.Run("map without a name is rejected", func(t *testing.T) {
		ns := buildCacheNS(context.Background(), nil)
		err := callVoidDirect(t, requireDirect(t, ns, "remote"), vm.NewMap())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no name")
	})
}

func TestDispatchBuzzDeps(t *testing.T) {
	t.Run("empty names is a no-op", func(t *testing.T) {
		require.NoError(t, dispatchBuzzDeps(context.Background(), nil, nil))
	})

	t.Run("inline path runs each named target once, deduped", func(t *testing.T) {
		// With no Source/pool in ctx, dispatchBuzzDeps takes the inline sequential
		// branch and calls each target's callable directly. A name listed twice must
		// run once (dedupStrings).
		var buildRuns, testRuns atomic.Int32
		targets := map[string]vm.Callable{
			"go-build": func(context.Context, []vm.Value) (vm.Value, error) {
				buildRuns.Add(1)
				return vm.Null, nil
			},
			"go-test": func(context.Context, []vm.Value) (vm.Value, error) {
				testRuns.Add(1)
				return vm.Null, nil
			},
		}
		err := dispatchBuzzDeps(context.Background(), targets, []string{"go-build", "go-test", "go-build"})
		require.NoError(t, err)
		assert.Equal(t, int32(1), buildRuns.Load())
		assert.Equal(t, int32(1), testRuns.Load())
	})

	t.Run("nil callable is a registered no-op target", func(t *testing.T) {
		require.NoError(t, dispatchBuzzDeps(context.Background(), noopTargets("go-build"), []string{"go-build"}))
	})

	t.Run("unknown target is an error", func(t *testing.T) {
		err := dispatchBuzzDeps(context.Background(), noopTargets("go-build"), []string{"missing"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `unknown target "missing"`)
	})

	t.Run("target error is wrapped with its name", func(t *testing.T) {
		targets := map[string]vm.Callable{
			"go-build": func(context.Context, []vm.Value) (vm.Value, error) {
				return vm.Null, stubErr{}
			},
		}
		err := dispatchBuzzDeps(context.Background(), targets, []string{"go-build"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "go-build: ")
	})
}

// TestDispatchBuzzExternalNoCoordinator asserts the graph-only fall-through: with an
// empty context there is no coordinator, source, or workspace, so a cross-project
// dispatch is a silent no-op (the describe/parse path keeps the handle graph-only).
func TestDispatchBuzzExternalNoCoordinator(t *testing.T) {
	err := dispatchBuzzExternal(context.Background(), externalTarget{Project: "../other", Target: "build"})
	require.NoError(t, err)
}

// stubErr is a sentinel error used to prove dispatchBuzzDeps wraps a target failure
// with the target name.
type stubErr struct{}

func (stubErr) Error() string { return "boom" }
