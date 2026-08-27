package bindings

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/secret"
	"github.com/egladman/magus/internal/spellruntime"
	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rootOnlyWS is a WorkspaceRepository that answers only Root(); the file resolver
// reads nothing else off the workspace, so the embedded nil interface is never called.
type rootOnlyWS struct {
	types.WorkspaceRepository
	root string
}

func (w rootOnlyWS) Root() string { return w.root }

func TestLoadLocalBuzzLibraryUsesProjectRootImports(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "lib/text.buzz", `export fun value() > str { return "ok"; }`)
	path := filepath.Join(root, "lib", "helper.buzz")
	writeFile(t, root, "lib/helper.buzz", `import "lib/text" as text;
export fun helper() > str { return text.value(); }`)

	ctx := interp.WithSource(context.Background(), &interp.Source{Dir: root})
	_, _, err := loadBuzzSpell(ctx, path)
	assert.ErrorIs(t, err, spellruntime.ErrNotASpell)
}

// TestProjectImportFileResolver exercises the reserved `.file(rel)` member on a
// project-import handle: at runtime `b.file("go.mod")` resolves to the authoritative
// WORKSPACE-relative path of a file in the imported project and feeds magus.inputs
// without error, whether or not a workspace is on the context (a bare script degrades
// to a cleaned relative path rather than failing).
func TestProjectImportFileResolver(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a/magusfile.buzz", `import "magus";
import "project/../b" as b;
export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.readsFiles(b.file("go.mod"));
}`)
	writeFile(t, root, "b/magusfile.buzz", `import "magus";
export fun compile(ctx: magus\Context, args: [str]) > void {}`)
	writeFile(t, root, "b/go.mod", "module b\n")

	src, err := interp.Find(filepath.Join(root, "a"))
	require.NoError(t, err)

	t.Run("with a workspace resolves and feeds magus.inputs", func(t *testing.T) {
		// wsRoot is derived from src.Dir so it stays symlink-consistent (t.TempDir
		// resolves through /private on macOS). callerRel is then "a", so the resolver
		// yields "b/go.mod" and magus.inputs (a runtime no-op) accepts it.
		ctx := types.WithWorkspace(context.Background(), rootOnlyWS{root: filepath.Dir(src.Dir)})
		_, err := interp.RunDir(ctx, filepath.Join(root, "a"), "build", nil)
		require.NoError(t, err, "b.file(...) must resolve and feed magus.inputs without error")
	})

	t.Run("without a workspace degrades without error", func(t *testing.T) {
		_, err := interp.RunDir(context.Background(), filepath.Join(root, "a"), "build", nil)
		require.NoError(t, err, "a bare script must degrade the resolver, not error")
	})
}

// TestMagusGraphReturnTypesMatchMirrors executes the exact typed magusfile
// boundary a renderer uses. Parsing only a target declaration skips its body,
// which was the coverage gap: an absent host-owned mirror surfaced only when the
// renderer actually loaded.
func TestMagusGraphReturnTypesMatchMirrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "magusfile.buzz", `import "magus";
export fun preflight(ctx: magus\Context, args: [str]) > void {
    final _targets: TargetGraph = null;
    final _graph: Graph = null;
}`)

	_, err := interp.RunDir(context.Background(), dir, "preflight", nil)
	require.NoError(t, err)
}

// writeFile writes content under dir/rel, creating parent dirs.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// parseMagusfile evaluates the magusfile in dir in parse mode, which fires
// its top-level spell imports / magus.project calls.
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

// TestBuzzLocalSpellImport verifies a workspace-local Buzz spell is importable by
// path — `import "spells/widget"` resolves ./spells/widget.buzz, binds the handle
// under the basename (widget), and binding it via magus.project registers
// the spell by value.
func TestBuzzLocalSpellImport(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir) // the import resolves relative to the cwd

	writeFile(t, dir, "spells/widget.buzz", `import "magus/spell";
export fun mgs_getName() > str { return "widgetimport"; }
export fun mgs_listRequiredGlobs() > [Path] { return [Path{value = "**/*.ts"}]; }
export fun mgs_listTargets() > any {
    return {"build": {"bin": "npm", "args": ["run", "build"]}};
}
`)
	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "spells/widget";
magus.project(".", {"spells": [widget]});`)

	require.NoError(t, parseMagusfile(t, dir))

	sp, ok := project.DefaultSpellRegistry().Lookup("widgetimport")
	require.True(t, ok, "widgetimport not registered after import + magus.project")
	assert.Contains(t, sp.Targets(), "build")
	assert.Contains(t, sp.Sources(), "**/*.ts")

	// Idempotent: re-parsing must not panic or error.
	require.NoError(t, parseMagusfile(t, dir), "second parse (idempotency)")
}

