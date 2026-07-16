package interp_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeMagusfile(t *testing.T, dir, body string) {
	t.Helper()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

func runTarget(t *testing.T, dir, target string, args ...string) error {
	t.Helper()
	ctx := context.Background()
	src, err := interp.Find(dir)
	require.NoError(t, err)
	require.NotNil(t, src, "Find: no source found")
	return interp.Run(ctx, src, target, args, dir)
}

func sentinel(dir string) string { return filepath.Join(dir, "ran") }

func TestRunTopLevelTarget(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
import "fs";
export fun build(args: [str]) > void {
    fs.writeFile("ran", "build");
}
`)
	require.NoError(t, runTarget(t, dir, "build"))
	got, err := os.ReadFile(sentinel(dir))
	require.NoError(t, err, "sentinel not created")
	assert.Equal(t, "build", string(got))
}

func TestRunPathTarget(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
import "fs";
export fun db_migrate(args: [str]) > void {
    fs.writeFile("ran", "db:migrate");
}
`)
	require.NoError(t, runTarget(t, dir, "db:migrate"))
	got, err := os.ReadFile(sentinel(dir))
	require.NoError(t, err, "sentinel not created")
	assert.Equal(t, "db:migrate", string(got))
}

// TestRunImportTargetCollision verifies a target whose name collides with an imported
// module's bound name fails at load with a clear shadow error, not a silent null when
// the target later reads a member off the shadowed module.
func TestRunImportTargetCollision(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
import "fs" as render;
export fun render(args: [str]) > void {
    render.writeFile("ran", "x");
}
`)
	err := runTarget(t, dir, "render")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `target "render" shadows the module import "fs"`)
}

// TestRunImportsMagusfilesSibling verifies a magusfile resolves a plain
// `import "<name>"` against the project's magusfiles/ directory (magus's
// override of gopherbuzz's default search paths). The helper lives in a
// magusfiles/ subdirectory so it is not auto-loaded as a magusfile source.
func TestRunImportsMagusfilesSibling(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "magusfiles", "lib"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfiles", "lib", "calc.buzz"),
		[]byte(`export final tag = "calc-ok";`), 0o644))
	writeMagusfile(t, dir, `
import "magus";
import "fs";
import "lib/calc";
export fun build(args: [str]) > void {
    fs.writeFile("ran", tag);
}
`)
	require.NoError(t, runTarget(t, dir, "build"))
	got, err := os.ReadFile(sentinel(dir))
	require.NoError(t, err, "sentinel not created")
	assert.Equal(t, "calc-ok", string(got))
}

// TestRunBuzzStdModule exercises the std host surface from a magusfile.buzz
// end-to-end: the magus-utils bindings-emitted host/gen trampolines must decode a variadic
// call (fs.join), a slice-in/map-out call (charm.append), and a void call
// (fs.writeFile). Modules are reached under bare module imports (fs.join,
// charm.append), with camelCase methods (Buzz's convention).
func TestRunBuzzStdModule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
import "fs";
import "charm";

export fun verify(_opts: [str]) > void {
    var joined = fs.join("a", "b", "c");
    var patch = charm.append(["y", "z"]);
    fs.writeFile("ran", joined + "|" + patch.ops[1].value);
}
`), 0o644))
	require.NoError(t, runTarget(t, dir, "verify"))
	got, err := os.ReadFile(sentinel(dir))
	require.NoError(t, err, "sentinel not created")
	assert.Equal(t, "a/b/c|z", string(got))
}

// TestRunBuzzMarkdownModule proves the markdown host module is reachable under
// the bare module import (markdown.toHtml), so a docs-site project can
// render Markdown to HTML in its own magusfile target.
func TestRunBuzzMarkdownModule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
import "fs";
import "markdown";

export fun verify(_opts: [str]) > void {
    fs.writeFile("ran", markdown.toHtml("# Hi"));
}
`), 0o644))
	require.NoError(t, runTarget(t, dir, "verify"))
	got, err := os.ReadFile(sentinel(dir))
	require.NoError(t, err, "sentinel not created")
	assert.Contains(t, string(got), `id="hi"`)
	assert.Contains(t, string(got), "Hi</h1>")
}

// TestRunBuzzFmtSprintf exercises fmt.sprintf end-to-end. It is the first std
// method with a variadic arg preceded by a fixed one (format), so it guards the
// magus-utils bindings lua/buzz variadic-offset decode in addition to the formatting itself.
func TestRunBuzzFmtSprintf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
import "fmt";
import "fs";

export fun verify(_opts: [str]) > void {
    var asset = fmt.sprintf("magus_%s_%s_%s.tar.gz", "1.0", "linux", "amd64");
    var none = fmt.sprintf("literal");
    fs.writeFile("ran", asset + "|" + none);
}
`), 0o644))
	require.NoError(t, runTarget(t, dir, "verify"))
	got, err := os.ReadFile(sentinel(dir))
	require.NoError(t, err, "sentinel not created")
	assert.Equal(t, "magus_1.0_linux_amd64.tar.gz|literal", string(got))
}

