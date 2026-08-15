package magus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// TestDeriveCrossProjectDeps verifies that a target-level cross-project dependency
// (a project import + <alias>.<target>) is folded into the depending project's
// DependsOn, so it counts toward the affected set without a separate project-level
// depends_on.
func TestDeriveCrossProjectDeps(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"gopherbuzz/magusfile.buzz": "export fun build(ctx: magus\\Context, args: [str]) > void {}\n",
		"web/magusfile.buzz": `import "project/../gopherbuzz" as gopherbuzz;
export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.needs(gopherbuzz.build);
}
`,
	}
	for rel, body := range files {
		abs := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	}

	ws, err := Inspect(context.Background(), root)
	require.NoError(t, err, "Inspect")
	graph, err := ws.TargetGraph(context.Background())
	require.NoError(t, err, "TargetGraph")
	var web *types.TargetGraphProject
	for i, p := range graph.Projects {
		if p.Path == "web" {
			web = &graph.Projects[i]
			break
		}
	}
	require.NotNil(t, web, "web project missing from graph")
	assert.Contains(t, web.DependsOn, "gopherbuzz",
		"web.DependsOn should contain \"gopherbuzz\" (derived from the target-level external dep)")
}

// TestAnyProjectDeclaresCI verifies that ci detection extracts target nodes
// statically: `ci` appearing only in a comment or string must NOT count, while a
// real `export fun ci` must. This guards against the old raw-text regex scan, which
// false-positived on `ci` in non-declaration positions.
func TestAnyProjectDeclaresCI(t *testing.T) {
	t.Parallel()

	declares := func(t *testing.T, magusfile string) bool {
		t.Helper()
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(magusfile), 0o644))
		ws, err := Inspect(context.Background(), root)
		require.NoError(t, err, "Inspect")
		has, scanErr := anyProjectDeclaresCI(ws.All())
		require.NoError(t, scanErr, "anyProjectDeclaresCI scan error")
		return has
	}

	t.Run("comment does not count", func(t *testing.T) {
		t.Parallel()
		src := "// export fun ci composes the gate\nexport fun build(ctx: magus\\Context, args: [str]) > void {}\n"
		assert.False(t, declares(t, src), "ci in a comment must not count as declaring ci")
	})

	t.Run("real declaration counts", func(t *testing.T) {
		t.Parallel()
		src := "export fun ci(ctx: magus\\Context, args: [str]) > void {}\n"
		assert.True(t, declares(t, src), "export fun ci must count as declaring ci")
	})
}

// newWorkspaceCustom creates a single-project workspace at a temp dir and
// returns it after applying opts. The root project has an empty magusfile.buzz.
func newWorkspaceCustom(t *testing.T, opts ...Option) types.WorkspaceRepository {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))
	ws, err := Inspect(context.Background(), root, opts...)
	require.NoError(t, err, "Inspect")
	return ws
}

func TestListSpells_ShapeAndOrder(t *testing.T) {
	// Not parallel: mutates global spell registry.
	const name = "zzz-describe-spells-test"
	spell := spells.NewSpell(name, spells.WithTargets("build", "test"))
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(name) })

	out, err := ListSpells(context.Background())
	require.NoError(t, err, "ListSpells")

	require.NotEmpty(t, out, "ListSpells: expected at least the test spell")

	// Entries must be sorted by name.
	for i := 1; i < len(out); i++ {
		assert.LessOrEqualf(t, out[i-1].Name, out[i].Name,
			"ListSpells: Spells not sorted at [%d]=%q, [%d]=%q",
			i-1, out[i-1].Name, i, out[i].Name)
	}

	// The test spell must appear (zzz-* sorts last).
	require.NotEmpty(t, out)
	assert.Equal(t, name, out[len(out)-1].Name,
		"ListSpells: expected test spell as last entry (zzz-prefix sorts last)")
}

func TestSpellToolchainsDeriveFromResolvedCommands(t *testing.T) {
	got := spellToolchains(map[string][]string{
		"test":    {"go", "test", "./..."},
		"build":   {"go", "build", "./..."},
		"format":  {"gofmt", "-w", "."},
		"dynamic": nil,
		"broken":  {""},
	})
	assert.Equal(t, []types.SpellToolchain{
		{Command: "go", Operations: []string{"build", "test"}},
		{Command: "gofmt", Operations: []string{"format"}},
	}, got)
	assert.Nil(t, spellToolchains(nil), "no static operations means no toolchain inventory")
}