// TestBuzzSpellImport verifies that a built-in spell is importable via
// `import "magus/spell/<name>"` in a Buzz magusfile, binding the spell handle
// under its basename (go, docker, etc.) with the expected name and callable ops.
func TestBuzzSpellImport(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "magus/spell/go";
import "magus/spell/docker";

export fun check(ctx: magus\Context, args: [str]) > void {
    if (go.name != "go") { error("go.name mismatch: " + go.name); }
    if (docker.name != "docker") { error("docker.name mismatch: " + docker.name); }
}
`)

	_, err := interp.RunDir(context.Background(), dir, "check", nil)
	require.NoError(t, err)
}

// TestBuzzSpellImportNoOps verifies that a Buzz spell with no ops still registers
// successfully — ops is optional; the spell is registered with an empty target list.
func TestBuzzSpellImportNoOps(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "spells/noops.buzz", `export fun mgs_getName() > str { return "noopsbuzzspell"; }
`)
	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "spells/noops";
magus.project(".", {"spells": [noops]});`)

	require.NoError(t, parseMagusfile(t, dir), "parse should not fail")
	_, ok := project.DefaultSpellRegistry().Lookup("noopsbuzzspell")
	assert.True(t, ok, "noopsbuzzspell should be registered even with no ops")
}

// TestBuzzSpellMethodForwardsOpts verifies a Buzz spell handle's per-target method
// (widget.capture(ctx, opts)): opts.args are appended to the target's base argv and the
// command runs in opts.cwd — what lets a magusfile drive a flag-carrying tool (e.g.
// docker.build({cwd: "..", args: [...]})) through the spell instead of proc.exec. It
// also checks listTargets() still exposes the op names for introspection.
func TestBuzzSpellMethodForwardsOpts(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// The "capture" target writes its forwarded args ($@) into captured.txt in the
	// process cwd, so the test can assert both the args and the working directory.
	writeFile(t, dir, "spells/widget.buzz", `export fun mgs_getName() > str { return "optswidget"; }
export fun mgs_listTargets() > any {
    return {"capture": {"bin": "sh", "args": ["-c", "printf '%s ' \"$@\" > captured.txt", "sh"]}};
}
`)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "spells/widget";
export fun build(ctx: magus\Context, args: [str]) > void {
    final names = widget.listTargets();
    if (names[0] != "capture") { error("listTargets mismatch"); }
    widget.capture(ctx.withCwd("sub"), {"args": ["alpha", "beta"]});
}`)

	_, runErr := interp.RunDir(context.Background(), dir, "build", nil)
	require.NoError(t, runErr, "invoking spell method with opts")

	// The file must land in the context's cwd (sub/), proving withCwd was honored, and
	// contain exactly the appended args, proving opts.args reached the forked command.
	// cwd rides the context now, not the opts table: only the context reaches the cache
	// key, and an opts-table cwd changed what ran while the key said otherwise.
	got, err := os.ReadFile(filepath.Join(dir, "sub", "captured.txt"))
	require.NoError(t, err, "expected captured.txt under opts.cwd")
	assert.Equal(t, "alpha beta ", string(got))
}

// TestBuzzSpellMethodEnv verifies ctx.withEnv overlays the forked subprocess
// environment — the cross-compile vars (GOOS/GOARCH/CGO_ENABLED) a release build
// needs. The capture target echoes $MYVAR into out.txt; the call sets it via withEnv,
// proving the overlay reached the subprocess.
func TestBuzzSpellMethodEnv(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "spells/widget.buzz", `export fun mgs_getName() > str { return "envwidget"; }
export fun mgs_listTargets() > any {
    return {"capture": {"bin": "sh", "args": ["-c", "printf \"$MYVAR\" > out.txt"]}};
}
`)
	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "spells/widget";
export fun build(ctx: magus\Context, args: [str]) > void {
    widget.capture(ctx.withEnv({"MYVAR": "overridden"}));
}`)

	_, runErr := interp.RunDir(context.Background(), dir, "build", nil)
	require.NoError(t, runErr, "invoking spell method with env")

	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	require.NoError(t, err, "expected out.txt")
	assert.Equal(t, "overridden", string(got))
}

// TestBuzzSpellCaptureReturnsObject verifies a capture=true target returns the
// {stdout, stderr, code, ok} object, accessed with dot syntax the way a
// magusfile reads proc.exec(...).stdout.
func TestBuzzSpellCaptureReturnsObject(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "spells/widget.buzz", `export fun mgs_getName() > str { return "capbuzzwidget"; }
export fun mgs_listTargets() > any {
    return {"hash": {"bin": "sh", "args": ["-c", "printf abc123"], "capture": true}};
}
`)
	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "spells/widget";
