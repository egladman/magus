package buzz

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/libs/diagnostics"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSession(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	require.NotNil(t, s, "NewSession returned nil")
	assert.NotNil(t, s.Targets(), "Targets() should return a non-nil map")
}

func TestSession_ExecSimpleAssignment(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	require.NoError(t, s.Exec(context.Background(), `var x: int = 42;`), "Exec")
	globals := s.Globals()
	v, ok := globals["x"]
	require.True(t, ok, "global 'x' not found after exec")
	require.True(t, v.IsInt(), "x.IsInt() = false, got Kind() = %q", v.Kind())
	assert.Equal(t, int64(42), v.AsInt(), "x.AsInt()")
}

func TestSession_EvalExpression(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	// Use a function that returns a value to test Eval's return path.
	require.NoError(t, s.Exec(context.Background(), `fun sum() > int { return 1 + 2; }`), "Exec")
	v, err := s.Eval(context.Background(), `return sum()`)
	require.NoError(t, err, "Eval(return sum())")
	require.True(t, v.IsInt(), "Eval(return sum()) = %v, want 3", v)
	assert.Equal(t, int64(3), v.AsInt(), "Eval(return sum()) = %v, want 3", v)
}

func TestSession_NativeModule(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	mod := vm.NewMap()
	mod.MapSet("answer", vm.IntValue(42))
	// Host registers the module under an import path; it resolves with no file
	// on disk and no include dirs configured.
	s.SetNativeModule("example/demo", mod)

	require.NoError(t, s.Exec(context.Background(), `
import "example/demo";
var x = demo\answer;
`), "Exec")
	v, ok := s.Globals()["x"]
	require.True(t, ok, "global 'x' not bound; native import did not resolve")
	require.True(t, v.IsInt(), "x = %v, want 42", v)
	assert.Equal(t, int64(42), v.AsInt(), "x = %v, want 42", v)
}

// TestExecUnusedImportStillRuns is the whole point of warning rather than erroring on
// an unused import (BZZ3001): Exec must still run the program and return no error.
func TestExecUnusedImportStillRuns(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	s.SetNativeModule("unused/mod", vm.NewMap())

	err := s.Exec(context.Background(), `import "unused/mod";
var x = 1;`)
	require.NoError(t, err, "Exec must not fail on an unused import")
	v, ok := s.Globals()["x"]
	require.True(t, ok, "global 'x' not bound; the program did not actually run")
	assert.Equal(t, int64(1), v.AsInt())
}

// TestCompileUnusedImportStillCompiles is Compile's sibling of the Exec test above.
func TestCompileUnusedImportStillCompiles(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	s.SetNativeModule("unused/mod", vm.NewMap())

	_, err := s.Compile(`import "unused/mod";`)
	require.NoError(t, err, "Compile must not fail on an unused import")
}

func TestSession_ModuleResolver(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	mod := vm.NewMap()
	mod.MapSet("answer", vm.IntValue(7))
	// The resolver gets first refusal on a path-style import that is neither
	// bound nor a native module; it binds the returned module under the
	// path's basename.
	var gotPath string
	s.SetModuleResolver(func(importPath string) (vm.Value, bool) {
		gotPath = importPath
		if importPath == "spells/widget" {
			return mod, true
		}
		return vm.Null, false
	})

	require.NoError(t, s.Exec(context.Background(), `
import "spells/widget";
var x = widget.answer;
`), "Exec")
	assert.Equal(t, "spells/widget", gotPath, "resolver called with %q", gotPath)
	v, ok := s.Globals()["x"]
	require.True(t, ok, "global 'x' not bound; resolver import did not resolve")
	require.True(t, v.IsInt(), "x = %v, want 7", v)
	assert.Equal(t, int64(7), v.AsInt(), "x = %v, want 7", v)
}

// The file search still picks the path; the reader only supplies its bytes, so a host can
// evaluate a revision's copy of a file that also exists on disk.
func TestSession_SourceReader(t *testing.T) {
	dir := t.TempDir()
	onDisk := filepath.Join(dir, "widget.buzz")
	require.NoError(t, os.WriteFile(onDisk, []byte(`export final answer = 1;`), 0o644))

	s := NewSession(context.Background(), WithEmbedded(), WithSearchPaths(filepath.Join(dir, "?.buzz")))
	var read []string
	s.SetSourceReader(func(path string) ([]byte, error) {
		read = append(read, path)
		return []byte(`export final answer = 2;`), nil
	})

	require.NoError(t, s.Exec(context.Background(), `
import "widget";
var x = answer;
`))
	abs, err := filepath.Abs(onDisk)
	require.NoError(t, err)
	assert.Equal(t, []string{abs}, absAll(t, read), "the reader is asked for the path the search found")
	v, ok := s.Globals()["x"]
	require.True(t, ok)
	assert.Equal(t, int64(2), v.AsInt(), "the bytes come from the reader, not the disk")
}

func absAll(t *testing.T, paths []string) []string {
	t.Helper()
	out := make([]string, len(paths))
	for i, p := range paths {
		abs, err := filepath.Abs(p)
		require.NoError(t, err)
		out[i] = abs
	}
	return out
}

// A host declares a module's types lazily, from its resolver, into the session it
// holds. An aliased import runs in a sub-session that copied the root's types
// before that call, and must still see them, however deep the alias sits.
func TestSession_HostTypesReachAnAliasedImport(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	for name, src := range map[string]string{
		"good":  `import "host"; export fun f(s: host\Site) > str { return s.file; }`,
		"bad":   `import "host"; fun f(s: host\Site) > str { return s.file; } export final n = f("x");`,
		"outer": `import "bad" as bad; export final n = 1;`,
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+".buzz"), []byte(src), 0o644))
	}
	exec := func(src string) error {
		s := NewSession(ctx, WithEmbedded(), WithSearchPaths(filepath.Join(dir, "?.buzz")))
		declared := false
		s.SetModuleResolver(func(importPath string) (vm.Value, bool) {
			if importPath != "host" {
				return vm.Null, false
			}
			if !declared {
				declared = true
				s.DeclareModuleTypes("host", `export object Site { file: str = "" }`)
			}
			return vm.NewMap(), true
		})
		return s.Exec(ctx, src)
	}

	require.NoError(t, exec(`import "good" as good;`))
	for _, src := range []string{`import "bad" as bad;`, `import "outer" as outer;`} {
		err := exec(src)
		require.Error(t, err, src)
		assert.Contains(t, err.Error(), `cannot pass str as argument "s" of type Site`, src)
	}
}

func TestSession_Compile_And_ExecChunk(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	chunk, err := s.Compile(`var y: str = "hello";`)
	require.NoError(t, err, "Compile")
	require.NoError(t, s.ExecChunk(context.Background(), chunk), "ExecChunk")
	v, ok := s.Globals()["y"]
	require.True(t, ok, "global 'y' not set after ExecChunk")
	assert.Equal(t, "hello", v.AsString(), "y")
}

// importModuleSrc is a flat-importable module: an exported function that reads a
// non-exported (captured) module var, plus a non-exported helper function. Under
// exports-only import visibility (M4) only `pub` crosses the import boundary; the
// module's own code still reads `secret` live at runtime.
const importModuleSrc = `
var secret = 42;
export fun pub() > int { return secret; }
fun privHelper() > int { return 7; }
`

func newImporter(t *testing.T) *Session {
	t.Helper()
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	s.SetPromoteTopLevel(true) // magusfile execution mode
	s.SetModuleDecls("mymod", importModuleSrc)
	return s
}

// TestImportVisibility_ExportedCrosses verifies an exported function is callable
// through a flat import and still reads its module's non-exported state live —
// the runtime Env is untouched; only the importer's checker view is narrowed.
func TestImportVisibility_ExportedCrosses(t *testing.T) {
	s := newImporter(t)
	v, err := s.Eval(context.Background(), `import "mymod"; return pub();`)
	require.NoError(t, err, "calling exported pub() across import failed")
	require.True(t, v.IsInt(), "pub() = %v, want 42 (exported fn must read its module's live secret)", v)
	assert.Equal(t, int64(42), v.AsInt(), "pub() = %v, want 42 (exported fn must read its module's live secret)", v)
}

// TestImportVisibility_NonExportedVarHidden verifies a module's non-exported var
// is invisible to the importer, and that the error names `export` as the fix.
func TestImportVisibility_NonExportedVarHidden(t *testing.T) {
	s := newImporter(t)
	_, err := s.Eval(context.Background(), `import "mymod"; return secret;`)
	require.Error(t, err, "referencing a module's non-exported var should fail under exports-only imports")
	assert.Contains(t, err.Error(), "export", "error should point at the missing export")
}

// TestImportVisibility_NonExportedFuncHidden verifies a module's non-exported
// function is likewise not callable through the import.
func TestImportVisibility_NonExportedFuncHidden(t *testing.T) {
	s := newImporter(t)
	_, err := s.Eval(context.Background(), `import "mymod"; return privHelper();`)
	require.Error(t, err, "calling a module's non-exported function should fail under exports-only imports")
	assert.Contains(t, err.Error(), "export", "error should point at the missing export")
}

// TestImportVisibility_ImporterMayShadow verifies the boundary hides only the
// imported binding: the importer can still declare its own same-named top-level
// var without colliding with the module's hidden one.
func TestImportVisibility_ImporterMayShadow(t *testing.T) {
	s := newImporter(t)
	v, err := s.Eval(context.Background(), `import "mymod"; var secret = 99; return secret;`)
	require.NoError(t, err, "importer declaring its own 'secret' should be fine")
	require.True(t, v.IsInt(), "importer's own secret = %v, want 99", v)
	assert.Equal(t, int64(99), v.AsInt(), "importer's own secret = %v, want 99", v)
}

// magusfileSrc mirrors the shape of a real magusfile: a top-level config var read
// by an exported target (so it is captured and must stay an Env binding), plus a
// chunk-private scratch loop (promotable to a slot under PromoteTopLevel).
const magusfileSrc = `
var config = 42;
export fun getConfig() > int { return config; }
var scratch = 0;
var i = 0;
while (i < 100) { scratch = scratch + i; i = i + 1; }
`

// TestPromoteSession_MagusfileShape exercises the M2 wiring: a session with
// SetPromoteTopLevel(true) (the magusfile execution path) must run a magusfile
// unchanged — the captured config stays live for its target, and the promoted
// scratch var simply drops out of the global namespace.
func TestPromoteSession_MagusfileShape(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	s.SetPromoteTopLevel(true)
	require.NoError(t, s.Exec(ctx, magusfileSrc), "Exec")

	// The exported target still resolves the captured top-level config (live Env).
	exports := s.Exports()
	getConfig, ok := exports["getConfig"]
	require.True(t, ok, "exported target getConfig missing")
	v, err := s.CallValue(ctx, getConfig, nil)
	require.NoError(t, err, "CallValue(getConfig)")
	require.True(t, v.IsInt(), "getConfig() = %v, want 42", v)
	assert.Equal(t, int64(42), v.AsInt(), "getConfig() = %v, want 42", v)

	// config is captured by getConfig, so it stays an Env binding (visible).
	_, ok = s.Globals()["config"]
	assert.True(t, ok, "captured top-level 'config' should remain a visible Env global")
	// scratch is chunk-private and promoted to a slot, so it is no longer a global.
	_, ok = s.Globals()["scratch"]
	assert.False(t, ok, "chunk-private 'scratch' should be slot-promoted out of the global namespace")
}