func TestListSpells_DescribesTypedSpell(t *testing.T) {
	// Not parallel: mutates the global spell registry.
	const name = "zzz-spell-inventory-test"
	spell := spells.NewSpell(name,
		spells.WithSources("**/*.demo"),
		spells.WithOutputs("out/**"),
		spells.WithTargets("build", "check", "dynamic"),
		spells.WithLanguage("demo"),
		spells.WithOpaque(),
		spells.WithTools(map[string]spells.Tool{"tool": {Probe: spells.Command{Bin: "tool", Args: []string{"--version"}}}}),
		spells.WithVersionProber(func(context.Context, spells.Command, string) (string, error) { return "v1", nil }),
		spells.WithTargetDocs(map[string]string{"build": "Build the demo."}),
		spells.WithCommandRenderer(func(target string, _ []string) (string, []string, bool, error) {
			switch target {
			case "build":
				return "democ", []string{"build"}, true, nil
			case "check":
				return "democ", []string{"check"}, true, nil
			default:
				return "", nil, false, nil // function operation: no static argv
			}
		}),
	)
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(name) })

	inventory, err := ListSpells(context.Background())
	require.NoError(t, err, "ListSpells")

	var got *types.Spell
	for i := range inventory {
		if inventory[i].Name == name {
			got = &inventory[i]
			break
		}
	}
	require.NotNil(t, got, "registered spell missing from inventory")
	assert.Equal(t, "magus/spell/"+name, got.BuzzImport)
	assert.Equal(t, []string{"**/*.demo"}, got.Sources)
	assert.Equal(t, []string{"out/**"}, got.Outputs)
	assert.Equal(t, []string{"build", "check", "dynamic"}, got.Targets)
	assert.Equal(t, "demo", got.Language)
	assert.True(t, got.Opaque)
	assert.True(t, got.VersionProbe)
	assert.Equal(t, map[string]string{"build": "Build the demo."}, got.TargetDocs)
	assert.Equal(t, map[string][]string{
		"build": {"democ", "build"},
		"check": {"democ", "check"},
	}, got.OpCommands, "dynamic operations must not pretend to have static argv")
	assert.Equal(t, []types.SpellToolchain{
		{Command: "democ", Operations: []string{"build", "check"}},
	}, got.Toolchains)
}

func TestListTargets_CanonicalCIFirst(t *testing.T) {
	// Not parallel: mutates global spell registry.
	const spellName = "zzz-targets-spell"
	spell := spells.NewSpell(spellName, spells.WithTargets("zzz-target-a", "zzz-target-b"))
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	ws := newWorkspaceCustom(t, WithWorkspaceRegistry(reg))

	out, err := ws.ListTargets(context.Background())
	require.NoError(t, err, "ListTargets")

	require.NotEmpty(t, out, "ListTargets: no targets")
	assert.Equal(t, "ci", out[0].Name, "ListTargets: first entry")
	assert.Equal(t, "canonical", out[0].Kind, "ListTargets: ci.Kind")

	byName := make(map[string]types.TargetEntry, len(out))
	for _, e := range out {
		byName[e.Name] = e
	}
	for _, target := range []string{"zzz-target-a", "zzz-target-b"} {
		e, ok := byName[target]
		require.Truef(t, ok, "ListTargets: expected spell target %q in output", target)
		assert.Equalf(t, "spell", e.Kind, "ListTargets: %q.Kind", target)
		assert.Containsf(t, e.Spells, spellName, "ListTargets: %q.Spells", target)
	}
}

func TestEvaluateTarget_Charms(t *testing.T) {
	const spellName = "zzz-charm-spell"
	s := spells.NewSpell(spellName,
		spells.WithTargets("lint"),
		spells.WithTargetCharms(map[string][]string{"lint": {"write", "debug"}}),
	)
	project.DefaultSpellRegistry().RegisterSpell(s)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	ws := newWorkspaceCustom(t, WithWorkspaceRegistry(reg))

	out, err := ws.EvaluateTarget(context.Background(), types.Target{Name: "lint"})
	require.NoError(t, err, "EvaluateTarget")
	var got []string
	for _, e := range out {
		if e.Target == "lint" {
			got = e.Charms
		}
	}
	assert.Equal(t, []string{"debug", "write"}, got, "EvaluateTarget(lint).Charms (sorted union across spells)")
}

func TestListCharms_InverseIndex(t *testing.T) {
	// Not parallel: mutates global spell registry.
	const spellName = "zzz-describe-charms-spell"
	s := spells.NewSpell(spellName,
		spells.WithTargets("lint"),
		spells.WithTargetCharms(map[string][]string{"lint": {"write", "debug"}}),
	)
	project.DefaultSpellRegistry().RegisterSpell(s)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	ws := newWorkspaceCustom(t, WithWorkspaceRegistry(reg), WithLoadedConfig(config.Config{DefaultCharms: []string{"write"}}))

	charms, err := ws.ListCharms(context.Background())
	require.NoError(t, err, "ListCharms")

	byName := make(map[string]types.Charm, len(charms))
	for _, c := range charms {
		byName[c.Name] = c
	}

	// Reserved built-ins always appear, documented, even where nothing declares them.
	for _, name := range types.ReservedCharms() {
		e, ok := byName[name]
		require.Truef(t, ok, "ListCharms: reserved charm %q missing", name)
		assert.Truef(t, e.Builtin, "ListCharms: %q.Builtin", name)
		assert.NotEmptyf(t, e.Doc, "ListCharms: %q.Doc", name)
	}

	// A spell-declared charm is indexed back to the target that declares it.
	for _, name := range []string{"write", "debug"} {
		e, ok := byName[name]
		require.Truef(t, ok, "ListCharms: declared charm %q missing", name)
		assert.Falsef(t, e.Builtin, "ListCharms: %q.Builtin should be false", name)
		require.Lenf(t, e.Declarations, 1, "ListCharms: %q declarations", name)
		d := e.Declarations[0]
		assert.Equal(t, ".", d.Project, "declaration project")
		assert.Equal(t, "lint", d.Target, "declaration target")
		assert.Equal(t, spellName, d.Spell, "declaration spell")
	}

	// The workspace default is flagged; a non-default charm is not.
	assert.True(t, byName["write"].Default, "ListCharms: write should be marked default")
	assert.False(t, byName["debug"].Default, "ListCharms: debug should not be default")

	// Entries are sorted by name.
	for i := 1; i < len(charms); i++ {
		assert.LessOrEqualf(t, charms[i-1].Name, charms[i].Name,
			"ListCharms: not sorted at [%d]=%q,[%d]=%q", i-1, charms[i-1].Name, i, charms[i].Name)
	}
}

