package bindings_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp"
	_ "github.com/egladman/magus/internal/interp/bindings"
	_ "github.com/egladman/magus/internal/interp/engine/lua/gopherlua"
	_ "github.com/egladman/magus/internal/interp/engine/lua/luajit"
	"github.com/egladman/magus/project"
)

// writeFile writes content under dir/rel, creating parent dirs.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// parseMagusfile evaluates the magusfile.tl in dir in parse mode, which fires
// its top-level magus.spell.* / magus.project.register calls.
func parseMagusfile(t *testing.T, dir string) error {
	t.Helper()
	srcs, err := interp.FindAll(dir)
	if err != nil {
		return err
	}
	for _, src := range srcs {
		if _, err := interp.Parse(context.Background(), src); err != nil {
			return err
		}
	}
	return nil
}

func TestSpellLoadRegistersForkSpell(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir) // magus.spell.load resolves the path relative to the cwd

	writeFile(t, dir, "spells/widget.tl", `
return {
    mgs_getName = function(): string return "widgetspell" end,
    mgs_listRequiredGlobs = function(_dir: string): {string}
        return {"**/*.ts", "package.json"}
    end,
    mgs_listProvidedGlobs = function(): {string} return {"dist/**"} end,
    mgs_listTargets = function(): any
        return { build = { cmd = "npm", args = {"run", "build"} } }
    end,
}
`)
	writeFile(t, dir, "magusfile.tl", `
local widget = magus.spell.load("spells/widget.tl")
magus.project.register(".", { spells = {widget} })
`)

	if err := parseMagusfile(t, dir); err != nil {
		t.Fatalf("parse: %v", err)
	}

	sp, ok := project.DefaultSpellRegistry().Lookup("widgetspell")
	if !ok {
		t.Fatal("widgetspell not registered after binding via project.register")
	}
	if !slices.Contains(sp.Targets(), "build") {
		t.Errorf("targets = %v, want to contain build", sp.Targets())
	}
	if !slices.Contains(sp.Sources(), "**/*.ts") {
		t.Errorf("sources = %v, want to contain **/*.ts", sp.Sources())
	}

	// Idempotent: requiring the same spell again must not panic or error.
	if err := parseMagusfile(t, dir); err != nil {
		t.Fatalf("second parse (idempotency): %v", err)
	}
}

// TestSpellLoadHandleExposesTargetMethods verifies the handle magus.spell.load
// returns carries a callable method per fork target, so a magusfile can
// delegate to hello.build() directly (the idiom the starter demonstrates).
func TestSpellLoadHandleExposesTargetMethods(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "spells/widget.tl", `
return {
    mgs_getName = function(): string return "widgethandle" end,
    mgs_listTargets = function(): any
        return { build = { cmd = "true" } }
    end,
}
`)
	// The build target invokes the handle method; a missing method would raise.
	writeFile(t, dir, "magusfile.tl", `
local widget = magus.spell.load("spells/widget.tl")
global function go(_args: {string})
    assert(type(widget.build) == "function", "load handle missing build method")
    widget.build()
end
`)

	srcs, err := interp.FindAll(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := interp.Run(context.Background(), srcs[0], "go", nil, dir); err != nil {
		t.Fatalf("invoking load-handle method: %v", err)
	}
}

// TestSpellLoadHandleListTargets verifies listTargets() on a loaded handle returns
// the runnable target names — the introspection complement to the per-target
// methods, which are how ops are actually invoked.
func TestSpellLoadHandleListTargets(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "spells/widget.tl", `
return {
    mgs_getName = function(): string return "widgetdispatch" end,
    mgs_listTargets = function(): any
        return { build = { cmd = "true" } }
    end,
}
`)
	writeFile(t, dir, "magusfile.tl", `
local widget = magus.spell.load("spells/widget.tl")
global function go(_args: {string})
    local names = widget.listTargets()
    assert(#names == 1 and names[1] == "build", "listTargets mismatch")
    widget.build()
end
`)

	srcs, err := interp.FindAll(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := interp.Run(context.Background(), srcs[0], "go", nil, dir); err != nil {
		t.Fatalf("listTargets: %v", err)
	}
}