// TestPromoteSession_DefaultOffKeepsGlobals confirms the REPL/default path is
// unchanged: without SetPromoteTopLevel every top-level var stays an Env global,
// so a later Exec (a subsequent prompt line) can still see it.
func TestPromoteSession_DefaultOffKeepsGlobals(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	require.NoError(t, s.Exec(ctx, magusfileSrc), "Exec")
	_, ok := s.Globals()["scratch"]
	assert.True(t, ok, "without promotion, 'scratch' must remain a visible Env global for later chunks")
	// A later chunk referencing the earlier scratch var compiles and runs.
	assert.NoError(t, s.Exec(ctx, `scratch = scratch + 1;`), "later chunk referencing earlier top-level var failed")
}

// TestFlatImportBindsNamespaceObject verifies that a flat `import "<mod>"`
// binds both the splatted unqualified exports AND a namespace object, so an
// importer can reach an export either way: `foo()` or `mod\foo()`. Upstream
// Buzz only accepts the qualified form, so this is what lets the same source
// run on both runtimes.
func TestFlatImportBindsNamespaceObject(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	s.SetModuleDecls("greet", `
namespace greet;
export fun hello(name: str) > str { return "hi " + name; }
`)
	require.NoError(t, s.Exec(ctx, `import "greet";`), "import")

	// Unqualified (splat) still works.
	v, err := s.Eval(ctx, `return hello("a")`)
	require.NoError(t, err, "unqualified call")
	assert.Equal(t, "hi a", v.String(), "unqualified hello")

	// Qualified (namespace object) resolves the same export.
	v, err = s.Eval(ctx, `return greet\hello("b")`)
	require.NoError(t, err, "qualified call")
	assert.Equal(t, "hi b", v.String(), "qualified greet\\hello")
}

// TestImportBindsDeclaredMultiSegmentNamespace covers the upstream-conformance
// fix: a module's exports are reachable under its full declared `namespace a\b`
// path, not only the import-path basename. The import path (modx) deliberately
// differs from the namespace (alpha\beta) so the declared-namespace binding is
// what resolves the access, not the basename object.
func TestImportBindsDeclaredMultiSegmentNamespace(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	s.SetModuleDecls("modx", `
namespace alpha\beta;
export fun hello(name: str) > str { return "hi " + name; }
`)
	require.NoError(t, s.Exec(ctx, `import "modx";`), "import")

	v, err := s.Eval(ctx, `return alpha\beta\hello("z")`)
	require.NoError(t, err, "declared-namespace call alpha\\beta\\hello")
	assert.Equal(t, "hi z", v.String(), "alpha\\beta\\hello")
}

// TestImportSiblingNamespacesSharePrefix verifies two modules whose declared
// namespaces share a leading segment (shared\one, shared\two) both resolve —
// the second must merge into the `shared` object the first created, not clobber
// it. Matches upstream, where distinct full namespaces coexist under a prefix.
func TestImportSiblingNamespacesSharePrefix(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	s.SetModuleDecls("sib1", `
namespace shared\one;
export final a = "A";
`)
	s.SetModuleDecls("sib2", `
namespace shared\two;
export final b = "B";
`)
	require.NoError(t, s.Exec(ctx, `import "sib1"; import "sib2";`), "import")

	v, err := s.Eval(ctx, `return shared\one\a`)
	require.NoError(t, err, "shared\\one\\a")
	assert.Equal(t, "A", v.String(), "shared\\one\\a")
	v, err = s.Eval(ctx, `return shared\two\b`)
	require.NoError(t, err, "shared\\two\\b (merged into existing `shared`)")
	assert.Equal(t, "B", v.String(), "shared\\two\\b")
}

// TestDuplicateNamespaceErrors verifies gopherbuzz now rejects two imports that
// declare the same namespace, matching upstream's "namespace already exists"
// (E92). Before the fix, gopherbuzz silently accepted the second and failed
// later with a confusing "undefined".
func TestDuplicateNamespaceErrors(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	s.SetModuleDecls("d1", `
namespace dup;
export final x = "1";
`)
	s.SetModuleDecls("d2", `
namespace dup;
export final y = "2";
`)
	err := s.Exec(ctx, `import "d1"; import "d2";`)
	require.Error(t, err, "duplicate namespace must error")
	assert.Contains(t, err.Error(), "already exists", "duplicate-namespace diagnostic")
}

// TestPrivateGlobalsDoNotCollideAcrossModules guards the per-module mangling of
// private top-level names. Two namespaced modules each declare a private `var
// panel` and a private `var items`; in SharedGlobals mode every module's top-level
// vars land in one shared Env, so without namespace-qualified keys moda's `panel`
// and modb's `panel` would be the same slot. The real-world symptom (bubblegum-wm): the
// status bar sets its `panel`, then an overlay module's `if (panel != null) return`
// sees it, skips building its `items`/`labels` list, and indexing it crashes.
func TestPrivateGlobalsDoNotCollideAcrossModules(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	s.SetModuleDecls("moda", `
namespace moda;
var panel: int? = null;
export fun setIt() > void { panel = 7; }
`)
	s.SetModuleDecls("modb", `
namespace modb;
var panel: int? = null;
var items = mut [<int>];
export fun build() > void {
    if (panel != null) { return; } // moda set ITS panel — must not be seen here
    foreach (i in 0..3) { items.append(i); }
}
export fun count() > int { return items.len(); }
`)
	require.NoError(t, s.Exec(ctx, `import "moda"; import "modb";`), "import")
	_, err := s.Eval(ctx, `return moda\setIt()`)
	require.NoError(t, err, "moda\\setIt")
	_, err = s.Eval(ctx, `return modb\build()`)
	require.NoError(t, err, "modb\\build")
	v, err := s.Eval(ctx, `return modb\count()`)
	require.NoError(t, err, "modb\\count")
	assert.Equal(t, "3", v.String(), "modb items count (moda's private panel leaked into modb)")
}

// TestPrivateFuncsDoNotCollideAcrossModules is the function-name facet of the same
// bug: two modules each declare a private `fun tag()` returning their own name. In
// the shared Env both would bind the key "tag", so whichever loaded last wins and a
// caller in the other module would invoke the wrong body. (In bubblegum-wm this is the
// real `fun labelAt` shared verbatim by cheatsheet and inspector.) Each module must
// call its OWN private function.
func TestPrivateFuncsDoNotCollideAcrossModules(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	// The exported entry points have distinct names (whoOne/whoTwo) so the test
	// isolates the PRIVATE `tag` collision; a shared export name would instead trip
	// a separate namespace-object issue unrelated to this fix.
	s.SetModuleDecls("mone", `
namespace mone;
fun tag() > str { return "one"; }
export fun whoOne() > str { return tag(); }
`)
	s.SetModuleDecls("mtwo", `
namespace mtwo;
fun tag() > str { return "two"; }
export fun whoTwo() > str { return tag(); }
`)
	require.NoError(t, s.Exec(ctx, `import "mone"; import "mtwo";`), "import")
	v, err := s.Eval(ctx, `return mone\whoOne()`)
	require.NoError(t, err, "mone\\whoOne")
	assert.Equal(t, "one", v.String(), "mone\\whoOne() (mtwo's private tag() shadowed mone's)")
	v, err = s.Eval(ctx, `return mtwo\whoTwo()`)
	require.NoError(t, err, "mtwo\\whoTwo")
	assert.Equal(t, "two", v.String(), "mtwo\\whoTwo()")
}

// TestSharedExportNameAcrossModules guards the namespace-object builder against
// the case where two modules export the SAME identifier. The builder used to
// diff the session-wide export set against a pre-import snapshot to find a
// module's exports; once `who` was in that set, the second module's `who` looked
// already-present and was dropped from its namespace object. The fix builds each
// namespace object from the chunk's own export list, so each module's `who`
// resolves to its own body.
func TestSharedExportNameAcrossModules(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	s.SetModuleDecls("alpha", `
namespace alpha;
export fun who() > str { return "alpha"; }
`)
	s.SetModuleDecls("beta", `
namespace beta;
export fun who() > str { return "beta"; }
`)
	require.NoError(t, s.Exec(ctx, `import "alpha"; import "beta";`), "import")
	v, err := s.Eval(ctx, `return alpha\who()`)
	require.NoError(t, err, "alpha\\who")
	assert.Equal(t, "alpha", v.String(), "alpha\\who() (beta's export dropped alpha's from its namespace?)")
	v, err = s.Eval(ctx, `return beta\who()`)
	require.NoError(t, err, "beta\\who")
	assert.Equal(t, "beta", v.String(), "beta\\who() (beta's shared export was dropped from its namespace)")
}

// TestNativeModuleEnumIsAValueNotJustAType covers the first of two breaks that made
// an inferred enum case reach a host method empty.
//
// A native module registers a Go map for the runtime and a declaration source for the
// checker, and resolveImport deliberately does not EXECUTE that source: executing
// would redefine the functions the native value already provides. An enum has no
// native counterpart to collide with, and skipping it left the compiler's lowering of
// `.case` (a real member lookup, ns\Enum.case) with nothing to find.
//
// Upstream Buzz never hits this: its stdlib is Buzz SOURCE that gets executed, so its
// enums are ordinary values. This is the behaviour being restored.
func TestNativeModuleEnumIsAValueNotJustAType(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	defer s.Close()

	mod := vm.NewMap()
	mod.MapSet("pick", vm.DirectValue("pick", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		return args[0], nil
	}))
	s.SetNativeModule("demo/sign", mod)
	s.SetModuleDecls("demo/sign", `
export enum<str> SignAlgorithm {
    Ed25519 = "ed25519",
}

export extern fun pick(alg: SignAlgorithm) > SignAlgorithm;
`)

	require.NoError(t, s.Exec(ctx, `
import "demo/sign";
final qualified = sign\SignAlgorithm.Ed25519;
final inferred = sign\pick(.Ed25519);
`), "Exec")

	qualified, ok := s.Globals()["qualified"]
	require.True(t, ok)
	require.False(t, qualified.IsNull(), "a declared enum must be reachable as a value, not null")

	inferred, ok := s.Globals()["inferred"]
	require.True(t, ok)
	backing, isEnum := inferred.EnumValue()
	require.True(t, isEnum, "an inferred case must arrive as an enum value")
	assert.Equal(t, "ed25519", backing.AsString())
}

// TestNativeModuleEnumRejectsAnUnknownCase: the point of declaring the enum is that a
// case it does not have fails at CHECK time, not as an empty string at the host.
func TestNativeModuleEnumRejectsAnUnknownCase(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	defer s.Close()

	mod := vm.NewMap()
	mod.MapSet("pick", vm.DirectValue("pick", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		return args[0], nil
	}))
	s.SetNativeModule("demo/sign", mod)
	s.SetModuleDecls("demo/sign", `
export enum<str> SignAlgorithm {
    Ed25519 = "ed25519",
}

export extern fun pick(alg: SignAlgorithm) > SignAlgorithm;
`)

	err := s.Exec(ctx, `
import "demo/sign";
final bad = sign\pick(.Sha256);
`)
	require.Error(t, err, "an unknown case must not reach the host")
	assert.Contains(t, err.Error(), `has no case "Sha256"`)
}

// busyLoopModuleSrc's top-level body loops long enough that, if the caller's
// ctx deadline never reaches it, Exec keeps running well past the bound below.
const busyLoopModuleSrc = `
var i = 0;
while (i < 50000000) { i = i + 1; }
`