export fun build(ctx: magus\Context, args: [str]) > void {
    final r = widget.hash(ctx);
    if (r.stdout != "abc123") { error("stdout mismatch: " + r.stdout); }
    if (r.code != 0) { error("code mismatch"); }
    if (r.ok != true) { error("ok mismatch"); }
}`)

	_, runErr := interp.RunDir(context.Background(), dir, "build", nil)
	require.NoError(t, runErr, "captured buzz target")
}

// TestBuzzSpellPipeStdin verifies pipe-style chaining: a captured target's stdout
// is fed as the stdin of the next target via opts.stdin — the Unix-pipe primitive.
func TestBuzzSpellPipeStdin(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "spells/widget.buzz", `export fun mgs_getName() > str { return "pipewidget"; }
export fun mgs_listTargets() > any {
    return {
        "emit": {"bin": "sh", "args": ["-c", "printf alpha"], "capture": true},
        "shout": {"bin": "tr", "args": ["a-z", "A-Z"], "capture": true}
    };
}
`)
	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "spells/widget";
export fun build(ctx: magus\Context, args: [str]) > void {
    final a = widget.emit(ctx);
    final b = widget.shout(ctx, {"stdin": a.stdout});
    if (b.stdout != "ALPHA") { error("pipe mismatch: " + b.stdout); }
}`)

	_, runErr := interp.RunDir(context.Background(), dir, "build", nil)
	require.NoError(t, runErr, "pipe stdin")
}

// TestVcsCommitFacadeBuzz exercises the vcs.commit() facade end-to-end the way
// magusfile.buzz build_date() does — including the (c.committer ?? c.author)
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
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%v: %s", args, out)
	}

	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "vcs";
export fun check(ctx: magus\Context, args: [str]) > void !> any {
    final c = vcs.commit();
    if (c.subject != "hello") { error("subject: " + c.subject); }
    if (c.author.name != "A") { error("author: " + c.author.name); }
    if (c.date == "") { error("date empty"); }
    if (c.id == "") { error("id empty"); }
}`)

	_, runErr := interp.RunDir(context.Background(), dir, "check", nil)
	require.NoError(t, runErr, "vcs.commit facade")
}

// TestVcsCommitRaisesOutsideRepo pins that an unavailable commit RAISES rather than
// handing back a zero object to sniff.
//
// This test previously asserted the opposite - that vcs.commit() returned an object with
// every field empty, and that callers should test `c.date == ""`. That is not how a Buzz
// function reports failure (upstream declares the error in the signature and the caller
// writes try/catch), and "" is a value some of those fields could legitimately hold, so
// the check could never distinguish "no commit" from "empty answer". Worse, it made the
// check optional: every call site had to remember it, and a magusfile that forgot
// interpolated an empty commit into a version string with nothing to surface it.
func TestVcsCommitRaisesOutsideRepo(t *testing.T) {
	dir := t.TempDir() // a bare temp dir, not under version control
	t.Chdir(dir)
	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "vcs";
export fun check(ctx: magus\Context, args: [str]) > void {
    var raised = false;
    try {
        vcs.commit();
    } catch (e) {
        raised = true;
    }
    if (!raised) { magus.fatal("vcs.commit should raise outside a repo, not return a zero object"); }
}`)
	_, runErr := interp.RunDir(context.Background(), dir, "check", nil)
	require.NoError(t, runErr, "vcs.commit raises outside a repo")
}

// TestEngineDescriptorParity locks the engine-agnostic mgs_ contract: a Buzz spell
// declaring every optional mgs_ function with record-shaped ops resolves to the
// expected Descriptor. It guards the resolver (internal/spellruntime/resolve.go)
// against dropping fields.
func TestEngineDescriptorParity(t *testing.T) {
	buzzSrc := `import "magus/spell";
export fun mgs_getName() > str { return "parity_buzz"; }
export fun mgs_listRequiredGlobs() > [Path] { return [Path{value = "**/*.rb"}, Path{value = "Gemfile.lock"}]; }
export fun mgs_listProvidedGlobs() > [Path] { return [Path{value = "vendor/bundle/**"}]; }
export fun mgs_getVersionProbe() > [str] { return ["ruby", "--version"]; }
export fun mgs_isOpaque() > bool { return false; }
export fun mgs_listTargets() > any {
    return {"rspec": {"bin": "bundle", "args": ["exec", "rspec"]}};
}
`

	bindBuzzSpell := func(t *testing.T, src string) {
		t.Helper()
		dir := t.TempDir()
		t.Chdir(dir)
		writeFile(t, dir, "spells/parity.buzz", src)
		writeFile(t, dir, "magusfile.buzz", `import "magus";
import "spells/parity";
magus.project(".", {"spells": [parity]});`)
		require.NoError(t, parseMagusfile(t, dir), "parse")
	}

	bindBuzzSpell(t, buzzSrc)

	buzzSp, ok := project.DefaultSpellRegistry().Lookup("parity_buzz")
	require.True(t, ok, "parity_buzz not registered")

	assert.Equal(t, []string{"**/*.rb", "Gemfile.lock"}, buzzSp.Sources())
	assert.Equal(t, []string{"vendor/bundle/**"}, buzzSp.Outputs())
	assert.Equal(t, []string{"rspec"}, buzzSp.Targets())
	assert.False(t, buzzSp.Opaque(), "opaque should be false")
}

