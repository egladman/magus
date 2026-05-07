package interp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	teal "github.com/egladman/magus/internal/interp/engine/lua/teal"
)

func writeTL(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunSmoke(t *testing.T) {
	dir := t.TempDir()
	path := writeTL(t, dir, "magusfile.tl", `
local ran = false

global function build(args: {string})
    ran = true
end
`)
	src := &Source{Dir: dir, Files: []string{path}}
	if err := Run(context.Background(), src, "build", nil, ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestCompileCacheRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := writeTL(t, dir, "magusfile.tl", `
global function noop(args: {string}) end
`)
	src := &Source{Dir: dir, Files: []string{path}}

	// First run: compiles and caches.
	if err := Run(context.Background(), src, "noop", nil, ""); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Second run: hits the cache. If caching is broken this will also succeed
	// (functionally), but we can verify the cache file exists.
	rawSrc, _ := os.ReadFile(path)
	decls, _ := teal.TypeDecls()
	key := cacheKey(teal.ConcatPreamble(decls, rawSrc))
	cd, _ := cacheDir()
	if _, err := os.Stat(filepath.Join(cd, key+".lua")); err != nil {
		t.Errorf("cache entry missing after first run: %v", err)
	}

	if err := Run(context.Background(), src, "noop", nil, ""); err != nil {
		t.Fatalf("second Run (cache hit): %v", err)
	}
}

func TestCompileError(t *testing.T) {
	dir := t.TempDir()
	path := writeTL(t, dir, "magusfile.tl", `
-- deliberate type error: passing integer where string expected
local x: string = 42
`)
	src := &Source{Dir: dir, Files: []string{path}}
	err := Run(context.Background(), src, "noop", nil, "")
	if err == nil {
		t.Fatal("expected a compile error, got nil")
	}
	t.Logf("got expected error: %v", err)
}

func TestParseTargets(t *testing.T) {
	dir := t.TempDir()
	path := writeTL(t, dir, "magusfile.tl", `
global function build(args: {string}) end
global function test(args: {string}) end
`)
	src := &Source{Dir: dir, Files: []string{path}}
	targets, err := Parse(context.Background(), src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d: %v", len(targets), targets)
	}
	names := map[string]bool{}
	for _, t := range targets {
		names[t.Key] = true
	}
	if !names["build"] {
		t.Error("expected target 'build'")
	}
	if !names["test"] {
		t.Error("expected target 'test'")
	}
}

func TestMagusRegisterBinding(t *testing.T) {
	dir := t.TempDir()
	path := writeTL(t, dir, "magusfile.tl", `
magus.project.register(".", {})

global function build(args: {string}) end
`)
	src := &Source{Dir: dir, Files: []string{path}}
	if err := Run(context.Background(), src, "build", nil, dir); err != nil {
		t.Fatalf("Run with magus.project.register: %v", err)
	}
}

func TestShRunBinding(t *testing.T) {
	dir := t.TempDir()
	path := writeTL(t, dir, "magusfile.tl", `
global function hello(args: {string})
    local os = require("magus.extra.os")
    os.exec("echo", {"hello from teal"})
end
`)
	src := &Source{Dir: dir, Files: []string{path}}
	if err := Run(context.Background(), src, "hello", nil, ""); err != nil {
		t.Fatalf("Run sh.run: %v", err)
	}
}

// TestStdNoAggregate locks in the removal of the magus.extra aggregate: reaching a
// std module through it is now a compile error. Std modules are require-only —
// see TestShRunBinding / TestArgBinding for the positive require path.
func TestStdNoAggregate(t *testing.T) {
	dir := t.TempDir()
	path := writeTL(t, dir, "magusfile.tl", `
global function build(_args: {string})
    magus.extra.os.exec("true")
end
`)
	src := &Source{Dir: dir, Files: []string{path}}
	if err := Run(context.Background(), src, "build", nil, ""); err == nil {
		t.Fatal("expected a compile error for magus.extra (aggregate removed), got nil")
	}
}

func TestVcsShortHashBinding(t *testing.T) {
	dir := t.TempDir()
	path := writeTL(t, dir, "magusfile.tl", `
global function show(args: {string})
    local vcs = require("magus.extra.vcs")
    local h = vcs.short_hash()
    -- h may be "" if not in a repo; that is acceptable
end
`)
	src := &Source{Dir: dir, Files: []string{path}}
	if err := Run(context.Background(), src, "show", nil, ""); err != nil {
		t.Fatalf("Run vcs.short_hash: %v", err)
	}
}

func TestFsGlobBinding(t *testing.T) {
	dir := t.TempDir()
	// Create a file to glob.
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	path := writeTL(t, dir, "magusfile.tl", `
global function check(args: {string})
    local fs = require("magus.extra.fs")
    local files = fs.glob("*.txt")
    if #files == 0 then
        error("expected at least one .txt file")
    end
end
`)
	src := &Source{Dir: dir, Files: []string{path}}
	// Change to dir so glob works relative to cwd.
	if err := Run(context.Background(), src, "check", nil, dir); err != nil {
		t.Fatalf("Run fs.glob: %v", err)
	}
}

// TestTargetNewIsGone verifies that magus.target.new is no longer in the Teal
// type system: using it must produce a compile error.
func TestTargetNewIsGone(t *testing.T) {
	dir := t.TempDir()
	path := writeTL(t, dir, "magusfile.tl", `
magus.target.new("build", function(_args: {string}) end)
`)
	src := &Source{Dir: dir, Files: []string{path}}
	err := Run(context.Background(), src, "build", nil, "")
	if err == nil {
		t.Fatal("expected a compile error when using magus.target.new, got nil")
	}
}
