package bindings_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egladman/magus/internal/interp"
	_ "github.com/egladman/magus/internal/interp/bindings"
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

// parseMagusfile evaluates the magusfile in dir in parse mode, which fires
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
final widget = magus.spell.load("spells/widget.bzz");
magus.project.register(".", fun(p, cb) > bool { cb({"spells": [widget]}); return true; });`)

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
magus.project.register(".", fun(p, cb) > bool { cb({"spells": [widget]}); return true; });`)

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
final noops = magus.spell.load("spells/noops.bzz");
magus.project.register(".", fun(p, cb) > bool { cb({"spells": [noops]}); return true; });`)

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
final widget = magus.spell.load("spells/widget.bzz");
export fun build(args: [str]) > void {
    final names = widget.listTargets();
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
final widget = magus.spell.load("spells/widget.bzz");
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

// TestBuzzSpellCaptureReturnsRecord verifies a capture=true target returns the
// {stdout, stderr, code, ok} record map, accessed with dot syntax the way a
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
final widget = magus.spell.load("spells/widget.bzz");
export fun build(args: [str]) > void {
    final r = widget.hash();
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
final widget = magus.spell.load("spells/widget.bzz");
export fun build(args: [str]) > void {
    final a = widget.emit();
    final b = widget.shout({"stdin": a.stdout});
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
import "vcs";
export fun check(args: [str]) > void {
    final c = vcs.commit();
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
import "vcs";
export fun check(args: [str]) > void {
    if (vcs.commit() != null) { error("expected null outside a repo"); }
}`)
	srcs, err := interp.FindAll(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := interp.Run(context.Background(), srcs[0], "check", nil, dir); err != nil {
		t.Fatalf("vcs.commit null case: %v", err)
	}
}

// TestEngineSpecParity locks the engine-agnostic mgs_ contract: a Buzz spell
// declaring every optional mgs_ function with record-shaped ops resolves to the
// expected Spec. It guards the resolver (internal/spell/resolve.go) against
// dropping fields — claims is asserted explicitly because that is the field that
// previously regressed.
func TestEngineSpecParity(t *testing.T) {
	buzzSrc := `export fun mgs_getName() > str { return "parity_buzz"; }
export fun mgs_listRequiredGlobs(_dir: str) > [str] { return ["**/*.rb", "Gemfile.lock"]; }
export fun mgs_listProvidedGlobs() > [str] { return ["vendor/bundle/**"]; }
export fun mgs_listClaimedGlobs() > [str] { return [".rubocop.yml", "Gemfile"]; }
export fun mgs_getVersionCommand() > [str] { return ["ruby", "--version"]; }
export fun mgs_isForeignProcess() > bool { return false; }
export fun mgs_listTargets() > any {
    return {"rspec": {"cmd": "bundle", "args": ["exec", "rspec"]}};
}
`

	loadAndBind := func(t *testing.T, ext, src string) {
		t.Helper()
		dir := t.TempDir()
		t.Chdir(dir)
		name := "spells/parity." + ext
		writeFile(t, dir, name, src)
		writeFile(t, dir, "magusfile.bzz", `import "magus";
final sp = magus.spell.load("`+name+`");
magus.project.register(".", fun(p, cb) > bool { cb({"spells": [sp]}); return true; });`)
		if err := parseMagusfile(t, dir); err != nil {
			t.Fatalf("parse (%s): %v", ext, err)
		}
	}

	loadAndBind(t, "bzz", buzzSrc)

	buzzSp, ok := project.DefaultSpellRegistry().Lookup("parity_buzz")
	if !ok {
		t.Fatal("parity_buzz not registered")
	}

	if want := []string{"**/*.rb", "Gemfile.lock"}; !slices.Equal(buzzSp.Sources(), want) {
		t.Errorf("sources = %v, want %v", buzzSp.Sources(), want)
	}
	if want := []string{"vendor/bundle/**"}; !slices.Equal(buzzSp.Outputs(), want) {
		t.Errorf("outputs = %v, want %v", buzzSp.Outputs(), want)
	}
	if want := []string{"rspec"}; !slices.Equal(buzzSp.Targets(), want) {
		t.Errorf("targets = %v, want %v", buzzSp.Targets(), want)
	}
	// claims is the field the resolver previously dropped — assert it directly.
	if want := []string{".rubocop.yml", "Gemfile"}; !slices.Equal(buzzSp.Claims(), want) {
		t.Errorf("claims = %v, want %v (mgs_listClaimedGlobs must be carried)", buzzSp.Claims(), want)
	}
	if buzzSp.ForeignProcess() {
		t.Errorf("foreignProcess = true, want false")
	}
}