func TestSpellDefineRegistersInlineSpell(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "magusfile.tl", `
local inline = magus.spell.define {
    name = "inlinespell",
    needs = function(_dir: string): {string} return {"**/*.ts"} end,
    provides = function(): {string} return {"out/**"} end,
    ops = {
        build = { cmd = "make", args = {"build"} },
    },
}
magus.project.register(".", { spells = {inline} })
`)

	if err := parseMagusfile(t, dir); err != nil {
		t.Fatalf("parse: %v", err)
	}
	sp, ok := project.DefaultSpellRegistry().Lookup("inlinespell")
	if !ok {
		t.Fatal("inlinespell not registered after binding via project.register")
	}
	if !slices.Contains(sp.Targets(), "build") {
		t.Errorf("targets = %v, want to contain build", sp.Targets())
	}
}

func TestSpellLoadFunctionOpsAreIgnored(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// A function value in the ops table is not a valid {cmd,args} spec and is
	// silently skipped. The spell still registers with no ops (not a failure).
	writeFile(t, dir, "spells/fnspell.tl", `
return {
    mgs_getName = function(): string return "fnspell" end,
    mgs_listTargets = function(): any
        return { build = { cmd = "true" } }
    end,
}
`)
	writeFile(t, dir, "magusfile.tl", `
local fnspell = magus.spell.load("spells/fnspell.tl")
magus.project.register(".", { spells = {fnspell} })
`)

	if err := parseMagusfile(t, dir); err != nil {
		t.Fatalf("parse should not fail: %v", err)
	}
	if _, ok := project.DefaultSpellRegistry().Lookup("fnspell"); !ok {
		t.Error("fnspell should be registered")
	}
}

// TestSpellRequireTypedAccess exercises the require("magus.spell.<name>") idiom:
// built-in spells are reachable as typed modules — the magusfile both type-checks
// and resolves the spell methods at run time — while a misspelled module name is
// a compile-time error rather than a runtime nil.
func TestSpellRequireTypedAccess(t *testing.T) {
	ctx := context.Background()

	t.Run("require resolves and exposes methods", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "magusfile.tl", `
local go = require("magus.spell.go")
global function check(_args: {string})
   assert(go ~= nil, "go spell not found")
   assert(type(go["go-build"]) == "function", "go-build is not a function")
end
`)
		srcs, err := interp.FindAll(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := interp.Run(ctx, srcs[0], "check", nil, dir); err != nil {
			t.Fatalf("running require magusfile: %v", err)
		}
	})

	t.Run("misspelled module is a compile error", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "magusfile.tl", `
local _ = require("magus.spell.nonexistent_spell")
`)
		err := parseMagusfile(t, dir)
		if err == nil {
			t.Fatal("expected a compile error for the misspelled module, got nil")
		}
		if !strings.Contains(err.Error(), "module not found") {
			t.Fatalf("expected 'module not found' compile error, got: %v", err)
		}
	})
}

// TestSpellLoadRegistersForkBuzzSpell exercises magus.spell.load dispatching
// to the Buzz engine for a .bzz workspace-local spell, registered by value when
// bound via magus.project.register.
func TestSpellLoadRegistersForkBuzzSpell(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir) // magus.spell.load resolves the path relative to the cwd

	writeFile(t, dir, "spells/widget.bzz", `export fun mgs_getName() > str { return "widgetbuzzspell"; }
export fun mgs_listRequiredGlobs(_dir: str) > [str] { return ["**/*.ts", "package.json"]; }
export fun mgs_listProvidedGlobs() > [str] { return ["dist/**"]; }
export fun mgs_listTargets() > any {
    return {"build": {"cmd": "npm", "args": ["run", "build"]}};
}
`)
	writeFile(t, dir, "magusfile.bzz", `import "magus";
const widget = magus.spell.load("spells/widget.bzz");
magus.project.register(".", {"spells": [widget]});`)

	if err := parseMagusfile(t, dir); err != nil {
		t.Fatalf("parse: %v", err)
	}

	sp, ok := project.DefaultSpellRegistry().Lookup("widgetbuzzspell")
	if !ok {
		t.Fatal("widgetbuzzspell not registered after binding via project.register")
	}
	if !slices.Contains(sp.Targets(), "build") {
		t.Errorf("targets = %v, want to contain build", sp.Targets())
	}
	if !slices.Contains(sp.Sources(), "**/*.ts") {
		t.Errorf("sources = %v, want to contain **/*.ts", sp.Sources())
	}

	// Idempotent: loading the same spell again must not panic or error.
	if err := parseMagusfile(t, dir); err != nil {
		t.Fatalf("second parse (idempotency): %v", err)
	}
}