// TestListTargets_CustomTargets exercises this package's own test build,
// which deliberately does not link the Buzz interpreter (see doc.go and
// register_test.go) — so validateTargetPolicies (A4) cannot see this project's
// magusfile at all and its "policy names an unknown target" enforcement does not
// apply here (see the interp.Available guard in load()). The end-to-end version
// of that check, exercised through a real magusfile via the linked interpreter,
// lives in cmd/magus (which blank-imports interp/bindings): see
// TestInspect_TargetPolicyNamingUnknownTarget in cmd/magus/project_options_test.go.
func TestListTargets_CustomTargets(t *testing.T) {
	t.Parallel()
	const customTarget = "zzz-custom-target"
	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithTarget(customTarget))
	ws := newWorkspaceCustom(t, WithWorkspaceRegistry(reg))

	out, err := ws.ListTargets(context.Background())
	require.NoError(t, err, "ListTargets")

	byName := make(map[string]types.TargetEntry, len(out))
	for _, e := range out {
		byName[e.Name] = e
	}
	e, ok := byName[customTarget]
	require.Truef(t, ok, "ListTargets: custom target %q not found in output", customTarget)
	assert.Equal(t, "custom", e.Kind, "ListTargets: Kind")
	assert.Contains(t, e.Projects, ".", "ListTargets: Projects")
}

func TestListProjects_Inventory(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	out, err := ws.ListProjects(context.Background())
	require.NoError(t, err, "ListProjects")

	assert.NotEmpty(t, out.Definition, "ListProjects: Definition is empty")
	wantPaths := []string{".", "api", "extensions/drape", "extensions/lattice", "web/studio"}
	assert.Equal(t, len(wantPaths), out.Count, "ListProjects: Count")
	assert.Equal(t, ws.Root(), out.Workspace, "ListProjects: Workspace")
	byPath := make(map[string]types.ProjectEntry, len(out.Projects))
	for _, e := range out.Projects {
		byPath[e.Path] = e
	}
	for _, p := range wantPaths {
		_, ok := byPath[p]
		assert.Truef(t, ok, "ListProjects: project %q missing from output", p)
	}
}

// TestListProjects_Manifests covers projectManifests, the resolution behind
// ProjectEntry.Manifests: a spell's declared candidates (spells.Spell.Manifests)
// are filtered down to the ones that exist in the project's directory, in
// declared order - so the first-existing-file-wins rule holds even when an
// earlier-declared candidate is absent - and a project whose spell's candidates
// are all absent reports no manifest at all.
func TestListProjects_Manifests(t *testing.T) {
	// Not parallel: mutates the global spell registry.
	const hasSpell, noneSpell = "zzz-manifest-has", "zzz-manifest-none"
	project.DefaultSpellRegistry().RegisterSpell(
		spells.NewSpell(hasSpell, spells.WithManifests("pyproject.toml", "setup.py", "setup.cfg")))
	project.DefaultSpellRegistry().RegisterSpell(
		spells.NewSpell(noneSpell, spells.WithManifests("Cargo.toml")))
	t.Cleanup(func() {
		project.DefaultSpellRegistry().UnregisterSpell(hasSpell)
		project.DefaultSpellRegistry().UnregisterSpell(noneSpell)
	})

	root := t.TempDir()
	for _, rel := range []string{"magusfile.buzz", "has/magusfile.buzz", "none/magusfile.buzz"} {
		abs := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(""), 0o644))
	}
	// "has" carries setup.py, NOT pyproject.toml (the first-declared candidate) -
	// Manifests must report the first EXISTING candidate, not the first declared one.
	require.NoError(t, os.WriteFile(filepath.Join(root, "has", "setup.py"), []byte(""), 0o644))

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".")
	reg.RegisterProject("has", WithSpell(hasSpell))
	reg.RegisterProject("none", WithSpell(noneSpell))
	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Inspect")

	out, err := ws.ListProjects(context.Background())
	require.NoError(t, err, "ListProjects")
	byPath := make(map[string]types.ProjectEntry, len(out.Projects))
	for _, e := range out.Projects {
		byPath[e.Path] = e
	}
	assert.Equal(t, []string{"setup.py"}, byPath["has"].Manifests, `ListProjects: "has".Manifests`)
	assert.Empty(t, byPath["none"].Manifests, `ListProjects: "none".Manifests`)
}

func TestEvaluateTarget_FanOut(t *testing.T) {
	t.Parallel()
	// A bare target ":build" should fan out to every project.
	ws := newWorkspace(t)
	out, err := ws.EvaluateTarget(context.Background(), types.Target{Name: "build"})
	require.NoError(t, err, "EvaluateTarget")
	wantProjects := []string{".", "api", "extensions/drape", "extensions/lattice", "web/studio"}
	assert.Len(t, out, len(wantProjects), "EvaluateTarget: one entry per project")
	byProject := make(map[string]types.EvaluatedTarget, len(out))
	for _, e := range out {
		byProject[e.Project] = e
	}
	for _, p := range wantProjects {
		e, ok := byProject[p]
		require.Truef(t, ok, "EvaluateTarget: project %q missing from output", p)
		assert.Equalf(t, "build", e.Target, "EvaluateTarget: project %q target", p)
		assert.NotEmptyf(t, e.Dir, "EvaluateTarget: project %q Dir is empty", p)
	}
}