// TestExecCtxBoundsImportExecution is the regression for imports resolving AND
// EXECUTING under the session's own stored ctx instead of the caller's per-call
// Exec ctx (session.go: compileShared -> checkShared -> loadFileImports ->
// resolveImport -> execImport). NewSession is given context.Background() (never
// expires); Exec is given a ctx with a short deadline. Before the fix,
// resolveImport's moduleDecls branch ran the imported module's busy loop under
// s.ctx (never expires), so the deadline on Exec's own ctx had no effect on the
// import and the call ran the loop to completion instead of aborting. Run via
// `select` + `time.After` per the repo's hang-test idiom (pool_test.go
// TestDispatchRejectsCycleWithMemo) so a regression fails rather than wedging
// the suite.
func TestExecCtxBoundsImportExecution(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	s.SetModuleDecls("busymod", busyLoopModuleSrc)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- s.Exec(ctx, `import "busymod";`)
	}()

	select {
	case err := <-done:
		require.Error(t, err, "want the import's execution to abort on the caller's ctx deadline")
	case <-time.After(2 * time.Second):
		t.Fatal("Exec did not return within bound: the import's ctx deadline was not honored")
	}
}

// Close releases every heap slot a session's values occupied, so a process that
// opens sessions in a loop (the daemon, one per invocation) holds a bounded
// number of heap objects. Before sessions owned their slots, each session's
// stdlib bindings, closures and literals stayed in the table for the life of
// the process.
func TestSessionCloseReleasesItsHeapObjects(t *testing.T) {
	const src = `
var xs = [1, 2, 3];
var m = {"a": xs, "b": {"c": true}};
fun count() > int { return xs.len(); }
object Point { x: int, y: int }
var p = Point{ x = 1, y = 2 };
`
	run := func() {
		s := NewSession(context.Background(), WithEmbedded())
		require.NoError(t, s.Exec(context.Background(), src))
		require.NoError(t, s.Close())
	}
	run()
	base := vm.ReadHeapStats().Objects
	const sessions = 50
	for range sessions {
		run()
	}
	if got := vm.ReadHeapStats().Objects; got > base {
		t.Fatalf("%d sessions grew the live heap by %d objects; a closed session must leave none", sessions, got-base)
	}
}

// TestSession_Warnings_VisibleAfterExec reproduces the gap this fix closes: BZZ3001
// was computed by compileShared and then thrown away (a comment there says so
// explicitly), so nothing on the normal Exec path could ever see it; only the
// separate Diagnostics call could, and that one re-executes every import, which is
// unsafe to call after a real run. Warnings() must expose the SAME warning
// compileShared already computed for this Exec, without re-resolving anything.
func TestSession_Warnings_VisibleAfterExec(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	s.SetNativeModule("unused/mod", vm.NewMap())

	err := s.Exec(context.Background(), `import "unused/mod";`)
	require.NoError(t, err, "a warning must never fail Exec")

	got := s.Warnings()
	require.Len(t, got, 1, "the unused import should surface as exactly one warning")
	assert.Equal(t, SeverityWarning, got[0].Severity)
	assert.Equal(t, diagnostics.Code("BZZ3001"), got[0].Code)
	assert.Contains(t, got[0].Msg, "unused/mod")
}

// TestSession_Warnings_LastCompileNotAccumulated verifies a second Exec call
// replaces, rather than appends to, the warning set. A Session is reused across many
// compiles (a session pool, NewChild sub-sessions, the REPL evaluating one line at a
// time), so accumulating would grow the slice unbounded over a long-lived session's
// life.
func TestSession_Warnings_LastCompileNotAccumulated(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	s.SetNativeModule("unused/mod", vm.NewMap())

	require.NoError(t, s.Exec(context.Background(), `import "unused/mod";`))
	require.Len(t, s.Warnings(), 1)

	require.NoError(t, s.Exec(context.Background(), `var x = 1;`))
	assert.Empty(t, s.Warnings(), "a clean second compile must clear the prior warning, not append to it")
}

// TestSession_Warnings_NilBeforeAnyCompile pins the zero-cost/zero-value contract: a
// session that never compiles anything pays nothing beyond the nil slice.
func TestSession_Warnings_NilBeforeAnyCompile(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	assert.Nil(t, s.Warnings())
}

// TestSession_Warnings_ReplSuppressed confirms a REPL session (WithREPL) still
// reports no warnings through the Exec path either, matching Diagnostics' existing
// REPL suppression (checkShared gates both on s.repl).
func TestSession_Warnings_ReplSuppressed(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded(), WithREPL())
	s.SetNativeModule("unused/mod", vm.NewMap())

	require.NoError(t, s.Exec(context.Background(), `import "unused/mod";`))
	assert.Empty(t, s.Warnings(), "a REPL session must not warn on an import unused so far")
}

func TestWithoutFileImports(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded(), WithoutFileImports())
	defer func() { _ = sess.Close() }()
	err := sess.Exec(ctx, `import "./helper.buzz";`)
	require.ErrorIs(t, err, UnresolvedImport)
	assert.ErrorContains(t, err, "file imports are unavailable")
}

func TestWithoutFFI(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded(), WithoutFFI())
	defer func() { _ = sess.Close() }()
	_, err := sess.CallValue(ctx, sess.GetGlobal("zdef"), nil)
	require.ErrorIs(t, err, FFIDisabled)
	assert.ErrorContains(t, err, "run the script through the Buzz CLI")
}

// The filter sees the entry alone: a module keeps what the entry's kept code
// calls into.
func TestEntryFilterSkipsImportedModules(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib.buzz"), []byte("fun helper() > int { return 2; }\nexport fun two() > int { return helper(); }\n"), 0o644))
	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded(), WithSearchPaths(filepath.Join(dir, "?.buzz")))
	defer func() { _ = sess.Close() }()
	var seen int
	sess.SetEntryFilter("test", func(prog *ast.Program, _ ImportLookup) {
		seen++
		kept := prog.Stmts[:0]
		for _, stmt := range prog.Stmts {
			if _, isFun := stmt.(*ast.FunDecl); !isFun {
				kept = append(kept, stmt)
			}
		}
		prog.Stmts = kept
	})
	require.NoError(t, sess.Exec(ctx, "import \"lib\";\nfun dropped() > void {}\nvar n = two();\n"))
	assert.Equal(t, 1, seen)
	assert.True(t, sess.GetGlobal("dropped").IsNull())
	assert.Equal(t, int64(2), sess.GetGlobal("n").AsInt())
}

func TestRejectImportMatchesEitherSpelling(t *testing.T) {
	rejected := errors.New("not offered here")
	for _, tc := range []struct{ rejected, imported string }{
		{"os", `import "os";`},
		{"os", `import "buzz:os";`},
		{"buzz:os", `import "os";`},
		{"buzz:os", `import "buzz:os";`},
	} {
		t.Run(tc.rejected+" "+tc.imported, func(t *testing.T) {
			ctx := context.Background()
			sess := NewSession(ctx, WithEmbedded())
			defer func() { _ = sess.Close() }()
			sess.RejectImport(tc.rejected, rejected)
			require.ErrorIs(t, sess.Exec(ctx, tc.imported), rejected)
		})
	}
}

// A check that redeclares an imported type in a nested scope rewrites that type's
// methods. The session's later checks share the imported types, so the rewrite
// must stay inside the check that made it.
func TestSession_NestedRedeclarationStaysInItsCheck(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded())
	sess.SetModuleDecls("shapes", "export object P { n: int = 0 }\n")
	require.NoError(t, sess.Exec(ctx, "import \"shapes\";\nfun f() > int {\n    object P { fun extra() > int { return 1; } }\n    return 0;\n}\n"))
	err := sess.Exec(ctx, "import \"shapes\";\nfun g(p: P) > int { return p.extra(); }\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "extra")
}

// hostShapedDecls is declaration source shaped like magus's generated bundles:
// objects whose fields name one another, then one extern per host method.
func hostShapedDecls(prefix string, objects, funcs int) string {
	var b strings.Builder
	for i := range objects {
		fmt.Fprintf(&b, "export object %s%d {\n    n: int = 0,\n    name: str = \"\",\n    tags: [str] = [],\n    next: [%s%d] = [],\n    prev: %s%d? = null,\n}\n\n",
			prefix, i, prefix, (i+1)%objects, prefix, (i+objects-1)%objects)
	}
	for i := range funcs {
		fmt.Fprintf(&b, "export extern fun %s%d(v: str, n: int) > %s%d !> any;\n", strings.ToLower(prefix), i, prefix, i%objects)
	}
	return b.String()
}

// BenchmarkSessionImportedTypes loads one session the way magus loads a
// magusfile: a typed host global, a large flat-imported types module, then a run
// of small files, each checked against both.
func BenchmarkSessionImportedTypes(b *testing.B) {
	typesSrc := hostShapedDecls("T", 120, 0)
	hostSrc := hostShapedDecls("H", 20, 80)
	files := make([]string, 12)
	for i := range files {
		files[i] = fmt.Sprintf("import \"types\";\n"+
			"fun f%d(x: T%d) > int { return x.n; }\n"+
			"fun useHost%d() > H%d !> any { return host\\h%d(\"x\", n: 1); }\n"+
			"export fun g%d() > int { return f%d(T%d{}); }\n", i, i, i, i, i, i, i, i)
	}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		s := NewSession(ctx, WithEmbedded())
		s.SetGlobal("host", vm.NewMap())
		s.DeclareModuleTypes("host", hostSrc)
		s.SetModuleDecls("types", typesSrc)
		for _, f := range files {
			if err := s.Exec(ctx, f); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// skipWithoutHeapAttribution skips a test that reads vm's heap attribution, which only
// the nanbox representation records: under buzz_safe or buzz_unsafe the readers are
// stubs that report nothing.
func skipWithoutHeapAttribution(t *testing.T) {
	t.Helper()
	if jitTagged {
		t.Skip("heap attribution is recorded only by the nanbox Value representation")
	}
}

// The attribution has to name the loop that is growing the heap. Reporting a
// large heap without saying where leaves the reader bisecting a magusfile by
// hand, which is the whole reason this exists.
//
// The program allocates LINEARLY: one slot per append. An earlier version used
// the pathological `kept = kept + line` shape instead, which drove this one test
// binary to a 13.5GB peak and killed a CI shard. That is an absurd price for a
// test about a diagnostic, and the diagnostic only needs a hot loop that grows.
//
// The assertion is that the loop is SURFACED, not that it ranks first. Sampling
// catches whichever instruction a tick landed on, so the exact ordering and the
// exact line vary by machine speed: an earlier version demanded the top slot and
// passed on macOS while failing on a Linux runner, which is a test asserting a
// probabilistic property as if it were deterministic.
//
// Either line 4 (the foreach) or line 5 (the append) is a correct answer; both put
// the reader on the same two lines, which is the whole job.
func TestHeapHotSitesNamesTheGrowingLine(t *testing.T) {
	skipWithoutHeapAttribution(t)
	const src = `
fun grow() > int {
    var parts = mut [<str>];
    foreach (i in 0..20000) {
        parts.append("a line of coverage profile text");
    }
    return parts.len();
}
grow();
`
	// Both tests here read the same process-global attribution table, and both
	// programs put their loop on the same line number, so without a reset each
	// could be satisfied by the other's samples.
	vm.ResetHeapStats()

	s := NewSession(context.Background())
	_, err := s.Eval(context.Background(), src)
	require.NoError(t, err)

	sites, _ := vm.HeapHotSites(10)
	require.NotEmpty(t, sites, "a loop of 20000 allocating iterations must have been sampled")

	var found *vm.HeapSite
	for i, site := range sites {
		if strings.HasSuffix(site.Site, ":4") || strings.HasSuffix(site.Site, ":5") {
			found = &sites[i]
			break
		}
	}
	require.NotNilf(t, found,
		"the growing loop (line 4 or 5) must appear among the hot sites, got %v", sites)
	assert.Positive(t, found.Objects, "the loop's entry must carry a growth figure")
}

// Every reported site must name a line a reader can actually open. A chunk with no
// line data used to surface as "<main>:?" and, on a runner, outranked the real
// loop; a ranking led by an entry nobody can act on is worse than a shorter one.
func TestHeapHotSitesNamesOnlyRealLines(t *testing.T) {
	skipWithoutHeapAttribution(t)
	vm.ResetHeapStats()

	s := NewSession(context.Background())
	_, err := s.Eval(context.Background(), `
fun churn() > int {
    var xs = mut [<str>];
    foreach (i in 0..20000) { xs.append("x"); }
    return xs.len();
}
churn();
`)
	require.NoError(t, err)

	sites, _ := vm.HeapHotSites(20)
	for _, site := range sites {
		assert.NotContainsf(t, site.Site, ":?",
			"site %q has no line number and cannot be acted on", site.Site)
	}
}

// Answers "what accumulates" for the shape that actually hurts: many independent
// programs in one process, which is what this package's own suite does and why it
// peaks at 13.5GB. Diagnostic, not an assertion. Run with -v.
func TestHeapProfileAcrossManyPrograms(t *testing.T) {
	skipWithoutHeapAttribution(t)
	before := vm.ReadHeapStats().Objects
	for i := range 200 {
		s := NewSession(context.Background())
		src := fmt.Sprintf(`
fun work() > str {
    var parts = mut [<str>];
    foreach (j in 0..50) { parts.append("row %d " + "{j}"); }
    final _m = {"a": parts.len(), "b": %d};
    return parts.join(",");
}
work();
`, i, i)
		if _, err := s.Eval(context.Background(), src); err != nil {
			t.Fatalf("program %d: %v", i, err)
		}
	}
	after := vm.ReadHeapStats().Objects

	p := vm.HeapProfile()
	type row struct {
		kind string
		n    int
	}
	rows := make([]row, 0, len(p))
	total := 0
	for k, n := range p {
		rows = append(rows, row{k, n})
		total += n
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].n > rows[j].n })
	t.Logf("200 programs added %d heap slots (%d -> %d)", after-before, before, after)
	t.Logf("heap slots by kind (%d total):", total)
	for _, r := range rows {
		t.Logf("  %-12s %8d  %5.1f%%", r.kind, r.n, 100*float64(r.n)/float64(total))
	}
}