// TestBuzzLocalSpellImport verifies a workspace-local Buzz spell is importable by
// path — `import "spells/widget"` resolves ./spells/widget.bzz, binds the handle
// under the basename (widget), and binding it via magus.project.register registers
// the spell by value. The import sugar for magus.spell.load on that path.
func TestBuzzLocalSpellImport(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir) // the import resolves relative to the cwd

	writeFile(t, dir, "spells/widget.bzz", `export fun mgs_getName() > str { return "widgetimport"; }
export fun mgs_listRequiredGlobs(_dir: str) > [str] { return ["**/*.ts"]; }
export fun mgs_listTargets() > any {
    return {"build": {"cmd": "npm", "args": ["run", "build"]}};
}
`)
	writeFile(t, dir, "magusfile.bzz", `import "magus";
import "spells/widget";
magus.project.register(".", {"spells": [widget]});`)

	if err := parseMagusfile(t, dir); err != nil {
		t.Fatalf("parse: %v", err)
	}

	sp, ok := project.DefaultSpellRegistry().Lookup("widgetimport")
	if !ok {
		t.Fatal("widgetimport not registered after import + project.register")
	}
	if !slices.Contains(sp.Targets(), "build") {
		t.Errorf("targets = %v, want to contain build", sp.Targets())
	}
	if !slices.Contains(sp.Sources(), "**/*.ts") {
		t.Errorf("sources = %v, want to contain **/*.ts", sp.Sources())
	}

	// Idempotent: re-parsing must not panic or error.
	if err := parseMagusfile(t, dir); err != nil {
		t.Fatalf("second parse (idempotency): %v", err)
	}
}