func TestEvaluateTarget_SingleProject(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	out, err := ws.EvaluateTarget(context.Background(), types.Target{Path: "api", Name: "test"})
	require.NoError(t, err, "EvaluateTarget")
	require.Len(t, out, 1, "EvaluateTarget: one entry")
	e := out[0]
	assert.Equal(t, "api", e.Project, "EvaluateTarget: Project")
	assert.Equal(t, "test", e.Target, "EvaluateTarget: Target")
}

func TestEvaluateTarget_UnknownProject(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	_, err := ws.EvaluateTarget(context.Background(), types.Target{Path: "does-not-exist", Name: "build"})
	assert.Error(t, err, "EvaluateTarget: expected error for unknown project")
}

func TestEvaluateTarget_WithSpellAndPolicy(t *testing.T) {
	// Not parallel: mutates global spell registry.
	const spellName = "zzz-dt-spell"
	spell := spells.NewSpell(
		spellName,
		spells.WithTargets("my-target"),
		spells.WithSources("**/*.zzz"),
	)
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(
		".",
		WithSpell(spellName),
		WithTarget("my-target", RetryOnVolatile()),
	)
	ws := newWorkspaceCustom(t, WithWorkspaceRegistry(reg))

	out, err := ws.EvaluateTarget(context.Background(), types.Target{Name: "my-target"})
	require.NoError(t, err, "EvaluateTarget")
	require.NotEmpty(t, out, "EvaluateTarget: no entries")
	e := out[0]

	// Spell entry must be present.
	require.NotEmpty(t, e.Spells, "EvaluateTarget: Spells is empty, expected at least one entry")
	assert.Equal(t, spellName, e.Spells[0].Name, "EvaluateTarget: Spells[0].Name")

	// Policy must be present with the volatility-retry flag set.
	require.NotNil(t, e.Policy, "EvaluateTarget: Policy is nil, want TrackVolatile=true")
	assert.True(t, e.Policy.RetryOnVolatile, "EvaluateTarget: Policy.RetryOnVolatile = false, want true")
}

func TestEvaluateProjects_Shape(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	out, err := ws.EvaluateProjects(context.Background())
	require.NoError(t, err, "EvaluateProjects")

	assert.NotEmpty(t, out.Definition, "EvaluateProjects: Definition is empty")
	wantPaths := []string{".", "api", "extensions/drape", "extensions/lattice", "web/studio"}
	assert.Equal(t, len(wantPaths), out.Count, "EvaluateProjects: Count")
	assert.Equal(t, ws.Root(), out.Workspace, "EvaluateProjects: Workspace")
	byPath := make(map[string]types.EvaluatedProject, len(out.Projects))
	for _, e := range out.Projects {
		byPath[e.Path] = e
	}
	for _, p := range wantPaths {
		_, ok := byPath[p]
		assert.Truef(t, ok, "EvaluateProjects: project %q missing from output", p)
	}
}

func TestEvaluateProjects_WorkspaceRootedSources(t *testing.T) {
	// Not parallel: mutates global spell registry.
	const spellName = "zzz-ep-spell"
	spell := spells.NewSpell(spellName, spells.WithSources("**/*.ep"))
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	// Build workspace with root + api project.
	root := t.TempDir()
	for _, rel := range []string{"magusfile.buzz", "api/magusfile.buzz"} {
		abs := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(""), 0o644))
	}

	reg := NewWorkspaceRegistry()
	reg.RegisterProject("api", WithSpell(spellName))
	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Inspect")

	out, err := ws.EvaluateProjects(context.Background())
	require.NoError(t, err, "EvaluateProjects")

	var apiEntry *types.EvaluatedProject
	for i := range out.Projects {
		if out.Projects[i].Path == "api" {
			apiEntry = &out.Projects[i]
			break
		}
	}
	require.NotNil(t, apiEntry, "EvaluateProjects: \"api\" project missing from output")

	// Sources must be workspace-rooted ("api/**/*.ep"), not project-relative.
	assert.NotContains(t, apiEntry.Sources, "**/*.ep",
		"EvaluateProjects: Sources contains project-relative glob, want workspace-rooted \"api/**/*.ep\"")
	assert.Contains(t, apiEntry.Sources, "api/**/*.ep",
		"EvaluateProjects: expected \"api/**/*.ep\" in Sources")
}

func TestWorkspace_SingleWorkspace(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	cfg := types.WorkspaceConfig{CacheDir: "/tmp/cache-test", Concurrency: 4}
	entry, err := ws.Workspace(context.Background(), cfg)
	require.NoError(t, err, "Workspace")

	assert.Equal(t, ws.Root(), entry.Root, "Root")
	assert.Equal(t, cfg.CacheDir, entry.CacheDir, "CacheDir")
	assert.Equal(t, cfg.Concurrency, entry.Concurrency, "Concurrency")
	assert.NotZero(t, entry.ProjectCount, "ProjectCount = 0, want > 0")
}

