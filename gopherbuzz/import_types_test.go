package buzz

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeModule drops a .bzz file into dir for an import test.
func writeModule(t *testing.T, dir, name, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestImport_ExportedObjectType verifies a flat-imported module's exported
// object type resolves in the importer's annotations and literals — both the
// type-check (during Exec) and the runtime construction/field read.
func TestImport_ExportedObjectType(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "lib.bzz", `export object Foo { n: int = 0 }`)

	ctx := context.Background()
	sess := NewSession(ctx)
	defer sess.Close()
	sess.SetIncludeDirs([]string{dir})

	src := `
import "lib";
fun make() > Foo { return Foo{ n = 7 }; }
export const result = make().n;
`
	if err := sess.Exec(ctx, src); err != nil {
		t.Fatalf("exec with imported object type: %v", err)
	}
	v, ok := sess.Exports()["result"]
	if !ok {
		t.Fatal("result not exported")
	}
	if !v.IsInt() || v.AsInt() != 7 {
		t.Errorf("result = %v, want 7", v.String())
	}
}

// TestImport_ExportedEnumType verifies a flat-imported module's exported enum
// type resolves in the importer (annotation + case access).
func TestImport_ExportedEnumType(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "palette.bzz", `export enum Color { Red, Green, Blue }`)

	ctx := context.Background()
	sess := NewSession(ctx)
	defer sess.Close()
	sess.SetIncludeDirs([]string{dir})

	src := `
import "palette";
fun pick() > Color { return Color.Green; }
const c = pick();
`
	if err := sess.Exec(ctx, src); err != nil {
		t.Fatalf("exec with imported enum type: %v", err)
	}
}

// TestImport_CrossReferencingObjectTypes verifies imported object types that
// reference each other (a field typed as a sibling imported object) resolve.
func TestImport_CrossReferencingObjectTypes(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "shapes.bzz", `
export object Inner { v: int = 0 }
export object Outer { inner: Inner = Inner{} }
`)

	ctx := context.Background()
	sess := NewSession(ctx)
	defer sess.Close()
	sess.SetIncludeDirs([]string{dir})

	src := `
import "shapes";
fun build() > Outer { return Outer{ inner = Inner{ v = 3 } }; }
export const got = build().inner.v;
`
	if err := sess.Exec(ctx, src); err != nil {
		t.Fatalf("exec with cross-referencing imported types: %v", err)
	}
	v, ok := sess.Exports()["got"]
	if !ok {
		t.Fatal("got not exported")
	}
	if !v.IsInt() || v.AsInt() != 3 {
		t.Errorf("got = %v, want 3", v.String())
	}
}

// TestSourceModule_ExportsTypes verifies a host-registered source module
// (embedded .bzz, no file on the include path) exposes its exported object/enum
// types to the importer — including object-typed and list field defaults, which
// the canonical magus/target module relies on.
func TestSourceModule_ExportsTypes(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx)
	defer sess.Close()
	sess.SetSourceModule("magus/lib", `
export object Strategy { name: str = "" }
export object Charm { name: str = "", args: [str] = [], strategy: Strategy = Strategy{} }
export object Target { name: str = "", charms: [Charm] = [] }
`)

	src := `
import "magus/lib";
fun pick() > Target {
    return Target{ name = "build", charms = [Charm{ name = "fast" }] };
}
export const tname = pick().name;
`
	if err := sess.Exec(ctx, src); err != nil {
		t.Fatalf("exec with source-module types: %v", err)
	}
	v, ok := sess.Exports()["tname"]
	if !ok || !v.IsStr() || v.AsString() != "build" {
		t.Errorf("tname = %v, want \"build\"", v.String())
	}
}

// TestImport_NonExportedObjectType_Errors verifies that only EXPORTED types
// cross the module boundary: a non-exported imported object is not visible to
// the importer's checker, so using it is a compile-time "undefined type" error.
func TestImport_NonExportedObjectType_Errors(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "internal.bzz", `object Secret { n: int = 0 }`)

	ctx := context.Background()
	sess := NewSession(ctx)
	defer sess.Close()
	sess.SetIncludeDirs([]string{dir})

	err := sess.Exec(ctx, `import "internal"; const s = Secret{ n = 1 };`)
	if err == nil {
		t.Fatal("expected error using a non-exported imported type, got nil")
	}
	if !strings.Contains(err.Error(), "undefined type") {
		t.Errorf("error = %q, want it to mention \"undefined type\"", err.Error())
	}
}