// TestRunBuzzAggregateUtil proves the magus host utilities resolve under bare
// module imports (fs.join / os.execSh, in camelCase) and coexist with Buzz's own
// stdlib in the same file: the magus fs/os methods are layered onto the bare
// fs/os modules, while hashing uses the stdlib `crypto.hash`.
func TestRunBuzzAggregateUtil(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
import "fs";
import "os";
import "crypto";

export fun verify(_opts: [str]) > void {
    var joined = fs.join("a", "b", "c");
    var res = os.execSh("printf hello").stdout;
    var digest = crypto.hash(crypto.HashAlgorithm.Sha256, "");
    fs.writeFile("ran", joined + "|" + res + "|" + digest);
}
`), 0o644))
	require.NoError(t, runTarget(t, dir, "verify"))
	got, err := os.ReadFile(sentinel(dir))
	require.NoError(t, err, "sentinel not created")
	want := "a/b/c|hello|e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	assert.Equal(t, want, string(got))
}

func TestRunTargetWithArgs(t *testing.T) {
	dir := t.TempDir()
	// Forwarded args are spread as positional parameters to a Buzz target, so the
	// target declares one parameter per forwarded arg.
	writeMagusfile(t, dir, `
import "magus";
import "fs";
export fun db_migrate(a: str, b: str, c: str) > void {
    fs.writeFile("ran", a + " " + b + " " + c);
}
`)
	require.NoError(t, runTarget(t, dir, "db:migrate", "a", "b", "c"))
	got, err := os.ReadFile(sentinel(dir))
	require.NoError(t, err, "sentinel not created")
	assert.Equal(t, "a b c", string(got))
}

func TestRunTargetReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
export fun db_migrate(args: [str]) > void {
    throw "boom";
}
`)
	err := runTarget(t, dir, "db:migrate")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestRunUnknownTarget(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
export fun db_migrate(args: [str]) > void {}
`)
	err := runTarget(t, dir, "no-such-target")
	assert.Error(t, err, "expected non-nil error for unknown target")
}

// TestParseLocalSpellFromOtherDir verifies a magusfile that require/imports a
// workspace-local spell parses (preloads) when its directory is not the process
// cwd — the workspace-preload case that used to fail with "module not found"
// (Teal) / "undefined variable" (Buzz) because local-spell lookup was cwd-relative.
func TestParseLocalSpellFromOtherDir(t *testing.T) {
	spell := `export fun mgs_getName() > str { return "hello"; }
export fun mgs_listTargets() > any { return {"build": {"bin": "echo", "args": ["hi"]}}; }`
	magusfile := `import "magus";
import "spells/hello";
export fun go(_a: [str]) > void { hello.build(); }`

	proj := filepath.Join(t.TempDir(), "proj")
	require.NoError(t, os.MkdirAll(filepath.Join(proj, "spells"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "spells", "hello.buzz"), []byte(spell), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "magusfile.buzz"), []byte(magusfile), 0o644))
	// Parse from the test's cwd, NOT from proj — exactly what workspace
	// preload does when it visits a sub-project's magusfile.
	srcs, err := interp.FindAll(proj)
	require.NoError(t, err)
	require.NotEmpty(t, srcs, "FindAll: no sources")
	for _, src := range srcs {
		_, err := interp.Parse(context.Background(), src)
		require.NoError(t, err, "Parse local-spell magusfile from other dir")
	}
}

// TestNeedsNonTargetFunctionFails verifies a needs on a function that is not an
// exported target - a non-exported helper, or a typo that resolves to some other
// in-scope function - fails fast rather than silently no-op'ing. (A name that
// resolves to nothing at all is a Buzz undefined-variable error even earlier; this
// guards the reachable footgun of passing a real function value that names no target.)
func TestNeedsNonTargetFunctionFails(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
fun helper(args: [str]) > void {}
export fun top(args: [str]) > void {
    magus.needs(helper);
}
`)
	err := runTarget(t, dir, "top")
	require.Error(t, err, "expected an error for a needs on a non-target function")
	assert.Contains(t, err.Error(), "does not name an exported target")
}

