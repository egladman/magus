package buzz_test

import (
	"context"
	"testing"

	"github.com/egladman/gopherbuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFlatImportBindsNamespaceObject verifies that a flat `import "<mod>"`
// binds both the splatted unqualified exports AND a namespace object, so an
// importer can reach an export either way: `foo()` or `mod\foo()`. Upstream
// Buzz only accepts the qualified form, so this is what lets the same source
// run on both runtimes.
func TestFlatImportBindsNamespaceObject(t *testing.T) {
	ctx := context.Background()
	s := buzz.NewSession(ctx, buzz.WithEmbedded())
	s.SetSourceModule("greet", `
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

// TestPrivateGlobalsDoNotCollideAcrossModules guards the per-module mangling of
// private top-level names. Two namespaced modules each declare a private `var
// panel` and a private `var items`; in SharedGlobals mode every module's top-level
// vars land in one shared Env, so without namespace-qualified keys moda's `panel`
// and modb's `panel` would be the same slot. The real-world symptom (bubblegum-wm): the
// status bar sets its `panel`, then an overlay module's `if (panel != null) return`
// sees it, skips building its `items`/`labels` list, and indexing it crashes.
func TestPrivateGlobalsDoNotCollideAcrossModules(t *testing.T) {
	ctx := context.Background()
	s := buzz.NewSession(ctx, buzz.WithEmbedded())
	s.SetSourceModule("moda", `
namespace moda;
var panel: int? = null;
export fun setIt() > void { panel = 7; }
`)
	s.SetSourceModule("modb", `
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
	s := buzz.NewSession(ctx, buzz.WithEmbedded())
	// The exported entry points have distinct names (whoOne/whoTwo) so the test
	// isolates the PRIVATE `tag` collision; a shared export name would instead trip
	// a separate namespace-object issue unrelated to this fix.
	s.SetSourceModule("mone", `
namespace mone;
fun tag() > str { return "one"; }
export fun whoOne() > str { return tag(); }
`)
	s.SetSourceModule("mtwo", `
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
	s := buzz.NewSession(ctx, buzz.WithEmbedded())
	s.SetSourceModule("alpha", `
namespace alpha;
export fun who() > str { return "alpha"; }
`)
	s.SetSourceModule("beta", `
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
