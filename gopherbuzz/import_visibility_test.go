package buzz_test

import (
	"context"
	"strings"
	"testing"

	"github.com/egladman/gopherbuzz"
)

// importModuleSrc is a flat-importable module: an exported function that reads a
// non-exported (captured) module var, plus a non-exported helper function. Under
// exports-only import visibility (M4) only `pub` crosses the import boundary; the
// module's own code still reads `secret` live at runtime.
const importModuleSrc = `
var secret = 42;
export fun pub() int { return secret; }
fun privHelper() int { return 7; }
`

func newImporter(t *testing.T) *buzz.Session {
	t.Helper()
	ctx := context.Background()
	s := buzz.NewSession(ctx)
	s.SetPromoteTopLevel(true) // magusfile execution mode
	s.SetSourceModule("mymod", importModuleSrc)
	return s
}

// TestImportVisibility_ExportedCrosses verifies an exported function is callable
// through a flat import and still reads its module's non-exported state live —
// the runtime Env is untouched; only the importer's checker view is narrowed.
func TestImportVisibility_ExportedCrosses(t *testing.T) {
	s := newImporter(t)
	v, err := s.Eval(context.Background(), `import "mymod"; return pub();`)
	if err != nil {
		t.Fatalf("calling exported pub() across import failed: %v", err)
	}
	if !v.IsInt() || v.AsInt() != 42 {
		t.Errorf("pub() = %v, want 42 (exported fn must read its module's live secret)", v)
	}
}

// TestImportVisibility_NonExportedVarHidden verifies a module's non-exported var
// is invisible to the importer, and that the error names `export` as the fix.
func TestImportVisibility_NonExportedVarHidden(t *testing.T) {
	s := newImporter(t)
	_, err := s.Eval(context.Background(), `import "mymod"; return secret;`)
	if err == nil {
		t.Fatal("referencing a module's non-exported var should fail under exports-only imports")
	}
	if !strings.Contains(err.Error(), "export") {
		t.Errorf("error should point at the missing export, got: %v", err)
	}
}

// TestImportVisibility_NonExportedFuncHidden verifies a module's non-exported
// function is likewise not callable through the import.
func TestImportVisibility_NonExportedFuncHidden(t *testing.T) {
	s := newImporter(t)
	_, err := s.Eval(context.Background(), `import "mymod"; return privHelper();`)
	if err == nil {
		t.Fatal("calling a module's non-exported function should fail under exports-only imports")
	}
	if !strings.Contains(err.Error(), "export") {
		t.Errorf("error should point at the missing export, got: %v", err)
	}
}

// TestImportVisibility_ImporterMayShadow verifies the boundary hides only the
// imported binding: the importer can still declare its own same-named top-level
// var without colliding with the module's hidden one.
func TestImportVisibility_ImporterMayShadow(t *testing.T) {
	s := newImporter(t)
	v, err := s.Eval(context.Background(), `import "mymod"; var secret = 99; return secret;`)
	if err != nil {
		t.Fatalf("importer declaring its own 'secret' should be fine: %v", err)
	}
	if !v.IsInt() || v.AsInt() != 99 {
		t.Errorf("importer's own secret = %v, want 99", v)
	}
}