// TestNeedsForwardReference verifies a magus.needs on an exported target declared
// LATER in the file resolves: target bodies run post-load, so the forward reference
// to the later export is already bound by the time top runs.
func TestNeedsForwardReference(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
import "fs";
export fun top(_a: [str]) > void {
    magus.needs(dep);
    fs.writeFile("ran", "top");
}
export fun dep(_a: [str]) > void { fs.writeFile("dep-ran", "dep"); }
`)
	require.NoError(t, runTarget(t, dir, "top"))
	_, err := os.Stat(filepath.Join(dir, "dep-ran"))
	require.NoError(t, err, "forward-referenced dependency did not run")
}

// TestNeedsStringArgumentFails verifies magus.needs rejects a bare string - the
// classic footgun - and points the author at magus.needsGlob for patterns.
func TestNeedsStringArgumentFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
export fun dep(_a: [str]) > void {}
export fun top(_a: [str]) > void { magus.needs("dep"); }
`), 0o644))
	err := runTarget(t, dir, "top")
	require.Error(t, err, "expected an error for a string argument to magus.needs")
	assert.Contains(t, err.Error(), "must be a target function")
}

// TestNeedsAnonymousFunctionFails verifies magus.needs rejects an anonymous function
// literal: it names no target, so it cannot declare a dependency.
func TestNeedsAnonymousFunctionFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
export fun top(_a: [str]) > void { magus.needs(fun (_a: [str]) > void {}); }
`), 0o644))
	err := runTarget(t, dir, "top")
	require.Error(t, err, "expected an error for an anonymous function argument to magus.needs")
	assert.Contains(t, err.Error(), "anonymous function is not a target")
}

// TestRunBuzzTargetNameCollision is the Buzz counterpart: two exports that
// normalize to the same canonical target must error.
func TestRunBuzzTargetNameCollision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";

export fun foo_bar(_a: [str]) > void {}
export fun fooBar(_a: [str]) > void {}
`), 0o644))
	err := runTarget(t, dir, "foo-bar")
	require.Error(t, err, "expected collision error")
	assert.Contains(t, err.Error(), "foo-bar")
	assert.Contains(t, err.Error(), "normalize")
}

// TestOsExitRaisesExitError verifies os.exit(code) aborts the target with a
// types.ExitError carrying the code (it must NOT call os.Exit), and that the
// typed error survives the VM boundary so the CLI/daemon can honor the code.
func TestOsExitRaisesExitError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
import "os";

export fun bail(_a: [str]) > void { os.exit(3); }
`), 0o644))
	err := runTarget(t, dir, "bail")
	require.Error(t, err, "expected error from os.exit")
	var ex types.ExitError
	require.ErrorAs(t, err, &ex)
	assert.Equal(t, 3, ex.Code)
}

// TestOsSleep exercises os.sleep (milliseconds, matching Buzz) from a Buzz
// magusfile, confirming the TypeFloat binding path works for fractional and int
// literals and returns.
func TestOsSleep(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
import "os";

export fun nap(_a: [str]) > void {
    os.sleep(1.5);
    os.sleep(0);
}
`), 0o644))
	require.NoError(t, runTarget(t, dir, "nap"))
}

// TestOsWhich verifies os.which resolves a real command to a non-empty path and
// returns "" for a missing one (asserted inside the magusfile via os.exit).
func TestOsWhich(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
import "os";

export fun checkwhich(_a: [str]) > void {
    if (os.which("sh") == "") { os.exit(2); }
    if (os.which("definitely-no-such-cmd-zzz") != "") { os.exit(3); }
}
`), 0o644))
	require.NoError(t, runTarget(t, dir, "checkwhich"))
}

// TestMagusHint confirms magus.hint is callable (and a repeated message is
// tolerated — dedup happens in the shared channel).
func TestMagusHint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";

export fun nudge(_a: [str]) > void {
    magus.hint("stale generated code — run: magus run generate -- --write");
    magus.hint("stale generated code — run: magus run generate -- --write");
}
`), 0o644))
	require.NoError(t, runTarget(t, dir, "nudge"))
}

// TestMagusFatal verifies magus.fatal aborts with a types.ExitError carrying
// code 1 (Buzz preserves the typed error across the boundary).
func TestMagusFatal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";

export fun boom(_a: [str]) > void { magus.fatal("boom"); }
`), 0o644))
	err := runTarget(t, dir, "boom")
	require.Error(t, err, "expected error from magus.fatal")
	var ex types.ExitError
	require.ErrorAs(t, err, &ex)
	assert.Equal(t, 1, ex.Code)
}

// TestOsExecShShellOption verifies opts.shell is accepted and the chosen shell
// runs (sh is always present; the flag/derivation is unit-tested in host).
func TestOsExecShShellOption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
import "os";

export fun viash(_a: [str]) > void {
    os.execSh("true", "", {"shell": "sh"});
}
`), 0o644))
	require.NoError(t, runTarget(t, dir, "viash"))
}

// TestNeedsDedup verifies magus.needs runs a duplicated target once — the footgun
// where the same exported target is listed more than once in one needs call.
func TestNeedsDedup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
import "os";