// TestClassifyFiles_Classification covers the roles end to end: a declared
// output, a declared source, an unclaimed path, and nested-project ownership.
// Globs come from registered spells, the same channel real projects declare
// them through.
func TestClassifyFiles_Classification(t *testing.T) {
	// Not parallel: mutates the global spell registry.
	const rootSpell, webSpell = "zzz-df-root", "zzz-df-web"
	project.DefaultSpellRegistry().RegisterSpell(
		spells.NewSpell(rootSpell, spells.WithSources("docs/**/*.md"), spells.WithOutputs("GEN.md", "gen/**")))
	project.DefaultSpellRegistry().RegisterSpell(
		spells.NewSpell(webSpell, spells.WithSources("**/*.ts"), spells.WithOutputs("dist/**")))
	t.Cleanup(func() {
		project.DefaultSpellRegistry().UnregisterSpell(rootSpell)
		project.DefaultSpellRegistry().UnregisterSpell(webSpell)
	})

	root := t.TempDir()
	for _, rel := range []string{"magusfile.buzz", "web/magusfile.buzz"} {
		abs := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(""), 0o644))
	}
	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(rootSpell))
	reg.RegisterProject("web", WithSpell(webSpell))
	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Inspect")

	out, err := ws.ClassifyFiles(context.Background(), []string{"GEN.md", "docs/guide.md", "web/dist/app.js", "web/app.ts", "scratch.tmp", "./web/magusfile.buzz", ".gitattributes"})
	require.NoError(t, err, "ClassifyFiles")
	require.Len(t, out, 7)
	byPath := map[string]types.FileEntry{}
	for _, f := range out {
		byPath[f.Path] = f
	}

	gen := byPath["GEN.md"]
	assert.Equal(t, ".", gen.Project)
	assert.Equal(t, "output", gen.Role)
	assert.Equal(t, []string{"."}, gen.OutputOf)
	assert.Contains(t, gen.Hint, "generated")

	assert.Equal(t, "source", byPath["docs/guide.md"].Role)
	assert.Equal(t, []string{"."}, byPath["docs/guide.md"].SourceOf)

	// Nested project claims ownership and the output role.
	dist := byPath["web/dist/app.js"]
	assert.Equal(t, "web", dist.Project)
	assert.Equal(t, "output", dist.Role)
	assert.Equal(t, []string{"web"}, dist.OutputOf)

	assert.Equal(t, "source", byPath["web/app.ts"].Role)
	assert.Equal(t, []string{"web"}, byPath["web/app.ts"].SourceOf)

	unclaimed := byPath["scratch.tmp"]
	assert.Equal(t, "unclaimed", unclaimed.Role)
	assert.Empty(t, unclaimed.OutputOf)
	assert.Contains(t, unclaimed.Hint, "no project declares")

	// magus maintains .gitattributes outside every target's globs, so it matches no
	// declared glob and would otherwise land in unclaimed beside scratch.tmp - with a
	// hint telling you to consider ignoring a file magus wrote and needs tracked.
	maintained := byPath[".gitattributes"]
	assert.Equal(t, "maintained", maintained.Role)
	assert.Empty(t, maintained.OutputOf, "maintained is not a declared output")
	assert.Empty(t, maintained.SourceOf, "maintained keys nothing")
	assert.Contains(t, maintained.Hint, "expects it committed")
	assert.NotContains(t, maintained.Hint, "ignore rules",
		"the unclaimed hint's ignore-rules advice must not reach a file magus maintains")

	// A ./ prefix normalizes away; magusfiles always count as sources.
	assert.Equal(t, "source", byPath["web/magusfile.buzz"].Role)
}

// TestClassifyFiles_DeclaredBeatsMaintained pins the precedence. maintained refines
// the UNCLAIMED default; it must never mask a project's declaration, or a workspace
// that legitimately declares one of these paths would stop being told it is keyed.
func TestClassifyFiles_DeclaredBeatsMaintained(t *testing.T) {
	// Not parallel: mutates the global spell registry.
	const spellName = "zzz-df-attrs"
	project.DefaultSpellRegistry().RegisterSpell(
		spells.NewSpell(spellName, spells.WithSources(".gitattributes")))
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))
	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Inspect")

	out, err := ws.ClassifyFiles(context.Background(), []string{".gitattributes"})
	require.NoError(t, err, "ClassifyFiles")
	require.Len(t, out, 1)
	assert.Equal(t, "source", out[0].Role)
	assert.Equal(t, []string{"."}, out[0].SourceOf)
}