// TestSuggestSpellName covers both suggestion paths: a known language/tool alias
// (the common slip) and nearest-handle edit distance (a typo), plus the
// no-suggestion floor.
func TestSuggestSpellName(t *testing.T) {
	cases := []struct{ in, want string }{
		// Real synonyms. The former rows here - python->py, rust->rs, markdown->md,
		// golang->go - are gone: each spell now IS the name a user reaches for, so
		// those imports resolve outright and never reach a suggestion.
		{"javascript", "typescript"},
		{"js", "typescript"},
		{"node", "typescript"},
		{"cargo", "rust"},
		{"python3", "python"},
		{"JavaScript", "typescript"}, // alias lookup is case-insensitive
		{"dcoker", "docker"},         // edit distance
		{"cosgin", "cosign"},         // edit distance
		{"zzzzzzzzzz", ""},           // nothing within threshold
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, suggestSpellName(c.in), "suggestSpellName(%q)", c.in)
	}
}

// TestCheckSpellImports verifies valid handles pass (built-in and host-registered)
// and an unknown handle yields a did-you-mean naming the right one.
func TestCheckSpellImports(t *testing.T) {
	require.NoError(t, checkSpellImports(nil))
	require.NoError(t, checkSpellImports([]string{"go", "typescript", "markdown"}),
		"built-in and host-registered handles must pass")

	// magusfile IS registered - dispatch needs it - so the generic registered-handle
	// check would accept this import. It is rejected explicitly instead: the handle
	// binds nothing an author can use, and accepting it taught readers magusfile was
	// a toolchain adapter like go or buf.
	err := checkSpellImports([]string{"magusfile"})
	require.Error(t, err, "importing the magusfile driver must fail")
	assert.Contains(t, err.Error(), string(types.MagusfileIsNotASpell))
	assert.Contains(t, err.Error(), "magusfile is not a spell")

	// javascript is a real SYNONYM for the typescript spell, not an abbreviation of
	// it - the abbreviation aliases went away when spells took the names users
	// already reach for.
	err = checkSpellImports([]string{"go", "javascript"})
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, `"javascript"`)
	assert.Contains(t, msg, `did you mean "typescript"`)
	assert.Contains(t, msg, `import "magus/spell/typescript"`)
}

// TestSpellImportSuggestionOnParse is the end-to-end path: a magusfile importing a
// wrong handle fails at load with the suggestion, before the handle surfaces as a
// disconnected "undefined" error.
func TestSpellImportSuggestionOnParse(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "magus/spell/javascript";
magus.project({"spells": [javascript]});
export fun build(ctx: magus\Context, args: [str]) > void {}`)

	err := parseMagusfile(t, dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `did you mean "typescript"`)
	assert.Contains(t, err.Error(), "magusfile.buzz", "error must name the offending file")
}

// TestSpellImportCaughtWithTopLevelControlFlow is the regression guard for the
// parser-mode bug: magusfiles load under ParseEmbedded, where top-level statements
// are allowed. If the check parsed with strict buzz.Parse instead, a file with a
// top-level `if` (the repo's own magusfile.buzz has one) would fail to parse, the
// handle list would come back empty, and the bad handle would slip through to the
// disconnected "undefined" error the check exists to replace.
func TestSpellImportCaughtWithTopLevelControlFlow(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "magus/spell/javascript";
if (1 > 0) {}
magus.project({"spells": [javascript]});
export fun build(ctx: magus\Context, args: [str]) > void {}`)

	err := parseMagusfile(t, dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `did you mean "typescript"`,
		"a top-level statement must not make the check silently skip")
}