export fun dep(_a: [str]) > void { os.execSh("printf x >> mark", ""); }
export fun top(_a: [str]) > void { magus.needs(dep, dep); }
`), 0o644))
	require.NoError(t, runTarget(t, dir, "top"))
	got, err := os.ReadFile(filepath.Join(dir, "mark"))
	require.NoError(t, err, "dep did not run")
	assert.Equal(t, "x", string(got), "dep should run once")
}

// TestMagusLoggingBuzz exercises the logging methods bound onto the magus
// namespace from a Buzz magusfile (with and without a fields map).
func TestMagusLoggingBuzz(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";

export fun logit(_a: [str]) > void {
    magus.info("hello");
    magus.debug("dbg", {"k": "v"});
    magus.warn("warn");
    magus.error("err");
}
`), 0o644))
	require.NoError(t, runTarget(t, dir, "logit"))
}

func TestParseIncludesPathTargets(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
export fun db_migrate(args: [str]) > void {}
export fun build(args: [str]) > void {}
`)
	src, err := interp.Find(dir)
	require.NoError(t, err)
	require.NotNil(t, src, "Find: no source found")

	targets, err := interp.Parse(context.Background(), src)
	require.NoError(t, err)

	keys := make(map[string]bool, len(targets))
	for _, tgt := range targets {
		keys[tgt.Key] = true
	}
	assert.True(t, keys["build"], "Parse missing 'build'")
	assert.True(t, keys["db-migrate"], "Parse missing 'db-migrate'")
}

// TestNeedsGlobHandle covers magus.needsGlob feeding a meta-target: the matched
// targets run (sorted) before the body, and non-matching targets are skipped. With
// no pool in ctx the deps run sequentially in the current VM, so the order is
// deterministic.
func TestNeedsGlobHandle(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
import "os";
fun note(s: str) > void {
   os.execSh("printf '%s\n' " + s + " >> ran", "");
}
export fun go_build(_a: [str]) > void { note("go-build"); }
export fun image_build(_a: [str]) > void { note("image-build"); }
export fun go_test(_a: [str]) > void { note("go-test"); }
export fun build(_a: [str]) > void {
   magus.needsGlob("*-build");
   note("build-body");
}
`)
	require.NoError(t, runTarget(t, dir, "build"))
	got, err := os.ReadFile(sentinel(dir))
	require.NoError(t, err, "sentinel not created")
	// Deps (sorted) run before the body; go-test does not match "*-build".
	want := "go-build\nimage-build\nbuild-body\n"
	assert.Equal(t, want, string(got))
	assert.NotContains(t, string(got), "go-test", "go-test ran but does not match *-build")
}

// TestTargetNamespaceIsGone verifies the removed magus.target.* query namespace no
// longer exists: a magusfile referencing it must error at runtime (magus.target is
// undefined), so the old needs-by-query API cannot silently linger.
func TestTargetNamespaceIsGone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
export fun build(_a: [str]) > void { magus.needs(magus.target.literal("build")); }
`), 0o644))
	err := runTarget(t, dir, "build")
	assert.Error(t, err, "expected an error: the magus.target namespace was removed")
}

// TestRunRelativeFsResolvesToProjectDir verifies the chdir->ctx-cwd change: a
// magusfile target running from a process cwd that is NOT the project dir still
// resolves relative filesystem paths (read/write/mkdir/glob/copy/walk) against
// the project dir, and glob/walk return paths relative to it. This is the
// regression guard for the deserialization that removed os.Chdir.
func TestRunRelativeFsResolvesToProjectDir(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
import "fs";
export fun build(args: [str]) > void {
    fs.mkdirall("sub");
    fs.writeFile("sub/a.txt", "alpha");
    fs.copyFile("sub/a.txt", "sub/b.txt");
    // glob must return paths relative to the project dir, sorted.
    var hits = fs.glob("sub/*.txt");
    var acc = "";
    var first = true;
    foreach (h in hits) {
        if (!first) { acc = acc + ","; }
        acc = acc + h;
        first = false;
    }
    fs.writeFile("glob.out", acc);
}
`)
	require.NoError(t, runTarget(t, dir, "build"))

	// Files landed in the project dir, not the process cwd.
	for _, rel := range []string{"sub/a.txt", "sub/b.txt", "glob.out"} {
		_, err := os.Stat(filepath.Join(dir, rel))
		require.NoErrorf(t, err, "%s not created in project dir", rel)
	}
	got, err := os.ReadFile(filepath.Join(dir, "glob.out"))
	require.NoError(t, err)
	// Relative, sorted — not absolute paths.
	assert.Equal(t, "sub/a.txt,sub/b.txt", string(got))
}
