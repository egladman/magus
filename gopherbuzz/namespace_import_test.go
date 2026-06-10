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
