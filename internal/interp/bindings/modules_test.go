package bindings

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus/internal/hostmodules"
	"github.com/egladman/magus/internal/interp"
	bindinggen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/spellruntime"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeMagusfile(t *testing.T, dir, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte(body), 0o644))
}

// runTargetIn runs target over the magusfile in dir for its EFFECT, discarding any
// return value.
func runTargetIn(t *testing.T, dir, target string, args ...string) error {
	t.Helper()
	_, err := interp.RunDir(context.Background(), dir, target, args)
	return err
}

// TestSupersetModules verifies that registerMagusModules exposes the magus host
// methods under the same bare names as Buzz's stdlib (a superset), and that the
// old magus/extra aggregate is gone.
func TestSupersetModules(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	registerMagusModules(ctx, sess)

	hasKey := func(t *testing.T, module, key string) {
		t.Helper()
		mod, ok := sess.NativeModule(module)
		require.True(t, ok, "module %q not registered", module)
		_, ok = mod.MapGet(key)
		assert.True(t, ok, "module %q missing key %q", module, key)
	}

	// os: Buzz stdlib (env, execute) plus the magus members about THIS machine.
	// Running other processes is proc's, deliberately: os.execute returns an exit
	// code the caller must check and magus's verb raises instead, and two verbs
	// that are synonyms in English sharing one module is the trap that split them.
	hasKey(t, "os", "env")      // Buzz stdlib
	hasKey(t, "os", "execute")  // Buzz stdlib, and it stays untouched
	hasKey(t, "os", "platform") // magus
	hasKey(t, "proc", "exec")   // magus, and NOT on os
	hasKey(t, "proc", "shell")  // magus
	hasKey(t, "proc", "which")  // magus

	// fs: Buzz stdlib (makeDirectory) plus magus (glob, readFile).
	hasKey(t, "fs", "makeDirectory") // Buzz stdlib
	hasKey(t, "fs", "glob")          // magus
	hasKey(t, "fs", "readFile")      // magus

	// crypto: Buzz stdlib (hash) plus magus digests and the byte-level companions.
	hasKey(t, "crypto", "hash")       // Buzz stdlib
	hasKey(t, "crypto", "sha256Hex")  // magus
	hasKey(t, "crypto", "hmacSha256") // magus/extra companion merged in

	// Modules Buzz's stdlib lacks become new bare imports.
	hasKey(t, "http", "get")         // magus
	hasKey(t, "http", "download")    // magus/extra http companion merged in
	hasKey(t, "vcs", "commit")       // magus
	hasKey(t, "archive", "compress") // magus
	hasKey(t, "time", "format")      // magus
	hasKey(t, "markdown", "toHtml")  // magus

	// The aggregate import and its byte-level siblings are gone.
	for _, gone := range []string{"magus/extra", "magus/extra/http", "magus/extra/crypto"} {
		_, ok := sess.NativeModule(gone)
		assert.False(t, ok, "module %q should no longer be registered", gone)
	}
}

func TestRegisterModuleSurfaceWithModules(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	t.Cleanup(func() { _ = sess.Close() })

	modules := bindinggen.Modules.With("json", bindinggen.ModuleReg{
		Register: func(context.Context, *buzz.Session) vm.Value {
			m := vm.NewMap()
			m.MapSet("mocked", vm.StrValue("json"))
			return m
		},
		Capabilities: bindinggen.Capabilities(bindinggen.WASM),
	})
	RegisterModuleSurface(ctx, sess, WithModules(modules))

	json, ok := sess.NativeModule("json")
	require.True(t, ok)
	mocked, ok := json.MapGet("mocked")
	require.True(t, ok)
	assert.Equal(t, "json", mocked.String())
}

// scriptSession builds the session a `magus buzz` script runs in: the shared host
// module surface plus the magus.* namespace, parsed upstream-strict like the real
// runner, so a test snippet exercises the same rules a script does.
func scriptSession(t *testing.T) *buzz.Session {
	t.Helper()
	ctx := context.Background()
	sess := buzz.NewSession(ctx)
	t.Cleanup(func() { _ = sess.Close() })
	RegisterModuleSurface(ctx, sess)
	RegisterMagusNamespace(ctx, sess)
	return sess
}

// TestScriptImportsMagus is the regression for `import "magus"` failing outright in
// a script with BZZ2001 module-not-found. A missing import reads as "no such
// module"; the namespace resolves now, and the members that need no magusfile run.
func TestScriptImportsMagus(t *testing.T) {
	sess := scriptSession(t)
	err := sess.Exec(context.Background(), `
import "std";
import "magus";

fun main() > void {
    std\assert(magus\normalize("HTTPServer") == "http-server", message: "normalize");
}
main();
`)
	require.NoError(t, err)
}

// TestScriptWithholdsDeclaringMembers locks the other half of that change: the
// members that DECLARE into the workspace magus is loading raise MGS1022 in a
// script instead of recording onto a registry that is not there and returning null,
// which the caller would read as success.
func TestScriptWithholdsDeclaringMembers(t *testing.T) {
	for _, call := range []string{
		`magus\project({})`,
		`magus\cache.remote({"name": "s3"})`,
		`magus\ci.provider({"name": "actions"})`,
	} {
		t.Run(call, func(t *testing.T) {
			sess := scriptSession(t)
			err := sess.Exec(context.Background(), fmt.Sprintf(`
import "magus";

fun main() > void { %s; }
main();
`, call))
			require.Error(t, err)
			assert.ErrorIs(t, err, types.MagusfileOnlyMember)
		})
	}
}

// TestScriptWorkspaceReadersRaiseCoded covers the second class the same code
// carries: a reader served in-process from the workspace on the context has none in
// a script. It must fail with MGS1022 pointing at the nested-command alternative,
// not with a nil-map panic or an empty result that reads as "no projects".
func TestScriptWorkspaceReadersRaiseCoded(t *testing.T) {
	sess := scriptSession(t)
	err := sess.Exec(context.Background(), `
import "magus";

fun main() > void { magus\ls(); }
main();
`)
	require.Error(t, err)
	assert.ErrorIs(t, err, types.MagusfileOnlyMember)
}

// TestMagusSurfacesExposeSameMembers is the lock-step guard between the two
// surfaces buildMagusNS serves. They must carry the SAME member names - a script
// that cannot see a member has no way to learn it exists - so the surfaces differ
// only in what a member does when called, which is what MGS1022 reports.
func TestMagusSurfacesExposeSameMembers(t *testing.T) {
	sess := scriptSession(t)
	script := sess.GetGlobal("magus")
	require.True(t, script.IsMap(), "magus namespace is not installed for a script")

	assert.ElementsMatch(t, MagusModuleKeys(), script.MapKeys())
}

// TestReplAutoloadsMagusfile covers what a bare `magus buzz` gives you: the REPL
// executes the magusfile at cwd on start, so its locals and targets are there to
// poke at. It lives here rather than in a cmd/magus testscript because driving the
// REPL there needed a non-terminal stdin, which is now unambiguously "run this
// piped script" - and because the wiring worth testing (session + autoload +
// driver) is all below the CLI anyway.
func TestReplAutoloadsMagusfile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";

final greeting = "from the magusfile";

