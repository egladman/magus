package bindings

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/spell"
	remotespell "github.com/egladman/magus/internal/spell/remote"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const resultFromAHostError = `import "fs";
import "result";

export fun read() > result\Result::<str> {
    try {
        return result\ok::<str>(fs\readFile("/nonexistent/result-module"));
    } catch (err: any) {
        return result\caught::<str>(err, doing: "read");
    }
    return result\fail::<str>("unreachable");
}

export fun answer() > str {
    final got = read();
    if (got.error -> err) {
        return err.message;
    }
    return "no error";
}
`

func assertAnswersHostErrorMessage(t *testing.T, sess *buzz.Session) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, sess.Exec(ctx, resultFromAHostError))
	got, err := sess.CallValue(ctx, sess.Exports()["answer"], nil)
	require.NoError(t, err)
	assert.Contains(t, got.AsString(), "read: fs.read_file")
	assert.NotContains(t, got.AsString(), `{"message"`, "a host error's map is read, not printed")
}

func TestScriptSessionSharesTheResultModule(t *testing.T) {
	assertAnswersHostErrorMessage(t, scriptSession(t))
}

func TestMagusfileSessionSharesTheResultModule(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	t.Cleanup(func() { _ = sess.Close() })
	require.NoError(t, registerMagusModules(ctx, sess))
	assertAnswersHostErrorMessage(t, sess)
}

func TestClientSessionOffersTheResultModule(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	t.Cleanup(func() { _ = sess.Close() })
	require.NoError(t, InstallClient(ctx, sess, os.Stderr))
	require.NoError(t, sess.Exec(ctx, `import "result";
export fun answer() > str { return result\fail::<int>("bad").error!.message; }
`))
	got, err := sess.CallValue(ctx, sess.Exports()["answer"], nil)
	require.NoError(t, err)
	assert.Equal(t, "bad", got.AsString())
}

// spellImportsWS is a workspace carrying resolved spell imports, the way Magus does.
type spellImportsWS struct {
	rootOnlyWS
	imports *remotespell.Imports
}

func (w spellImportsWS) SpellImports() *remotespell.Imports { return w.imports }

// declaredContext resolves cfg for the workspace at root the way a workspace load does,
// and returns a context carrying it.
func declaredContext(t *testing.T, root string, cfg config.SpellsConfig) context.Context {
	t.Helper()
	im, err := remotespell.LoadImports(t.Context(), root, cfg, remotespell.LoadOptions{
		Embedded: func(name string) bool { _, ok := spell.Builtins()[name]; return ok },
	})
	require.NoError(t, err)
	return types.WithWorkspace(t.Context(), spellImportsWS{rootOnlyWS{root: root}, im})
}

func parseIn(ctx context.Context, t *testing.T, dir string) error {
	t.Helper()
	src, err := interp.Find(dir)
	require.NoError(t, err)
	_, err = interp.Parse(ctx, src)
	return err
}

// Registration is by name, so a workspace spell carrying an embedded spell's name
// would silently bind the embedded one. Undeclared, that is MGS1002.
func TestWorkspaceSpellWithAnEmbeddedNameIsRefused(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root := w.Root()
	w.Write("spells/go/spell.buzz", `export fun mgs_getName() > str { return "go"; }`)
	w.Write("magusfile.buzz", "import \"magus\";\nimport \"spells/go\" as gocopy;\n")

	err := parseIn(declaredContext(t, root, config.SpellsConfig{}), t, root)
	require.ErrorIs(t, err, types.SpellShadowed)
	require.ErrorContains(t, err, "spells: {magus/spell/go: {path: <dir>}}")
}

func TestOverrideMustCarryTheEmbeddedName(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root := w.Root()
	w.Write("spells/mygo/spell.buzz", `export fun mgs_getName() > str { return "mygo"; }`)
	w.Write("magusfile.buzz", "import \"magus\";\nimport \"magus/spell/go\";\n")
	ctx := declaredContext(t, root, config.SpellsConfig{Imports: map[string]config.SpellImport{
		"magus/spell/go": {Path: "spells/mygo"},
	}})

	err := parseIn(ctx, t, root)
	require.ErrorIs(t, err, types.SpellOverrideInvalid)
	require.ErrorContains(t, err, `holds the spell "mygo", not "go"`)
}

