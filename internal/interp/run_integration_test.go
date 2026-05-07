package interp_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/types"
)

func writeMagusfile(t *testing.T, dir, body string) {
	t.Helper()
	path := filepath.Join(dir, "magusfile.tl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runTarget(t *testing.T, dir, target string, args ...string) error {
	t.Helper()
	ctx := context.Background()
	src, err := interp.Find(dir)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if src == nil {
		t.Fatal("Find: no source found")
	}
	return interp.Run(ctx, src, target, args, dir)
}

func sentinel(dir string) string { return filepath.Join(dir, "ran") }

func TestRunTopLevelTarget(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
global function build(args: {string})
    local f = io.open("ran", "w")
    if f then f:write("build") f:close() end
end
`)
	if err := runTarget(t, dir, "build"); err != nil {
		t.Fatalf("run build: %v", err)
	}
	got, err := os.ReadFile(sentinel(dir))
	if err != nil {
		t.Fatalf("sentinel not created: %v", err)
	}
	if string(got) != "build" {
		t.Errorf("sentinel = %q, want %q", got, "build")
	}
}

func TestRunPathTarget(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
global function db_migrate(args: {string})
    local f = io.open("ran", "w")
    if f then f:write("db:migrate") f:close() end
end
`)
	if err := runTarget(t, dir, "db:migrate"); err != nil {
		t.Fatalf("run db:migrate: %v", err)
	}
	got, err := os.ReadFile(sentinel(dir))
	if err != nil {
		t.Fatalf("sentinel not created: %v", err)
	}
	if string(got) != "db:migrate" {
		t.Errorf("sentinel = %q, want %q", got, "db:migrate")
	}
}