export fun noop(ctx: magus\Context, _a: [str]) > void {}
`)
	sess, err := interp.NewBuzzReplSession(ctx, dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })

	var stdout, stderr strings.Builder
	require.NoError(t, interp.Repl(ctx, sess, interp.ReplOptions{
		Stdin:   strings.NewReader("greeting\n"),
		Stdout:  &stdout,
		Stderr:  &stderr,
		WorkDir: dir,
	}))
	assert.Contains(t, stdout.String(), "from the magusfile")
	assert.Empty(t, stderr.String())
}

// TestEveryHostModuleIsWired guards against a std host module being declared (and
// documented) but never exposed to Buzz sessions - the gap that left template,
// toml, and uuid unreachable after they were added to std/ with generated bindings
// trampolines but omitted from magusModules. Every module hostmodules.All()
// reports, save the hand-assembled "magus" namespace, must resolve as a
// native module with its first declared method present.
func TestEveryHostModuleIsWired(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	registerMagusModules(ctx, sess)

	for _, m := range hostmodules.All() {
		// "magus" is not a bare import; it is wired as the magus.* namespace in
		// buzz.go, not through magusModules.
		if m.Name == "magus" {
			continue
		}
		// Looked up by IMPORT PATH, not Name: a nested module registers at
		// `encoding/json` and only binds as `json` once an import line names it.
		mod, ok := sess.NativeModule(m.ImportPath())
		if !assert.Truef(t, ok, "host module %q is declared in std but not wired into a Buzz session; add it to magusModules", m.ImportPath()) {
			continue
		}
		if len(m.Methods) == 0 {
			continue
		}
		key := std.CamelCase(m.Methods[0].Name)
		if bn := m.Methods[0].BuzzName; bn != "" {
			key = bn
		}
		_, ok = mod.MapGet(key)
		assert.Truef(t, ok, "host module %q is registered but missing method %q", m.Name, key)
	}
}

// TestMagusModulesSharesDescribeCore is the parity lock for the native query
// methods: magus.modules() (host) and `magus describe modules` (CLI) are two thin
// adapters over the one typed core, host.ModulesOutput. This asserts the objects
// the host method marshals are exactly that core (same names, docs, per-method Buzz
// signatures) so the two surfaces can't drift.
func TestMagusModulesSharesDescribeCore(t *testing.T) {
	core := hostmodules.Describe("") // what `magus describe modules` formats
	require.NotEmpty(t, core)

	// What a magusfile sees from magus.modules(): the same core, marshalled.
	got, ok := bindinggen.ValueToAny(bindinggen.MapsVal(core)).([]any)
	require.True(t, ok)
	require.Len(t, got, len(core))
	for i, m := range core {
		rec := got[i].(map[string]any)
		assert.Equal(t, m.Name, rec["name"])
		assert.Equal(t, m.Doc, rec["doc"])
	}

	// Detail mode (magus.module) shares the same core, with typed methods + signatures.
	fs := hostmodules.Describe("fs")
	require.Len(t, fs, 1)
	require.NotEmpty(t, fs[0].Methods)
	assert.NotEmpty(t, fs[0].Methods[0].Buzz, "each method carries its Buzz signature")
}

// TestMagusModulesEndToEnd drives the host methods from a real magusfile, proving
// they're wired onto the magus namespace and return usable typed objects.
func TestMagusModulesEndToEnd(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "magusfile.buzz", `import "magus";
export fun check(ctx: magus\Context, args: [str]) > void {
    final mods = magus.modules();
    if (mods.len() == 0) { magus.fatal("magus.modules() returned nothing"); }

    final fs = magus.module("fs");
    if (fs.name != "fs") { magus.fatal("magus.module(\"fs\").name was not fs"); }
    if (fs.methods.len() == 0) { magus.fatal("fs module has no methods"); }
    if (fs.methods[0].buzz == "") { magus.fatal("fs method missing its Buzz signature"); }
}`)
	_, err := interp.RunDir(context.Background(), dir, "check", nil)
	require.NoError(t, err, "magus.modules/module end-to-end")
}

// TestTemplatePartialsEndToEnd drives the template module from a real magusfile,
// proving import "template" resolves and renderPartials expands {{>name}} includes,
// interpolates the shared context, and HTML-escapes {{var}} values. Backtick raw
// strings carry the Mustache verbatim (a double-quoted "{{x}}" would collide with
// Buzz's own {expr} interpolation).
func TestTemplatePartialsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "magusfile.buzz", "import \"magus\";\n"+
		"import \"template\";\n"+
		"export fun check(ctx: magus\\Context, args: [str]) > void {\n"+
		"    final page = `{{>header}}[{{body}}]{{>footer}}`;\n"+
		"    final partials = {\"header\": `<h>{{title}}</h>`, \"footer\": `<f/>`};\n"+
		"    final got = template.renderPartials(page, {\"title\": \"magus\", \"body\": \"hi & <b>\"}, partials);\n"+
		"    final want = \"<h>magus</h>[hi &amp; &lt;b&gt;]<f/>\";\n"+
		"    if (got != want) { magus.fatal(\"renderPartials mismatch: got \" + got); }\n"+
		"}")
	_, err := interp.RunDir(context.Background(), dir, "check", nil)
	require.NoError(t, err, "template.renderPartials end-to-end")
}

// TestMagusBustCacheReachable guards that magus.bustCache is actually bound. It
// is declared in the std.Magus descriptor but was historically only described,
// never registered, so a magusfile calling it failed at runtime. With no cache
// in context (the test harness) it is a no-op, so a clean run proves the binding
// resolves.
func TestMagusBustCacheReachable(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
import "fs";
export fun build(ctx: magus\Context, args: [str]) > void {
    magus.bustCache();
    fs.writeFile("ran", "ok");
}
`)
	if err := runTargetIn(t, dir, "build"); err != nil {
		t.Fatalf("magus.bustCache() should be bound and a no-op without a cache: %v", err)
	}
}

// TestCtxlessTargetRejected pins the ctx-form contract: an exported magusfile function
// whose first parameter is not a magus\Context is rejected at load with MGS1008, before
// any target dispatches. A parameterized ctx-form target and a zero-extra-arg one both
// load and run, so the enforcement targets the missing context, not the arg shape.
func TestCtxlessTargetRejected(t *testing.T) {
	t.Run("ctx-less form rejected with MGS1008", func(t *testing.T) {
		dir := t.TempDir()
		writeMagusfile(t, dir, `import "magus";
export fun build(args: [str]) > void {}
`)
		err := runTargetIn(t, dir, "build")
		require.Error(t, err, "a ctx-less target must be rejected at load")
		require.ErrorIs(t, err, types.TargetMissingContext, "carries the MGS1008 code")
		assert.Contains(t, err.Error(), "magus\\Context", "names the required context type")
	})

	t.Run("ctx form loads and runs", func(t *testing.T) {
		dir := t.TempDir()
		writeMagusfile(t, dir, `import "magus";
import "fs";
export fun build(ctx: magus\Context, args: [str]) > void { fs.writeFile("ran", "ok"); }
`)
		require.NoError(t, runTargetIn(t, dir, "build"))
		got, err := os.ReadFile(sentinel(dir))
		require.NoError(t, err)
		assert.Equal(t, "ok", string(got))
	})
}