// The import string never changes: `import "magus/spell/go"` binds the declared copy,
// and the copy owns the name, so every use of go runs it.
func TestOverrideReplacesTheEmbeddedSpell(t *testing.T) {
	embedded, ok := project.DefaultSpellRegistry().Lookup("go")
	require.True(t, ok)
	t.Cleanup(func() { project.DefaultSpellRegistry().ReplaceSpell(embedded) })

	w := testkit.NewWorkspace(t)
	root := w.Root()
	w.Write("spells/go/spell.buzz", `import "magus/spell";
export fun mgs_getName() > str { return "go"; }
fun vet(t: Target) > Command { return Command{bin = "go", args = ["vet", "./..."]}; }
export fun mgs_listTargets() > {str: fun(Target) Command} { return {"vet": vet}; }
`)
	w.Write("magusfile.buzz", "import \"magus\";\nimport \"magus/spell/go\";\n")
	ctx := declaredContext(t, root, config.SpellsConfig{Imports: map[string]config.SpellImport{
		"magus/spell/go": {Path: "spells/go"},
	}})

	require.NoError(t, parseIn(ctx, t, root))
	got, ok := project.DefaultSpellRegistry().Lookup("go")
	require.True(t, ok)
	assert.Equal(t, []string{"vet"}, got.Targets())
}

func TestUndeclaredRemoteImportIsRefused(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root := w.Root()
	w.Write("magusfile.buzz", "import \"magus\";\nimport \"ghcr.io/team/spells/lint\";\n")

	err := parseIn(declaredContext(t, root, config.SpellsConfig{}), t, root)
	require.ErrorIs(t, err, types.RemoteSpellUndeclared)
}

// A remote spell replaced by a workspace copy binds under the path's last segment, as
// the remote one would, and never reaches a registry.
func TestRemoteOverrideBindsTheWorkspaceCopy(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root := w.Root()
	w.Write("vendor/lint/spell.buzz", `export fun mgs_getName() > str { return "lintcopy"; }`)
	w.Write("magusfile.buzz", `import "magus";
import "ghcr.io/team/spells/lint";

export fun check(ctx: magus\Context, args: [str]) > void !> any {
    if (lint.name != "lintcopy") { throw "lint.name: " + lint.name; }
}
`)
	ctx := declaredContext(t, root, config.SpellsConfig{Imports: map[string]config.SpellImport{
		"ghcr.io/team/spells/lint": {Path: "vendor/lint"},
	}})

	_, err := interp.RunDir(ctx, root, "check", nil)
	require.NoError(t, err)
}

// everyFileTracked reports every path it is asked about as tracked.
type everyFileTracked struct{}

func (everyFileTracked) TrackedFiles(_ context.Context, _ string, paths []string) ([]string, error) {
	return paths, nil
}