// TestSpellImportValidOnParse guards against false positives: a correct built-in
// import loads cleanly.
func TestSpellImportValidOnParse(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "magus/spell/go";
magus.project({"spells": [go]});
export fun build(ctx: magus\Context, args: [str]) > void { go["go-build"](); }`)

	require.NoError(t, parseMagusfile(t, dir))
}

// TestProjectImportResolvesInRunMode guards the run-mode source-availability fix.
// `import "project/<path>"` resolves via resolveProjectImport, which needs the
// magusfile Source on the context. Run mode used to reach the import with a nil
// Source (it was set only for target dispatch), so the cross-project handle bound
// nothing — tolerable while imports silently no-op'd, but once an unresolved import
// is a hard error, that silent no-op became a spurious "module not found". This
// runs (not just parses) a target whose magusfile imports a sibling project; it
// must load without error.
func TestProjectImportResolvesInRunMode(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a/magusfile.buzz", `import "magus";
import "project/../b" as b;
export fun go(ctx: magus\Context, args: [str]) > void {}`)
	writeFile(t, root, "b/magusfile.buzz", `import "magus";
export fun build(ctx: magus\Context, args: [str]) > void {}`)

	_, runErr := interp.RunDir(context.Background(), filepath.Join(root, "a"), "go", nil)
	require.NoError(t, runErr,
		"a project/ import must resolve when a target is run, not only parsed")
}

// TestProjectImportHandleNeedsAndDirectCall exercises the new cross-project
// dependency surface end to end: `import "project/<path>" as b` binds each of the
// sibling's exported targets as a callable handle, so ctx.needs(b.build) declares
// the dependency (recognized by value identity through the session's handle registry)
// and b.build() dispatches it directly. Under interp.RunDir there is no CrossDispatch
// coordinator, so the actual cross dispatch no-ops; this asserts the binding,
// identity-recognition, and direct-invocation paths all load and run without error
// (a bad target name in either form would fail at load).
func TestProjectImportHandleNeedsAndDirectCall(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a/magusfile.buzz", `import "magus";
import "project/../b" as b;
export fun go(ctx: magus\Context, args: [str]) > void {
    ctx.needs(b.build);
    b.build();
}`)
	writeFile(t, root, "b/magusfile.buzz", `import "magus";
export fun build(ctx: magus\Context, args: [str]) > void {}`)

	_, runErr := interp.RunDir(context.Background(), filepath.Join(root, "a"), "go", nil)
	require.NoError(t, runErr,
		"ctx.needs(b.build) and the direct b.build() call must both resolve the cross handle")
}

// TestSpellImportIgnoresComments is the payoff of reading the AST rather than the
// raw text: a wrong handle that appears only in a comment must not be flagged.
func TestSpellImportIgnoresComments(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "magus/spell/go";
// for a TS project you would instead: import "magus/spell/typescript";
magus.project({"spells": [go]});
export fun build(ctx: magus\Context, args: [str]) > void { go["go-build"](); }`)

	require.NoError(t, parseMagusfile(t, dir), "a bad handle in a comment must not be flagged")
}

func writeSpellMagusfile(t *testing.T, dir, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte(content), 0o644))
}

// TestSpellModuleForkTarget verifies that a fork target (which has no function
// in the compiled spell table — only a spells.json data entry) is still
// callable programmatically. registerSpells overlays a Go-backed function for
// each fork target; here go-vet is a fork no-op that must resolve to
// a function and run without error.
func TestSpellModuleForkTarget(t *testing.T) {
	dir := t.TempDir()
	writeSpellMagusfile(t, dir, `
import "magus";
import "magus/spell/go";

export fun check(ctx: magus\Context, _args: [str]) > void {
    if (go.name != "go") { throw "spell not found"; }
    if (go["go-vet"] == null) { throw "fork go-vet must be a function (overlay)"; }
}
`)
	_, runErr := interp.RunDir(context.Background(), dir, "check", nil)
	require.NoError(t, runErr)
}

// TestSpellModuleRequireBuiltin verifies a built-in spell can be imported as a
// typed module: import "magus/spell/docker" binds the handle under its basename
// (docker), and the resolved value is the live spell handle (docker-build is callable).
func TestSpellModuleRequireBuiltin(t *testing.T) {
	dir := t.TempDir()
	writeSpellMagusfile(t, dir, `
import "magus";
import "magus/spell/docker";

export fun check(ctx: magus\Context, _args: [str]) > void {
    if (docker.name != "docker") { error("name mismatch: " + docker.name); }
    if (docker["docker-build"] == null) { throw "docker-build op must be callable as a method"; }
}
`)
	_, runErr := interp.RunDir(context.Background(), dir, "check", nil)
	require.NoError(t, runErr)
}

// TestSpellModuleRequireUnknownFailsToCompile verifies a misspelled built-in
// module is a compile error — the point of typed import — not a silent runtime nil.
func TestSpellModuleRequireUnknownFailsToCompile(t *testing.T) {
	dir := t.TempDir()
	writeSpellMagusfile(t, dir, `
import "magus";
import "magus/spell/dockr";
magus.project(".", {"spells": [dockr]});
`)
	_, err := interp.RunDir(context.Background(), dir, "noop", nil)
	assert.Error(t, err, "expected a compile error for the misspelled module")
}

// TestSpellModuleRequireLocal verifies a workspace-local Buzz spell is
// importable by path — import "spells/locreq" resolves ./spells/locreq.buzz,
// binds the handle under the basename (locreq), so its target dispatches.
func TestSpellModuleRequireLocal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "spells"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "spells", "locreq.buzz"), []byte(`
export fun mgs_getName() > str { return "locreq"; }
export fun mgs_listTargets() > any {
    return {"build": {"bin": "true"}};
}
`), 0o644))
	writeSpellMagusfile(t, dir, `
import "magus";
import "spells/locreq";

export fun check(ctx: magus\Context, args: [str]) > void {
    if (locreq.name != "locreq") { error("name mismatch: " + locreq.name); }
}
magus.project(".", {"spells": [locreq]});
`)
	_, runErr := interp.RunDir(context.Background(), dir, "check", nil)
	require.NoError(t, runErr)
}