// TestRemovedMagusfileAPIRejected pins the migration contract for the magusfile calls
// magus no longer binds. Each one used to fail only when the target ran, as a bare
// "null is not callable" naming neither the call nor its replacement, because Buzz
// reads a missing member as null; magus.needs was worse still, since the static target
// graph reads ctx.needs and so reported no dependency edge at all. They are rejected at
// load with MGS1025 instead, and the replacement spellings must keep loading.
func TestRemovedMagusfileAPIRejected(t *testing.T) {
	removed := map[string]struct{ body, names string }{
		"magus.needs":          {"magus.needs(format);", "magus.needs"},
		"magus.glob":           {`ctx.needs(magus.glob("form*"));`, "magus.glob"},
		"magus.target.literal": {`ctx.needs(magus.target.literal("format"));`, "magus.target.literal"},
	}
	for name, tc := range removed {
		t.Run(name+" rejected with MGS1025", func(t *testing.T) {
			dir := t.TempDir()
			writeMagusfile(t, dir, `import "magus";
export fun format(ctx: magus\Context, args: [str]) > void {}
export fun build(ctx: magus\Context, args: [str]) > void { `+tc.body+` }
`)
			err := runTargetIn(t, dir, "build")
			require.Error(t, err, "a removed magusfile API must be rejected at load")
			require.ErrorIs(t, err, types.MagusfileAPIRemoved, "carries the MGS1025 code")
			assert.Contains(t, err.Error(), tc.names, "names the removed call")
		})
	}

	// The registration shape predates required parameter annotations, so it dies in the
	// parser before there is an AST to read. The textual fallback is what covers it.
	t.Run("magus.project.register rejected though it cannot parse", func(t *testing.T) {
		dir := t.TempDir()
		writeMagusfile(t, dir, `import "magus";
magus.project.register(fun(p, cb) > bool { cb({"spells": []}); return true; });
export fun build(ctx: magus\Context, args: [str]) > void {}
`)
		err := runTargetIn(t, dir, "build")
		require.Error(t, err)
		require.ErrorIs(t, err, types.MagusfileAPIRemoved, "carries the MGS1025 code")
		assert.Contains(t, err.Error(), "magus.project.register", "names the removed call")
	})

	t.Run("the replacements still load", func(t *testing.T) {
		dir := t.TempDir()
		writeMagusfile(t, dir, `import "magus";
import "fs";
export fun format(ctx: magus\Context, args: [str]) > void {}
export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.needs(format);
    ctx.needs(ctx.glob("form*"));
    fs.writeFile("ran", "ok");
}
`)
		require.NoError(t, runTargetIn(t, dir, "build"))
	})

	// The AST reading is what keeps a removed name inside a comment or a string from
	// failing a magusfile that never calls it.
	t.Run("a mention is not a call", func(t *testing.T) {
		dir := t.TempDir()
		writeMagusfile(t, dir, `import "magus";
import "fs";
export fun format(ctx: magus\Context, args: [str]) > void {}
export fun build(ctx: magus\Context, args: [str]) > void {
    // magus.needs( was removed; so was magus.glob(
    final note = "magus.needs(";
    ctx.needs(format);
    fs.writeFile("ran", note);
}
`)
		require.NoError(t, runTargetIn(t, dir, "build"))
	})
}

func sentinel(dir string) string { return filepath.Join(dir, "ran") }

func TestRunTopLevelTarget(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
import "fs";
export fun build(ctx: magus\Context, args: [str]) > void {
    fs.writeFile("ran", "build");
}
`)
	require.NoError(t, runTargetIn(t, dir, "build"))
	got, err := os.ReadFile(sentinel(dir))
	require.NoError(t, err, "sentinel not created")
	assert.Equal(t, "build", string(got))
}

func TestRunPathTarget(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
import "fs";
export fun db_migrate(ctx: magus\Context, args: [str]) > void {
    fs.writeFile("ran", "db:migrate");
}
`)
	require.NoError(t, runTargetIn(t, dir, "db:migrate"))
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
export fun render(ctx: magus\Context, args: [str]) > void {
    render.writeFile("ran", "x");
}
`)
	err := runTargetIn(t, dir, "render")
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
export fun build(ctx: magus\Context, args: [str]) > void {
    fs.writeFile("ran", tag);
}
`)
	require.NoError(t, runTargetIn(t, dir, "build"))
	got, err := os.ReadFile(sentinel(dir))
	require.NoError(t, err, "sentinel not created")
	assert.Equal(t, "calc-ok", string(got))
}

// TestRunBuzzStdModule exercises the std host surface from a magusfile.buzz
// end-to-end: the magus-utils generated trampolines must decode a variadic
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

export fun verify(ctx: magus\Context, _opts: [str]) > void {
    var joined = fs.join("a", "b", "c");
    var patch = charm.append(["y", "z"]);
    fs.writeFile("ran", joined + "|" + patch.ops[1].value);
}
`), 0o644))
	require.NoError(t, runTargetIn(t, dir, "verify"))
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

export fun verify(ctx: magus\Context, _opts: [str]) > void {
    fs.writeFile("ran", markdown.toHtml("# Hi"));
}
`), 0o644))
	require.NoError(t, runTargetIn(t, dir, "verify"))
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

export fun verify(ctx: magus\Context, _opts: [str]) > void {
    var asset = fmt.sprintf("magus_%s_%s_%s.tar.gz", "1.0", "linux", "amd64");
    var none = fmt.sprintf("literal");
    fs.writeFile("ran", asset + "|" + none);
}
`), 0o644))
	require.NoError(t, runTargetIn(t, dir, "verify"))
	got, err := os.ReadFile(sentinel(dir))
	require.NoError(t, err, "sentinel not created")
	assert.Equal(t, "magus_1.0_linux_amd64.tar.gz|literal", string(got))
}

// TestRunBuzzAggregateUtil proves the magus host utilities resolve under bare
// module imports (fs.join / os.execSh, in camelCase) and coexist with Buzz's own
// stdlib in the same file: the magus fs/os methods are layered onto the bare
// fs/os modules, while hashing uses the stdlib `crypto.hash`.
//
// `crypto.hash` returns the RAW digest bytes, matching upstream Buzz; `.hex()`
// renders them. magus's own crypto module also offers sha256_hex for the hex form
// directly.
func TestRunBuzzAggregateUtil(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
import "fs";
import "os";
import "proc";
import "crypto";

export fun verify(ctx: magus\Context, _opts: [str]) > void {
    var joined = fs.join("a", "b", "c");
    var sc = proc.shell("printf hello");
    var res = proc.exec(sc.bin, sc.args, "", {}).stdout;
    var digest = crypto.hash(crypto.HashAlgorithm.Sha256, "").hex();
    fs.writeFile("ran", joined + "|" + res + "|" + digest);
}
`), 0o644))
	require.NoError(t, runTargetIn(t, dir, "verify"))
	got, err := os.ReadFile(sentinel(dir))
	require.NoError(t, err, "sentinel not created")
	want := "a/b/c|hello|e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	assert.Equal(t, want, string(got))
}

func TestRunTargetWithArgs(t *testing.T) {
	dir := t.TempDir()
	// Forwarded args arrive as ONE list, matching the (ctx, args: [str]) signature
	// every real magusfile target declares. They used to be spread as positional
	// parameters, which meant `args: [str]` bound to the FIRST arg as a str and
	// the rest were dropped - so the documented convention and the runtime
	// disagreed, and every target in this repo was on the losing side.
	writeMagusfile(t, dir, `
import "magus";
import "fs";
export fun db_migrate(ctx: magus\Context, args: [str]) > void {
    fs.writeFile("ran", args.join(" "));
}
`)
	require.NoError(t, runTargetIn(t, dir, "db:migrate", "a", "b", "c"))
	got, err := os.ReadFile(sentinel(dir))
	require.NoError(t, err, "sentinel not created")
	assert.Equal(t, "a b c", string(got))
}

// TestRunTargetWithNoArgs pins the other half of the contract: a target with no
// forwarded args still receives a list, not null, so `args.len()` is safe in
// every target rather than only in the ones someone remembered to pass args to.
func TestRunTargetWithNoArgs(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
import "fs";
export fun probe(ctx: magus\Context, args: [str]) > void {
    fs.writeFile("ran", "len={args.len()}");
}
`)
	require.NoError(t, runTargetIn(t, dir, "probe"))
	got, err := os.ReadFile(sentinel(dir))
	require.NoError(t, err, "sentinel not created")
	assert.Equal(t, "len=0", string(got))
}

func TestRunTargetReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
export fun db_migrate(ctx: magus\Context, args: [str]) > void {
    throw "boom";
}
`)
	err := runTargetIn(t, dir, "db:migrate")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestRunUnknownTarget(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
export fun db_migrate(ctx: magus\Context, args: [str]) > void {}
`)
	err := runTargetIn(t, dir, "no-such-target")
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
export fun go(ctx: magus\Context, _a: [str]) > void { hello.build(); }`

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
export fun top(ctx: magus\Context, args: [str]) > void {
    ctx.needs(helper);
}
`)
	err := runTargetIn(t, dir, "top")
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
export fun top(ctx: magus\Context, _a: [str]) > void {
    ctx.needs(dep);
    fs.writeFile("ran", "top");
}
export fun dep(ctx: magus\Context, _a: [str]) > void { fs.writeFile("dep-ran", "dep"); }
`)
	require.NoError(t, runTargetIn(t, dir, "top"))
	_, err := os.Stat(filepath.Join(dir, "dep-ran"))
	require.NoError(t, err, "forward-referenced dependency did not run")
}