// pulledContext caches each directory as a pull leaves it and pins it in magus.lock.
// It returns a context carrying the load's imports, held offline to that cache.
func pulledContext(t *testing.T, root string, dirs map[string]string) context.Context {
	t.Helper()
	t.Setenv("MAGUS_OFFLINE", "1")
	cache := t.TempDir()
	cfg := config.SpellsConfig{Imports: map[string]config.SpellImport{}}
	pins := map[string]remotespell.LockEntry{}
	for importPath, dir := range dirs {
		content, err := remotespell.Build(t.Context(), dir, everyFileTracked{}, remotespell.Provenance{Title: filepath.Base(dir)})
		require.NoError(t, err)
		m, err := content.Manifest()
		require.NoError(t, err)
		slot := filepath.Join(cache, fmt.Sprintf("%s-%s", m.Digest.Algorithm(), m.Digest.Encoded()))
		require.NoError(t, os.CopyFS(filepath.Join(slot, "src"), os.DirFS(dir)))
		require.NoError(t, os.WriteFile(filepath.Join(slot, "manifest.json"), m.Body, 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(slot, content.Blobs[0].Title), content.Blobs[0].Data, 0o644))
		cfg.Imports[importPath] = config.SpellImport{Tag: "v1"}
		pins[importPath] = remotespell.LockEntry{Tag: "v1", Digest: m.Digest}
	}
	require.NoError(t, remotespell.UpdateLock(t.Context(), root, func(l remotespell.Lock) (remotespell.Lock, error) {
		l.Spells = pins
		return l, nil
	}))
	im, err := remotespell.LoadImports(t.Context(), root, cfg, remotespell.LoadOptions{CacheRoot: cache})
	require.NoError(t, err)
	require.NoError(t, im.Error())
	return types.WithWorkspace(t.Context(), spellImportsWS{rootOnlyWS{root: root}, im})
}