// Hermetic coverage for the upstream-parity language features. TestUpstreamConformance
// is the broader gate, but it needs a pinned foreign checkout and is deliberately
// opt-in (see magusfile.buzz: the `conformance` target is not part of `ci`), so on
// its own it protects none of this in CI. These cases run in strict (upstream-parity)
// mode with no host wiring, so a regression in any feature below fails `go test`.

// evalParity execs src in a strict session and returns the result of calling
// probe(), the zero-argument function every case declares. Strict mode is the
// point: these features must parse under upstream's rules, not the lenient
// embedded ones. It calls probe through the session rather than eval-ing a
// `return`, which strict mode rightly rejects at the top level.
func evalParity(t *testing.T, src string) vm.Value {
	t.Helper()
	ctx := context.Background()
	s := NewSession(ctx)
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, s.Exec(ctx, src), "Exec")
	probe, ok := s.Globals()["probe"]
	require.True(t, ok, "source must declare a zero-argument probe()")
	v, err := s.CallValue(ctx, probe, nil)
	require.NoError(t, err, "probe()")
	return v
}

func TestParity_ForLoopMultipleClauses(t *testing.T) {
	v := evalParity(t, `
fun probe() > int {
    var sum = 0;
    for (i: int = 0, j: int = 9; i < 10 and j >= 0; i = i + 1, j = j - 1) {
        sum = sum + i + j;
    }
    return sum;
}`)
	assert.Equal(t, int64(90), v.AsInt(), "each clause list runs every iteration")
}

func TestParity_NullableDeclarationDefaultsToNull(t *testing.T) {
	v := evalParity(t, `
fun probe() > bool {
    final hello: int?;
    return hello == null;
}`)
	assert.True(t, v.AsBool(), "a nullable declaration may omit its initializer")
}

func TestParity_ObjectLiteralFieldPunning(t *testing.T) {
	v := evalParity(t, `
object Person {
    name: str,
    age: int,
    sex: bool,
}

fun probe() > bool {
    final name = "joe";
    final age = 24;
    final person = Person{
        name,
        age,
        sex = true,
    };
    return person.name == "joe" and person.age == 24 and person.sex;
}`)
	assert.True(t, v.AsBool(), "a bare field name puns the same-named variable")
}

func TestParity_AnonymousObjectLiteralFieldPunning(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    final data = "hello";
    final info = .{ data };
    return info.data;
}`)
	assert.Equal(t, "hello", v.AsString(), "punning works in a .{} literal too")
}

func TestParity_VoidArrowBodyAndUnannotatedReturn(t *testing.T) {
	// The arrow body is an expression statement, not a return, so `> void` does
	// not reject its own sugar; and `fun ()` must accept a `fun () > void`.
	v := evalParity(t, `
fun upvals() > fun () {
    final upvalue = 12;
    return fun () > void => upvalue + 1;
}

fun probe() > bool {
    upvals()();
    return true;
}`)
	assert.True(t, v.AsBool(), "a void arrow body evaluates for effect")
}

func TestParity_EnumStrBackingType(t *testing.T) {
	v := evalParity(t, `
enum<str> StrEnum {
    one,
    two,
}

fun probe() > str {
    return "{StrEnum.one.value}/{StrEnum.two.value}";
}`)
	assert.Equal(t, "one/two", v.AsString(), "a str-backed case takes its own name as its value")
}

func TestParity_EnumIntExplicitCaseValues(t *testing.T) {
	v := evalParity(t, `
enum<int> IntEnum {
    one = 1,
    two = 2,
    three = 3,
}

fun probe() > int {
    return IntEnum.one.value + IntEnum.two.value + IntEnum.three.value;
}`)
	assert.Equal(t, int64(6), v.AsInt(), "an explicit case value wins over the ordinal")
}

func TestParity_PlainEnumKeepsOrdinalValues(t *testing.T) {
	v := evalParity(t, `
enum NaturalEnum {
    zero,
    one,
    two,
}

fun probe() > int {
    return NaturalEnum.zero.value + NaturalEnum.one.value + NaturalEnum.two.value;
}`)
	assert.Equal(t, int64(3), v.AsInt(), "an unbacked enum still numbers its cases from zero")
}

func TestParity_OptionalChaining(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want any
	}{
		{"member on non-null", `final o: [int]? = [1, 2, 3]; return o?.len();`, int64(3)},
		{"method call on null", `final o: [int]? = null; return o?.len();`, nil},
		{"subscript on non-null", `final o: [int]? = [7, 8]; return o?[1];`, int64(8)},
		{"subscript on null", `final o: [int]? = null; return o?[1];`, nil},
		{"chained hops short-circuit", `final o: {str: [int]}? = null; return o?["k"]?.len();`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, "fun probe() > any {\n"+tc.src+"\n}")
			if tc.want == nil {
				assert.True(t, v.IsNull(), "a null receiver yields null, it does not error")
				return
			}
			assert.Equal(t, tc.want, v.AsInt(), "a non-null receiver passes straight through")
		})
	}
}

func TestParity_DefaultArguments(t *testing.T) {
	const decls = `
fun hey(name: str = "Joe", age: int = 12, father: str?, fourth: int = 1) > str
    => "Hello {name} you're {age} {father} {fourth}";
`
	cases := []struct {
		name string
		call string
		want string
	}{
		{"one positional", `hey("John")`, "Hello John you're 12 null 1"},
		{"labeled middle", `hey(age: 25)`, "Hello Joe you're 25 null 1"},
		{"nullable parameter defaults to null", `hey(father: "Doe")`, "Hello Joe you're 12 Doe 1"},
		{"labeled last", `hey(fourth: 42)`, "Hello Joe you're 12 null 42"},
		{"labels out of order", `hey(fourth: 12, age: 44)`, "Hello Joe you're 44 null 12"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, decls+"\nfun probe() > str { return "+tc.call+"; }")
			assert.Equal(t, tc.want, v.AsString(), "omitted slots take their declared defaults")
		})
	}
}

func TestParity_MissingArgumentWithoutDefaultStillErrors(t *testing.T) {
	// Defaults must not turn arity checking off: a parameter with no default and
	// no nullable type is still required.
	s := NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `
fun need(a: int = 1, b: str) > str => "{a}{b}";
fun probe() > str { return need(); }`)
	require.Error(t, err, "a required parameter cannot be omitted")
	assert.Contains(t, err.Error(), `missing argument "b"`)
}

func TestParity_FunctionTypeErrorSetAndYieldSuffix(t *testing.T) {
	// `!>` and `*>` may follow a function TYPE in parameter position. They are
	// consumed but kept out of the annotation text, so the parameter's type is
	// still `fun(int)int` and an ordinary function is assignable to it.
	v := evalParity(t, `
fun twice(value: int) > int {
    return value * 2;
}

fun callDynamicWithCatch(fn: fun (value: int) > int !>
    str, value: int) > int {
    return fn(value) catch 99;
}

fun probe() > int {
    return callDynamicWithCatch(twice, value: 21);
}`)
	assert.Equal(t, int64(42), v.AsInt(), "the call runs; 99 would mean it threw instead")
}

func TestParity_MultipleTypedCatchClauses(t *testing.T) {
	const decls = `
object SomeError {}

fun willFail(kind: str) > void !> str {
    if (kind == "str") {
        throw "boom";
    }
    throw SomeError{};
}
`
	cases := []struct {
		name string
		kind string
		want string
	}{
		{"first clause matches", "str", "str"},
		{"later clause matches", "obj", "obj"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, decls+`
fun probe() > str {
    try {
        willFail("`+tc.kind+`");
    } catch (_: str) {
        return "str";
    } catch (_: SomeError) {
        return "obj";
    }
    return "none";
}`)
			assert.Equal(t, tc.want, v.AsString(), "the first clause whose type matches runs")
		})
	}
}

func TestParity_CatchAnyMatchesEverything(t *testing.T) {
	v := evalParity(t, `
fun willFail() > void !> str {
    throw "Yolo";
}

fun probe() > bool {
    var caught = false;
    try {
        willFail();
    } catch (error: any) {
        caught = error is str;
    }
    return caught;
}`)
	assert.True(t, v.AsBool(), "`any` is inhabited by every value, so it catches everything")
}

func TestParity_UnmatchedCatchRethrows(t *testing.T) {
	// A try whose clauses do not claim the error must not swallow it: the error
	// has to reach the enclosing handler.
	v := evalParity(t, `
fun willFail() > void !> str {
    throw "boom";
}

fun inner() > str !> str {
    try {
        willFail();
    } catch (_: int) {
        return "wrong";
    }
    return "fell through";
}