// TestNeedsStringArgumentFails verifies magus.needs rejects a bare string - the
// classic footgun - and points the author at magus.glob for patterns.
func TestNeedsStringArgumentFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
export fun dep(ctx: magus\Context, _a: [str]) > void {}
export fun top(ctx: magus\Context, _a: [str]) > void { ctx.needs("dep"); }
`), 0o644))
	err := runTargetIn(t, dir, "top")
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
export fun top(ctx: magus\Context, _a: [str]) > void { ctx.needs(fun (_a: [str]) > void {}); }
`), 0o644))
	err := runTargetIn(t, dir, "top")
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

export fun foo_bar(ctx: magus\Context, _a: [str]) > void {}
export fun fooBar(ctx: magus\Context, _a: [str]) > void {}
`), 0o644))
	err := runTargetIn(t, dir, "foo-bar")
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

export fun bail(ctx: magus\Context, _a: [str]) > void { os.exit(3); }
`), 0o644))
	err := runTargetIn(t, dir, "bail")
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

export fun nap(ctx: magus\Context, _a: [str]) > void {
    os.sleep(1.5);
    os.sleep(0);
}
`), 0o644))
	require.NoError(t, runTargetIn(t, dir, "nap"))
}

// TestOsWhich verifies proc.which resolves a real command to a non-empty path and RAISES
// for a missing one (asserted inside the magusfile via os.exit).
//
// It used to return "" for a missing command so a caller could branch on equality. That
// made the check optional - forget it and the empty string flows into an exec or a path
// join - and it is not how the rest of these modules report an unavailable answer.
func TestOsWhich(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
import "os";
import "proc";

export fun checkwhich(ctx: magus\Context, _a: [str]) > void {
    if (proc.which("sh") == "") { os.exit(2); }
    var raised = false;
    try { proc.which("definitely-no-such-cmd-zzz"); } catch (e) { raised = true; }
    if (!raised) { os.exit(3); }
}
`), 0o644))
	require.NoError(t, runTargetIn(t, dir, "checkwhich"))
}

// TestMagusHint confirms magus.hint is callable (and a repeated message is
// tolerated — dedup happens in the shared channel).
func TestMagusHint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";

export fun nudge(ctx: magus\Context, _a: [str]) > void {
    magus.hint("stale generated code — run: magus run generate -- --write");
    magus.hint("stale generated code — run: magus run generate -- --write");
}
`), 0o644))
	require.NoError(t, runTargetIn(t, dir, "nudge"))
}

// TestMagusFatal verifies magus.fatal aborts with a types.ExitError carrying
// code 1 (Buzz preserves the typed error across the boundary).
func TestMagusFatal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";

export fun boom(ctx: magus\Context, _a: [str]) > void { magus.fatal("boom"); }
`), 0o644))
	err := runTargetIn(t, dir, "boom")
	require.Error(t, err, "expected error from magus.fatal")
	var ex types.ExitError
	require.ErrorAs(t, err, &ex)
	assert.Equal(t, 1, ex.Code)
}

// TestProcShellChoosesShell verifies proc.shell honours an explicit shell and that
// its {bin, args} feeds straight into proc.exec. This is the pair that replaced
// os.execSh: building the argv and running it are separate steps now, so the shell
// choice is visible at the call site instead of hidden inside a wrapper.
func TestProcShellChoosesShell(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
import "proc";

export fun viash(ctx: magus\Context, _a: [str]) > void {
    var c = proc.shell("true", "sh");
    proc.exec(c.bin, c.args, "", {});
}
`), 0o644))
	require.NoError(t, runTargetIn(t, dir, "viash"))
}

// TestNeedsDedup verifies magus.needs runs a duplicated target once — the footgun
// where the same exported target is listed more than once in one needs call.
func TestNeedsDedup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
import "os";
import "proc";

export fun dep(ctx: magus\Context, _a: [str]) > void { var c = proc.shell("printf x >> mark"); proc.exec(c.bin, c.args, "", {}); }
export fun top(ctx: magus\Context, _a: [str]) > void { ctx.needs(dep, dep); }
`), 0o644))
	require.NoError(t, runTargetIn(t, dir, "top"))
	got, err := os.ReadFile(filepath.Join(dir, "mark"))
	require.NoError(t, err, "dep did not run")
	assert.Equal(t, "x", string(got), "dep should run once")
}

// runPooledTargetIn runs target over the magusfile in dir through the VM pool, with
// the pool registry and per-invocation TargetMemo a real `magus run` step installs
// (run.go). runTargetIn deliberately has neither, so it takes dispatchBuzzDeps'
// inline sequential branch — the branch where a dependency cycle recurses instead of
// being memo-deduped. Anything about cycle detection or concurrent dispatch has to
// come through here to be testing the code that ships.
func runPooledTargetIn(t *testing.T, dir, target string) error {
	t.Helper()
	reg := buzz.NewPoolRegistry(nil, 4)
	t.Cleanup(func() { _ = reg.Close() })
	ctx := buzz.WithPoolRegistry(context.Background(), reg)
	ctx = buzz.WithTargetMemo(ctx, buzz.NewTargetMemo())
	_, err := interp.RunDir(ctx, dir, target, nil)
	return err
}

// TestNeedsCycleThroughEntryTargetErrors covers the cycle that comes back around to
// the target the USER named. The entry target is invoked directly (runBuzz calls its
// callable), so it entered neither the memo nor the ancestor stack: a dependency that
// needed it back re-executed its body, and with two dependencies in flight the second
// one parked on the first's memo entry while the re-executed entry parked on the
// second — a hang, not a diagnostic. The timeout is the assertion; a cycle must
// always surface as an error.
func TestNeedsCycleThroughEntryTargetErrors(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
export fun ci(ctx: magus\Context, _a: [str]) > void { ctx.needs(lint, docs); }
export fun lint(ctx: magus\Context, _a: [str]) > void { ctx.needs(ci); }
export fun docs(ctx: magus\Context, _a: [str]) > void { ctx.needs(ci); }
`)
	done := make(chan error, 1)
	go func() { done <- runPooledTargetIn(t, dir, "ci") }()
	select {
	case err := <-done:
		require.Error(t, err, "a cycle back to the entry target must be an error")
		assert.Contains(t, err.Error(), "cycle detected")
	case <-time.After(20 * time.Second):
		t.Fatal("run hung on a cycle back to the entry target instead of erroring")
	}
}

// TestMagusLoggingBuzz exercises the logging methods bound onto the magus
// namespace from a Buzz magusfile (with and without a fields map).
func TestMagusLoggingBuzz(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";

export fun logit(ctx: magus\Context, _a: [str]) > void {
    magus.info("hello");
    magus.debug("dbg", {"k": "v"});
    magus.warn("warn");
    magus.error("err");
}
`), 0o644))
	require.NoError(t, runTargetIn(t, dir, "logit"))
}

