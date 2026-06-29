package bindings

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	writeFile(t, dir, "spells/widget.buzz", `export fun mgs_getName() > str { return "widgetimport"; }
export fun mgs_listRequiredGlobs(_dir: str) > [str] { return ["**/*.ts"]; }
export fun mgs_listTargets() > any {
    return {"build": {"cmd": "npm", "args": ["run", "build"]}};
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

export fun check(args: [str]) > void {
    if (go.name != "go") { error("go.name mismatch: " + go.name); }
    if (docker.name != "docker") { error("docker.name mismatch: " + docker.name); }
}
`)

	src, err := interp.Find(dir)
	require.NoError(t, err)
	require.NotNil(t, src)
	require.NoError(t, interp.Run(context.Background(), src, "check", nil, dir))
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
// (widget.capture(opts)): opts.args are appended to the target's base argv and the
// command runs in opts.cwd — what lets a magusfile drive a flag-carrying tool (e.g.
// docker.build({cwd: "..", args: [...]})) through the spell instead of os.exec. It
// also checks listTargets() still exposes the op names for introspection.
func TestBuzzSpellMethodForwardsOpts(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// The "capture" target writes its forwarded args ($@) into captured.txt in the
	// process cwd, so the test can assert both the args and the working directory.
	writeFile(t, dir, "spells/widget.buzz", `export fun mgs_getName() > str { return "optswidget"; }
export fun mgs_listTargets() > any {
    return {"capture": {"cmd": "sh", "args": ["-c", "printf '%s ' \"$@\" > captured.txt", "sh"]}};
}
`)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "spells/widget";
export fun build(args: [str]) > void {
    final names = widget.listTargets();
    if (names[0] != "capture") { error("listTargets mismatch"); }
    widget.capture({"cwd": "sub", "args": ["alpha", "beta"]});
}`)

	srcs, err := interp.FindAll(dir)
	require.NoError(t, err)
	require.NoError(t, interp.Run(context.Background(), srcs[0], "build", nil, dir), "invoking spell method with opts")

	// The file must land in opts.cwd (sub/), proving cwd was honored, and contain
	// exactly the appended args, proving opts.args reached the forked command.
	got, err := os.ReadFile(filepath.Join(dir, "sub", "captured.txt"))
	require.NoError(t, err, "expected captured.txt under opts.cwd")
	assert.Equal(t, "alpha beta ", string(got))
}

// TestBuzzSpellMethodEnv verifies the env opt overlays the forked subprocess
// environment — the cross-compile vars (GOOS/GOARCH/CGO_ENABLED) a release build
// needs. The capture target echoes $MYVAR into out.txt; the call sets it via env,
// proving the overlay reached the subprocess.
func TestBuzzSpellMethodEnv(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "spells/widget.buzz", `export fun mgs_getName() > str { return "envwidget"; }
export fun mgs_listTargets() > any {
    return {"capture": {"cmd": "sh", "args": ["-c", "printf \"$MYVAR\" > out.txt"]}};
}
`)
	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "spells/widget";
export fun build(args: [str]) > void {
    widget.capture({"env": {"MYVAR": "overridden"}});
}`)

	srcs, err := interp.FindAll(dir)
	require.NoError(t, err)
	require.NoError(t, interp.Run(context.Background(), srcs[0], "build", nil, dir), "invoking spell method with env")

	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	require.NoError(t, err, "expected out.txt")
	assert.Equal(t, "overridden", string(got))
}

// TestBuzzSpellCaptureReturnsRecord verifies a capture=true target returns the
// {stdout, stderr, code, ok} record map, accessed with dot syntax the way a
// magusfile reads os.exec(...).stdout.
func TestBuzzSpellCaptureReturnsRecord(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "spells/widget.buzz", `export fun mgs_getName() > str { return "capbuzzwidget"; }
export fun mgs_listTargets() > any {
    return {"hash": {"cmd": "sh", "args": ["-c", "printf abc123"], "capture": true}};
}
`)
	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "spells/widget";
export fun build(args: [str]) > void {
    final r = widget.hash();
    if (r.stdout != "abc123") { error("stdout mismatch: " + r.stdout); }
    if (r.code != 0) { error("code mismatch"); }
    if (r.ok != true) { error("ok mismatch"); }
}`)

	srcs, err := interp.FindAll(dir)
	require.NoError(t, err)
	require.NoError(t, interp.Run(context.Background(), srcs[0], "build", nil, dir), "captured buzz target")
}

// TestBuzzSpellPipeStdin verifies pipe-style chaining: a captured target's stdout
// is fed as the stdin of the next target via opts.stdin — the Unix-pipe primitive.
func TestBuzzSpellPipeStdin(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "spells/widget.buzz", `export fun mgs_getName() > str { return "pipewidget"; }
export fun mgs_listTargets() > any {
    return {
        "emit": {"cmd": "sh", "args": ["-c", "printf alpha"], "capture": true},
        "shout": {"cmd": "tr", "args": ["a-z", "A-Z"], "capture": true}
    };
}
`)
	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "spells/widget";
export fun build(args: [str]) > void {
    final a = widget.emit();
    final b = widget.shout({"stdin": a.stdout});
    if (b.stdout != "ALPHA") { error("pipe mismatch: " + b.stdout); }
}`)

	srcs, err := interp.FindAll(dir)
	require.NoError(t, err)
	require.NoError(t, interp.Run(context.Background(), srcs[0], "build", nil, dir), "pipe stdin")
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
export fun check(args: [str]) > void {
    final c = vcs.commit();
    if (c.subject != "hello") { error("subject: " + c.subject); }
    if (c.author.name != "A") { error("author: " + c.author.name); }
    if (c.date == "") { error("date empty"); }
    if (c.id == "") { error("id empty"); }
}`)

	srcs, err := interp.FindAll(dir)
	require.NoError(t, err)
	require.NoError(t, interp.Run(context.Background(), srcs[0], "check", nil, dir), "vcs.commit facade")
}

// TestVcsCommitEmptyOutsideRepo pins the contract that powers build_date's
// fallback: outside any repository, vcs.commit() returns the zero record (every
// field empty), not null — callers test a field (c.date == "") for "no commit".
func TestVcsCommitEmptyOutsideRepo(t *testing.T) {
	dir := t.TempDir() // a bare temp dir, not under version control
	t.Chdir(dir)
	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "vcs";
export fun check(args: [str]) > void {
    final c = vcs.commit();
    if (c == null) { magus.fatal("vcs.commit should be an empty record, not null, outside a repo"); }
    if (c.date != "") { magus.fatal("vcs.commit().date should be empty outside a repo"); }
    if (c.id != "") { magus.fatal("vcs.commit().id should be empty outside a repo"); }
}`)
	srcs, err := interp.FindAll(dir)
	require.NoError(t, err)
	require.NoError(t, interp.Run(context.Background(), srcs[0], "check", nil, dir), "vcs.commit empty-record case")
}

