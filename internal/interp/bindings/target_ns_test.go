package bindings

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
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

// requireDirect asserts that a namespace exposes name as a DirectValue and returns it.
// A missing key or a non-callable entry is a wiring regression. Direct invocation of
// the returned callable is not possible from outside package vm (the *directObj
// accessor is unexported, and Session.CallValue discards a DirectValue's pushed result
// because no frame is created); value-returning builtins are exercised end to end
// through the interpreter instead (see TestTargetNamespaceEndToEnd).
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

func TestResolveTargetQuery(t *testing.T) {
	targets := noopTargets("go-build", "go-test", "rust-build")

	t.Run("literal normalizes and does not consult the target set", func(t *testing.T) {
		// A literal resolves to its own normalized name - it is not required to be a
		// registered target here (existence is enforced later at dispatch). Uses the
		// same normalizer targetMap registration does (execBuzzSrc), so a needs
		// literal gets the CLI's many-spellings forgiveness.
		got, err := resolveTargetQuery(targets, types.TargetQuery{Mode: types.QueryLiteral, Pattern: "GO-Build"})
		require.NoError(t, err)
		require.Equal(t, []string{"go-build"}, got)
	})

	t.Run("literal normalizes camelCase to the kebab-registered name", func(t *testing.T) {
		got, err := resolveTargetQuery(targets, types.TargetQuery{Mode: types.QueryLiteral, Pattern: "goBuild"})
		require.NoError(t, err)
		require.Equal(t, []string{"go-build"}, got)
	})

	t.Run("literal normalizes snake_case to the kebab-registered name", func(t *testing.T) {
		got, err := resolveTargetQuery(targets, types.TargetQuery{Mode: types.QueryLiteral, Pattern: "go_build"})
		require.NoError(t, err)
		require.Equal(t, []string{"go-build"}, got)
	})

	t.Run("glob defers to matchBuzzTargets", func(t *testing.T) {
		got, err := resolveTargetQuery(targets, types.TargetQuery{Mode: types.QueryGlob, Pattern: "go-*"})
		require.NoError(t, err)
		require.Equal(t, []string{"go-build", "go-test"}, got)
	})

	t.Run("regex matches registered names, sorted", func(t *testing.T) {
		got, err := resolveTargetQuery(targets, types.TargetQuery{Mode: types.QueryRegex, Pattern: `-build$`})
		require.NoError(t, err)
		require.Equal(t, []string{"go-build", "rust-build"}, got)
	})

	t.Run("regex with no match yields nil", func(t *testing.T) {
		got, err := resolveTargetQuery(targets, types.TargetQuery{Mode: types.QueryRegex, Pattern: `^nope$`})
		require.NoError(t, err)
		require.Nil(t, got)
	})

	t.Run("invalid regex is a compile error", func(t *testing.T) {
		_, err := resolveTargetQuery(targets, types.TargetQuery{Mode: types.QueryRegex, Pattern: `([`})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "target.regex")
	})

	t.Run("unknown mode is rejected", func(t *testing.T) {
		// An external query (or any non-same-project mode) is not valid here; the
		// caller routes those through dispatchBuzzExternal instead.
		_, err := resolveTargetQuery(targets, types.TargetQuery{Mode: "external"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a same-project query")
	})
}

func TestTargetQueryToBuzzRoundTrip(t *testing.T) {
	// targetQueryToBuzz encodes the same field shape decodeTargetQuery reads back, so a
	// query survives the Buzz boundary intact. Assert the whole struct, not per-field.
	want := types.TargetQuery{Mode: types.QueryLiteral, Pattern: "build", Project: "sub/dir"}
	got, ok := decodeTargetQuery(targetQueryToBuzz(want))
	require.True(t, ok)
	require.Equal(t, want, got)
}

func TestDecodeTargetQuery(t *testing.T) {
	t.Run("valid map decodes every field", func(t *testing.T) {
		m := vm.NewMap()
		m.MapSet("mode", vm.StrValue(types.QueryGlob))
		m.MapSet("pattern", vm.StrValue("go-*"))
		m.MapSet("project", vm.StrValue("pkg/a"))
		got, ok := decodeTargetQuery(m)
		require.True(t, ok)
		require.Equal(t, types.TargetQuery{Mode: types.QueryGlob, Pattern: "go-*", Project: "pkg/a"}, got)
	})

	t.Run("non-map is rejected", func(t *testing.T) {
		// A bare string is the classic magus.needs footgun; decode must reject it so
		// magus.needs can demand a typed magus.target.* query.
		_, ok := decodeTargetQuery(vm.StrValue("build"))
		require.False(t, ok)
	})

	t.Run("map without a valid mode is rejected", func(t *testing.T) {
		m := vm.NewMap()
		m.MapSet("mode", vm.StrValue("bogus"))
		m.MapSet("pattern", vm.StrValue("go-*"))
		_, ok := decodeTargetQuery(m)
		require.False(t, ok)
	})

	t.Run("missing pattern and project default to empty", func(t *testing.T) {
		m := vm.NewMap()
		m.MapSet("mode", vm.StrValue(types.QueryLiteral))
		got, ok := decodeTargetQuery(m)
		require.True(t, ok)
		require.Equal(t, types.TargetQuery{Mode: types.QueryLiteral}, got)
	})
}

func TestBuildBuzzTargetHandle(t *testing.T) {
	t.Run("string literal builds the query shape", func(t *testing.T) {
		handle := buildBuzzTargetHandle(types.QueryGlob)
		v, err := handle(context.Background(), []vm.Value{vm.StrValue("go-*")})
		require.NoError(t, err)
		got, ok := decodeTargetQuery(v)
		require.True(t, ok)
		require.Equal(t, types.TargetQuery{Mode: types.QueryGlob, Pattern: "go-*"}, got)
	})

	t.Run("missing argument is an error", func(t *testing.T) {
		handle := buildBuzzTargetHandle(types.QueryLiteral)
		_, err := handle(context.Background(), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "string literal")
	})

	t.Run("non-string argument is an error", func(t *testing.T) {
		// The literal-first-arg discipline: a computed (non-str) argument would defeat
		// the static extractor, so it is rejected at the boundary.
		handle := buildBuzzTargetHandle(types.QueryRegex)
		_, err := handle(context.Background(), []vm.Value{vm.IntValue(7)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "magus.target.regex")
	})
}

// TestBuildTargetNS asserts the namespace wiring: every magus.target.* builtin is
// present as a DirectValue. Their value-returning behavior (expand_globs matching, the
// literal/glob/regex constructors) is proven end to end in TestTargetNamespaceEndToEnd
// and unit-tested directly via matchBuzzTargets / buildBuzzTargetHandle above.
func TestBuildTargetNS(t *testing.T) {
	ns := buildTargetNS(nil, noopTargets("go-build", "go-test", "lint"))
	for _, name := range []string{"expand_globs", "literal", "glob", "regex"} {
		requireDirect(t, ns, name)
	}
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

func TestBuildBuzzNeeds(t *testing.T) {
	t.Run("same-project glob resolves and dispatches", func(t *testing.T) {
		var runs atomic.Int32
		record := func(context.Context, []vm.Value) (vm.Value, error) {
			runs.Add(1)
			return vm.Null, nil
		}
		targets := map[string]vm.Callable{"go-build": record, "go-test": record}
		needs := buildBuzzNeeds(targets)

		glob := targetQueryToBuzz(types.TargetQuery{Mode: types.QueryGlob, Pattern: "go-*"})
		v, err := needs(context.Background(), []vm.Value{glob})
		require.NoError(t, err)
		require.Equal(t, vm.Null, v)
		assert.Equal(t, int32(2), runs.Load())
	})

	t.Run("a non-query argument is rejected", func(t *testing.T) {
		needs := buildBuzzNeeds(noopTargets("go-build"))
		_, err := needs(context.Background(), []vm.Value{vm.StrValue("go-build")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "magus.target.*")
	})

	t.Run("a resolve error surfaces under magus.needs", func(t *testing.T) {
		needs := buildBuzzNeeds(noopTargets("go-build"))
		bad := targetQueryToBuzz(types.TargetQuery{Mode: types.QueryRegex, Pattern: `([`})
		_, err := needs(context.Background(), []vm.Value{bad})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "magus.needs")
	})

	t.Run("an external query is dispatched separately and is a no-op without a coordinator", func(t *testing.T) {
		// dispatchBuzzExternal no-ops when there is no CrossDispatch/Source/Workspace
		// in ctx (the describe/parse path), so magus.needs of an external query
		// succeeds without touching the same-project target set.
		needs := buildBuzzNeeds(noopTargets("go-build"))
		ext := targetQueryToBuzz(types.TargetQuery{Mode: types.QueryLiteral, Pattern: "build", Project: "other"})
		v, err := needs(context.Background(), []vm.Value{ext})
		require.NoError(t, err)
		require.Equal(t, vm.Null, v)
	})
}

func TestDispatchBuzzExternalNoCoordinator(t *testing.T) {
	// Directly assert the graph-only fall-through: with an empty context there is no
	// coordinator, source, or workspace, so the external dispatch is a silent no-op.
	err := dispatchBuzzExternal(context.Background(), types.TargetQuery{Mode: types.QueryLiteral, Pattern: "build", Project: "other"})
	require.NoError(t, err)
}

// stubErr is a sentinel error used to prove dispatchBuzzDeps wraps a target failure
// with the target name.
type stubErr struct{}

func (stubErr) Error() string { return "boom" }

// TestTargetNamespaceEndToEnd drives magus.target.* and magus.needs from a real
// magusfile so the DirectValue closures the namespace map holds actually run through
// the interpreter (the one path that returns their list/query values). The `all`
// target expands a glob to its dependency targets and needs them; each dependency
// writes a marker file, proving the same-project query resolved, deduped, and
// dispatched via the Buzz pool. It also asserts expand_globs' returned list is usable
// from Buzz.
func TestTargetNamespaceEndToEnd(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "fs";

export fun go_build(args: [str]) > void { fs.writeFile("built", "go"); }
export fun rust_build(args: [str]) > void { fs.writeFile("built-rust", "rust"); }

export fun all(args: [str]) > void {
    // expand_globs returns the matching target names as a Buzz list.
    final names = magus.target.expand_globs("*-build");
    if (names.len() != 2) { magus.fatal("expand_globs did not match both build targets"); }

    // magus.needs of a glob resolves + dispatches the same-project deps.
    magus.needs(magus.target.glob("*-build"));
    fs.writeFile("all", "done");
}`)

	srcs, err := interp.FindAll(dir)
	require.NoError(t, err)
	require.NoError(t, interp.Run(context.Background(), srcs[0], "all", nil, dir), "magus.target/needs end-to-end")

	// Every dependency ran (through the query -> resolve -> dispatch path) and the
	// caller finished.
	for _, marker := range []string{"built", "built-rust", "all"} {
		_, err := os.Stat(filepath.Join(dir, marker))
		require.NoErrorf(t, err, "marker %q not written; dependency did not run", marker)
	}
}