fun probe() > str {
    return inner() catch "rethrown";
}`)
	assert.Equal(t, "rethrown", v.AsString(), "an unclaimed error propagates outward")
}

func TestParity_LabeledLoops(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int64
	}{
		{
			name: "labeled break",
			body: `
    var i = 0;
    while (i < 100) :here {
        i = i + 1;
        if (i == 10) {
            break here;
        }
    }
    return i;`,
			want: 10,
		},
		{
			// The foreach's iterator state has to come off the stack on the way
			// out, or every later stack offset is wrong.
			name: "labeled break out of a nested loop",
			body: `
    var i = 0;
    foreach (_ in 0..100) :here {
        while (i < 100) {
            i = i + 1;
            if (i == 10) {
                break here;
            }
        }
    }
    return i;`,
			want: 10,
		},
		{
			name: "labeled break out of a deeply nested loop",
			body: `
    var i = 0;
    foreach (j in 0..100) :here {
        while (j < 100) {
            while (i < 100) {
                i = i + 1;
                if (i == 10) {
                    break here;
                }
            }
        }
    }
    return i;`,
			want: 10,
		},
		{
			name: "labeled continue",
			body: `
    var i = 0;
    foreach (j in 0..10) :here {
        if (j == 3) {
            continue here;
        }
        i = i + j;
    }
    return i;`,
			want: 42,
		},
		{
			// The strongest witness that the iterator state is discarded: the
			// OUTER foreach's next step peeks the top of the stack, so an inner
			// state left behind is the one it would advance.
			name: "labeled break leaves the inner iterator state balanced",
			body: `
    var acc = 0;
    foreach (_ in 0..3) {
        foreach (j in 0..3) :inner {
            if (j == 1) {
                break inner;
            }
            acc = acc + 1;
        }
    }
    return acc;`,
			want: 3,
		},
		{
			name: "unlabeled break still targets the innermost loop",
			body: `
    var i = 0;
    foreach (_ in 0..10) :here {
        while (true) {
            i = i + 1;
            break;
        }
    }
    return i;`,
			want: 10,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, "fun probe() > int {"+tc.body+"\n}")
			assert.Equal(t, tc.want, v.AsInt(), "the label selects which loop is left")
		})
	}
}

func TestParity_BreakWithUnknownLabelIsAnError(t *testing.T) {
	s := NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `
fun probe() > int {
    while (true) :here {
        break elsewhere;
    }
    return 0;
}`)
	require.Error(t, err, "a label naming no enclosing loop cannot compile")
	assert.Contains(t, err.Error(), "elsewhere")
}

func TestParity_BlockExpressionWithoutOutIsRejected(t *testing.T) {
	// This case used to live in TestParity_BlockExpression asserting the opposite:
	// that `from { final unused = 1; }` evaluates to null. Upstream rejects it
	// (tests/compile_errors/block-expression-partial-out.buzz, "All block expression
	// paths must end with `out`"), and a block expression that silently produces null
	// is the dangerous kind of divergence: the value is consumed somewhere. Nothing
	// in this repo or in magus used the form, so the rule was adopted.
	ctx := context.Background()
	s := NewSession(ctx)
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(ctx, `fun probe() > any { return from { final unused = 1; }; }`)
	require.Error(t, err, "a block expression with no out must be rejected")
	assert.Contains(t, err.Error(), "must end with `out`")
}

func TestParity_BlockExpression(t *testing.T) {
	cases := []struct {
		name string
		body string
		want any
	}{
		{name: "straight out", body: `return from { out "my value"; };`, want: "my value"},
		{
			name: "out from either branch",
			body: `
    final flag = false;
    return from {
        if (flag) {
            out "then";
        } else {
            out "else";
        }
    };`,
			want: "else",
		},
		{
			// The early out must skip the fallback, not fall through to it.
			name: "early out beats the fallback out",
			body: `
    final flag = true;
    return from {
        if (flag) {
            out "early";
        }
        out "fallback";
    };`,
			want: "early",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.body
			if body == "" {
				body = tc.name
			}
			v := evalParity(t, "fun probe() > any {\n"+body+"\n}")
			if tc.want == nil {
				assert.True(t, v.IsNull(), "a block that never outs is null")
				return
			}
			assert.Equal(t, tc.want, v.AsString(), "the block's value is the out that ran")
		})
	}
}

func TestParity_NestedBlockExpressionOutBindsInnermost(t *testing.T) {
	// Each `out` leaves the block it sits in, so the inner block's value feeds
	// the outer one rather than escaping past it.
	v := evalParity(t, `
fun probe() > str {
    return from {
        final inner = from {
            out "in";
        };
        out "{inner}/out";
    };
}`)
	assert.Equal(t, "in/out", v.AsString(), "out targets the innermost enclosing from block")
}

func TestParity_FreeIdentifiers(t *testing.T) {
	v := evalParity(t, `
object A {
    @"type": str,
}

fun probe() > str {
    final @"non-standard-identifier" = "hello";
    final a = A{
        @"type" = "world",
    };
    return "{@"non-standard-identifier"} {a.@"type"}";
}`)
	assert.Equal(t, "hello world", v.AsString(), "@\"...\" names a binding, a field, and a member")
}

func TestParity_FreeIdentifierMayBeAReservedWord(t *testing.T) {
	// The quotes are the whole point: `type` is reserved, `@"type"` is not.
	v := evalParity(t, `
fun probe() > int {
    final @"type" = 7;
    return @"type";
}`)
	assert.Equal(t, int64(7), v.AsInt(), "the reserved-word rule does not reach a raw identifier")
}

func TestParity_BareReservedWordIsStillRejected(t *testing.T) {
	s := NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `fun probe() > int { final type = 7; return type; }`)
	require.Error(t, err, "only the quoted form escapes the reserved-word rule")
	assert.Contains(t, err.Error(), "reserved word")
}

func TestParity_GenericObjectDeclaration(t *testing.T) {
	// Type parameters are erased, so the object's identity stays its bare name:
	// `payload is Payload::<str, int>` has to test against `Payload`.
	v := evalParity(t, `
object Payload::<K, V> {
    data: mut {K: V},
}

fun probe() > bool {
    final payload = Payload::<str, int>{
        data = mut { "one": 1 },
    };
    payload.data["two"] = 2;
    return payload is Payload::<str, int> and payload.data["two"] == 2;
}`)
	assert.True(t, v.AsBool(), "a generic object declares, instantiates, and type-tests")
}

func TestParity_InlineIfExpression(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "constant condition", body: `return if (true) "then" else "else";`, want: "then"},
		{name: "computed condition", body: `return if ("hello".len() == 2) "then" else "else";`, want: "else"},
		{
			name: "else-if chain",
			body: `
    final value = 12;
    return if (value == 14)
        "hello"
    else if (value == 12)
        "yolo"
    else
        "fallback";`,
			want: "yolo",
		},
		{
			name: "nested in a larger expression",
			body: `
    final value = 12;
    return (if (value == 14) "hello" else "yolo") + "!";`,
			want: "yolo!",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, "fun probe() > str {\n"+tc.body+"\n}")
			assert.Equal(t, tc.want, v.AsString(), "only the selected branch's value survives")
		})
	}
}

func TestParity_InlineIfRequiresBothBranches(t *testing.T) {
	// An expression has to produce a value on every path, so unlike the statement
	// form the else is mandatory.
	s := NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `fun probe() > int { return if (true) 1; }`)
	require.Error(t, err, "an inline if without else cannot compile")
}

func TestParity_InlineCatchVoid(t *testing.T) {
	v := evalParity(t, `
fun willFailVoid() > void !> str {
    throw "i'm failing";
}

fun willFail() > int !> str {
    throw "i'm failing";
}

fun probe() > int {
    willFailVoid() catch void;
    return willFail() catch 7;
}`)
	assert.Equal(t, int64(7), v.AsInt(), "catch void swallows the error and yields nothing")
}

func TestParity_InferredEnumCaseFromExpectedType(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int64
	}{
		{
			name: "call argument",
			src: `
enum Suit { hearts, spades }
fun pick(s: Suit) > int => s.value;
fun probe() > int { return pick(.spades); }`,
			want: 1,
		},
		{
			name: "parameter default",
			src: `
enum Suit { hearts, spades }
fun pick(s: Suit = .spades) > int => s.value;
fun probe() > int { return pick(); }`,
			want: 1,
		},
		{
			// The enum is declared AFTER the object, so the method's signature is
			// built before the enum exists and has to be refreshed before the
			// default can resolve against it.
			name: "method default with a forward-declared enum",
			src: `
object Defaults {
    static fun pick(s: Suit = .spades) > int => s.value;
}
enum Suit { hearts, spades }
fun probe() > int { return Defaults.pick(); }`,
			want: 1,
		},
		{
			name: "anonymous object literal field",
			src: `
enum Suit { hearts, spades }
object Setting { suit: Suit }
fun probe() > int {
    final s: Setting = .{ suit = .spades };
    return s.suit.value;
}`,
			want: 1,
		},
		{
			name: "nested in an annotated map literal",
			src: `
enum Suit { hearts, spades }
object Setting { suit: Suit }
fun probe() > int {
    final m: {str: Setting} = { "a": .{ suit = .spades } };
    return m["a"]?.suit.value ?? -1;
}`,
			want: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, tc.src)
			assert.Equal(t, tc.want, v.AsInt(), "the expected type says which enum a bare .case names")
		})
	}
}

func TestParity_AnnotatedMapLiteralIsStillAMap(t *testing.T) {
	// The anonymous-object path must not swallow a real map literal: `.{}` and
	// `{}` are different forms and only the first fills an object's fields.
	v := evalParity(t, `
fun probe() > int {
    final m: {str: int} = { "a": 1, "b": 2 };
    return m["a"] ?? 0;
}`)
	assert.Equal(t, int64(1), v.AsInt(), "a brace literal stays a map")
}

func TestParity_EnumFromValue(t *testing.T) {
	v := evalParity(t, `
enum Suit { hearts, spades }

fun probe() > str {
    final found = Suit(1);
    final missing = Suit(9);
    return "{found?.name}/{missing == null}";
}`)
	assert.Equal(t, "spades/true", v.AsString(), "calling an enum looks a case up by value, or yields null")
}

func TestParity_EnumFromValueUsesBackingValuesNotOrdinals(t *testing.T) {
	v := evalParity(t, `
enum<str> Suit { hearts, spades }

fun probe() > str {
    return "{Suit("spades")?.name}/{Suit(1) == null}";
}`)
	assert.Equal(t, "spades/true", v.AsString(), "the lookup is by the case's value, not its position")
}

func TestParity_RangeBindsTighterThanComparison(t *testing.T) {
	// `range == 0..10` must parse as `range == (0..10)`. At a looser precedence
	// it becomes `(range == 0)..10`, which ranges a bool.
	v := evalParity(t, `
fun probe() > bool {
    final limit = 10;
    final r = 0..limit;
    return r == 0..10;
}`)
	assert.True(t, v.AsBool(), "..  binds tighter than ==")
}

func TestParity_RangeEqualityIsStructural(t *testing.T) {
	v := evalParity(t, `
fun probe() > bool {
    return 0..10 == 0..10 and !(0..10 == 10..0);
}`)
	assert.True(t, v.AsBool(), "two ranges are equal when both operands match")
}

func TestParity_RangeMethods(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want int64
	}{
		// low/high report the operands as written, not the smaller and larger.
		{"low", `(0..10).low()`, 0},
		{"high", `(0..10).high()`, 10},
		{"inverted low", `(10..0).low()`, 10},
		{"inverted high", `(10..0).high()`, 0},
		{"len", `(0..10).len()`, 10},
		{"inverted len", `(10..0).len()`, 10},
		{"toList length", `(0..10).toList().len()`, 10},
		{"inverted toList length", `(10..0).toList().len()`, 10},
		{"toList is ascending", `(0..10).toList()[0]`, 0},
		{"inverted toList descends", `(10..0).toList()[0]`, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, "fun probe() > int { return "+tc.expr+"; }")
			assert.Equal(t, tc.want, v.AsInt(), "a range runs from low toward high and stops before it")
		})
	}
}

func TestParity_RangeInvertAndContains(t *testing.T) {
	v := evalParity(t, `
fun probe() > bool {
    return (0..10).invert() == 10..0
        and (0..10).contains(0)
        and (0..10).contains(9)
        and !(0..10).contains(10)
        and !(0..10).contains(-1)
        and (10..0).contains(10)
        and !(10..0).contains(0);
}`)
	assert.True(t, v.AsBool(), "contains matches exactly what foreach would yield")
}

func TestParity_RangeMethodsMatchForeach(t *testing.T) {
	// len/toList/contains all have to agree with iteration, in both directions.
	v := evalParity(t, `
