package interp_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp"
)

func TestColorEnabledForFile_Nil(t *testing.T) {
	if interp.ColorEnabledForFile(nil) {
		t.Error("ColorEnabledForFile(nil) = true, want false")
	}
}

func TestPrintSourceContext_NonexistentFile(t *testing.T) {
	var sb strings.Builder
	interp.PrintSourceContext(&sb, "/no/such/file/xyz.go", 1, 2, false)
	if !strings.Contains(sb.String(), "cannot read source") {
		t.Errorf("expected error message in output, got: %q", sb.String())
	}
}

func TestPrintSourceContext_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.txt")
	content := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var sb strings.Builder
	interp.PrintSourceContext(&sb, path, 3, 1, false)
	out := sb.String()
	if !strings.Contains(out, "line2") || !strings.Contains(out, "line3") || !strings.Contains(out, "line4") {
		t.Errorf("PrintSourceContext output missing expected lines: %q", out)
	}
}

func writeBzzBP(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSpellModuleGet verifies that magus.spell.get(name) returns the spell handle
// for a built-in spell.
func TestSpellModuleGet(t *testing.T) {
	dir := t.TempDir()
	writeBzzBP(t, dir, `
import "magus";

export fun check(_args: [str]) > void {
    var s = magus.spell.get("json");
    if (s.name != "json") { error("name mismatch: " + s.name); }
}
`)
	src, err := interp.Find(dir)
	if err != nil || src == nil {
		t.Fatalf("Find: %v (src=%v)", err, src)
	}
	if err := interp.Run(context.Background(), src, "check", nil, dir); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestSpellModuleBuildTarget verifies that a spell op can be invoked as a method
// (json exposes a single tool-native op, prettier).
func TestSpellModuleBuildTarget(t *testing.T) {
	dir := t.TempDir()
	writeBzzBP(t, dir, `
import "magus";

export fun check(_args: [str]) > void {
    var json = magus.spell.get("json");
    if (json["prettier"] == null) { throw "prettier op must be callable as a method"; }
}
`)
	src, err := interp.Find(dir)
	if err != nil || src == nil {
		t.Fatalf("Find: %v (src=%v)", err, src)
	}
	if err := interp.Run(context.Background(), src, "check", nil, dir); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestSpellModuleForkTarget verifies that a fork target (which has no function
// in the compiled spell table — only a spells.json data entry) is still
// callable programmatically. registerSpells overlays a Go-backed function for
// each fork target; here go-vet is a fork no-op that must resolve to
// a function and run without error.
func TestSpellModuleForkTarget(t *testing.T) {
	dir := t.TempDir()
	writeBzzBP(t, dir, `
import "magus";

export fun check(_args: [str]) > void {
    var go = magus.spell.get("go");
    if (go.name != "go") { throw "spell not found"; }
    if (go["go-vet"] == null) { throw "fork go-vet must be a function (overlay)"; }
}
`)
	src, err := interp.Find(dir)
	if err != nil || src == nil {
		t.Fatalf("Find: %v (src=%v)", err, src)
	}
	if err := interp.Run(context.Background(), src, "check", nil, dir); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestSpellGetMultiple verifies magus.spell.get returns one spell per call and
// an empty handle (no fork-target methods) for an unknown name.
func TestSpellGetMultiple(t *testing.T) {
	dir := t.TempDir()
	writeBzzBP(t, dir, `
import "magus";

export fun check(_args: [str]) > void {
    var ts = magus.spell.get("ts");
    var go = magus.spell.get("go");
    if (ts.name != "ts") { throw "ts get failed"; }
    if (go.name != "go") { throw "go get failed"; }
    var one = magus.spell.get("json");
    if (one.name != "json") { throw "single get failed"; }
    var missing = magus.spell.get("nonexistent-xyz");
    if (missing["listTargets"] != null) { throw "unknown spell must have no targets"; }
}
`)
	src, err := interp.Find(dir)
	if err != nil || src == nil {
		t.Fatalf("Find: %v (src=%v)", err, src)
	}
	if err := interp.Run(context.Background(), src, "check", nil, dir); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestSpellModuleRequireBuiltin verifies a built-in spell can be imported as a
// typed module: import "magus/spell/json" binds the handle under its basename
// (json), and the resolved value is the live spell handle (prettier is callable).
func TestSpellModuleRequireBuiltin(t *testing.T) {
	dir := t.TempDir()
	writeBzzBP(t, dir, `
import "magus";
import "magus/spell/json";

export fun check(_args: [str]) > void {
    if (json.name != "json") { error("name mismatch: " + json.name); }
    if (json["prettier"] == null) { throw "prettier op must be callable as a method"; }
}
`)
	src, err := interp.Find(dir)
	if err != nil || src == nil {
		t.Fatalf("Find: %v (src=%v)", err, src)
	}
	if err := interp.Run(context.Background(), src, "check", nil, dir); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestSpellModuleRequireUnknownFailsToCompile verifies a misspelled built-in
// module is a compile error — the point of typed import — not a silent runtime nil.
func TestSpellModuleRequireUnknownFailsToCompile(t *testing.T) {
	dir := t.TempDir()
	writeBzzBP(t, dir, `
import "magus";
import "magus/spell/dockr";
magus.project.register(".", fun(p, cb) > bool { cb({"spells": [dockr]}); return true; });
`)
	src, err := interp.Find(dir)
	if err != nil || src == nil {
		t.Fatalf("Find: %v (src=%v)", err, src)
	}
	err = interp.Run(context.Background(), src, "noop", nil, dir)
	if err == nil {
		t.Fatal("expected a compile error for the misspelled module, got nil")
	}
}

// TestSpellModuleRequireLocal verifies a workspace-local Buzz spell is
// importable by path — import "spells/locreq" resolves ./spells/locreq.buzz,
// binds the handle under the basename (locreq), so its target dispatches.
func TestSpellModuleRequireLocal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(dir, "spells"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spells", "locreq.buzz"), []byte(`
export fun mgs_getName() > str { return "locreq"; }
export fun mgs_listTargets() > any {
    return {"build": {"cmd": "true"}};
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeBzzBP(t, dir, `
import "magus";
import "spells/locreq";

export fun check(_args: [str]) > void {
    if (locreq.name != "locreq") { error("name mismatch: " + locreq.name); }
}
magus.project.register(".", fun(p, cb) > bool { cb({"spells": [locreq]}); return true; });
`)
	src, err := interp.Find(dir)
	if err != nil || src == nil {
		t.Fatalf("Find: %v (src=%v)", err, src)
	}
	if err := interp.Run(context.Background(), src, "check", nil, dir); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestSpellMultipleFields verifies that the spell handle for the go spell has
// the expected fork-target methods beyond just name.
func TestSpellMultipleFields(t *testing.T) {
	dir := t.TempDir()
	writeBzzBP(t, dir, `
import "magus";
import "magus/spell/go";

export fun check(_args: [str]) > void {
    if (go.name != "go") { error("name mismatch: " + go.name); }
    if (go["go-build"] == null) { throw "go-build must be a function"; }
    if (go["go-fmt"] == null) { throw "go-fmt must be a function"; }
}
`)
	src, err := interp.Find(dir)
	if err != nil || src == nil {
		t.Fatalf("Find: %v (src=%v)", err, src)
	}
	if err := interp.Run(context.Background(), src, "check", nil, dir); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