// TestInspectorMethods_HonorCancelledContext pins that a context cancelled BEFORE
// an Inspector method's outer walk begins is REPORTED, not silently swallowed: the
// method returns a non-nil error satisfying errors.Is(err, context.Canceled)
// together with the zero value of its result type. A truncated-but-returned result
// (e.g. Count: 3 out of 50 projects) is indistinguishable from a genuinely small
// workspace, which is the exact bug describeCancelled exists to prevent - so the
// cancelled case here asserts BOTH halves: the error, and that nothing rides along
// with it. Table-driven over all nine methods.
//
// ListCharms and ListTargets build one unconditional entry (the reserved
// charms; the canonical "ci" target) before their per-project walk starts, so their
// cases use a dedicated spell+target fixture: the live control proves the per-project
// walk actually ran (the fixture's charm/target is present), which the cancelled case
// then proves never happens (the whole result, baseline included, is the zero value).
//
// Each case also runs the same call against a live context as a positive control:
// without it, a method broken to unconditionally error would pass the cancelled case
// for the wrong reason.
func TestInspectorMethods_HonorCancelledContext(t *testing.T) {
	// Not parallel: mutates the global spell registry.
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	liveCtx := context.Background()

	ws := newWorkspace(t)

	// ListCharms/ListTargets need a project that actually USES a declared
	// spell, or their per-project walk contributes nothing even when it runs to
	// completion - making the live case pass by coincidence rather than because the
	// walk executed.
	const spellName = "zzz-ctx-cancel-spell"
	const customTarget = "zzz-ctx-cancel-target"
	const customCharm = "zzz-ctx-cancel-charm"
	spell := spells.NewSpell(spellName,
		spells.WithTargets(customTarget),
		spells.WithTargetCharms(map[string][]string{customTarget: {customCharm}}),
	)
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })
	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	spellWs := newWorkspaceCustom(t, WithWorkspaceRegistry(reg))

	hasCharm := func(entries []types.Charm, name string) bool {
		for _, e := range entries {
			if e.Name == name {
				return true
			}
		}
		return false
	}
	hasTarget := func(entries []types.TargetEntry, name string) bool {
		for _, e := range entries {
			if e.Name == name {
				return true
			}
		}
		return false
	}

	tests := []struct {
		name      string
		cancelled func(t *testing.T)
		live      func(t *testing.T)
	}{
		{
			name: "ListSpells",
			cancelled: func(t *testing.T) {
				out, err := ListSpells(cancelledCtx)
				require.Error(t, err, "ListSpells")
				assert.ErrorIs(t, err, context.Canceled, "ListSpells error")
				assert.Nil(t, out, "ListSpells entries")
			},
			live: func(t *testing.T) {
				out, err := ListSpells(liveCtx)
				require.NoError(t, err, "ListSpells")
				assert.NotEmpty(t, out)
			},
		},
		{
			name: "ListCharms",
			cancelled: func(t *testing.T) {
				out, err := spellWs.ListCharms(cancelledCtx)
				require.Error(t, err, "ListCharms")
				assert.ErrorIs(t, err, context.Canceled, "ListCharms error")
				assert.Nil(t, out, "ListCharms entries")
			},
			live: func(t *testing.T) {
				out, err := spellWs.ListCharms(liveCtx)
				require.NoError(t, err, "ListCharms")
				assert.True(t, hasCharm(out, customCharm), "declared charm missing from a live walk")
			},
		},
		{
			name: "ListTargets",
			cancelled: func(t *testing.T) {
				out, err := spellWs.ListTargets(cancelledCtx)
				require.Error(t, err, "ListTargets")
				assert.ErrorIs(t, err, context.Canceled, "ListTargets error")
				assert.Nil(t, out, "ListTargets entries")
			},
			live: func(t *testing.T) {
				out, err := spellWs.ListTargets(liveCtx)
				require.NoError(t, err, "ListTargets")
				assert.True(t, hasTarget(out, customTarget), "custom target missing from a live walk")
			},
		},
		{
			name: "ListProjects",
			cancelled: func(t *testing.T) {
				out, err := ws.ListProjects(cancelledCtx)
				require.Error(t, err, "ListProjects")
				assert.ErrorIs(t, err, context.Canceled, "ListProjects error")
				assert.Zero(t, out, "ListProjects result")
			},
			live: func(t *testing.T) {
				out, err := ws.ListProjects(liveCtx)
				require.NoError(t, err, "ListProjects")
				assert.NotEmpty(t, out.Projects, "Projects")
				assert.NotZero(t, out.Count, "Count")
			},
		},
		{
			name: "EvaluateProjects",
			cancelled: func(t *testing.T) {
				out, err := ws.EvaluateProjects(cancelledCtx)
				require.Error(t, err, "EvaluateProjects")
				assert.ErrorIs(t, err, context.Canceled, "EvaluateProjects error")
				assert.Zero(t, out, "EvaluateProjects result")
			},
			live: func(t *testing.T) {
				out, err := ws.EvaluateProjects(liveCtx)
				require.NoError(t, err, "EvaluateProjects")
				assert.NotEmpty(t, out.Projects, "Projects")
				assert.NotZero(t, out.Count, "Count")
			},
		},
		{
			name: "ClassifyFiles",
			cancelled: func(t *testing.T) {
				out, err := ws.ClassifyFiles(cancelledCtx, []string{"magusfile.buzz", "api/magusfile.buzz"})
				require.Error(t, err, "ClassifyFiles")
				assert.ErrorIs(t, err, context.Canceled, "ClassifyFiles error")
				assert.Nil(t, out, "ClassifyFiles entries")
			},
			live: func(t *testing.T) {
				out, err := ws.ClassifyFiles(liveCtx, []string{"magusfile.buzz", "api/magusfile.buzz"})
				require.NoError(t, err, "ClassifyFiles")
				assert.Len(t, out, 2)
			},
		},
		{
			name: "TargetGraph",
			cancelled: func(t *testing.T) {
				out, err := ws.TargetGraph(cancelledCtx)
				require.Error(t, err, "TargetGraph")
				assert.ErrorIs(t, err, context.Canceled, "TargetGraph error")
				assert.Zero(t, out, "TargetGraph result")
			},
			live: func(t *testing.T) {
				out, err := ws.TargetGraph(liveCtx)
				require.NoError(t, err, "TargetGraph")
				assert.NotEmpty(t, out.Projects)
			},
		},
		{
			name: "EvaluateTarget",
			cancelled: func(t *testing.T) {
				out, err := ws.EvaluateTarget(cancelledCtx, types.Target{Name: "build"})
				require.Error(t, err, "EvaluateTarget")
				assert.ErrorIs(t, err, context.Canceled, "EvaluateTarget error")
				assert.Nil(t, out, "EvaluateTarget entries")
			},
			live: func(t *testing.T) {
				out, err := ws.EvaluateTarget(liveCtx, types.Target{Name: "build"})
				require.NoError(t, err, "EvaluateTarget")
				assert.NotEmpty(t, out)
			},
		},
		{
			name: "Workspace",
			cancelled: func(t *testing.T) {
				cfg := types.WorkspaceConfig{CacheDir: "/tmp/cache-test", Concurrency: 4}
				out, err := ws.Workspace(cancelledCtx, cfg)
				require.Error(t, err, "Workspace")
				assert.ErrorIs(t, err, context.Canceled, "Workspace error")
				assert.Zero(t, out, "Workspace entry")
			},
			live: func(t *testing.T) {
				cfg := types.WorkspaceConfig{CacheDir: "/tmp/cache-test", Concurrency: 4}
				out, err := ws.Workspace(liveCtx, cfg)
				require.NoError(t, err, "Workspace")
				assert.NotZero(t, out.ProjectCount)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Run("cancelled", tt.cancelled)
			t.Run("live", tt.live)
		})
	}
}