fun probe() > str {
    var up = 0;
    foreach (n in 0..10) {
        up = up + n;
    }
    var down = 0;
    foreach (n in 10..0) {
        down = down + n;
    }
    return "{up}/{down}";
}`)
	assert.Equal(t, "45/55", v.AsString(), "0..10 yields 0-9 and 10..0 yields 10-1")
}

func TestParity_VoidArrowBodyAcceptsAnAssignment(t *testing.T) {
	// A `> void` arrow body is a statement position. An assignment is not an
	// expression in Buzz, so this only parses because the void path reads a
	// statement rather than an expression.
	v := evalParity(t, `
fun probe() > int {
    var sum = 0;
    final add = fun (n: int) > void => sum = sum + n;
    add(5);
    return sum;
}`)
	// 5: closures capture by REFERENCE, as upstream does, so the assignment reaches
	// the enclosing sum. This pinned 0 while capture was by value.
	assert.Equal(t, int64(5), v.AsInt(), "the body parses and runs, and the write reaches the enclosing local")
}

// TestParity_MatchCompoundTypeCondition pins that a match arm whose condition is a
// COMPOUND type value actually matches. A type value carries the canonical spelling
// ("[str]") while the runtime test compares the reduced shape ("list"), so passing
// the canonical name through made every such arm silently dead: it fell to `else`
// where the equivalent `is` answered true. Upstream's match.buzz exercises only
// simple arms, which reduce to themselves, so it cannot catch this.
func TestParity_MatchCompoundTypeCondition(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    final xs = [ "a", "b" ];
    final byList = match (xs) { <[str]> -> "L", else -> "none" };
    final byMap = match ({ "k": 1 }) { <{str: int}> -> "M", else -> "none" };
    final mismatch = match (xs) { <{str: int}> -> "M", else -> "none" };
    return "{byList}{byMap}{mismatch}";
}`)
	assert.Equal(t, "LMnone", v.String(), "compound type arms match, and a wrong shape still falls to else")
}

func TestParity_ClosureUpvaluesAreCapturedByReference(t *testing.T) {
	// Matches upstream: a captured local is one shared cell, not a per-closure copy,
	// so a closure assigning to it updates the variable itself. This asserted 0 while
	// gopherbuzz captured by value.
	v := evalParity(t, `
fun probe() > int {
    var sum = 0;
    final add = fun (n: int) > void { sum = sum + n; };
    add(5);
    return sum;
}`)
	assert.Equal(t, int64(5), v.AsInt(), "a closure mutating an enclosing local updates that local")
}

func TestParity_AnonymousObjectLiteralBecomesTheExpectedObject(t *testing.T) {
	// Resolved to a named object, `.{ ... }` gains that type's methods and the
	// defaults of the fields it does not mention. As a plain map it would answer
	// field reads but have neither.
	v := evalParity(t, `
object Payload {
    data: str,
    tag: str = "default",

    fun describe() > str => "{this.data}/{this.tag}";
}

fun take(p: Payload) > str => p.describe();

fun probe() > str {
    return take(.{ data = "hello" });
}`)
	assert.Equal(t, "hello/default", v.AsString(), "the literal is built as the object, not as a map")
}

func TestParity_AnonymousObjectLiteralStaysAMapWithoutAnExpectedObject(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    final info = .{ name = "joe" };
    return info.name;
}`)
	assert.Equal(t, "joe", v.AsString(), "with no object to fill it remains a map")
}

func TestParity_CallWithoutParenthesesAroundAnObjectLiteral(t *testing.T) {
	v := evalParity(t, `
object Payload {
    data: str,

    fun len() => this.data.len();

    fun join(other: Payload) => "{this.data}:{other.data}";
}

fun callMe(payload: Payload) => payload.len();

fun probe() > str {
    final len = callMe .{
        data = "hello",
    };
    final payload = Payload{ data = "hello" };
    final joined = payload.join .{
        data = "world",
    };
    return "{len}/{joined}";
}`)
	assert.Equal(t, "5/hello:world", v.AsString(), "a lone object-literal argument may drop its parentheses")
}

func TestParity_DotStillMeansMemberAccess(t *testing.T) {
	// The paren-free call only triggers on `.` followed by `{`; ordinary member
	// access must be untouched.
	v := evalParity(t, `
object P { data: str }

fun probe() > str {
    final p = P{ data = "ok" };
    return p.data;
}`)
	assert.Equal(t, "ok", v.AsString(), "a dot before an identifier is still member access")
}

// TestParity_ExternFunDeclaresASignature covers `extern fun name(...) > T;`, the
// body-less declaration upstream uses to type its native stdlib. It emits no code by
// design: the implementation is whatever the host already bound to that name, so the
// declaration must not shadow it with an empty closure.
func TestParity_ExternFunDeclaresASignature(t *testing.T) {
	s := NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, s.Exec(context.Background(), `
extern fun native(n: int) > str;
export extern fun exported(value: any) > void;

fun probe() > int { return 1; }`), "a body-less extern declaration parses")

	_, bound := s.Globals()["native"]
	assert.False(t, bound, "an extern declaration must not bind a value: the host owns the implementation")
}

// TestParity_ExternReturnTypeIsChecked is the reason the declaration exists. The
// signature has to reach call sites, or it is decoration: a host call's result
// must type as its declared return, not as Unknown.
func TestParity_ExternReturnTypeIsChecked(t *testing.T) {
	s := NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `
extern fun native(n: int) > str;
fun probe() > int { return native(1); }`)
	require.Error(t, err, "the declared return type must reach the call site")
	assert.Contains(t, err.Error(), "return type mismatch")
}

// TestParity_ExternRejectsABody pins the shape: `extern` means the
// implementation is elsewhere, so a body is a contradiction rather than an
// extra. Catching it at the parser keeps the compiler's "extern emits nothing"
// rule from silently discarding real code.
func TestParity_ExternRejectsABody(t *testing.T) {
	s := NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `
extern fun native(n: int) > str { return "x"; }
fun probe() > int { return 1; }`)
	require.Error(t, err, "an extern declaration must not carry a body")
}

// TestParity_ExternStaysAnOrdinaryIdentifier guards the contextual lookahead:
// `extern` introduces a declaration only when `fun` follows it. Upstream reserves
// the word from BINDING positions (so an object field named extern is rightly
// refused) while leaving the non-binding ones open, and the new lookahead must
// not narrow that further.
func TestParity_ExternStaysAnOrdinaryIdentifier(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    final m = {"extern": "key"};
    return m["extern"] ?? "missing";
}`)
	assert.Equal(t, "key", v.AsString(), "`extern` is only a keyword directly before `fun`")
}

// TestParity_MapMerge covers `{...} + {...}`, the map counterpart of list
// concatenation (upstream tests/behavior/composite-assign.buzz). The checker
// rejected it outright before, so `m += {...}` could not compile.
func TestParity_MapMerge(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    final a = {"one": 1};
    final b = {"one": 9, "two": 2};
    final m = a + b;
    return "{m.size()}/{m["one"] ?? 0}/{a.size()}";
}`)
	// Right wins the duplicate key, and the left operand is untouched: `+` is an
	// expression, so it must copy rather than mutate in place.
	assert.Equal(t, "2/9/1", v.AsString(), "map merge takes the right operand's value and leaves the left alone")
}

// TestParity_FloatModulo covers `%` on doubles, which raised "not supported for
// float operands". Upstream's composite-assign.buzz asserts `4.0 %= 2.0` is 0,
// so this is fmod, not an integer-only operator.
func TestParity_FloatModulo(t *testing.T) {
	v := evalParity(t, `
fun probe() > double {
    var b = 4.0;
    b %= 2.0;
    return b + (5.5 % 2.0);
}`)
	assert.InDelta(t, 1.5, v.AsFloat(), 1e-9, "4.0 % 2.0 is 0.0 and 5.5 % 2.0 is 1.5")
}

// TestParity_ModuloByZeroReports guards the divisor check added alongside float
// modulo: math.Mod would return NaN, which would propagate silently instead of
// reporting where it went wrong.
func TestParity_ModuloByZeroReports(t *testing.T) {
	s := NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `fun probe() > double { return 5.5 % 0.0; }
final __r = probe();`)
	require.Error(t, err, "a zero divisor must report, not yield NaN")
	assert.Contains(t, err.Error(), "modulo by zero")
}

// TestParity_NullCoalescingBindsTighterThanTerm pins `??` at upstream's
// Precedence.NullCoalescing, which sits between Term (`+`/`-`) and Bitwise.
// gopherbuzz had it above `or`, looser than every binary operator, so the
// upstream idiom of coalescing a nullable where it is used raised at runtime:
// the `+` saw the null before `??` could replace it.
func TestParity_NullCoalescingBindsTighterThanTerm(t *testing.T) {
	v := evalParity(t, `
fun probe() > int {
    final n: int? = null;
    return 1 + n ?? 0;
}`)
	assert.Equal(t, int64(1), v.AsInt(), "`1 + n ?? 0` is `1 + (n ?? 0)`")
}

// TestParity_NullCoalescingStillLooserThanBitwise pins the other side of the
// level: moving `??` down must not push it past Bitwise.
func TestParity_NullCoalescingStillLooserThanBitwise(t *testing.T) {
	v := evalParity(t, `
fun probe() > int {
    final n: int? = null;
    return n ?? 1 | 2;
}`)
	assert.Equal(t, int64(3), v.AsInt(), "`n ?? 1 | 2` is `n ?? (1 | 2)`")
}

// TestParity_TypeValues covers `<T>` and `typeof`, upstream's type-as-value pair.
//
// The load-bearing property is that typeof is STATIC. `[]` and `[]` annotated
// `[str]` are the same empty list at runtime, so an implementation that probed
// the value could not tell them apart; upstream compares the type DEFS the
// compiler resolved, and so does this.
func TestParity_TypeValues(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    final list = [];
    final slist: [str] = [];
    final map = {};
    final smap: {str: int} = {};
    return "{typeof list}/{typeof slist}/{typeof map}/{typeof smap}/{typeof 1}";
}`)
	assert.Equal(t, "<[any]>/<[str]>/<{any: any}>/<{str: int}>/<int>", v.AsString(),
		"an unannotated empty collection is [any]/{any: any}; an annotated one keeps its declared types")
}

// TestParity_TypeValueEquality pins the comparison. Two type values are built
// independently (one from a literal, one from typeof), so equality has to be
// structural on the canonical spelling; reference equality would make every
// `typeof x == <T>` false.
func TestParity_TypeValueEquality(t *testing.T) {
	v := evalParity(t, `
fun probe() > bool {
    final slist: [str] = [];
    return typeof slist == <[str]> and <int> != <str> and <{str: int}> == <{str: int}>;
}`)
	assert.True(t, v.AsBool(), "type values compare by what they denote, not by identity")
}

// TestParity_TypeOfDoesNotEvaluateItsOperand guards the static-ness directly: a
// typeof whose operand has a side effect must not run it.
func TestParity_TypeOfDoesNotEvaluateItsOperand(t *testing.T) {
	v := evalParity(t, `
var calls = 0;

fun bump() > int {
    calls = calls + 1;
    return 1;
}

fun probe() > int {
    _ = typeof bump();
    return calls;
}`)
	assert.Equal(t, int64(0), v.AsInt(), "typeof reads a type, it does not call anything")
}