// A pulled module binds as a file import: its type constructs, its sibling resolves.
// A pulled spell beside it still binds a spell handle.
func TestPulledModuleBindsAsAFileImport(t *testing.T) {
	lib := filepath.Join(t.TempDir(), "widget")
	require.NoError(t, os.MkdirAll(lib, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(lib, "widget.buzz"), []byte(`import "helper";
export object Part { size: int }
export fun part(n: int) > Part { return Part{ size = twice(n) }; }
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(lib, "helper.buzz"), []byte(`export fun twice(n: int) > int { return n * 2; }`), 0o644))
	lint := filepath.Join(t.TempDir(), "lint")
	require.NoError(t, os.MkdirAll(lint, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(lint, "spell.buzz"), []byte(`export fun mgs_getName() > str { return "lint"; }`), 0o644))

	for name, magusfile := range map[string]string{
		"flat": `import "magus";
import "ghcr.io/team/libs/widget";
import "ghcr.io/team/spells/lint";

export fun check(ctx: magus\Context, args: [str]) > void !> any {
    final q = Part{ size = part(3).size + 1 };
    if (q.size != 7) { throw "flat import: wrong size"; }
    if (lint.name != "lint") { throw "lint.name: " + lint.name; }
}
`,
		"aliased": `import "magus";
import "ghcr.io/team/libs/widget" as w;

export fun check(ctx: magus\Context, args: [str]) > void !> any {
    final p: w\Part = w\part(3);
    if (p.size != 6) { throw "aliased import: wrong size"; }
}
`,
	} {
		t.Run(name, func(t *testing.T) {
			w := testkit.NewWorkspace(t)
			root := w.Root()
			w.Write("magusfile.buzz", magusfile)
			ctx := pulledContext(t, root, map[string]string{"ghcr.io/team/libs/widget": lib, "ghcr.io/team/spells/lint": lint})
			_, err := interp.RunDir(ctx, root, "check", nil)
			require.NoError(t, err)
		})
	}
}

// A workspace copy declared for a module path binds as the pulled module would.
func TestModuleOverrideBindsTheWorkspaceCopy(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root := w.Root()
	w.Write("libs/widget/widget.buzz", "import \"helper\";\nexport fun answer() > int { return twice(21); }\n")
	w.Write("libs/widget/helper.buzz", "export fun twice(n: int) > int { return n * 2; }\n")
	w.Write("magusfile.buzz", `import "magus";
import "ghcr.io/team/libs/widget";

export fun check(ctx: magus\Context, args: [str]) > void !> any {
    if (answer() != 42) { throw "answer"; }
}
`)
	ctx := declaredContext(t, root, config.SpellsConfig{Imports: map[string]config.SpellImport{
		"ghcr.io/team/libs/widget": {Path: "libs/widget"},
	}})
	_, err := interp.RunDir(ctx, root, "check", nil)
	require.NoError(t, err)
}

// TestRootFirstLevels pins the walk-up spell-search chain: root-first from the
// workspace root down to the importing file's dir, bounded to the workspace.
func TestRootFirstLevels(t *testing.T) {
	j := filepath.Join
	root := j("/", "w")
	t.Run("nested project walks root down to the file dir", func(t *testing.T) {
		got := rootFirstLevels(root, j(root, "web", "studio"))
		assert.Equal(t, []string{root, j(root, "web"), j(root, "web", "studio")}, got)
	})
	t.Run("file at the root yields just the root", func(t *testing.T) {
		assert.Equal(t, []string{root}, rootFirstLevels(root, root))
	})
	t.Run("no workspace root yields just the file dir (out-of-workspace script)", func(t *testing.T) {
		assert.Equal(t, []string{j(root, "web")}, rootFirstLevels("", j(root, "web")))
	})
	t.Run("file outside the root is not walked above itself (hermetic)", func(t *testing.T) {
		assert.Equal(t, []string{j("/", "other", "x")}, rootFirstLevels(root, j("/", "other", "x")))
	})
}

// BenchmarkResolveImportedSpell imports one spell four times, as the loads of one run
// do, with and without the run's probes; only with them is it compiled once.
func BenchmarkResolveImportedSpell(b *testing.B) {
	w := testkit.NewWorkspace(b)
	root := w.Root()
	w.Write("spells/benchspell.buzz", `export fun mgs_getName() > str { return "benchspell"; }`)
	base := interp.WithSource(types.WithWorkspace(b.Context(), rootOnlyWS{root: root}), &interp.Source{Dir: root})
	for _, probes := range []bool{false, true} {
		b.Run(fmt.Sprintf("probes=%v", probes), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				ctx, seal := base, func() {}
				if probes {
					ctx, seal = interp.WithImportProbes(base)
				}
				for range 4 {
					if _, ok := resolveLocalSpellImport(ctx, nil, "spells/benchspell"); !ok {
						b.Fatal("spells/benchspell did not resolve")
					}
				}
				seal()
			}
		})
	}
}

// BenchmarkResolveLocalSpellImport replays one load's worth of non-spell imports from
// a nested project, shaped like this repo's docs magusfile: every import statement
// reaches the resolver, so each module is resolved once per importing file.
func BenchmarkResolveLocalSpellImport(b *testing.B) {
	w := testkit.NewWorkspace(b)
	root := w.Root()
	var imports []string
	for i := range 20 {
		name := fmt.Sprintf("page%02d", i)
		w.Write(filepath.Join("docs", "site", name+".buzz"), "export fun f() > void {}\n")
		imports = append(imports, "site/"+name)
	}
	for i := range 10 {
		name := fmt.Sprintf("lib%02d", i)
		w.Write(filepath.Join("hack", name+".buzz"), "export fun f() > void {}\n")
		imports = append(imports, "../hack/"+name, "hack/"+name)
	}
	src := &interp.Source{Dir: filepath.Join(root, "docs")}
	base := interp.WithSource(types.WithWorkspace(b.Context(), rootOnlyWS{root: root}), src)
	for _, probes := range []bool{false, true} {
		b.Run(fmt.Sprintf("probes=%v", probes), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				ctx, seal := base, func() {}
				if probes {
					ctx, seal = interp.WithImportProbes(base)
				}
				for range 4 {
					for _, imp := range imports {
						if _, ok := resolveLocalSpellImport(ctx, nil, imp); ok {
							b.Fatalf("%s resolved as a spell", imp)
						}
					}
				}
				seal()
			}
		})
	}
}

// A load's probes answer the same imports the same way stat does.
func TestResolveLocalSpellImportWithProbes(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root := w.Root()
	w.Write("spells/hello/spell.buzz", `export fun mgs_getName() > str { return "probehello"; }`)
	w.Write("spells/flat.buzz", `export fun mgs_getName() > str { return "probeflat"; }`)
	w.Write("web/lib.buzz", "export fun f() > void {}\n")
	src := &interp.Source{Dir: filepath.Join(root, "web")}
	base := interp.WithSource(declaredContext(t, root, config.SpellsConfig{}), src)
	im := remotespell.ImportsFromContext(base)
	ctx, seal := interp.WithImportProbes(base)
	defer seal()

	for _, imp := range []string{"spells/hello", "spells/flat", "lib", "spells/missing", "../web/lib"} {
		want, wantOK := resolveLocalSpellImport(base, im, imp)
		for range 2 {
			got, ok := resolveLocalSpellImport(ctx, im, imp)
			assert.Equal(t, wantOK, ok, imp)
			assert.Equal(t, want.String(), got.String(), imp)
		}
	}
}

// A workspace bounds the spell search, so `magus --root` from another checkout
// never falls back to that checkout's spells/.
func TestSpellSearchLevelsFallBackToCwdOnlyWithoutWorkspace(t *testing.T) {
	root := filepath.Join("/", "w")
	src := &interp.Source{Dir: filepath.Join(root, "web")}
	ctx := interp.WithSource(context.Background(), src)

	levels, bound := spellSearchLevels(ctx)
	assert.Equal(t, []string{src.Dir, ""}, levels)
	assert.Empty(t, bound)
	levels, bound = spellSearchLevels(types.WithWorkspace(ctx, rootOnlyWS{root: root}))
	assert.Equal(t, []string{root, src.Dir}, levels)
	assert.Equal(t, root, bound)
}

// escapeeSpell plants a spell in outer, the directory holding the workspace root, at
// spells/escapee/spell.buzz: where `../spells/escapee` joined onto the root lands.
func escapeeSpell(t *testing.T) (w *testkit.Workspace, root, outside string) {
	t.Helper()
	w = testkit.NewWorkspace(t)
	root = w.Path("ws")
	outside = w.Write("spells/escapee/spell.buzz", `export fun mgs_getName() > str { return "escapee"; }`)
	require.Equal(t, outside, filepath.Join(root, "../spells/escapee", "spell.buzz"))
	return w, root, outside
}

// The root level used to find a spell above the workspace for a `../` import that
// the project's own level resolves inside it; from a worktree that loaded the main
// checkout's spell.
func TestSpellImportAboveTheRootIsNotLoaded(t *testing.T) {
	w, root, _ := escapeeSpell(t)
	w.Write("ws/app/magusfile.buzz", "import \"magus\";\n")
	base := interp.WithSource(declaredContext(t, root, config.SpellsConfig{}), &interp.Source{Dir: filepath.Join(root, "app")})
	ctx, seal := interp.WithImportProbes(base)
	defer seal()

	_, ok := resolveLocalSpellImport(ctx, remotespell.ImportsFromContext(ctx), "../spells/escapee")

	assert.False(t, ok)
	_, loaded := project.DefaultSpellRegistry().Lookup("escapee")
	assert.False(t, loaded)
}

// With no level left inside the root the import is refused rather than handed to a
// file search that would read outside the workspace.
func TestSpellImportEscapingEveryLevelIsRefused(t *testing.T) {
	w, root, _ := escapeeSpell(t)
	w.Write("ws/magusfile.buzz", "import \"magus\";\nimport \"../spells/escapee\" as escapee;\n")

	err := parseIn(declaredContext(t, root, config.SpellsConfig{}), t, root)

	require.ErrorIs(t, err, types.SpellImportEscapesWorkspace)
	require.ErrorContains(t, err, fmt.Sprintf(
		`import "../spells/escapee" resolves outside the workspace: from %s it reaches above the workspace root %s, and an import never loads a file outside the root; import a path inside the workspace`,
		root, root))
	_, loaded := project.DefaultSpellRegistry().Lookup("escapee")
	assert.False(t, loaded)
}

// The key is the absolute candidate, so the same relative import from two projects
// resolves two spells.
func TestSameRelativeImportFromTwoProjectsResolvesTwoSpells(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root := w.Root()
	w.Write("a/spells/p/spell.buzz", `export fun mgs_getName() > str { return "memoa"; }`)
	w.Write("b/spells/p/spell.buzz", `export fun mgs_getName() > str { return "memob"; }`)
	base, seal := interp.WithImportProbes(declaredContext(t, root, config.SpellsConfig{}))
	defer seal()
	im := remotespell.ImportsFromContext(base)
	name := func(project string) string {
		t.Helper()
		ctx := interp.WithSource(base, &interp.Source{Dir: filepath.Join(root, project, "sub")})
		v, ok := resolveLocalSpellImport(ctx, im, "../spells/p")
		require.True(t, ok, project)
		n, _ := v.MapGet("name")
		return n.AsString()
	}

	assert.Equal(t, "memoa", name("a"))
	assert.Equal(t, "memob", name("b"))
}

// A spell deleted after its directory was listed stops resolving instead of
// answering from the descriptor compiled earlier.
func TestSpellDeletedMidRunStopsResolving(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root := w.Root()
	w.Write("spells/vanishing/spell.buzz", `export fun mgs_getName() > str { return "vanishingspell"; }`)
	base, seal := interp.WithImportProbes(declaredContext(t, root, config.SpellsConfig{}))
	defer seal()
	im := remotespell.ImportsFromContext(base)
	_, ok := resolveLocalSpellImport(base, im, "spells/vanishing")
	require.True(t, ok)

	require.NoError(t, os.RemoveAll(filepath.Join(root, "spells")))

	_, ok = resolveLocalSpellImport(base, im, "spells/vanishing")
	assert.False(t, ok, "a spell deleted mid-run still resolved")
}

// A step cut short mid-compile is a failure of that step, not proof that no spell
// lives at the path: the next load must compile it.
func TestCancelledCompileIsNotRememberedAsNoSpell(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root := w.Root()
	// The loop gives the VM enough backward jumps to notice the cancellation.
	w.Write("spells/cancelled.buzz", "var i: int = 0;\nwhile (i < 1000) { i = i + 1; }\nexport fun mgs_getName() > str { return \"cancelledspell\"; }\n")
	base, seal := interp.WithImportProbes(declaredContext(t, root, config.SpellsConfig{}))
	defer seal()
	im := remotespell.ImportsFromContext(base)
	cancelled, cancel := context.WithCancel(base)
	cancel()

	_, ok := resolveLocalSpellImport(cancelled, im, "spells/cancelled")
	require.False(t, ok, "the cancelled compile cannot have produced a spell")
	v, ok := resolveLocalSpellImport(base, im, "spells/cancelled")

	require.True(t, ok, "the failed compile was remembered as no spell")
	n, _ := v.MapGet("name")
	assert.Equal(t, "cancelledspell", n.AsString())
}

// A spell rewritten during a run is compiled again, not answered from the first
// descriptor.
func TestSpellEditedMidRunCompilesAgain(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root := w.Root()
	path := w.Write("spells/edited.buzz", `export fun mgs_getName() > str { return "editedv1"; }`)
	base, seal := interp.WithImportProbes(declaredContext(t, root, config.SpellsConfig{}))
	defer seal()
	im := remotespell.ImportsFromContext(base)
	name := func() string {
		t.Helper()
		v, ok := resolveLocalSpellImport(base, im, "spells/edited")
		require.True(t, ok)
		n, _ := v.MapGet("name")
		return n.AsString()
	}

	require.Equal(t, "editedv1", name())
	require.NoError(t, os.WriteFile(path, []byte(`export fun mgs_getName() > str { return "editedv2"; }`), 0o644))

	assert.Equal(t, "editedv2", name())
}

// The importing directory is a search root for the spell's own imports, so one
// spell file imported from two directories can answer two ways.
func TestTwoImportingDirectoriesEachCompile(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root := w.Root()
	w.Write("spells/viadir.buzz", "import \"dirhelper\";\nexport fun mgs_getName() > str { return dirhelper\\label(); }\n")
	w.Write("a/dirhelper.buzz", `export fun label() > str { return "viadira"; }`)
	w.Write("b/dirhelper.buzz", `export fun label() > str { return "viadirb"; }`)
	base, seal := interp.WithImportProbes(declaredContext(t, root, config.SpellsConfig{}))
	defer seal()
	im := remotespell.ImportsFromContext(base)
	name := func(project string) string {
		t.Helper()
		ctx := interp.WithSource(base, &interp.Source{Dir: filepath.Join(root, project)})
		v, ok := resolveLocalSpellImport(ctx, im, "../spells/viadir")
		require.True(t, ok, project)
		n, _ := v.MapGet("name")
		return n.AsString()
	}

	assert.Equal(t, "viadira", name("a"))
	assert.Equal(t, "viadirb", name("b"))
}

// resolveEvents counts the spell compiles that reach spell.Resolve, by the name the
// compile answered; every other method is observability.Nop's.
type resolveEvents struct {
	observability.Nop
	mu    sync.Mutex
	total int
}

func (r *resolveEvents) Enabled() bool { return true }

func (r *resolveEvents) RecordBuzzSpellResolve(context.Context, observability.BuzzSpellResolve) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.total++
}

func (r *resolveEvents) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.total
}

// A file read while a step rewrites it can be a truncated prefix with no spell in it.
// The mark that read made must not outlive the full write.
func TestPlainModuleMarkedFromATruncatedReadIsCompiledOnceRewritten(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root := w.Root()
	path := w.Write("spells/growing.buzz", "// still being written\n")
	base, seal := interp.WithImportProbes(declaredContext(t, root, config.SpellsConfig{}))
	defer seal()
	im := remotespell.ImportsFromContext(base)
	_, ok := resolveLocalSpellImport(base, im, "spells/growing")
	require.False(t, ok, "a prefix with no spell in it resolved")

	require.NoError(t, os.WriteFile(path, []byte(`export fun mgs_getName() > str { return "growingspell"; }`), 0o644))

	v, ok := resolveLocalSpellImport(base, im, "spells/growing")
	require.True(t, ok, "the mark outlived the full write")
	n, _ := v.MapGet("name")
	assert.Equal(t, "growingspell", n.AsString())
}

// The VM polls cancellation only every few hundred jumps, so a short spell compiles
// to success under an ended context; that answer is not recorded.
func TestCompileUnderAnEndedContextIsNotRecorded(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root := w.Root()
	w.Write("spells/shortspell.buzz", `export fun mgs_getName() > str { return "shortspell"; }`)
	events := &resolveEvents{}
	base, seal := interp.WithImportProbes(observability.WithProvider(declaredContext(t, root, config.SpellsConfig{}), events))
	defer seal()
	im := remotespell.ImportsFromContext(base)
	ended, cancel := context.WithCancel(base)
	cancel()

	_, ok := resolveLocalSpellImport(ended, im, "spells/shortspell")
	require.True(t, ok, "a short compile succeeds under an ended context")
	_, ok = resolveLocalSpellImport(base, im, "spells/shortspell")

	require.True(t, ok)
	assert.Equal(t, 2, events.count(), "the compile under the ended context was recorded")
}

// A module whose text holds mgs_getName without exporting it is found to be no spell
// by running it; the next import must not run it again.
func TestModuleNamingTheSpellFuncWithoutExportingItIsRunOnce(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root := w.Root()
	w.Write("spells/mention.buzz", "// mgs_getName is the export that makes a spell\nexport fun helper() > void {}\n")
	events := &resolveEvents{}
	base, seal := interp.WithImportProbes(observability.WithProvider(declaredContext(t, root, config.SpellsConfig{}), events))
	defer seal()
	im := remotespell.ImportsFromContext(base)

	for range 3 {
		_, ok := resolveLocalSpellImport(base, im, "spells/mention")
		require.False(t, ok, "a module that exports no spell resolved as one")
	}

	assert.Equal(t, 1, events.count())
}
