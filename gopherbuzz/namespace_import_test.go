package buzz_test

import (
	"context"
	"testing"

	"github.com/egladman/gopherbuzz"
)

// TestFlatImportBindsNamespaceObject verifies that a flat `import "<mod>"`
// binds both the splatted unqualified exports AND a namespace object, so an
// importer can reach an export either way: `foo()` or `mod\foo()`. Upstream
// Buzz only accepts the qualified form, so this is what lets the same source
// run on both runtimes.
func TestFlatImportBindsNamespaceObject(t *testing.T) {
	ctx := context.Background()
	s := buzz.NewSession(ctx)
	s.SetSourceModule("greet", `
namespace greet;
export fun hello(name: str) > str { return "hi " + name; }
`)
	if err := s.Exec(ctx, `import "greet";`); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Unqualified (splat) still works.
	v, err := s.Eval(ctx, `return hello("a")`)
	if err != nil {
		t.Fatalf("unqualified call: %v", err)
	}
	if got := v.String(); got != "hi a" {
		t.Errorf("unqualified hello = %q, want %q", got, "hi a")
	}

	// Qualified (namespace object) resolves the same export.
	v, err = s.Eval(ctx, `return greet\hello("b")`)
	if err != nil {
		t.Fatalf("qualified call: %v", err)
	}
	if got := v.String(); got != "hi b" {
		t.Errorf("qualified greet\\hello = %q, want %q", got, "hi b")
	}
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
	s := buzz.NewSession(ctx)
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
	if err := s.Exec(ctx, `import "moda"; import "modb";`); err != nil {
		t.Fatalf("import: %v", err)
	}
	if _, err := s.Eval(ctx, `return moda\setIt()`); err != nil {
		t.Fatalf("moda\\setIt: %v", err)
	}
	if _, err := s.Eval(ctx, `return modb\build()`); err != nil {
		t.Fatalf("modb\\build: %v", err)
	}
	v, err := s.Eval(ctx, `return modb\count()`)
	if err != nil {
		t.Fatalf("modb\\count: %v", err)
	}
	if got := v.String(); got != "3" {
		t.Errorf("modb items count = %q, want %q (moda's private panel leaked into modb)", got, "3")
	}
}

// TestPrivateFuncsDoNotCollideAcrossModules is the function-name facet of the same
// bug: two modules each declare a private `fun tag()` returning their own name. In
// the shared Env both would bind the key "tag", so whichever loaded last wins and a
// caller in the other module would invoke the wrong body. (In bubblegum-wm this is the
// real `fun labelAt` shared verbatim by cheatsheet and inspector.) Each module must
// call its OWN private function.
func TestPrivateFuncsDoNotCollideAcrossModules(t *testing.T) {
	ctx := context.Background()
	s := buzz.NewSession(ctx)
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
	if err := s.Exec(ctx, `import "mone"; import "mtwo";`); err != nil {
		t.Fatalf("import: %v", err)
	}
	v, err := s.Eval(ctx, `return mone\whoOne()`)
	if err != nil {
		t.Fatalf("mone\\whoOne: %v", err)
	}
	if got := v.String(); got != "one" {
		t.Errorf("mone\\whoOne() = %q, want %q (mtwo's private tag() shadowed mone's)", got, "one")
	}
	v, err = s.Eval(ctx, `return mtwo\whoTwo()`)
	if err != nil {
		t.Fatalf("mtwo\\whoTwo: %v", err)
	}
	if got := v.String(); got != "two" {
		t.Errorf("mtwo\\whoTwo() = %q, want %q", got, "two")
	}
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
	s := buzz.NewSession(ctx)
	s.SetSourceModule("alpha", `
namespace alpha;
export fun who() > str { return "alpha"; }
`)
	s.SetSourceModule("beta", `
namespace beta;
export fun who() > str { return "beta"; }
`)
	if err := s.Exec(ctx, `import "alpha"; import "beta";`); err != nil {
		t.Fatalf("import: %v", err)
	}
	v, err := s.Eval(ctx, `return alpha\who()`)
	if err != nil {
		t.Fatalf("alpha\\who: %v", err)
	}
	if got := v.String(); got != "alpha" {
		t.Errorf("alpha\\who() = %q, want %q (beta's export dropped alpha's from its namespace?)", got, "alpha")
	}
	v, err = s.Eval(ctx, `return beta\who()`)
	if err != nil {
		t.Fatalf("beta\\who: %v", err)
	}
	if got := v.String(); got != "beta" {
		t.Errorf("beta\\who() = %q, want %q (beta's shared export was dropped from its namespace)", got, "beta")
	}
}