// TestParity_CollectionCloneAliases covers upstream's copyMutable/copyImmutable,
// which obj.zig declares as ALIASES of cloneMutable/cloneImmutable rather than
// as separate operations. Missing them made `list.copyImmutable()` a null call.
func TestParity_CollectionCloneAliases(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    final l = [1, 2, 3];
    final m = {"a": 1};
    return "{l.copyImmutable().len()}/{m.copyImmutable().size()}/{l.cloneMutable().len()}";
}`)
	assert.Equal(t, "3/1/3", v.AsString(), "the copy* names are the clone* operations under upstream's spelling")
}

// TestParity_MapHasKey covers map.hasKey, which upstream declares on the map
// object and gopherbuzz did not implement at all.
func TestParity_MapHasKey(t *testing.T) {
	v := evalParity(t, `
fun probe() > bool {
    final m = {"hello": "world"};
    return m.hasKey("hello") and !m.hasKey("absent");
}`)
	assert.True(t, v.AsBool(), "hasKey reports presence, not the value")
}

// TestParity_ListFillRange covers fill's start/len window. Filling the whole list
// regardless passes the simple case and silently corrupts the windowed one, which
// is why this asserts the UNTOUCHED neighbours rather than only the filled span.
func TestParity_ListFillRange(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    final all = (mut [1, 2, 3]).fill(42);
    final some = (mut [0, 1, 2, 3, 4, 5]).fill(42, start: 2, len: 3);
    return "{all[0]}{all[2]}/{some[1]}{some[2]}{some[4]}{some[5]}";
}`)
	assert.Equal(t, "4242/142425", v.AsString(), "fill without a window covers everything; with one it covers exactly [start, start+len)")
}

// TestParity_ListRemoveOutOfRangeIsNull pins remove's miss behaviour. Upstream
// documents "or null when out of bounds" and asserts `list.remove(12) == null`;
// raising instead made a miss unrecoverable.
func TestParity_ListRemoveOutOfRangeIsNull(t *testing.T) {
	v := evalParity(t, `
fun probe() > bool {
    final l = mut ["a", "b", "c"];
    return l.remove(12) == null and l.len() == 3 and l.remove(1) == "b";
}`)
	assert.True(t, v.AsBool(), "an out-of-range remove yields null and changes nothing")
}

// ── Features this branch added ───────────────────────────────────────────────
//
// Everything below is covered by the upstream behavior suite too, but that suite is
// opt-in (magusfile.buzz keeps `conformance` out of `ci`, since it needs a foreign
// checkout and the network). Without these, protocols, match, the boxed-upvalue
// capture, the typed host modules and the raw-string rules would ship with no gate
// that a plain `go test` runs -- which is how parseProtocolDecl reached 0% coverage.

func TestParity_ProtocolDeclarationAndConformance(t *testing.T) {
	v := evalParity(t, `
protocol Nameable {
    mut fun rename(name: str) > void;
}

protocol Sized {
    fun size() > int;
}

object<Nameable, Sized> Pet {
    name: str,

    mut fun rename(name: str) > void {
        this.name = name;
    }

    fun size() > int => 4;
}

fun probe() > str {
    final bandit = mut Pet{ name = "bandit" };
    // A protocol-typed binding accepts a declared conformer, and dispatch on it is
    // ordinary dynamic dispatch on the object actually there.
    final named: Nameable = bandit;
    named.rename("Chili");
    final sized: [Sized] = [ bandit ];
    return "{bandit.name}:{sized[0].size()}";
}`)
	assert.Equal(t, "Chili:4", v.AsString(), "a protocol declares signatures; the object supplies them")
}

func TestParity_ProtocolRejectsUndeclaredConformer(t *testing.T) {
	// Conformance is DECLARED, not structural: an object with a matching method set
	// that never named the protocol is not assignable to it.
	s := NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `
protocol Nameable {
    fun rename(name: str) > void;
}

object Impostor {
    fun rename(name: str) > void {}
}

fun probe() > bool {
    final x: Nameable = Impostor{};
    return true;
}`)
	require.Error(t, err, "structural match alone must not satisfy a protocol")
}

func TestParity_MatchStatementAndExpressionForms(t *testing.T) {
	v := evalParity(t, `
fun classify(n: int) > str {
    return match (n) {
        1, 2 -> "low",
        3 -> "three",
        else -> "high",
    };
}

fun probe() > str {
    // Statement form with block bodies; sibling arms get their own scopes.
    var tag = "unset";
    match (2) {
        1 -> {
            final local = "one";
            tag = local;
        },
        2 -> {
            final local = "two";
            tag = local;
        },
        else -> {
            tag = "other";
        },
    }
    return "{classify(1)}/{classify(2)}/{classify(3)}/{classify(9)}/{tag}";
}`)
	assert.Equal(t, "low/low/three/high/two", v.AsString(),
		"a multi-condition arm matches any of its conditions; a block body runs its statements")
}

func TestParity_MatchComparisonRules(t *testing.T) {
	// Upstream picks the comparison from the operand kinds rather than always using
	// ==: a range tests containment, a pattern against a string tests a regex match
	// (both directions), and a type value tests `is`.
	cases := []struct{ name, src, want string }{
		{"range contains an int", `match (7) { 0..5 -> "low", 5..10 -> "mid", else -> "high" }`, "mid"},
		{"range contains a double", `match (3.14) { 0..3 -> "low", 3..4 -> "mid", else -> "high" }`, "mid"},
		{"pattern condition against a string", `match ("hello joe") { $"hello [a-z]+" -> "rx", else -> "no" }`, "rx"},
		{"string condition against a pattern", `match ($"hello [a-z]+") { "hello joe" -> "rx", else -> "no" }`, "rx"},
		{"type condition tests is", `match ("s") { <int> -> "i", <str> -> "s", else -> "no" }`, "s"},
		{"type subject tests the condition", `match (<str>) { "hello" -> "inhabits", else -> "no" }`, "inhabits"},
		{"equality is the fallback", `match (true) { true -> "yes", false -> "no" }`, "yes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, "fun probe() > str { return "+tc.src+"; }")
			assert.Equal(t, tc.want, v.AsString(), "the rule is chosen by the operand kinds")
		})
	}
}

func TestParity_MatchInfersEnumCaseFromSubject(t *testing.T) {
	v := evalParity(t, `
enum Axis { up, down }

fun probe() > str {
    final a = Axis.down;
    // A bare case as a CONDITION resolves against the subject's type; a bare case as
    // a branch VALUE resolves against the match's expected type.
    final byCase = match (a) { .up -> "u", .down -> "d" };
    final asValue: Axis? = match (1) { 1 -> .up, else -> null };
    return "{byCase}{asValue == Axis.up}";
}`)
	assert.Equal(t, "dtrue", v.AsString(), "both positions resolve without naming the enum")
}

func TestParity_MatchWithoutElseIsRejected(t *testing.T) {
	// This test used to assert the opposite: that falling off the last arm yields
	// null. True of the VM, but not parity: upstream rejects the source outright
	// (compile_errors/match-non-exhaustive.buzz), so it never lets the null be
	// observed. The rejection is the guarantee worth pinning.
	//
	// Fall-through-yields-null is deliberately UNCHANGED in the VM, so a `.bo` blob
	// compiled before this check still runs.
	ctx := context.Background()
	s := NewSession(ctx)
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(ctx, `
fun probe() > bool {
    final r = match (99) { 1 -> "one", 2 -> "two" };
    return r == null;
}`)
	require.Error(t, err, "a non-enum, non-bool match with no else must be rejected")
	assert.Contains(t, err.Error(), "non-exhaustive match")
}

func TestParity_MatchExhaustiveWithoutElse(t *testing.T) {
	// The flip side: a subject with a finite case set needs no else. Upstream's
	// match.buzz asserts this for bool in so many words ("boolean match can be
	// exhaustive without else"), and the same reasoning covers a fully-named enum.
	v := evalParity(t, `
enum Side { left, right }

fun probe() > bool {
    final b = match (true) { true -> "y", false -> "n" };
    final e = match (Side.left) { Side.left -> "l", Side.right -> "r" };
    return b == "y" and e == "l";
}`)
	assert.True(t, v.AsBool(), "bool and fully-covered enum matches need no else")
}

func TestParity_MatchEvaluatesSubjectOnce(t *testing.T) {
	// The subject goes into a temp, so a side-effecting subject must not re-run per
	// arm -- otherwise an iterator or counter advances once per condition tested.
	v := evalParity(t, `
var calls = 0;

fun bump() > int {
    calls = calls + 1;
    return 3;
}

fun probe() > int {
    _ = match (bump()) { 1 -> "a", 2 -> "b", 3 -> "c", else -> "d" };
    return calls;
}`)
	assert.Equal(t, int64(1), v.AsInt(), "the subject is evaluated once, not once per arm")
}

func TestParity_ClosureCaptureIsShared(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int64
	}{
		{"writer visible to reader", `
    var n = 0;
    final set = fun () > void { n = 2; };
    final get = fun () > int => n;
    set();
    return get();`, 2},
		{"captured parameter keeps its argument then mutates", `
    return bumped(5);`, 9},
		{"transitive capture through a frame that never names it", `
    return outer();`, 7},
		{"capture inside a match arm", `
    var hit = 0;
    match (1) { 1 -> { final f = fun () > void { hit = 7; }; f(); }, else -> {} }
    return hit;`, 7},
		{"capture inside a catch body", `
    var seen = 0;
    try {
        throw "x";
    } catch (e: str) {
        final f = fun () > void { seen = 4; };
        f();
    }
    return seen;`, 4},
		{"foreach body captures the loop variable", `
    var last = 0;
    final fns = mut [];
    foreach (i in [1, 2, 3]) {
        _ = fns.append(fun () > void { last = i; });
    }
    fns[0]();
    return last;`, 1},
	}
	const decls = `
fun bumped(start: int) > int {
    final f = fun () > void { start = start + 4; };
    f();
    return start;
}

fun outer() > int {
    var target = 0;
    final middle = fun () > void {
        final inner = fun () > void { target = 7; };
        inner();
    };
    middle();
    return target;
}
`
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, decls+"\nfun probe() > int {"+tc.src+"\n}")
			assert.Equal(t, tc.want, v.AsInt(), "a captured local is one shared cell")
		})
	}
}

func TestParity_RawStringRules(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"interpolates an expression", "`sum={1 + 2}`", "sum=3"},
		{"escaped brace stays literal", "`\\{literal\\}`", "{literal}"},
		{"mustache survives as literal text", "`{{name}}`", "{{name}}"},
		{"nested raw string inside an interpolation", "`outer {`inner`} end`", "outer inner end"},
		{"newlines are literal", "`a\nb`", "a\nb"},
		{"regex classes pass through", "`[0-9]+`", "[0-9]+"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, "fun probe() > str { return "+tc.src+"; }")
			assert.Equal(t, tc.want, v.AsString(), "a raw string interpolates but unescapes only \\{ and \\}")
		})
	}
}

func TestParity_ZdefBlockIsNotInterpolated(t *testing.T) {
	// A zdef declaration block is a raw string full of Zig braces. Upstream reads it
	// from the raw token and never interpolates it; the two positional arguments are
	// also exempt from the strict argument-labeling rule.
	s := NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `
zdef(
    "tests/utils/libforeign",
    `+"`"+`
        const Data = extern struct {
            msg: [*:0]const u8,
            id: i32,
        };
    `+"`"+`
);

fun probe() > bool => true;`)
	// The library cannot be opened in a test environment; what matters is that the
	// block PARSED (braces intact, no labeling error) rather than how dlopen fared.
	if err != nil {
		assert.NotContains(t, err.Error(), "must be labeled", "zdef args are exempt from labeling")
		assert.NotContains(t, err.Error(), "interpolation", "a zdef block is never interpolated")
	}
}