// TestEngineDescriptorParity locks the engine-agnostic mgs_ contract: a Buzz spell
// declaring every optional mgs_ function with record-shaped ops resolves to the
// expected Descriptor. It guards the resolver (internal/spell/resolve.go) against
// dropping fields — claims is asserted explicitly because that is the field that
// previously regressed.
func TestEngineDescriptorParity(t *testing.T) {
	buzzSrc := `export fun mgs_getName() > str { return "parity_buzz"; }
export fun mgs_listRequiredGlobs(_dir: str) > [str] { return ["**/*.rb", "Gemfile.lock"]; }
export fun mgs_listProvidedGlobs() > [str] { return ["vendor/bundle/**"]; }
export fun mgs_listClaimedGlobs() > [str] { return [".rubocop.yml", "Gemfile"]; }
export fun mgs_getVersionCommand() > [str] { return ["ruby", "--version"]; }
export fun mgs_isOpaque() > bool { return false; }
export fun mgs_listTargets() > any {
    return {"rspec": {"cmd": "bundle", "args": ["exec", "rspec"]}};
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
	// claims is the field the resolver previously dropped — assert it directly.
	assert.Equal(t, []string{".rubocop.yml", "Gemfile"}, buzzSp.Claims(), "mgs_listClaimedGlobs must be carried")
	assert.False(t, buzzSp.Opaque(), "opaque should be false")
}

// TestSuggestSpellName covers both suggestion paths: a known language/tool alias
// (the common slip) and nearest-handle edit distance (a typo), plus the
// no-suggestion floor.
func TestSuggestSpellName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"typescript", "ts"},
		{"javascript", "ts"},
		{"js", "ts"},
		{"python", "py"},
		{"rust", "rs"},
		{"markdown", "md"},
		{"golang", "go"},
		{"TypeScript", "ts"}, // alias lookup is case-insensitive
		{"dcoker", "docker"}, // edit distance
		{"cosgin", "cosign"}, // edit distance
		{"zzzzzzzzzz", ""},   // nothing within threshold
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, suggestSpellName(c.in), "suggestSpellName(%q)", c.in)
	}
}

// TestCheckSpellImports verifies valid handles pass (built-in and host-registered)
// and an unknown handle yields a did-you-mean naming the right one.
func TestCheckSpellImports(t *testing.T) {
	require.NoError(t, checkSpellImports(nil))
	require.NoError(t, checkSpellImports([]string{"go", "ts", "md", "magusfile"}),
		"built-in and host-registered handles must pass")

	err := checkSpellImports([]string{"go", "typescript"})
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, `"typescript"`)
	assert.Contains(t, msg, `did you mean "ts"`)
	assert.Contains(t, msg, `import "magus/spell/ts"`)
}

// TestSpellImportSuggestionOnParse is the end-to-end path: a magusfile importing a
// wrong handle fails at load with the suggestion, before the handle surfaces as a
// disconnected "undefined" error.
func TestSpellImportSuggestionOnParse(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "magusfile.buzz", `import "magus";
import "magus/spell/typescript";
magus.project({"spells": [typescript]});
export fun build(args: [str]) > void {}`)

	err := parseMagusfile(t, dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `did you mean "ts"`)
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
import "magus/spell/typescript";
if (1 > 0) {}
magus.project({"spells": [typescript]});
export fun build(args: [str]) > void {}`)

	err := parseMagusfile(t, dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `did you mean "ts"`,
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
export fun build(args: [str]) > void { go["go-build"](); }`)

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
export fun go(args: [str]) > void {}`)
	writeFile(t, root, "b/magusfile.buzz", `import "magus";
export fun build(args: [str]) > void {}`)

	src, err := interp.Find(filepath.Join(root, "a"))
	require.NoError(t, err)
	require.NoError(t,
		interp.Run(context.Background(), src, "go", nil, filepath.Join(root, "a")),
		"a project/ import must resolve when a target is run, not only parsed")
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
export fun build(args: [str]) > void { go["go-build"](); }`)

	require.NoError(t, parseMagusfile(t, dir), "a bad handle in a comment must not be flagged")
}
