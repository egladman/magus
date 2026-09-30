package bindings

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/spell"
	remotespell "github.com/egladman/magus/internal/spell/remote"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// spellImportsWS is a workspace carrying resolved spell imports, the way Magus does.
type spellImportsWS struct {
	rootOnlyWS
	imports *remotespell.Imports
}

func (w spellImportsWS) SpellImports() *remotespell.Imports { return w.imports }

// declaredCtx resolves cfg for the workspace at root the way a workspace load does,
// and returns a context carrying it.
func declaredCtx(t *testing.T, root string, cfg config.SpellsConfig) context.Context {
	t.Helper()
	im, err := remotespell.LoadImports(t.Context(), root, cfg, remotespell.LoadOptions{
		Embedded: func(name string) bool { _, ok := spell.Builtins()[name]; return ok },
	})
	require.NoError(t, err)
	return types.WithWorkspace(t.Context(), spellImportsWS{rootOnlyWS{root: root}, im})
}

// symlinkFreeTempDir keeps the workspace root and the magusfile's directory in one
// spelling, as a real workspace root is (macOS puts t.TempDir behind /var -> /private).
func symlinkFreeTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return dir
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
	root := symlinkFreeTempDir(t)
	writeFile(t, root, "spells/go/spell.buzz", `export fun mgs_getName() > str { return "go"; }`)
	writeFile(t, root, "magusfile.buzz", "import \"magus\";\nimport \"spells/go\" as gocopy;\n")

	err := parseIn(declaredCtx(t, root, config.SpellsConfig{}), t, root)
	require.ErrorIs(t, err, types.SpellShadowed)
	require.ErrorContains(t, err, "spells: {magus/spell/go: {path: <dir>}}")
}

func TestOverrideMustCarryTheEmbeddedName(t *testing.T) {
	root := symlinkFreeTempDir(t)
	writeFile(t, root, "spells/mygo/spell.buzz", `export fun mgs_getName() > str { return "mygo"; }`)
	writeFile(t, root, "magusfile.buzz", "import \"magus\";\nimport \"magus/spell/go\";\n")
	ctx := declaredCtx(t, root, config.SpellsConfig{Imports: map[string]config.SpellImport{
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

	root := symlinkFreeTempDir(t)
	writeFile(t, root, "spells/go/spell.buzz", `export fun mgs_getName() > str { return "go"; }
export fun mgs_listTargets() > any {
    return {"vet": {"bin": "go", "args": ["vet", "./..."]}};
}
`)
	writeFile(t, root, "magusfile.buzz", "import \"magus\";\nimport \"magus/spell/go\";\n")
	ctx := declaredCtx(t, root, config.SpellsConfig{Imports: map[string]config.SpellImport{
		"magus/spell/go": {Path: "spells/go"},
	}})

	require.NoError(t, parseIn(ctx, t, root))
	got, ok := project.DefaultSpellRegistry().Lookup("go")
	require.True(t, ok)
	assert.Equal(t, []string{"vet"}, got.Targets())
}

func TestUndeclaredRemoteImportIsRefused(t *testing.T) {
	root := symlinkFreeTempDir(t)
	writeFile(t, root, "magusfile.buzz", "import \"magus\";\nimport \"ghcr.io/team/spells/lint\";\n")

	err := parseIn(declaredCtx(t, root, config.SpellsConfig{}), t, root)
	require.ErrorIs(t, err, types.RemoteSpellUndeclared)
}

// A remote spell replaced by a workspace copy binds under the path's last segment, as
// the remote one would, and never reaches a registry.
func TestRemoteOverrideBindsTheWorkspaceCopy(t *testing.T) {
	root := symlinkFreeTempDir(t)
	writeFile(t, root, "vendor/lint/spell.buzz", `export fun mgs_getName() > str { return "lintcopy"; }`)
	writeFile(t, root, "magusfile.buzz", `import "magus";
import "ghcr.io/team/spells/lint";

export fun check(ctx: magus\Context, args: [str]) > void {
    if (lint.name != "lintcopy") { error("lint.name: " + lint.name); }
}
`)
	ctx := declaredCtx(t, root, config.SpellsConfig{Imports: map[string]config.SpellImport{
		"ghcr.io/team/spells/lint": {Path: "vendor/lint"},
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

// BenchmarkResolveLocalSpellImport replays one load's worth of non-spell imports from
// a nested project, shaped like this repo's docs magusfile: every import statement
// reaches the resolver, so each module is resolved once per importing file.
func BenchmarkResolveLocalSpellImport(b *testing.B) {
	root, err := filepath.EvalSymlinks(b.TempDir())
	require.NoError(b, err)
	var imports []string
	for i := range 20 {
		name := fmt.Sprintf("page%02d", i)
		writeFile(b, root, filepath.Join("docs", "site", name+".buzz"), "export fun f() > void {}\n")
		imports = append(imports, "site/"+name)
	}
	for i := range 10 {
		name := fmt.Sprintf("lib%02d", i)
		writeFile(b, root, filepath.Join("hack", name+".buzz"), "export fun f() > void {}\n")
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
	root := symlinkFreeTempDir(t)
	writeFile(t, root, "spells/hello/spell.buzz", `export fun mgs_getName() > str { return "probehello"; }`)
	writeFile(t, root, "spells/flat.buzz", `export fun mgs_getName() > str { return "probeflat"; }`)
	writeFile(t, root, "web/lib.buzz", "export fun f() > void {}\n")
	src := &interp.Source{Dir: filepath.Join(root, "web")}
	base := interp.WithSource(declaredCtx(t, root, config.SpellsConfig{}), src)
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

	assert.Equal(t, []string{src.Dir, ""}, spellSearchLevels(ctx))
	assert.Equal(t, []string{root, src.Dir}, spellSearchLevels(types.WithWorkspace(ctx, rootOnlyWS{root: root})))
}