func TestParity_TypedCollectionLiteralsResolveTheirElements(t *testing.T) {
	v := evalParity(t, `
enum Locale { fr, en, it }

fun probe() > bool {
    // The annotation is a hint, not an element: list[0] is the first VALUE after it.
    final list = [ <Locale>, .it, .fr ];
    final map = { <str: Locale>, "it": .it };
    return list[0] == Locale.it and list[1] == Locale.fr and map["it"] == Locale.it;
}`)
	assert.True(t, v.AsBool(), "an explicit element type resolves a bare enum case inside the literal")
}

func TestParity_AnnotatedDiscardAndAsBinding(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    // An annotated discard asserts a shape without binding.
    _: obj{ name: str } = .{ name = "joe" };
    // `+"`"+`as name: Type`+"`"+` binds the cast value only when the cast succeeds.
    final anything: any = "hello";
    var hit = "none";
    if (anything as s: str) {
        hit = s;
    }
    var missed = "none";
    if (anything as n: int) {
        missed = "int";
    }
    return "{hit}/{missed}";
}`)
	assert.Equal(t, "hello/none", v.AsString(), "the branch is taken only when the cast holds")
}

func TestParity_CheckedOptionalCastDoesNotCoerce(t *testing.T) {
	v := evalParity(t, `
fun probe() > bool {
    final n: any = 12;
    // as? is a type TEST, not a conversion: an int is not a str.
    return (n as? int) == 12 and (n as? str) == null;
}`)
	assert.True(t, v.AsBool(), "as? yields null on a type mismatch instead of stringifying")
}

func TestParity_StructuralAnonymousObjectTest(t *testing.T) {
	v := evalParity(t, `
fun probe() > bool {
    final x = .{ f = "a", g = 1 };
    return (x is obj{ f: str })
        and (x is obj{ f: str, g: int })
        and !(x is obj{ f: str, missing: int })
        // A function-typed member contains a bare `+"`"+`>`+"`"+` that must not be read as a
        // bracket closer, or the fields after it are dropped and this answers true.
        and !(x is obj{ f: fun () > str, missing: int });
}`)
	assert.True(t, v.AsBool(), "the structural test checks every named field")
}

func TestParity_TypeValuesAndTypeof(t *testing.T) {
	v := evalParity(t, `
fun probe() > bool {
    // typeof is STATIC: it reports the checker's inferred type, so an annotated
    // empty list and a bare one disagree even though both are empty at run time.
    final bare = [];
    final typed: [str] = [];
    return typeof 3 == <int>
        and typeof "s" == <str>
        and typeof bare == <[any]>
        and typeof typed == <[str]>
        and <int> != <str>;
}`)
	assert.True(t, v.AsBool(), "a type value compares by canonical spelling")
}

func TestParity_MapAndListFunctionalMethods(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    // sort reorders the RECEIVER in place and returns it.
    final l = mut [ 3, 1, 2 ];
    _ = l.sort(fun (a: int, b: int) => a < b);
    final m = mut { "b": 2, "a": 1 };
    _ = m.sort(fun (x: str, y: str) => x < y);
    // map builds a new map from {key, value} records.
    final inverted = { "a": 1 }.map(fun (k: str, v: int) => .{ key = "{v}", value = k });
    return "{l[0]}{l[1]}{l[2]}:{m.keys()[0]}:{inverted["1"]}";
}`)
	assert.Equal(t, "123:a:a", v.AsString(), "sort is in place; map rebuilds from records")
}

func TestParity_ImmutableCollectionsRejectSorting(t *testing.T) {
	// This used to assert that `sort` refused at RUNTIME. The checker now rejects an
	// in-place mutator on a receiver it can see is immutable, so the source no longer
	// compiles: an earlier failure for the same rule, and what upstream does.
	//
	// The VM's guard is unchanged and still load-bearing: it catches a receiver whose
	// mutability the checker cannot determine, so errImmutable is not dead code.
	ctx := context.Background()
	s := NewSession(ctx)
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(ctx, `
fun probe() > bool {
    final l = [ 2, 1 ];
    _ = l.sort(fun (a: int, b: int) => a < b);
    return true;
}`)
	require.Error(t, err, "sorting an immutable list must be rejected")
	assert.Contains(t, err.Error(), "requires a mutable list")
}
func TestParity_StoredMapKeyWinsOverBuiltinMethod(t *testing.T) {
	// An anonymous object literal is represented as a map, so a field whose name
	// collides with a map builtin must still read as the field.
	v := evalParity(t, `
fun probe() > bool {
    final rec = .{ map = "field", keys = "also a field" };
    return rec.map == "field" and rec.keys == "also a field";
}`)
	assert.True(t, v.AsBool(), "a stored key shadows the same-named builtin")
}

// ── Checker false positives, each measured against upstream ──────────────────
//
// Every case below is a program `~/Repos/buzz/zig-out/bin/buzz` compiles clean and
// gopherbuzz rejected. They are grouped because they share a failure MODE that the
// allowlists cannot catch: a strictness check that over-claims rejects correct
// source, and neither upstream suite contains the shape, so both stayed green while
// magus's own tree failed to load.

// TestParity_WriteThroughANameJustifiesItsVar covers the var-not-assigned check's
// blind spot. It fired only for an *ast.IdentExpr target, so `digests[p] = h` and
// `point.x = 2` both read as "never assigned" and the declaration was rejected,
// which is what stopped hack/drift.buzz from loading. Upstream accepts both; where
// it comments at all (W102 on an index-assigned `var`) it warns rather than errors.
func TestParity_WriteThroughANameJustifiesItsVar(t *testing.T) {
	cases := []struct{ name, body string }{
		{"index assignment", `
    var digests: mut {str: str} = mut {<str: str>};
    digests["k"] = "v";
    return digests["k"] ?? "";`},
		{"field assignment", `
    var p = mut Point{ x = 1 };
    p.x = 2;
    return "{p.x}";`},
		{"nested target marks the root", `
    var box = mut Box{ inner = mut Point{ x = 1 } };
    box.inner.x = 9;
    return "{box.inner.x}";`},
	}
	const decls = `
object Point { x: int }
object Box { inner: mut Point }
`
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, decls+"\nfun probe() > str {"+tc.body+"\n}")
			assert.NotEmpty(t, v.AsString(), "the program compiles and runs")
		})
	}
}

// TestParity_BreakOutOfADoUntilLeavesLiveCode pins the DoStmt terminal-flow guard.
// `do { ... } until (cond)` runs its body once, so a body that transfers control
// away does end the loop, but a break that exits the DO lands on the statement
// after it, exactly as for while and for. Without the guard everything following a
// `do { break; } until (...)` was reported unreachable.
func TestParity_BreakOutOfADoUntilLeavesLiveCode(t *testing.T) {
	v := evalParity(t, `
fun probe() > int {
    var i = 0;
    do {
        i = i + 1;
        break;
    } until (i > 10)
    // Dead by the old analysis; upstream compiles this clean and reaches it.
    i = i + 100;
    return i;
}`)
	assert.Equal(t, int64(101), v.AsInt(), "the break exits the do and the next statement runs")
}

// TestParity_ClosureMayReuseAnEnclosingFunctionsLocalName pins the shadowing rule's
// scope. It walked every enclosing local scope, so a nested closure could not reuse
// an outer name; upstream scopes the rule to the CURRENT function's locals, so a new
// frame starts the name space over.
func TestParity_ClosureMayReuseAnEnclosingFunctionsLocalName(t *testing.T) {
	v := evalParity(t, `
fun probe() > int {
    final name = 1;
    final f = fun () > int {
        // A different frame, so this is a fresh name rather than a shadow.
        final name = 2;
        return name;
    };
    return name + f();
}`)
	assert.Equal(t, int64(3), v.AsInt(), "a closure's local is its own, and the outer one is untouched")
}

// TestParity_ShadowingWithinOneFunctionIsStillRejected is the other side of that
// boundary: narrowing the walk must not switch the check off. A nested BLOCK in the
// same function is still the same frame.
func TestParity_ShadowingWithinOneFunctionIsStillRejected(t *testing.T) {
	s := NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `
fun probe() > int {
    final name = 1;
    if (true) {
        final name = 2;
        return name;
    }
    return name;
}`)
	require.Error(t, err, "a block in the same function still shadows")
	assert.Contains(t, err.Error(), "already exists in an enclosing scope")
}

// TestParity_MutatorThroughAnImmutableAnnotationIsRejected pins the rule that forced
// magus's own 126-site migration, because the tree was what was wrong. Upstream
// rejects this source in the same words ("Method `append` requires mutable list"):
// the ANNOTATION narrows the type, so appending through it is a type error however
// the value was built. Recorded as a test so nobody relaxes the rule to spare
// another migration.
func TestParity_MutatorThroughAnImmutableAnnotationIsRejected(t *testing.T) {
	s := NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `
fun probe() > int {
    final files: [str] = mut [<str>];
    files.append("x");
    return files.len();
}`)
	require.Error(t, err, "a mut value does not widen an immutable annotation")
	assert.Contains(t, err.Error(), "requires a mutable list")
}

// TestParity_MutAnnotationAcceptsTheMutator is the migration's target shape, and the
// reason the migration is safe: `mut [str]` takes the mut value AND permits the
// mutator, and stays assignable where a plain `[str]` is wanted.
func TestParity_MutAnnotationAcceptsTheMutator(t *testing.T) {
	v := evalParity(t, `
fun consume(xs: [str]) > int => xs.len();

fun probe() > int {
    final files: mut [str] = mut [<str>];
    files.append("x");
    // mut T is assignable to T, never the reverse, so widening the declaration
    // cannot break a call that wanted the immutable one.
    return consume(files);
}`)
	assert.Equal(t, int64(1), v.AsInt(), "the mut annotation permits the write and still satisfies [str]")
}

// TestParity_ConstFoldMustNotSwallowABranchTarget pins the bug behind a wrong answer that
// looked like a precedence problem and was not: `(a ?? 0) + 1` returned `a`.
//
// The parse was always correct (`(a ?? 0) + 1`), and the coalesce's jump lands on the
// `1`. That `1` is also the middle of a foldable `LoadConst; LoadConst; OpAdd` triple, and
// FoldConsts rewrote the trailing two into OpNop without checking branch targets, so the
// non-null path jumped onto a Nop and the operator never ran. Upstream Buzz evaluates
// these to 6 and 15 (measured against ~/.local/bin/buzz).
//
// The multiplication case is the one that proves it is not arithmetic: a mis-associated
// parse could yield 5 for the addition by coincidence, but nothing about `*` yields 5
// except skipping the operator outright.
func TestParity_ConstFoldMustNotSwallowABranchTarget(t *testing.T) {
	add := evalParity(t, `
fun probe() > int {
    final a: int? = 5;
    return (a ?? 0) + 1;
}`)
	assert.Equal(t, int64(6), add.AsInt(), "`(a ?? 0) + 1` with a non-null a")

	mul := evalParity(t, `
fun probe() > int {
    final a: int? = 5;
    return (a ?? 0) * 3;
}`)
	assert.Equal(t, int64(15), mul.AsInt(), "the operator runs, rather than being skipped")

	// The unparenthesised form is the same expression: ?? binds tighter than +.
	bare := evalParity(t, `
fun probe() > int {
    final a: int? = 5;
    return a ?? 0 + 1;
}`)
	assert.Equal(t, int64(6), bare.AsInt(), "`a ?? 0 + 1` is `(a ?? 0) + 1`")

	// The null path always worked; it must keep working, and folding is still allowed
	// where no branch lands inside the triple.
	null := evalParity(t, `
fun probe() > int {
    final a: int? = null;
    return (a ?? 0) + 1;
}`)
	assert.Equal(t, int64(1), null.AsInt(), "the fall-through path is unchanged")

	folded := evalParity(t, `fun probe() > int { return 2 + 3 * 4; }`)
	assert.Equal(t, int64(14), folded.AsInt(), "constant folding still applies off a branch")
}
