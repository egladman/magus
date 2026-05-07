package interp_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp"
)

func writeTLBP(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "magusfile.tl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSpellModuleGet verifies that magus.spell.get(name) returns the spell table
// for a built-in spell.
func TestSpellModuleGet(t *testing.T) {
	dir := t.TempDir()
	writeTLBP(t, dir, `
global function check(args: {string})
    local s = magus.spell.get('json')
    assert(s ~= nil, "spell not found")
    assert(s.name == 'json', "name mismatch: " .. tostring(s.name))
end
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
	writeTLBP(t, dir, `
local json = require("magus.spell.json")
global function check(args: {string})
    assert(type(json.prettier) == 'function', "prettier op must be callable as a method")
end
`)
	src, err := interp.Find(dir)
	if err != nil || src == nil {
		t.Fatalf("Find: %v (src=%v)", err, src)
	}
	if err := interp.Run(context.Background(), src, "check", nil, dir); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestSpellModuleForkTarget verifies that a fork target (which has no Lua
// function in the compiled spell table — only a spells.json data entry) is still
// callable programmatically. registerSpells overlays a Go-backed function for
// each fork target; here go-vet is a fork no-op that must resolve to
// a function and run without error.
func TestSpellModuleForkTarget(t *testing.T) {
	dir := t.TempDir()
	writeTLBP(t, dir, `
local go = require("magus.spell.go")
global function check(args: {string})
    assert(go ~= nil, "spell not found")
    assert(type(go["go-vet"]) == 'function', "fork go-vet must be a function (overlay)")
end
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
// nil for an unknown name.
func TestSpellGetMultiple(t *testing.T) {
	dir := t.TempDir()
	writeTLBP(t, dir, `
global function check(args: {string})
    local ts = magus.spell.get('ts')
    local go = magus.spell.get('go')
    assert(ts ~= nil and ts.name == 'ts', "ts get failed")
    assert(go ~= nil and go.name == 'go', "go get failed")
    local one = magus.spell.get('json')
    assert(one ~= nil and one.name == 'json', "single get failed")
    local missing = magus.spell.get('nonexistent-xyz')
    assert(missing == nil, "unknown spell must be nil")
end
`)
	src, err := interp.Find(dir)
	if err != nil || src == nil {
		t.Fatalf("Find: %v (src=%v)", err, src)
	}
	if err := interp.Run(context.Background(), src, "check", nil, dir); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestSpellModuleRequireBuiltin verifies a built-in spell can be referenced as a
// typed module: require("magus.spell.json") type-checks to MagusSpell and the
// resolved value is the live spell table (build is callable).
func TestSpellModuleRequireBuiltin(t *testing.T) {
	dir := t.TempDir()
	writeTLBP(t, dir, `
local json = require("magus.spell.json")
global function check(args: {string})
    assert(json ~= nil, "spell not found")
    assert(json.name == 'json', "name mismatch: " .. tostring(json.name))
    assert(type(json.prettier) == 'function', "prettier op must be callable as a method")
end
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
// module is a compile error — the point of typed require — not a silent runtime nil.
func TestSpellModuleRequireUnknownFailsToCompile(t *testing.T) {
	dir := t.TempDir()
	writeTLBP(t, dir, `
local d = require("magus.spell.dockr")
magus.project.register(".", {spells = {d}})
`)
	src, err := interp.Find(dir)
	if err != nil || src == nil {
		t.Fatalf("Find: %v (src=%v)", err, src)
	}
	err = interp.Run(context.Background(), src, "noop", nil, dir)
	if err == nil {
		t.Fatal("expected a compile error for the misspelled module, got nil")
	}
	if !strings.Contains(err.Error(), "module not found") {
		t.Fatalf("expected 'module not found' compile error, got: %v", err)
	}
}

// TestSpellModuleRequireLocal verifies a workspace-local Teal spell is
// require-able — the runtime twin of the compile-time search path:
// require("locreq") resolves ./spells/locreq.tl, compiles+registers it, and binds
// via spells = { ... } so its target dispatches.
func TestSpellModuleRequireLocal(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "spells"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spells", "locreq.tl"), []byte(`
return {
   mgs_getName = function(): string return "locreq" end,
   mgs_listTargets = function(): any
      return { build = { cmd = "true" } }
   end,
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTLBP(t, dir, `
local locreq = require("locreq")
global function check(args: {string})
    assert(locreq ~= nil, "local spell not resolved")
    assert(locreq.name == 'locreq', "name mismatch: " .. tostring(locreq.name))
end
magus.project.register(".", {spells = {locreq}})
`)
	src, err := interp.Find(dir)
	if err != nil || src == nil {
		t.Fatalf("Find: %v (src=%v)", err, src)
	}
	// Running the inline check target exercises the full path: require resolves
	// ./spells/locreq.tl at compile (type-checks to MagusSpell) and at runtime
	// (compiles+registers it), the handle carries the right name, and binding it
	// via spells = { ... } succeeds. (CLI dispatch of the spell's own build target
	// is covered separately.)
	if err := interp.Run(context.Background(), src, "check", nil, dir); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestSpellModuleRequireLocalDotted verifies the explicit dotted form
// require("spells.locreq") resolves ./spells/locreq.tl, the Teal parallel to the
// Buzz `import "spells/locreq"` idiom (a dot maps to a path separator).
func TestSpellModuleRequireLocalDotted(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "spells"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spells", "locreq.tl"), []byte(`
return {
   mgs_getName = function(): string return "locreqdotted" end,
   mgs_listTargets = function(): any
      return { build = { cmd = "true" } }
   end,
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTLBP(t, dir, `
local locreq = require("spells.locreq")
global function check(args: {string})
    assert(locreq ~= nil, "local spell not resolved")
    assert(locreq.name == 'locreqdotted', "name mismatch: " .. tostring(locreq.name))
end
magus.project.register(".", {spells = {locreq}})
`)
	src, err := interp.Find(dir)
	if err != nil || src == nil {
		t.Fatalf("Find: %v (src=%v)", err, src)
	}
	if err := interp.Run(context.Background(), src, "check", nil, dir); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestSpellMultipleFields verifies that the spell table returned by
// require("magus.spell.go") has the expected fields beyond just name.
func TestSpellMultipleFields(t *testing.T) {
	dir := t.TempDir()
	writeTLBP(t, dir, `
local go = require("magus.spell.go")
global function check(args: {string})
    assert(go ~= nil, "spell not found")
    assert(go.name == 'go', "name mismatch: " .. tostring(go.name))
    assert(type(go["go-build"]) == 'function', "go-build must be a function")
    assert(type(go["go-fmt"]) == 'function', "go-fmt must be a function")
end
`)
	src, err := interp.Find(dir)
	if err != nil || src == nil {
		t.Fatalf("Find: %v (src=%v)", err, src)
	}
	if err := interp.Run(context.Background(), src, "check", nil, dir); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