// TestSpellMultipleFields verifies that the spell handle for the go spell has
// the expected fork-target methods beyond just name.
func TestSpellMultipleFields(t *testing.T) {
	dir := t.TempDir()
	writeSpellMagusfile(t, dir, `
import "magus";
import "magus/spell/go";

export fun check(ctx: magus\Context, args: [str]) > void {
    if (go.name != "go") { error("name mismatch: " + go.name); }
    if (go["go-build"] == null) { throw "go-build must be a function"; }
    if (go["go-fmt"] == null) { throw "go-fmt must be a function"; }
}
`)
	_, runErr := interp.RunDir(context.Background(), dir, "check", nil)
	require.NoError(t, runErr)
}

// TestDispatchOpInjectsDeclaredSecrets proves an op's declared Secrets are resolved
// through the run's secret resolver and land in the ONE child process's environment
// - the declarative escape hatch for a command op (static argv, can't call
// magus\secret.read) and for a "provided" project with no magusfile at all.
func TestDispatchOpInjectsDeclaredSecrets(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NPM_TOKEN", "s3cr3t-token")

	r := secret.New()
	ctx := secret.ContextWithResolver(context.Background(), r)

	ops := map[string]spells.Op{
		"publish": {Command: spells.Command{
			Bin:     "sh",
			Args:    []string{"-c", `printf "$NPM_TOKEN" > out.txt`},
			Secrets: map[string]string{"NPM_TOKEN": "NPM_TOKEN"},
		}},
	}

	_, err := dispatchOp(ctx, ops, nil, nil, spells.InvokeRequest{Target: "publish", Dir: dir})
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	require.NoError(t, err)
	assert.Equal(t, "s3cr3t-token", string(got), "the declared secret must reach the child's environment")
}