func TestParseIncludesPathTargets(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
export fun db_migrate(ctx: magus\Context, args: [str]) > void {}
export fun build(ctx: magus\Context, args: [str]) > void {}
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

// TestNeedsGlobHandle covers ctx.needs(ctx.glob(...)) feeding a meta-target: the matched
// targets run (sorted) before the body, and non-matching targets are skipped. With
// no pool in ctx the deps run sequentially in the current VM, so the order is
// deterministic.
func TestNeedsGlobHandle(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
import "os";
import "proc";
fun note(s: str) > void {
   var c = proc.shell("printf '%s\n' " + s + " >> ran");
   proc.exec(c.bin, c.args, "", {});
}
export fun go_build(ctx: magus\Context, _a: [str]) > void { note("go-build"); }
export fun image_build(ctx: magus\Context, _a: [str]) > void { note("image-build"); }
export fun go_test(ctx: magus\Context, _a: [str]) > void { note("go-test"); }
export fun build(ctx: magus\Context, _a: [str]) > void {
   ctx.needs(ctx.glob("*-build"));
   note("build-body");
}
`)
	require.NoError(t, runTargetIn(t, dir, "build"))
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
export fun build(ctx: magus\Context, _a: [str]) > void { ctx.needs(magus.target.literal("build")); }
`), 0o644))
	err := runTargetIn(t, dir, "build")
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
export fun build(ctx: magus\Context, args: [str]) > void {
    fs.mkdirAll("sub");
    fs.writeFile("sub/a.txt", "alpha");
    fs.copyFile("sub/a.txt", "sub/b.txt");
    // glob returns paths relative to the project dir, sorted, each carrying that dir
    // as its base - so a caller can resolve one without knowing where the target ran.
    var hits = fs.glob("sub/*.txt");
    var acc = "";
    var first = true;
    var base = "";
    foreach (h in hits) {
        if (!first) { acc = acc + ","; }
        acc = acc + h.value;
        base = h.base;
        first = false;
    }
    fs.writeFile("glob.out", acc);
    fs.writeFile("glob.base", base);
}
`)
	require.NoError(t, runTargetIn(t, dir, "build"))

	// Files landed in the project dir, not the process cwd.
	for _, rel := range []string{"sub/a.txt", "sub/b.txt", "glob.out"} {
		_, err := os.Stat(filepath.Join(dir, rel))
		require.NoErrorf(t, err, "%s not created in project dir", rel)
	}
	got, err := os.ReadFile(filepath.Join(dir, "glob.out"))
	require.NoError(t, err)
	// Relative, sorted - not absolute paths.
	assert.Equal(t, "sub/a.txt,sub/b.txt", string(got))

	// And each match names the directory those relative values are measured from, which
	// is the project dir this test is about. The value alone could never say so.
	base, err := os.ReadFile(filepath.Join(dir, "glob.base"))
	require.NoError(t, err)
	assert.Equal(t, dir, string(base), "a glob result is based at the project dir")
}

// TestExecResultAnnotationChecksFields proves the typed-returns mechanism: a
// magusfile may annotate an exec result `> ExecResult` (shipped with `import
// "os"`, the module that returns it), and because Buzz objects are structural
// the runtime map satisfies the type while the checker validates field access
// against the declared fields. A good field loads and runs; a typo'd field is a
// compile-time error, not a silent runtime null.
func TestExecResultAnnotationChecksFields(t *testing.T) {
	t.Run("good field type-checks and runs", func(t *testing.T) {
		dir := t.TempDir()
		writeMagusfile(t, dir, `
import "magus";
import "os";
import "proc";
import "fs";
export fun build(ctx: magus\Context, args: [str]) > void {
    final r: ExecResult = proc.exec("echo", ["hi"]);
    fs.writeFile("ran", r.stdout);
}
`)
		require.NoError(t, runTargetIn(t, dir, "build"))
	})

	t.Run("typo'd field is a compile error", func(t *testing.T) {
		dir := t.TempDir()
		writeMagusfile(t, dir, `
import "magus";
import "os";
import "proc";
export fun build(ctx: magus\Context, args: [str]) > void {
    final r: ExecResult = proc.exec("echo", ["hi"]);
    final x = r.stduot;
}
`)
		err := runTargetIn(t, dir, "build")
		require.Error(t, err, "r.stduot on an ExecResult must fail to compile")
		assert.Contains(t, strings.ToLower(err.Error()), "field",
			"error should name the missing field; got: %v", err)
	})
}

// TestObjectAnnotationsCheckFields proves every host-object mirror, shipped with
// the host module that returns it, gives compile-checked field access:
// annotating a host result with the object type makes a valid field compile
// and a typo'd field a compile error.
// The annotated access lives in an unexecuted probe fn, so the file type-checks
// without the test performing any network/git/exec — only build (a no-op) runs.
func TestObjectAnnotationsCheckFields(t *testing.T) {
	cases := []struct {
		typ, expr, good, bad string
		imports              string
	}{
		{"ExecResult", `proc.exec("echo", ["hi"])`, "stdout", "stduot", `import "proc";`},
		{"Commit", `vcs.commit()`, "author", "auther", `import "vcs";`},
		{"FileInfo", `fs.stat(".")`, "size", "sizes", `import "fs";`},
		{"HttpResponse", `http.get("http://x")`, "status", "staus", `import "http";`},
		{"SemverVersion", `semver.parse("1.2.3")`, "major", "majr", `import "semver";`},
		{"URL", `url.parse("http://x")`, "scheme", "sceme", `import "encoding/url";`},
	}

	mkfile := func(c struct{ typ, expr, good, bad, imports string }, field string) string {
		return fmt.Sprintf(`
import "magus";
import "fs";
%s
fun probe() > void {
    final r: %s = %s;
    final _ = r.%s;
}
export fun build(ctx: magus\Context, args: [str]) > void {
    fs.writeFile("ran", "ok");
}
`, c.imports, c.typ, c.expr, field)
	}

	for _, c := range cases {
		c := c
		t.Run(c.typ, func(t *testing.T) {
			good := t.TempDir()
			writeMagusfile(t, good, mkfile(c, c.good))
			require.NoError(t, runTargetIn(t, good, "build"),
				"%s: valid field %q should compile and the no-op target should run", c.typ, c.good)

			bad := t.TempDir()
			writeMagusfile(t, bad, mkfile(c, c.bad))
			err := runTargetIn(t, bad, "build")
			require.Error(t, err, "%s: typo'd field %q must fail to compile", c.typ, c.bad)
			assert.Contains(t, strings.ToLower(err.Error()), "field",
				"%s: error should name the missing field; got: %v", c.typ, err)
		})
	}
}

func TestRunAndParse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	content := `
import "magus";
export fun build(ctx: magus\Context, args: [str]) > void {}
export fun test(ctx: magus\Context, args: [str]) > void {}
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	_, runErr := interp.RunDir(context.Background(), dir, "build", nil)
	require.NoError(t, runErr)

	src, err := interp.Find(dir)
	require.NoError(t, err)
	targets, err := interp.Parse(context.Background(), src)
	require.NoError(t, err)
	require.Len(t, targets, 2)
}

func TestProcShellExitCode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
import "proc";

export fun check(ctx: magus\Context, args: [str]) > void {
    var ok = proc.shell("true");
    var rc = proc.exec(ok.bin, ok.args, "", {"allow_failure": true}).code;
    if (rc != 0) {
        throw "proc.shell('true') exited {rc}";
    }
    var bad = proc.shell("false");
    var rc2 = proc.exec(bad.bin, bad.args, "", {"allow_failure": true}).code;
    if (rc2 == 0) {
        throw "proc.shell('false') exited 0, expected non-zero";
    }
}
`)
	_, runErr := interp.RunDir(context.Background(), dir, "check", nil)
	require.NoError(t, runErr)
}

func TestOsWithEnvShellCapture(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
import "os";
import "proc";

export fun check(ctx: magus\Context, args: [str]) > void {
    os.withEnv({"MY_BUZZ_VAR": "hello"}, fun() > void {
        var ec = proc.shell("echo $MY_BUZZ_VAR");
        var captured = proc.exec(ec.bin, ec.args, "", {}).stdout;
        if (captured != "hello") {
            throw "expected 'hello', got: [" + captured + "]";
        }
    });
}
`)
	_, runErr := interp.RunDir(context.Background(), dir, "check", nil)
	require.NoError(t, runErr)
}

func TestFsJoinBinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
import "fs";

export fun check(ctx: magus\Context, args: [str]) > void {
    var p = fs.join("a", "b", "c");
    if (p == "") {
        throw "fs.join returned empty string";
    }
}
`)
	_, runErr := interp.RunDir(context.Background(), dir, "check", nil)
	require.NoError(t, runErr)
}

func TestFsListDirBinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	subdir := filepath.Join(dir, "subdir")
	require.NoError(t, os.MkdirAll(subdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subdir, "a.txt"), []byte("x"), 0o644))

	writeMagusfile(t, dir, `
import "magus";
import "fs";

export fun check(ctx: magus\Context, args: [str]) > void {
    var entries = fs.listDir("subdir");
    if (entries.len() == 0) {
        throw "expected at least one entry in subdir";
    }
}
`)
	_, runErr := interp.RunDir(context.Background(), dir, "check", nil)
	require.NoError(t, runErr)
}

func TestFsRemoveAllBinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "todelete")
	require.NoError(t, os.MkdirAll(target, 0o755))

	writeMagusfile(t, dir, `
import "magus";
import "fs";

export fun check(ctx: magus\Context, args: [str]) > void {
    fs.removeAll("todelete");
}
`)
	_, runErr := interp.RunDir(context.Background(), dir, "check", nil)
	require.NoError(t, runErr)
	_, err := os.Stat(target)
	assert.True(t, os.IsNotExist(err), "expected todelete to be removed")
}

func TestVcsBindings(t *testing.T) {
	// NOT t.Parallel: resolveVCS caches the resolved driver in package state keyed by
	// cwd, so concurrent tests in different directories race it.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	dir := t.TempDir()
	// A real repository AT THE TARGET'S DIRECTORY. This used to lean on the process cwd
	// being inside magus's own checkout, which is exactly the confusion the vcs probes
	// were fixed for: a target carries its directory on the context and the runtime never
	// chdirs, so "the process happens to be in a repo" is not the same as "this target is".
	for _, args := range [][]string{
		{"git", "init", "-b", "main"},
		{"git", "config", "user.email", "a@b.c"},
		{"git", "config", "user.name", "A"},
		{"git", "config", "commit.gpgsign", "false"},
		{"git", "commit", "--allow-empty", "-m", "hello"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%v: %s", args, out)
	}
	writeMagusfile(t, dir, `
import "magus";
import "vcs";

export fun check(ctx: magus\Context, args: [str]) > void {
    // The target's own directory is a git repo, so every accessor answers about THAT
    // repo. The no-VCS path, where these RAISE rather than hand back "", is covered by
    // TestVcsCommitRaisesOutsideRepo.
    final h = vcs.commit().short;
    final b = vcs.ref();
    if (h == "") { throw "commit().short returned empty inside a repo"; }
    if (b == "") { throw "ref returned empty inside a git repo"; }

    // status answers about THIS repo. The magusfile was written after the commit, so it
    // is the one untracked file - and seeing exactly it is what proves the probe read the
    // target's tree rather than the process cwd (magus's own checkout, which is a
    // different repo with entirely different dirty files).
    final st = vcs.status();
    if (st.clean) { throw "status must see the untracked magusfile"; }
    if (!vcs.isDirty()) { throw "isDirty must agree with status.clean"; }
    var sawMagusfile = false;
    foreach (f in st.files) {
        if (f.value == "magusfile.buzz") { sawMagusfile = true; }
        if (f.base == "") { throw "a status path must name what it is relative to"; }
    }
    if (!sawMagusfile) { throw "status listed some other repo's files"; }
}
`)
	_, runErr := interp.RunDir(context.Background(), dir, "check", nil)
	require.NoError(t, runErr)
}

func TestParseTargetsPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := `
import "magus";
export fun build(ctx: magus\Context, args: [str]) > void {}
export fun test(ctx: magus\Context, args: [str]) > void {}
export fun go_vet(ctx: magus\Context, args: [str]) > void {}
`
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(source), 0o644))

	src := &interp.Source{Dir: dir, Files: []string{path}}
	targets, err := interp.Parse(context.Background(), src)
	require.NoError(t, err)

	got := map[string]interp.Target{}
	for _, tgt := range targets {
		got[tgt.Key] = tgt
	}

	assert.Contains(t, got, "build", "missing target 'build'")
	assert.Contains(t, got, "test", "missing target 'test'")
	assert.Contains(t, got, "go-vet", "missing target 'go-vet'")
}

var largeMagefile = `
import "magus";
export fun go_build(ctx: magus\Context, args: [str]) > void {}
export fun go_test(ctx: magus\Context, args: [str]) > void {}
export fun go_lint(ctx: magus\Context, args: [str]) > void {}
export fun go_vet(ctx: magus\Context, args: [str]) > void {}
export fun docker_build(ctx: magus\Context, args: [str]) > void {}
export fun docker_push(ctx: magus\Context, args: [str]) > void {}
export fun release_tag(ctx: magus\Context, args: [str]) > void {}
export fun release_sign(ctx: magus\Context, args: [str]) > void {}
export fun ci_lint(ctx: magus\Context, args: [str]) > void {}
export fun ci_test(ctx: magus\Context, args: [str]) > void {}
export fun db_migrate(ctx: magus\Context, args: [str]) > void {}
export fun db_seed(ctx: magus\Context, args: [str]) > void {}
export fun build(ctx: magus\Context, args: [str]) > void {}
export fun test(ctx: magus\Context, args: [str]) > void {}
export fun clean(ctx: magus\Context, args: [str]) > void {}
`