// TestRunBuzzStdModule exercises the std host surface from a magusfile.bzz
// end-to-end: the magus-bindings-gen-emitted buzzgen trampolines must decode a variadic
// call (fs.join), a slice-in/map-out call (charm.append), and a void call
// (fs.writeFile). Modules are reached off the `import "magus/extra"` aggregate,
// with camelCase methods (Buzz's convention).
func TestRunBuzzStdModule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.bzz")
	if err := os.WriteFile(path, []byte(`
import "magus";
import "magus/extra";

export fun verify(_opts: [str]) > void {
    var joined = extra.fs.join("a", "b", "c");
    var patch = extra.charm.append(["y", "z"]);
    extra.fs.writeFile("ran", joined + "|" + patch.ops[1].value);
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runTarget(t, dir, "verify"); err != nil {
		t.Fatalf("run verify: %v", err)
	}
	got, err := os.ReadFile(sentinel(dir))
	if err != nil {
		t.Fatalf("sentinel not created: %v", err)
	}
	if string(got) != "a/b/c|z" {
		t.Errorf("sentinel = %q, want %q", got, "a/b/c|z")
	}
}

// TestRunBuzzMarkdownModule proves the markdown host module is reachable through
// the magus/extra aggregate (extra.markdown.toHtml), so a docs-site project can
// render Markdown to HTML in its own magusfile target.
func TestRunBuzzMarkdownModule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.bzz")
	if err := os.WriteFile(path, []byte(`
import "magus";
import "magus/extra";

export fun verify(_opts: [str]) > void {
    extra.fs.writeFile("ran", extra.markdown.toHtml("# Hi"));
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runTarget(t, dir, "verify"); err != nil {
		t.Fatalf("run verify: %v", err)
	}
	got, err := os.ReadFile(sentinel(dir))
	if err != nil {
		t.Fatalf("sentinel not created: %v", err)
	}
	if !strings.Contains(string(got), `id="hi"`) || !strings.Contains(string(got), "Hi</h1>") {
		t.Errorf("markdown.toHtml output = %q, want an <h1 id=\"hi\">Hi</h1>", got)
	}
}

// TestRunBuzzFmtSprintf exercises fmt.sprintf end-to-end. It is the first std
// method with a variadic arg preceded by a fixed one (format), so it guards the
// magus-bindings-gen lua/buzz variadic-offset decode in addition to the formatting itself.
func TestRunBuzzFmtSprintf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.bzz")
	if err := os.WriteFile(path, []byte(`
import "magus";
import "magus/extra";

export fun verify(_opts: [str]) > void {
    var asset = extra.fmt.sprintf("magus_%s_%s_%s.tar.gz", "1.0", "linux", "amd64");
    var none = extra.fmt.sprintf("literal");
    extra.fs.writeFile("ran", asset + "|" + none);
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runTarget(t, dir, "verify"); err != nil {
		t.Fatalf("run verify: %v", err)
	}
	got, err := os.ReadFile(sentinel(dir))
	if err != nil {
		t.Fatalf("sentinel not created: %v", err)
	}
	if string(got) != "magus_1.0_linux_amd64.tar.gz|literal" {
		t.Errorf("sentinel = %q, want %q", got, "magus_1.0_linux_amd64.tar.gz|literal")
	}
}

// TestRunTealFmtSprintf is the Teal/lua counterpart to TestRunBuzzFmtSprintf:
// fmt.sprintf must type-check (via the fmt.d.tl record) and format identically
// through the lua engine.
func TestRunTealFmtSprintf(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
global function verify(args: {string})
    local fmt = require("magus.extra.fmt")
    local fs = require("magus.extra.fs")
    fs.write_file("ran", fmt.sprintf("magus_%s_%s_%s.tar.gz", "1.0", "linux", "amd64"))
end
`)
	if err := runTarget(t, dir, "verify"); err != nil {
		t.Fatalf("run verify: %v", err)
	}
	got, err := os.ReadFile(sentinel(dir))
	if err != nil {
		t.Fatalf("sentinel not created: %v", err)
	}
	if string(got) != "magus_1.0_linux_amd64.tar.gz" {
		t.Errorf("sentinel = %q, want %q", got, "magus_1.0_linux_amd64.tar.gz")
	}
}

// TestRunBuzzAggregateUtil proves the magus host utilities resolve through the
// single `import "magus/extra"` aggregate (extra.fs.join / extra.os.execSh, in
// camelCase) and coexist with Buzz's own stdlib in the same file: hashing uses
// the stdlib `crypto.hash`, which the aggregate deliberately leaves room for by
// keeping the bare name `crypto` free.
func TestRunBuzzAggregateUtil(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.bzz")
	if err := os.WriteFile(path, []byte(`
import "magus";
import "magus/extra";
import "crypto";

export fun verify(_opts: [str]) > void {
    var joined = extra.fs.join("a", "b", "c");
    var out = extra.os.execSh("printf hello").stdout;
    var digest = crypto.hash(crypto.HashAlgorithm.Sha256, "");
    extra.fs.writeFile("ran", joined + "|" + out + "|" + digest);
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runTarget(t, dir, "verify"); err != nil {
		t.Fatalf("run verify: %v", err)
	}
	got, err := os.ReadFile(sentinel(dir))
	if err != nil {
		t.Fatalf("sentinel not created: %v", err)
	}
	want := "a/b/c|hello|e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if string(got) != want {
		t.Errorf("sentinel = %q, want %q", got, want)
	}
}

func TestRunTargetWithArgs(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
global function db_migrate(args: {string})
    local f = io.open("ran", "w")
    if f then f:write(table.concat(args, " ")) f:close() end
end
`)
	if err := runTarget(t, dir, "db:migrate", "a", "b", "c"); err != nil {
		t.Fatalf("run db:migrate: %v", err)
	}
	got, err := os.ReadFile(sentinel(dir))
	if err != nil {
		t.Fatalf("sentinel not created: %v", err)
	}
	if string(got) != "a b c" {
		t.Errorf("sentinel = %q, want %q", got, "a b c")
	}
}

func TestRunTargetReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
global function db_migrate(args: {string})
    error("boom")
end
`)
	err := runTarget(t, dir, "db:migrate")
	if err == nil {
		t.Fatal("expected non-nil error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want it to contain %q", err, "boom")
	}
}

func TestRunUnknownTarget(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
global function db_migrate(args: {string}) end
`)
	err := runTarget(t, dir, "no-such-target")
	if err == nil {
		t.Fatal("expected non-nil error for unknown target, got nil")
	}
}

// TestParseLocalSpellFromOtherDir verifies a magusfile that require/imports a
// workspace-local spell parses (preloads) when its directory is not the process
// cwd — the workspace-preload case that used to fail with "module not found"
// (Teal) / "undefined variable" (Buzz) because local-spell lookup was cwd-relative.
func TestParseLocalSpellFromOtherDir(t *testing.T) {
	cases := []struct {
		name, ext, spell, magusfile string
	}{
		{
			name: "teal", ext: "tl",
			spell: `return {
   mgs_getName = function(): string return "hello" end,
   mgs_listTargets = function(): any return { build = { cmd = "echo", args = {"hi"} } } end,
}`,
			magusfile: `local hello = require("spells.hello")
global function go(_a: {string})
    hello.build()
end`,
		},
		{
			name: "buzz", ext: "bzz",
			spell: `export fun mgs_getName() > str { return "hello"; }
export fun mgs_listTargets() > any { return {"build": {"cmd": "echo", "args": ["hi"]}}; }`,
			magusfile: `import "magus";
import "spells/hello";
export fun go(_a: [str]) > void { hello.build(); }`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proj := filepath.Join(t.TempDir(), "proj")
			if err := os.MkdirAll(filepath.Join(proj, "spells"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(proj, "spells", "hello."+tc.ext), []byte(tc.spell), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(proj, "magusfile."+tc.ext), []byte(tc.magusfile), 0o644); err != nil {
				t.Fatal(err)
			}
			// Parse from the test's cwd, NOT from proj — exactly what workspace
			// preload does when it visits a sub-project's magusfile.
			srcs, err := interp.FindAll(proj)
			if err != nil {
				t.Fatal(err)
			}
			if len(srcs) == 0 {
				t.Fatal("FindAll: no sources")
			}
			for _, src := range srcs {
				if _, err := interp.Parse(context.Background(), src); err != nil {
					t.Fatalf("Parse local-spell magusfile from other dir: %v", err)
				}
			}
		})
	}
}

// TestDependsOnUnknownTargetFails verifies a typo'd or removed dependency fails
// fast rather than silently no-op'ing. The Buzz and pool dispatch paths already
// errored; this pins the same for the Teal non-pool path (the branch that used
// to `continue` past an unknown name).
func TestDependsOnUnknownTargetFails(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
global function top(args: {string})
    magus.depends_on({"does_not_exist"})
end
`)
	err := runTarget(t, dir, "top")
	if err == nil {
		t.Fatal("expected an error for depends_on on an unknown target, got nil")
	}
	if !strings.Contains(err.Error(), "unknown target") {
		t.Errorf("error = %v, want it to mention %q", err, "unknown target")
	}
}

// TestRunTealTargetNameCollision verifies that two Teal target functions whose
// names normalize to the same canonical target are a hard error, not a silent
// last-write-wins clobber.
func TestRunTealTargetNameCollision(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
global function foo_bar(args: {string}) end
global function fooBar(args: {string}) end
`)
	err := runTarget(t, dir, "foo-bar")
	if err == nil {
		t.Fatal("expected collision error, got nil")
	}
	if !strings.Contains(err.Error(), "foo-bar") || !strings.Contains(err.Error(), "normalize") {
		t.Errorf("error should name the colliding canonical target and the cause; got: %v", err)
	}
}