// TestDispatchOpSecretsRequireAResolver proves an op that declares Secrets fails
// loudly - naming the op - when no secret resolver is on the run, rather than
// silently spawning with the secret unset.
func TestDispatchOpSecretsRequireAResolver(t *testing.T) {
	ops := map[string]spells.Op{
		"publish": {Command: spells.Command{
			Bin:     "true",
			Secrets: map[string]string{"NPM_TOKEN": "NPM_TOKEN"},
		}},
	}
	_, err := dispatchOp(context.Background(), ops, nil, nil, spells.InvokeRequest{Target: "publish", Dir: t.TempDir()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"publish"`)
	assert.Contains(t, err.Error(), "no secret resolver is on this run")
}

// TestDispatchOpSecretsPropagateResolverError proves an unresolvable reference
// surfaces as a dispatch error naming both the op and the secret, rather than
// running the command with a missing value.
func TestDispatchOpSecretsPropagateResolverError(t *testing.T) {
	r := secret.New() // built-in env provider; NOT_SET_ANYWHERE is deliberately unset
	ctx := secret.ContextWithResolver(context.Background(), r)

	ops := map[string]spells.Op{
		"publish": {Command: spells.Command{
			Bin:     "true",
			Secrets: map[string]string{"TOKEN": "NOT_SET_ANYWHERE"},
		}},
	}
	_, err := dispatchOp(ctx, ops, nil, nil, spells.InvokeRequest{Target: "publish", Dir: t.TempDir()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"publish"`)
	assert.Contains(t, err.Error(), `"TOKEN"`)
}

// TestResolveSecretEnvMergesWithoutClobberingReservedEnv proves resolveSecretEnv
// merges resolved secrets alongside magus-reserved env (e.g. MAGUS_SYMBOL_INDEX, set
// by symbolIndexEnv for the scip op) without disturbing it - the existing
// symbolIndexEnv behavior is unaffected by an op that also declares secrets.
func TestResolveSecretEnvMergesWithoutClobberingReservedEnv(t *testing.T) {
	t.Setenv("NPM_TOKEN", "s3cr3t-token")
	r := secret.New()
	ctx := secret.ContextWithResolver(context.Background(), r)

	base := map[string]string{"MAGUS_SYMBOL_INDEX": "/cache/index.scip"}
	env, err := resolveSecretEnv(ctx, "scip", map[string]string{"NPM_TOKEN": "NPM_TOKEN"}, base)
	require.NoError(t, err)
	assert.Equal(t, "/cache/index.scip", env["MAGUS_SYMBOL_INDEX"], "reserved env must survive the merge")
	assert.Equal(t, "s3cr3t-token", env["NPM_TOKEN"])
}

// TestResolveSecretEnvCollisionErrors proves a declared secret whose env var name
// collides with a magus-reserved env var (MAGUS_SYMBOL_INDEX) is a load-time error
// naming the op and the colliding name, rather than a silent override or drop in a
// security-sensitive path.
func TestResolveSecretEnvCollisionErrors(t *testing.T) {
	r := secret.New()
	ctx := secret.ContextWithResolver(context.Background(), r)

	base := map[string]string{"MAGUS_SYMBOL_INDEX": "/cache/index.scip"}
	_, err := resolveSecretEnv(ctx, "scip", map[string]string{"MAGUS_SYMBOL_INDEX": "MAGUS_SYMBOL_INDEX"}, base)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"scip"`)
	assert.Contains(t, err.Error(), "MAGUS_SYMBOL_INDEX")
	assert.Contains(t, err.Error(), "collides")
}

// TestDispatchOpResolvesSymbolIndexRefInArgs proves the mechanism that replaced
// `sh -c 'scip-go --output "$MAGUS_SYMBOL_INDEX"'` in the built-in scip spells: a
// bare $MAGUS_SYMBOL_INDEX token in an op's Args is resolved by the runner (via
// opts.refs, wired in dispatchOp) to the real cache destination BEFORE the process
// forks - not left for a shell to expand. The op here writes its raw argv[1] (not
// an env var) to a file, so a script that merely referenced $MAGUS_SYMBOL_INDEX as
// an environment variable - which the child also receives, for a workspace-local
// scip spell that still shells out - would not make this test pass; only genuine
// engine-side substitution of the arg token does.
func TestDispatchOpResolvesSymbolIndexRefInArgs(t *testing.T) {
	ctx := context.Background()
	c, err := cache.Open(ctx, t.TempDir(), cache.WithMutable(true))
	require.NoError(t, err)
	ctx = cache.NewContext(ctx, c)

	projDir := t.TempDir()
	argvFile := filepath.Join(projDir, "argv.txt")
	ops := map[string]spells.Op{
		"scip": {Command: spells.Command{
			Bin:  "sh",
			Args: []string{"-c", `printf '%s' "$1" > "$2"`, "sh", "$MAGUS_SYMBOL_INDEX", argvFile},
		}},
	}

	_, err = dispatchOp(ctx, ops, nil, nil, spells.InvokeRequest{Target: symbols.IndexOp, Dir: projDir})
	require.NoError(t, err)

	got, err := os.ReadFile(argvFile)
	require.NoError(t, err)
	want := symbols.IndexPath(c.Dir(), projDir)
	assert.Equal(t, want, string(got), "the resolved arg must be the real index path")
	assert.NotContains(t, string(got), "$MAGUS_SYMBOL_INDEX", "the literal reference token must never reach the child's argv")
}

// alwaysTerminalProbe reports every fd as a terminal, so probeUntilReady takes its
// interactive wait branch instead of failing fast.
type alwaysTerminalProbe struct{}

func (alwaysTerminalProbe) IsTerminal(fd uintptr) bool                     { return true }
func (alwaysTerminalProbe) Size(fd uintptr) (width, height int, err error) { return 80, 24, nil }

// A failing probe must stop the op BEFORE it forks, and say what is actually wrong.
// Without this the op ran, the tool failed on its own terms, and magus reported a
// build failure for a project with nothing wrong with it.
func TestReadinessGateBlocksOpAndCarriesCode(t *testing.T) {
	readinessMemo.Clear()
	readiness := map[string]spells.Tool{
		"faketool": {Ready: spells.Command{Bin: "sh", Args: []string{"-c", "exit 1"}}},
	}
	op := spells.Op{Command: spells.Command{Bin: "faketool"}}

	err := checkReady(context.Background(), readiness, op, t.TempDir())
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.ToolNotReady), "want MGS3004, got %v", err)
	assert.Contains(t, err.Error(), "faketool")
	assert.Contains(t, err.Error(), "not ready")
}

// The tool's own diagnosis is what tells someone what to fix - `docker info` names the
// socket and asks whether the daemon is running. Paraphrasing it discarded the most
// actionable line available, so the probe's output must reach the error.
func TestReadinessErrorCarriesProbeOutput(t *testing.T) {
	readinessMemo.Clear()
	readiness := map[string]spells.Tool{
		"faketool": {Ready: spells.Command{Bin: "sh", Args: []string{"-c", "echo 'Cannot connect to the daemon at /var/run/x.sock' >&2; exit 1"}}},
	}
	op := spells.Op{Command: spells.Command{Bin: "faketool"}}

	err := checkReady(context.Background(), readiness, op, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/var/run/x.sock", "the probe's own message was discarded")
	assert.True(t, errors.Is(err, types.ToolNotReady), "wrapping must not lose the code")
}

// A silent probe still produces a usable error rather than a dangling colon.
func TestReadinessErrorWithoutProbeOutput(t *testing.T) {
	readinessMemo.Clear()
	readiness := map[string]spells.Tool{
		"faketool": {Ready: spells.Command{Bin: "sh", Args: []string{"-c", "exit 1"}}},
	}
	op := spells.Op{Command: spells.Command{Bin: "faketool"}}

	err := checkReady(context.Background(), readiness, op, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs it running")
	assert.True(t, errors.Is(err, types.ToolNotReady))
}

// A passing probe lets the op through.
func TestReadinessGateAllowsWhenProbePasses(t *testing.T) {
	readinessMemo.Clear()
	readiness := map[string]spells.Tool{
		"faketool": {Ready: spells.Command{Bin: "sh", Args: []string{"-c", "exit 0"}}},
	}
	op := spells.Op{Command: spells.Command{Bin: "faketool"}}
	assert.NoError(t, checkReady(context.Background(), readiness, op, t.TempDir()))
}

// An op whose tool declares no probe is never gated - the hadolint-beside-docker case.
func TestReadinessGateIgnoresUndeclaredTools(t *testing.T) {
	readinessMemo.Clear()
	readiness := map[string]spells.Tool{
		"docker": {Ready: spells.Command{Bin: "sh", Args: []string{"-c", "exit 1"}}},
	}
	lint := spells.Op{Command: spells.Command{Bin: "hadolint"}}
	assert.NoError(t, checkReady(context.Background(), readiness, lint, t.TempDir()),
		"linting a Dockerfile must not wait on the docker daemon")
}

// The probe forks, so it must run once per (bin, dir) and not once per op.
func TestReadinessProbeIsMemoized(t *testing.T) {
	readinessMemo.Clear()
	dir := t.TempDir()
	// A probe that succeeds only while the marker is absent: if it ran twice, the
	// second call would see the file it created and fail.
	readiness := map[string]spells.Tool{
		"faketool": {Ready: spells.Command{Bin: "sh", Args: []string{"-c", "test ! -e ran && touch ran"}}},
	}
	op := spells.Op{Command: spells.Command{Bin: "faketool"}}

	require.NoError(t, checkReady(context.Background(), readiness, op, dir))
	assert.NoError(t, checkReady(context.Background(), readiness, op, dir),
		"probe re-ran; a forked check must be memoized per (bin, dir)")
}

// The grace period is interactive-only, and this pins the half that would otherwise
// go unnoticed: under CI or an agent (no TTY) a failing probe must return AT ONCE.
// Waiting there burns the grace period per project to reach the same failure, and
// nobody is going to start a daemon mid-run.
func TestReadinessDoesNotWaitWhenNotInteractive(t *testing.T) {
	readinessMemo.Clear()
	readiness := map[string]spells.Tool{
		"faketool": {Ready: spells.Command{Bin: "sh", Args: []string{"-c", "exit 1"}}},
	}
	op := spells.Op{Command: spells.Command{Bin: "faketool"}}

	start := time.Now()
	err := checkReady(context.Background(), readiness, op, t.TempDir())
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Less(t, elapsed, readinessGrace/2,
		"a non-interactive run waited %s; the grace period must not apply without a TTY", elapsed)
}

// TestReadinessCancelledProbeIsNotMemoized proves a run cancelled mid-probe (Ctrl-C)
// does not poison the memo for the process's remaining lifetime. Before the fix,
// checkReady stored whatever probeUntilReady returned unconditionally, including
// ctx.Err() from the interactive wait loop's `case <-ctx.Done()` branch - so one
// cancelled run made every later op on the same (bin, dir) fail with "context
// canceled" for as long as the daemon lived, even once the tool was actually ready.
func TestReadinessCancelledProbeIsNotMemoized(t *testing.T) {
	readinessMemo.Clear()
	prev := tty.SystemProbe
	tty.SystemProbe = alwaysTerminalProbe{}
	defer func() { tty.SystemProbe = prev }()

	dir := t.TempDir()
	readiness := map[string]spells.Tool{
		// Fails on the first attempt so probeUntilReady enters the interactive wait
		// loop, where an already-cancelled context is picked up by ctx.Done().
		"faketool": {Ready: spells.Command{Bin: "sh", Args: []string{"-c", "exit 1"}}},
	}
	op := spells.Op{Command: spells.Command{Bin: "faketool"}}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	err := checkReady(cancelled, readiness, op, dir)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "want context.Canceled, got %v", err)

	if _, hit := readinessMemo.Load("faketool\x00" + dir); hit {
		t.Fatal("a cancelled probe's outcome must not be memoized")
	}

	// A later call with a live context must re-probe rather than return the stale
	// cancellation - swap in a passing probe to prove the probe actually re-ran.
	readiness["faketool"] = spells.Tool{Ready: spells.Command{Bin: "sh", Args: []string{"-c", "exit 0"}}}
	assert.NoError(t, checkReady(context.Background(), readiness, op, dir))
}