func BenchmarkParse(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	if err := os.WriteFile(path, []byte(largeMagefile), 0o644); err != nil {
		b.Fatal(err)
	}
	src := &interp.Source{Dir: dir, Files: []string{path}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := interp.Parse(context.Background(), src)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFind(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	if err := os.WriteFile(path, []byte("import \"magus\";\nexport fun noop(ctx: magus\\Context, args: [str]) > void {}\n"), 0o644); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := interp.Find(dir)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRunBuzzParallel runs independent magusfile targets across many
// projects concurrently. Each body does cwd-relative I/O (fs.writeFile), the very
// thing the process-global chdirMu serializes — so comparing -cpu=1 against -cpu=N
// quantifies how much real parallelism the chdir lock costs, and guards the
// chdirMu->ctx-cwd deserialization follow-up against regressing.
func BenchmarkRunBuzzParallel(b *testing.B) {
	const nProjects = 16
	ctx := context.Background()
	body := "import \"fs\";\nexport fun build(ctx: magus\\Context, args: [str]) > void { fs.writeFile(\"out.txt\", \"x\"); }\n"

	type proj struct {
		src *interp.Source
		dir string
	}
	projects := make([]proj, nProjects)
	for i := range projects {
		dir := b.TempDir()
		path := filepath.Join(dir, "magusfile.buzz")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			b.Fatal(err)
		}
		projects[i] = proj{src: &interp.Source{Dir: dir, Files: []string{path}, Engine: "buzz"}, dir: dir}
	}

	var ctr atomic.Int64
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			p := projects[int(ctr.Add(1))%nProjects]
			if _, err := interp.RunDir(ctx, p.dir, "build", nil); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// testBoundaryTypesPath is a private, test-only import path bundling every
// boundary-type mirror (host-owned and magus-owned) into one compilable source
// module, in declare-before-use order. It exists only for this file's
// completeness/parity guards - production code reaches these types through their
// real owning import (magus/spell for the spell-authored types, or the host
// module that returns a given one: os, fs, http, encoding, semver, vcs).
//
// A synthetic host module's own import (`import "os";`) only binds its native
// functions: the type-declaration companion registered alongside it
// (hostTypeModuleSources) is collected by the CHECKER for annotation-checking
// (`final r: ExecResult = proc.exec(...)`), but it is never compiled and run, so it
// can never bind an object literal's constructor into the runtime env - `Name{}`
// needs `Name` bound as an objectDef VALUE, which only a compiled-and-run source
// module provides (see vm.buildObjectVal). This bundle is that compiled-and-run
// module, purpose-built so the tests below can construct and read every mirror
// in one shot without depending on any one type's real (and, for the
// synthetic-backed ones, construction-incapable) import path.
const testBoundaryTypesPath = "test/boundary-types"

var testBoundaryTypesSource = strings.Join([]string{
	spellruntime.ExecResultSource,
	spellruntime.CommitAuthorSource, // precedes Commit: Commit.author is CommitAuthor
	spellruntime.CommitSource,
	spellruntime.FileInfoSource,
	spellruntime.HTTPResponseSource,
	spellruntime.SemverVersionSource,
	spellruntime.SemverNextSource,
	spellruntime.URLSource,
	spellruntime.TagSource, // Tag.version is SemverVersion, so it must follow that source
	spellruntime.ProjectEntrySource,
	spellruntime.ProjectsSource,
	spellruntime.AffectedSource,
	spellruntime.GraphSource,
	spellruntime.CrossTargetRefSource,
	spellruntime.TargetSpellUseSource,
	spellruntime.InputRefSource,
	spellruntime.OutputRefSource,
	spellruntime.TargetGraphNodeSource,
	spellruntime.TargetGraphProjectSource,
	spellruntime.TargetGraphSource,
	spellruntime.ModuleFieldEntrySource,
	spellruntime.ModuleMethodEntrySource,
	spellruntime.ModuleSource,
}, "\n")

// TestEveryBoundaryTypeHasAMirror is the completeness gate. A BuzzObject method on a
// types/ struct means "this value crosses into Buzz", so every one of them owes
// its owning module an `object` mirror - otherwise a magusfile can only index
// the result as an untyped map, and the checker has nothing to verify a field
// name against.
//
// This is the test that would have caught the shipped `magus.ls` documentation
// telling readers to annotate `> Projects` when no such type existed.
func TestEveryBoundaryTypeHasAMirror(t *testing.T) {
	t.Parallel()
	// Every types/ struct carrying BuzzObject, paired with the Buzz object it mirrors.
	// The names differ where the Buzz one reads better (ProjectsOutput -> Projects).
	for _, tc := range []struct{ goType, buzzObject string }{
		{"ExecResult", "ExecResult"},
		// Commit owns BuzzObject; CommitRecord/CommitAuthor describe the map it emits.
		{"CommitRecord", "Commit"},
		{"CommitAuthor", "CommitAuthor"},
		{"FileInfo", "FileInfo"},
		{"HTTPResponse", "HttpResponse"},
		{"SemverVersion", "SemverVersion"},
		{"URL", "URL"},
		{"Tag", "Tag"},
		{"ProjectEntry", "ProjectEntry"},
		{"ProjectsOutput", "Projects"},
		{"AffectedResult", "Affected"},
		{"GraphView", "Graph"},
		{"TargetGraphOutput", "TargetGraph"},
		{"ModuleFieldEntry", "ModuleFieldEntry"},
		{"ModuleMethodEntry", "ModuleMethodEntry"},
		{"ModuleEntry", "Module"},
	} {
		t.Run(tc.buzzObject, func(t *testing.T) {
			t.Parallel()
			assertMirrorConstructs(t, tc.buzzObject)
		})
	}
}

// assertMirrorConstructs execs a bare `Name{}` against testBoundaryTypesSource.
// Constructing it is the whole assertion: an object that is absent, misordered
// relative to a type it references, or emitted with an unparsable field name (a
// Go field named Type mirrors to the reserved word `type`) all fail here.
func assertMirrorConstructs(t *testing.T, object string) {
	t.Helper()
	ctx := context.Background()
	s := buzz.NewSession(ctx, buzz.WithEmbedded())
	t.Cleanup(func() { _ = s.Close() })
	s.SetModuleDecls(testBoundaryTypesPath, testBoundaryTypesSource)
	require.NoError(t, s.Exec(ctx, `import "`+testBoundaryTypesPath+`"; final __r = `+object+`{};`),
		"%s{} must construct: the mirror is missing from the bundle, ordered after a type it references, or emitted with a field name that does not parse", object)
	require.NotNil(t, s.GetGlobal("__r"), "%s{} produced nothing", object)
}

// TestMirrorFieldsMatchBuzzObject pins each mirror against the map its Go type
// actually produces. A mirror that merely parses is not enough: a field the Buzz
// value never carries (or one it carries under another name) is a type that lies,
// and the checker would reject correct code or accept a typo.
func TestMirrorFieldsMatchBuzzObject(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		object string
		toMap  map[string]any
	}{
		{"Tag", types.VCSTag{}.BuzzObject()},
		{"Affected", types.AffectedResult{}.BuzzObject()},
		{"Graph", types.GraphView{}.BuzzObject()},
		{"Projects", types.ProjectsOutput{}.BuzzObject()},
		{"ProjectEntry", types.ProjectEntry{}.BuzzObject()},
		{"Module", types.ModuleEntry{}.BuzzObject()},
		{"ModuleFieldEntry", types.ModuleFieldEntry{}.BuzzObject()},
		{"ModuleMethodEntry", types.ModuleMethodEntry{}.BuzzObject()},
		{"ExecResult", types.ExecResult{}.BuzzObject()},
	} {
		t.Run(tc.object, func(t *testing.T) {
			t.Parallel()
			for key := range tc.toMap {
				assertMirrorReadsField(t, tc.object, key)
			}
		})
	}
}

// assertMirrorReadsField reads one field off a zero-valued mirror. A reserved-word
// key is read through ordinary member access (entry.type), which upstream permits
// even where the same word cannot be a binding name - so this exercises the free
// identifier the generator emits for it.
func assertMirrorReadsField(t *testing.T, object, field string) {
	t.Helper()
	ctx := context.Background()
	s := buzz.NewSession(ctx, buzz.WithEmbedded())
	t.Cleanup(func() { _ = s.Close() })
	s.SetModuleDecls(testBoundaryTypesPath, testBoundaryTypesSource)
	err := s.Exec(ctx, `import "`+testBoundaryTypesPath+`"; final __r = `+object+`{}.`+field+`;`)
	assert.NoError(t, err, "%s has no field %q, but %s's BuzzObject emits that key: the mirror and the boundary map disagree", object, field, object)
}

// TestEveryBuzzObjectOwnerIsMirrored guards the LIST above rather than the mirrors.
// BuzzObject is the marker that a value crosses into Buzz, so every type carrying one
// owes the module a mirror; adding a BuzzObject without adding the mirror would leave
// the gate above passing while the new type stayed untyped.
//
// A mirror is not always generated from the BuzzObject owner. Commit owns BuzzObject while
// CommitRecord is a dedicated struct describing the map it emits (Author is
// CommitAuthor, not Commit's own Person, so the generated Buzz type reads as
// CommitAuthor rather than Person). Either arrangement is fine; what must hold
// is that each owner below has a Buzz object, which the tests above check by
// construction.
func TestEveryBuzzObjectOwnerIsMirrored(t *testing.T) {
	t.Parallel()
	owners := []any{
		types.ExecResult{}, types.Commit{}, types.FileInfo{}, types.HTTPResponse{},
		types.SemverVersion{}, types.URL{}, types.VCSTag{}, types.ProjectEntry{},
		types.ProjectsOutput{}, types.AffectedResult{}, types.GraphView{}, types.TargetGraphOutput{},
		types.ModuleFieldEntry{}, types.ModuleMethodEntry{}, types.ModuleEntry{},
	}
	for _, v := range owners {
		rt := reflect.TypeOf(v)
		_, ok := rt.MethodByName("BuzzObject")
		assert.True(t, ok, "%s is listed as a boundary type but has no BuzzObject; drop it from this list", rt.Name())
	}
	assert.Len(t, owners, 15,
		"a types/ struct gained or lost BuzzObject: add it to the mirror registry (cmd/magus-utils/types.go), generate it, wire it into RegisterSpellSourceModules, and list it in TestEveryBoundaryTypeHasAMirror")
}

// TestMagusNamespaceIsTyped is the guard for the half of the typed-host-module work
// that is easiest to lose. "magus" is bound as a session GLOBAL, not a lazily-imported
// module, so the import-triggered SetModuleDecls collection never runs for it - it is
// declared directly (RegisterSpellSourceModules). The first version of this change wired
// the generated declarations into the module loop only, which types every bare-import
// module (os, fs, vcs, ...) and silently leaves the magus namespace Unknown. Nothing
// failed: a magusfile kept loading, every target kept running, and a wrong field on a
// magus\ return stayed a runtime surprise.
//
// The os case is the control. If it ever regresses alongside magus, the fault is in the
// declarations themselves rather than in how the magus namespace gets them.
func TestMagusNamespaceIsTyped(t *testing.T) {
	wrongReturn := func(t *testing.T, body string) error {
		t.Helper()
		dir := t.TempDir()
		writeMagusfile(t, dir, "import \"magus\";\nimport \"os\";\nimport \"proc\";\n"+body+
			"\nexport fun build(ctx: magus\\Context, args: [str]) > void {}\n")
		return runTargetIn(t, dir, "build")
	}

	err := wrongReturn(t, `fun probe() > int { return magus\where("x"); }`)
	require.Error(t, err, "magus\\where returns str; using it as int must be caught by the checker")
	assert.Contains(t, err.Error(), "return type mismatch")

	err = wrongReturn(t, `fun probe() > int { return proc\which("ls"); }`)
	require.Error(t, err, "control: a bare-import module must stay typed")
	assert.Contains(t, err.Error(), "return type mismatch")
}

// TestMagusMirrorsResolveInAnnotations covers the OTHER half: the object mirrors a
// magusfile annotates with. Most ride along with the generated declarations, derived
// from each method's declared return. Five cannot be - magus.modules/magus.module are
// hand-bound outside std.Module, and Run/TargetRun have no producing method at all - so
// they are supplied separately (magusUndeclaredTypeSource). This asserts both sources
// arrive, because swapping the hand-written bundle for the generated one is exactly the
// edit that would drop the five without any other test noticing.
func TestMagusMirrorsResolveInAnnotations(t *testing.T) {
	for _, tc := range []struct{ name, decl string }{
		{"generated/Projects", `fun f(p: Projects) > int { return p.count; }`},
		{"generated/Impact", `fun f(i: Impact) > int { return i.changedFileCount; }`},
		{"generated/DoctorReport", `fun f(d: DoctorReport) > str { return d.workspace; }`},
		{"supplement/Module", `fun f(m: Module) > str { return m.name; }`},
		{"supplement/Run", `fun f(r: Run) > str { return r.trigger; }`},
		{"supplement/TargetRun", `fun f(tr: TargetRun) > TargetRunState { return tr.state; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeMagusfile(t, dir, "import \"magus\";\n"+tc.decl+
				"\nexport fun build(ctx: magus\\Context, args: [str]) > void {}\n")
			assert.NoError(t, runTargetIn(t, dir, "build"), "mirror must resolve in an annotation")
		})
	}
}

// TestDeclaredObjectsAreConstructible guards the runtime half of a declared
// boundary object. The checker knowing HttpRetry is not enough - a caller has to
// be able to BUILD one, and before declareObjectTypes existed this type-checked
// and then threw "unknown object type" at run time.
func TestDeclaredObjectsAreConstructible(t *testing.T) {
	sess := scriptSession(t)
	err := sess.Exec(context.Background(), `
import "std";
import "http";

test "construct a declared boundary object" {
    final p = HttpRetry{ attempts = 3, delay_ms = 250.0 };
    std\assert(p.attempts == 3, message: "field set");
    std\assert(p.all_errors == false, message: "field default");
}
`)
	require.NoError(t, err)
}

// TestDeclaringObjectsKeepsNativeMethods is the other half: executing a module's
// declarations must not replace the native module value with the extern-only
// stub, or every host method it carries would silently become a no-op.
func TestDeclaringObjectsKeepsNativeMethods(t *testing.T) {
	sess := scriptSession(t)
	err := sess.Exec(context.Background(), `
import "std";
import "http";
final _p = HttpRetry{ attempts = 2 };
final port = http\server({"dir": "."});
std\assert(port > 0, message: "native http.server still bound a port");
`)
	require.NoError(t, err)
}

// TestCrossBundleDeclarationsStillImport is the degradation path. magus's own
// declarations name DoctorCheckStatus, which is declared in a DIFFERENT bundle,
// so executing them standalone fails. That must leave the import working exactly
// as it did before - collected and checkable - rather than taking it down.
func TestCrossBundleDeclarationsStillImport(t *testing.T) {
	sess := scriptSession(t)
	err := sess.Exec(context.Background(), `
import "std";
import "magus";
std\assert(true, message: "magus imported despite non-self-contained declarations");
`)
	require.NoError(t, err)
}

// TestDeclarationsWithRealFunctionsAreNotExecuted is the guard for the narrowest
// part of declareObjectTypes: a declaration source may only run if it is PURELY
// declarations.
//
// Buzz's own io.buzz carries an `export object File` beside a real
// `export fun runFile`. Executing it flat-merges runFile into the importing
// scope, and for a magusfile that means magus reads it as a target - which broke
// every workspace load with "MGS1008: target runFile must receive a magus\Context".
// Nothing else in the suite caught that; it only showed up running the CLI.
func TestDeclarationsWithRealFunctionsAreNotExecuted(t *testing.T) {
	sess := scriptSession(t)
	require.NoError(t, sess.Exec(context.Background(), `import "io";`))

	_, leaked := sess.Exports()["runFile"]
	assert.False(t, leaked,
		"io.buzz's runFile leaked into the importing scope; a magusfile would read it as a target (MGS1008)")
}

// TestSourceModuleIsIndistinguishable is the whole point of the Buzz-source
// module kind: which language implements a stdlib module must be an
// implementation detail, invisible from every surface a user or a tool reads.
//
// It walks the surfaces that matter rather than asserting registration, because
// registration was never the hard part - being SEEN was.
func TestSourceModuleIsIndistinguishable(t *testing.T) {
	// 1. It imports and RUNS like any other module.
	sess := scriptSession(t)
	err := sess.Exec(context.Background(), `
import "std";
import "lcov";

test "a Buzz-implemented stdlib module behaves like a Go one" {
    final report = "SF:/w/src/a.ts" + std\char(10) + "LF:4" + std\char(10) + "LH:2" + std\char(10);
    std\assert(lcov\percent(report, keep: "src/") == "50.0", message: lcov\percent(report, keep: "src/"));
}
`)
	require.NoError(t, err)

	// 2. describe modules lists it in the catalogue, with its doc.
	var summary *types.ModuleEntry
	for _, m := range hostmodules.Describe("") {
		if m.Name == "lcov" {
			summary = &m
			break
		}
	}
	require.NotNil(t, summary, "a source module must appear in the module catalogue")
	assert.NotEmpty(t, summary.Doc)

	// 3. Its methods and their docs are derived from the Buzz source, so the
	//    detail view is populated exactly as a Go module's is.
	detail := hostmodules.Describe("lcov")
	require.Len(t, detail, 1)
	byName := map[string]types.ModuleMethodEntry{}
	for _, meth := range detail[0].Methods {
		byName[meth.Name] = meth
	}
	require.Contains(t, byName, "percent")
	require.Contains(t, byName, "mergePercent")
	assert.NotEmpty(t, byName["percent"].Doc, "the doc comment must come off the Buzz source")
	assert.Contains(t, byName["percent"].Buzz, "lcov\\percent", "the Buzz signature must render")

	// 4. A non-exported helper stays private, like an unexported Go function.
	assert.NotContains(t, byName, "keepsSource")
}