// TestEvaluateTarget_ReportsPerTargetOutputs pins that a target's description
// carries the target's OWN declared outputs. It read the project-wide globs
// before, so a target whose outputs come from ctx.writesFiles described itself as
// producing nothing - the described plan disagreed with what the cache keys and
// snapshots, which is the one thing this command exists to show.
func TestEvaluateTarget_ReportsPerTargetOutputs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const mf = `export fun generate(ctx: magus\Context, args: [str]) > void {
    ctx.writesFiles("GEN.md");
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(mf), 0o644))

	m, err := Open(context.Background(), root)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	out, err := m.EvaluateTarget(context.Background(), types.Target{Name: "generate"})
	require.NoError(t, err, "EvaluateTarget")
	require.Len(t, out, 1)
	assert.Equal(t, []string{"GEN.md"}, out[0].Outputs,
		"a per-target ctx.writesFiles glob belongs in that target's own description")
}

// TestClassifyFiles_PerTargetOutputs pins the classification against the whole
// declared set, not the project-wide globs alone. A file declared only by a
// per-target ctx.writesFiles is generated by every other consumer's reckoning (clean
// removes it, the merge driver regenerates it), so reporting it as a source told
// an agent it was safe to hand-edit the one file it must never hand-edit. This
// repo's own root MAGUS.md is declared exactly this way.
func TestClassifyFiles_PerTargetOutputs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const mf = `export fun generate(ctx: magus\Context, args: [str]) > void {
    ctx.writesFiles("GEN.md");
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(mf), 0o644))

	m, err := Open(context.Background(), root)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	out, err := m.ClassifyFiles(context.Background(), []string{"GEN.md"})
	require.NoError(t, err, "ClassifyFiles")
	require.Len(t, out, 1)
	assert.Equal(t, types.FileEntry{
		Path:     "GEN.md",
		Project:  ".",
		Role:     "output",
		OutputOf: []string{"."},
		Claims:   []types.FileClaim{{Project: ".", Target: "generate", Role: "output", Glob: "GEN.md"}},
		Hint:     out[0].Hint,
	}, out[0])
	assert.Contains(t, out[0].Hint, "generated")
}

// TestDescribeFileSeesCrossProjectReads pins that a file a target declares via a
// cross-project ctx.readsFiles reports the READING project in source_of, not just the
// project whose directory contains it.
//
// The regression it guards is not cosmetic. `magus vcs add` decides whether a drifted
// output is explained by asking, per PROJECT, whether that project has a dirty declared
// source (types.SplitExplainedOutputs). describeFile built source_of from the project-wide
// baseline only, while the output side already folded in per-target ctx.writesFiles via
// AllOutputs - so a cross-project read was invisible, and regenerating an output from a
// source in another project was reported as MGS4005 environmental drift with "not your
// change, do not commit".
//
// magusfile.buzz's own generate gate declines magus.diagnoseDrift citing this exact
// symptom, which is how long it went unfixed.
func TestDescribeFileSeesCrossProjectReads(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"skills/note.md":        "# a source another project renders\n",
		"skills/magusfile.buzz": "export fun build(ctx: magus\\Context, args: [str]) > void {}\n",
		"site/magusfile.buzz": `import "project/../skills" as skills;
export fun render(ctx: magus\Context, args: [str]) > void {
    ctx.readsFiles(skills.file("note.md"));
    ctx.writesFiles("gen/*.html");
}
`,
	}
	for rel, body := range files {
		abs := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	}

	ws, err := Inspect(context.Background(), root)
	require.NoError(t, err, "Inspect")
	m, ok := ws.(*Magus)
	require.True(t, ok, "Inspect returns *Magus")
	entries, err := m.ClassifyFiles(context.Background(), []string{"skills/note.md"})
	require.NoError(t, err, "ClassifyFiles")
	require.Len(t, entries, 1)

	assert.Contains(t, entries[0].SourceOf, "site",
		"site declares skills/note.md via a cross-project ctx.readsFiles, so it must appear in source_of; "+
			"without it magus vcs add calls site's regenerated output MGS4005 and tells the author not to commit it")
}