// TestBuzzSpellImport verifies that a built-in spell is importable via
// `import "magus/spell/<name>"` in a Buzz magusfile, binding the spell handle
// under its basename (go, docker, etc.) with the expected name and callable ops.
func TestBuzzSpellImport(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "magusfile.bzz", `import "magus";
import "magus/spell/go";
import "magus/spell/json";

export fun check(args: [str]) > void {
    if (go.name != "go") { error("go.name mismatch: " + go.name); }
    if (json.name != "json") { error("json.name mismatch: " + json.name); }
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

// TestSpellLoadBuzzNoOps verifies that a Buzz spell with no ops still registers
// successfully — ops is optional; the spell is registered with an empty target list.
func TestSpellLoadBuzzNoOps(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "spells/noops.bzz", `export fun mgs_getName() > str { return "noopsbuzzspell"; }
`)
	writeFile(t, dir, "magusfile.bzz", `import "magus";
const noops = magus.spell.load("spells/noops.bzz");
magus.project.register(".", {"spells": [noops]});`)

	if err := parseMagusfile(t, dir); err != nil {
		t.Fatalf("parse should not fail: %v", err)
	}
	if _, ok := project.DefaultSpellRegistry().Lookup("noopsbuzzspell"); !ok {
		t.Error("noopsbuzzspell should be registered even with no ops")
	}
}

// TestBuzzSpellMethodForwardsOpts verifies a Buzz spell handle's per-target method
// (widget.capture(opts)): opts.args are appended to the target's base argv and the
// command runs in opts.cwd — what lets a magusfile drive a flag-carrying tool (e.g.
// docker.build({cwd: "..", args: [...]})) through the spell instead of os.exec. It
// also checks listTargets() still exposes the op names for introspection.
func TestBuzzSpellMethodForwardsOpts(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// The "capture" target writes its forwarded args ($@) into captured.txt in the
	// process cwd, so the test can assert both the args and the working directory.
	writeFile(t, dir, "spells/widget.bzz", `export fun mgs_getName() > str { return "optswidget"; }
export fun mgs_listTargets() > any {
    return {"capture": {"cmd": "sh", "args": ["-c", "printf '%s ' \"$@\" > captured.txt", "sh"]}};
}
`)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "magusfile.bzz", `import "magus";
const widget = magus.spell.load("spells/widget.bzz");
export fun build(args: [str]) > void {
    const names = widget.listTargets();
    if (names[0] != "capture") { error("listTargets mismatch"); }
    widget.capture({"cwd": "sub", "args": ["alpha", "beta"]});
}`)

	srcs, err := interp.FindAll(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := interp.Run(context.Background(), srcs[0], "build", nil, dir); err != nil {
		t.Fatalf("invoking spell method with opts: %v", err)
	}

	// The file must land in opts.cwd (sub/), proving cwd was honored, and contain
	// exactly the appended args, proving opts.args reached the forked command.
	got, err := os.ReadFile(filepath.Join(dir, "sub", "captured.txt"))
	if err != nil {
		t.Fatalf("expected captured.txt under opts.cwd: %v", err)
	}
	if want := "alpha beta "; string(got) != want {
		t.Errorf("forwarded args = %q, want %q", string(got), want)
	}
}

// TestBuzzSpellMethodEnv verifies the env opt overlays the forked subprocess
// environment — the cross-compile vars (GOOS/GOARCH/CGO_ENABLED) a release build
// needs. The capture target echoes $MYVAR into out.txt; the call sets it via env,
// proving the overlay reached the subprocess.
func TestBuzzSpellMethodEnv(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "spells/widget.bzz", `export fun mgs_getName() > str { return "envwidget"; }
export fun mgs_listTargets() > any {
    return {"capture": {"cmd": "sh", "args": ["-c", "printf \"$MYVAR\" > out.txt"]}};
}
`)
	writeFile(t, dir, "magusfile.bzz", `import "magus";
const widget = magus.spell.load("spells/widget.bzz");
export fun build(args: [str]) > void {
    widget.capture({"env": {"MYVAR": "overridden"}});
}`)

	srcs, err := interp.FindAll(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := interp.Run(context.Background(), srcs[0], "build", nil, dir); err != nil {
		t.Fatalf("invoking spell method with env: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("expected out.txt: %v", err)
	}
	if want := "overridden"; string(got) != want {
		t.Errorf("out.txt = %q, want %q", string(got), want)
	}
}

// TestSpellCaptureReturnsRecord verifies that a target declared capture=true
// returns the {stdout, stderr, code, ok} record to the magusfile (the same shape
// os.exec returns) rather than void — the facet VCS query targets build on.
func TestSpellCaptureReturnsRecord(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// "build" is a known op name in the built-in union, so it type-checks on the
	// MagusSpell handle; capture=true makes it return the record at runtime.
	writeFile(t, dir, "spells/widget.tl", `
return {
    mgs_getName = function(): string return "capwidget" end,
    mgs_listTargets = function(): any
        return { build = { cmd = "sh", args = {"-c", "printf abc123"}, capture = true } }
    end,
}
`)
	writeFile(t, dir, "magusfile.tl", `
local widget = magus.spell.load("spells/widget.tl")
global function go(_args: {string})
    local r = widget.build()
    assert(r.stdout == "abc123", "stdout = " .. tostring(r.stdout))
    assert(r.code == 0, "code = " .. tostring(r.code))
    assert(r.ok == true, "ok = " .. tostring(r.ok))
end
`)

	srcs, err := interp.FindAll(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := interp.Run(context.Background(), srcs[0], "go", nil, dir); err != nil {
		t.Fatalf("captured target: %v", err)
	}
}

// TestBuzzSpellCaptureReturnsRecord is the Buzz twin of TestSpellCaptureReturnsRecord:
// a capture=true target returns the record map, accessed with dot syntax the way a
// magusfile reads os.exec(...).stdout.
func TestBuzzSpellCaptureReturnsRecord(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "spells/widget.bzz", `export fun mgs_getName() > str { return "capbuzzwidget"; }
export fun mgs_listTargets() > any {
    return {"hash": {"cmd": "sh", "args": ["-c", "printf abc123"], "capture": true}};
}
`)
	writeFile(t, dir, "magusfile.bzz", `import "magus";
const widget = magus.spell.load("spells/widget.bzz");
export fun build(args: [str]) > void {
    const r = widget.hash();
    if (r.stdout != "abc123") { error("stdout mismatch: " + r.stdout); }
    if (r.code != 0) { error("code mismatch"); }
    if (r.ok != true) { error("ok mismatch"); }
}`)

	srcs, err := interp.FindAll(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := interp.Run(context.Background(), srcs[0], "build", nil, dir); err != nil {
		t.Fatalf("captured buzz target: %v", err)
	}
}

// TestBuzzSpellPipeStdin verifies pipe-style chaining: a captured target's stdout
// is fed as the stdin of the next target via opts.stdin — the Unix-pipe primitive.
func TestBuzzSpellPipeStdin(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "spells/widget.bzz", `export fun mgs_getName() > str { return "pipewidget"; }
export fun mgs_listTargets() > any {
    return {
        "emit": {"cmd": "sh", "args": ["-c", "printf alpha"], "capture": true},
        "shout": {"cmd": "tr", "args": ["a-z", "A-Z"], "capture": true}
    };
}
`)
	writeFile(t, dir, "magusfile.bzz", `import "magus";
const widget = magus.spell.load("spells/widget.bzz");
export fun build(args: [str]) > void {
    const a = widget.emit();
    const b = widget.shout({"stdin": a.stdout});
    if (b.stdout != "ALPHA") { error("pipe mismatch: " + b.stdout); }
}`)

	srcs, err := interp.FindAll(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := interp.Run(context.Background(), srcs[0], "build", nil, dir); err != nil {
		t.Fatalf("pipe stdin: %v", err)
	}
}

// TestVcsCommitFacadeBuzz exercises the vcs.commit() facade end-to-end the way
// magusfile.bzz build_date() does — including the (c.committer ?? c.author)
// fallback that keeps it agnostic across VCSes.
func TestVcsCommitFacadeBuzz(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	for _, args := range [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "a@b.c"},
		{"git", "config", "user.name", "A"},
		{"git", "config", "commit.gpgsign", "false"},
		{"git", "commit", "--allow-empty", "-m", "hello"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", args, out)
		}
	}

	writeFile(t, dir, "magusfile.bzz", `import "magus";
import "magus/extra";
export fun check(args: [str]) > void {
    const c = extra.vcs.commit();
    if (c.subject != "hello") { error("subject: " + c.subject); }
    if (c.author.name != "A") { error("author: " + c.author.name); }
    if (c.date == "") { error("date empty"); }
    if (c.id == "") { error("id empty"); }
}`)

	srcs, err := interp.FindAll(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := interp.Run(context.Background(), srcs[0], "check", nil, dir); err != nil {
		t.Fatalf("vcs.commit facade: %v", err)
	}
}

// TestVcsCommitNullOutsideRepo pins the new contract that powers build_date's
// fallback: outside any repository, vcs.commit() is null (not an empty record).
func TestVcsCommitNullOutsideRepo(t *testing.T) {
	dir := t.TempDir() // a bare temp dir, not under version control
	t.Chdir(dir)
	writeFile(t, dir, "magusfile.bzz", `import "magus";
import "magus/extra";
export fun check(args: [str]) > void {
    if (extra.vcs.commit() != null) { error("expected null outside a repo"); }
}`)
	srcs, err := interp.FindAll(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := interp.Run(context.Background(), srcs[0], "check", nil, dir); err != nil {
		t.Fatalf("vcs.commit null case: %v", err)
	}
}