// TestRunBuzzTargetNameCollision is the Buzz counterpart: two exports that
// normalize to the same canonical target must error.
func TestRunBuzzTargetNameCollision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.bzz")
	if err := os.WriteFile(path, []byte(`
import "magus";

export fun foo_bar(_a: [str]) > void {}
export fun fooBar(_a: [str]) > void {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runTarget(t, dir, "foo-bar")
	if err == nil {
		t.Fatal("expected collision error, got nil")
	}
	if !strings.Contains(err.Error(), "foo-bar") || !strings.Contains(err.Error(), "normalize") {
		t.Errorf("error should name the colliding canonical target and the cause; got: %v", err)
	}
}

// TestOsExitRaisesExitError verifies os.exit(code) aborts the target with a
// types.ExitError carrying the code (it must NOT call os.Exit), and that the
// typed error survives the VM boundary so the CLI/daemon can honor the code.
func TestOsExitRaisesExitError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.bzz")
	if err := os.WriteFile(path, []byte(`
import "magus";
import "magus/extra";

export fun bail(_a: [str]) > void { extra.os.exit(3); }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runTarget(t, dir, "bail")
	if err == nil {
		t.Fatal("expected error from os.exit, got nil")
	}
	var ex types.ExitError
	if !errors.As(err, &ex) {
		t.Fatalf("expected types.ExitError, got %T: %v", err, err)
	}
	if ex.Code != 3 {
		t.Errorf("exit code = %d, want 3", ex.Code)
	}
}

// TestOsSleep exercises os.sleep (milliseconds, matching Buzz) from a Buzz
// magusfile, confirming the TypeFloat binding path works for fractional and int
// literals and returns.
func TestOsSleep(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.bzz")
	if err := os.WriteFile(path, []byte(`
import "magus";
import "magus/extra";

export fun nap(_a: [str]) > void {
    extra.os.sleep(1.5);
    extra.os.sleep(0);
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runTarget(t, dir, "nap"); err != nil {
		t.Fatalf("os.sleep: %v", err)
	}
}

// TestOsWhich verifies os.which resolves a real command to a non-empty path and
// returns "" for a missing one (asserted inside the magusfile via os.exit).
func TestOsWhich(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.bzz")
	if err := os.WriteFile(path, []byte(`
import "magus";
import "magus/extra";

export fun checkwhich(_a: [str]) > void {
    if (extra.os.which("sh") == "") { extra.os.exit(2); }
    if (extra.os.which("definitely-no-such-cmd-zzz") != "") { extra.os.exit(3); }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runTarget(t, dir, "checkwhich"); err != nil {
		t.Fatalf("os.which assertions failed: %v", err)
	}
}

// TestMagusHint confirms magus.hint is callable (and a repeated message is
// tolerated — dedup happens in the shared channel).
func TestMagusHint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.bzz")
	if err := os.WriteFile(path, []byte(`
import "magus";

export fun nudge(_a: [str]) > void {
    magus.hint("stale generated code — run: magus run generate -- --write");
    magus.hint("stale generated code — run: magus run generate -- --write");
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runTarget(t, dir, "nudge"); err != nil {
		t.Fatalf("magus.hint: %v", err)
	}
}

// TestMagusFatal verifies magus.fatal aborts with a types.ExitError carrying
// code 1 (Buzz preserves the typed error across the boundary).
func TestMagusFatal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.bzz")
	if err := os.WriteFile(path, []byte(`
import "magus";

export fun boom(_a: [str]) > void { magus.fatal("boom"); }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runTarget(t, dir, "boom")
	if err == nil {
		t.Fatal("expected error from magus.fatal, got nil")
	}
	var ex types.ExitError
	if !errors.As(err, &ex) {
		t.Fatalf("expected types.ExitError, got %T: %v", err, err)
	}
	if ex.Code != 1 {
		t.Errorf("exit code = %d, want 1", ex.Code)
	}
}

// TestOsExecShShellOption verifies opts.shell is accepted and the chosen shell
// runs (sh is always present; the flag/derivation is unit-tested in host).
func TestOsExecShShellOption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.bzz")
	if err := os.WriteFile(path, []byte(`
import "magus";
import "magus/extra";

export fun viash(_a: [str]) > void {
    extra.os.execSh("true", "", {"shell": "sh"});
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runTarget(t, dir, "viash"); err != nil {
		t.Fatalf("os.exec_sh with shell opt: %v", err)
	}
}

// TestOsExitTealCarriesCode is the regression test for the Lua-engine bug where
// os.exit(code) dropped the code (the typed ExitError was stringified by the VM).
// The recorder must carry it so a Teal os.exit(7) yields ExitError{Code:7}.
func TestOsExitTealCarriesCode(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
global function bail(args: {string})
    local os = require("magus.extra.os")
    os.exit(7)
end
`)
	err := runTarget(t, dir, "bail")
	if err == nil {
		t.Fatal("expected error from os.exit, got nil")
	}
	var ex types.ExitError
	if !errors.As(err, &ex) {
		t.Fatalf("expected types.ExitError on the Lua engine, got %T: %v", err, err)
	}
	if ex.Code != 7 {
		t.Errorf("exit code = %d, want 7", ex.Code)
	}
}

// TestMagusFatalTealCarriesCode verifies magus.fatal also carries its exit code
// (1) as a typed ExitError on the Lua engine.
func TestMagusFatalTealCarriesCode(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
global function boom(args: {string})
    magus.fatal("boom")
end
`)
	err := runTarget(t, dir, "boom")
	if err == nil {
		t.Fatal("expected error from magus.fatal, got nil")
	}
	var ex types.ExitError
	if !errors.As(err, &ex) {
		t.Fatalf("expected types.ExitError, got %T: %v", err, err)
	}
	if ex.Code != 1 {
		t.Errorf("exit code = %d, want 1", ex.Code)
	}
}

// TestDependsOnDedup verifies magus.depends_on runs a duplicated target once —
// the footgun where a manually-listed target also matches an expand_globs glob.
func TestDependsOnDedup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.bzz")
	if err := os.WriteFile(path, []byte(`
import "magus";
import "magus/extra";

export fun dep(_a: [str]) > void { extra.os.execSh("printf x >> mark", ""); }
export fun top(_a: [str]) > void { magus.depends_on(["dep", "dep"]); }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runTarget(t, dir, "top"); err != nil {
		t.Fatalf("run top: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "mark"))
	if err != nil {
		t.Fatalf("dep did not run: %v", err)
	}
	if string(got) != "x" {
		t.Errorf("dep ran %d time(s) (mark=%q), want once", len(got), got)
	}
}

// TestMagusLoggingBuzz exercises the logging methods bound onto the magus
// namespace from a Buzz magusfile (with and without a fields map).
func TestMagusLoggingBuzz(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.bzz")
	if err := os.WriteFile(path, []byte(`
import "magus";

export fun logit(_a: [str]) > void {
    magus.info("hello");
    magus.debug("dbg", {"k": "v"});
    magus.warn("warn");
    magus.error("err");
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runTarget(t, dir, "logit"); err != nil {
		t.Fatalf("magus logging (buzz): %v", err)
	}
}

// TestMagusLoggingTeal exercises the same methods from a Teal magusfile (the
// Lua-engine harvest path).
func TestMagusLoggingTeal(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
global function logit(args: {string})
    magus.info("hello", {})
    magus.debug("dbg", {k = "v"})
    magus.warn("warn", {})
    magus.error("err", {})
end
`)
	if err := runTarget(t, dir, "logit"); err != nil {
		t.Fatalf("magus logging (teal): %v", err)
	}
}

func TestParseIncludesPathTargets(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
global function db_migrate(args: {string}) end
global function build(args: {string}) end
`)
	src, err := interp.Find(dir)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if src == nil {
		t.Fatal("Find: no source found")
	}

	targets, err := interp.Parse(context.Background(), src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	keys := make(map[string]bool, len(targets))
	for _, tgt := range targets {
		keys[tgt.Key] = true
	}
	if !keys["build"] {
		t.Errorf("Parse missing 'build': targets=%v", targets)
	}
	if !keys["db-migrate"] {
		t.Errorf("Parse missing 'db-migrate': targets=%v", targets)
	}
}

// TestTargetDependsOnExpandGlobs covers magus.target.expand_globs feeding a
// meta-target's depends_on: the matched targets run (sorted) before the body,
// and non-matching targets are skipped. With no pool in ctx the deps run
// sequentially in the current VM, so the order is deterministic.
func TestTargetDependsOnExpandGlobs(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
local function note(s: string)
   local f = io.open("ran", "a")
   if f then f:write(s .. "\n") f:close() end
end
global function go_build(_a: {string}) note("go-build") end
global function image_build(_a: {string}) note("image-build") end
global function go_test(_a: {string}) note("go-test") end
global function build(_a: {string})
   magus.depends_on(magus.target.expand_globs("*-build"))
   note("build-body")
end
`)
	if err := runTarget(t, dir, "build"); err != nil {
		t.Fatalf("run build: %v", err)
	}
	got, err := os.ReadFile(sentinel(dir))
	if err != nil {
		t.Fatalf("sentinel not created: %v", err)
	}
	// Deps (sorted) run before the body; go-test does not match "*-build".
	want := "go-build\nimage-build\nbuild-body\n"
	if string(got) != want {
		t.Errorf("run order = %q, want %q", got, want)
	}
	if strings.Contains(string(got), "go-test") {
		t.Errorf("go-test ran but does not match *-build: %q", got)
	}
}

// TestExpandGlobsReturnsSortedNames covers the return value of
// magus.target.expand_globs directly: sorted, deduped matches, and that a
// non-glob pattern is treated as the "*-<suffix>" shorthand.
func TestExpandGlobsReturnsSortedNames(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
global function image_build(_a: {string}) end
global function go_build(_a: {string}) end
global function go_test(_a: {string}) end
global function probe(_a: {string})
   local glob   = magus.target.expand_globs("*-build")
   local suffix = magus.target.expand_globs("build")
   local f = io.open("ran", "w")
   if f then
      f:write(table.concat(glob, ",") .. "|" .. table.concat(suffix, ","))
      f:close()
   end
end
`)
	if err := runTarget(t, dir, "probe"); err != nil {
		t.Fatalf("run probe: %v", err)
	}
	got, err := os.ReadFile(sentinel(dir))
	if err != nil {
		t.Fatalf("sentinel not created: %v", err)
	}
	// Both the glob ("*-build") and the suffix shorthand ("build") resolve to
	// the same sorted set; go-test is excluded.
	want := "go-build,image-build|go-build,image-build"
	if string(got) != want {
		t.Errorf("expand_globs = %q, want %q", got, want)
	}
}

// TestTargetNewBuzzIsGone verifies that magus.target.new no longer exists in
// the Buzz binding: a magusfile.bzz using it must error at runtime.
func TestTargetNewBuzzIsGone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.bzz")
	if err := os.WriteFile(path, []byte(`
import "magus";
magus.target.new("build", fun(_args: [str]) void {});
`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runTarget(t, dir, "build")
	if err == nil {
		t.Fatal("expected an error when using magus.target.new in buzz, got nil")
	}
}