// TestClassifyFiles_Claims pins the per-declaration facts, which are what a caller
// handing paths to concurrent authors needs and what output_of/source_of cannot
// say: WHICH target declared each path, the glob it matched, and - for a
// cross-project write - the project that DECLARED it rather than the tree it lands
// in. It also pins the two dependency edges a cross-project ref creates, in
// opposite directions.
func TestClassifyFiles_Claims(t *testing.T) {
	root := t.TempDir()
	// Three projects, because a target may not both read from and write into one
	// other project (MGS1012): site reads skills and writes dist.
	files := map[string]string{
		"skills/note.md":        "# a source another project renders\n",
		"skills/magusfile.buzz": "export fun build(ctx: magus\\Context, args: [str]) > void {}\n",
		"dist/magusfile.buzz":   "export fun build(ctx: magus\\Context, args: [str]) > void {}\n",
		"site/magusfile.buzz": `import "project/../skills" as skills;
import "project/../dist" as dist;
export fun render(ctx: magus\Context, args: [str]) > void {
    ctx.readsFiles(skills.file("note.md"));
    ctx.writesFiles("gen/*.html");
}
export fun publish(ctx: magus\Context, args: [str]) > void {
    ctx.writesFiles(dist.file("index.html"));
}
export fun stamp(ctx: magus\Context, args: [str]) > void {
    ctx.modifiesExistingFiles("README.md");
}
`,
	}
	for rel, body := range files {
		abs := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	}

	ws, err := Inspect(context.Background(), root)
	require.NoError(t, err, "Inspect")
	paths := []string{"skills/note.md", "dist/index.html", "skills/magusfile.buzz", "site/gen/a.html", "site/gen/b.html", "site/README.md"}
	entries, err := ws.ClassifyFiles(context.Background(), paths)
	require.NoError(t, err, "ClassifyFiles")
	require.Len(t, entries, len(paths))
	byPath := map[string]types.FileEntry{}
	for _, f := range entries {
		byPath[f.Path] = f
	}

	for _, tc := range []struct {
		name      string
		path      string
		role      string
		claims    []types.FileClaim
		dependsOn []string
		hint      string // substring, when the hint is part of what the case pins
	}{
		{
			// No depends_on: a cross-project READ gives the edge to the reader, so it
			// shows up on site's files below, not here.
			name:   "cross-project read is a source claim of the READING target",
			path:   "skills/note.md",
			role:   "source",
			claims: []types.FileClaim{{Project: "site", Target: "render", Role: "source", Glob: "skills/note.md"}},
		},
		{
			// The file lands in dist, so output_of says dist; only site's magusfile
			// can regenerate it, so the claim says site.
			name:      "cross-project write is claimed by the WRITER, not the owner",
			path:      "dist/index.html",
			role:      "output",
			claims:    []types.FileClaim{{Project: "site", Target: "publish", Role: "output", Glob: "dist/index.html"}},
			dependsOn: []string{"site"},
		},
		{
			name:   "a project-wide glob carries no target",
			path:   "skills/magusfile.buzz",
			role:   "source",
			claims: []types.FileClaim{{Project: "skills", Role: "source", Glob: "skills/magusfile.buzz"}},
		},
		{
			name:      "a per-target write glob names its target",
			path:      "site/gen/a.html",
			role:      "output",
			claims:    []types.FileClaim{{Project: "site", Target: "render", Role: "output", Glob: "site/gen/*.html"}},
			dependsOn: []string{"skills"},
		},
		{
			// An in-place edit is neither an output nor a project-wide source, so the
			// role stays unclaimed - but without the claim the only write set that
			// names the file would be invisible, and the default hint would tell the
			// reader nothing declares it.
			name:      "an in-place edit is claimed, and the hint stops calling it undeclared",
			path:      "site/README.md",
			role:      "unclaimed",
			claims:    []types.FileClaim{{Project: "site", Target: "stamp", Role: "update", Glob: "site/README.md"}},
			dependsOn: []string{"skills"},
			hint:      "edits it in place",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := byPath[tc.path]
			assert.Equal(t, tc.claims, got.Claims, "claims")
			assert.Equal(t, tc.dependsOn, got.DependsOn, "depends_on")
			assert.Equal(t, tc.role, got.Role, "role")
			if tc.hint != "" {
				assert.Contains(t, got.Hint, tc.hint, "hint")
			}
		})
	}

	assert.Equal(t, []types.FileClaim{{
		Project: "site", Target: "render", Role: "output", Glob: "site/gen/*.html",
		Paths: []string{"site/gen/a.html", "site/gen/b.html"},
	}}, types.NewFileReport(entries).Overlaps,
		"one ctx.writesFiles glob covers both html paths, and that is the collision fact - "+
			"the same declaration regenerates them, so two authors editing one each are editing one write set")

	assert.Equal(t, []string{"dist"}, byPath["dist/index.html"].OutputOf,
		"output_of stays the tree the file lands in, whatever the claim says about who writes it")
}
