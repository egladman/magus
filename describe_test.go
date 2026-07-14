package magus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// TestDeriveCrossProjectDeps verifies that a target-level cross-project dependency
// (a project import + <alias>.<target>) is folded into the depending project's
// DependsOn, so it counts toward the affected set without a separate project-level
// depends_on.
func TestDeriveCrossProjectDeps(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"gopherbuzz/magusfile.buzz": "export fun build(args: [str]) > void {}\n",
		"web/magusfile.buzz": `import "project/../gopherbuzz" as gopherbuzz;
export fun build(args: [str]) > void {
    magus.needs(gopherbuzz.build);
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
	var web *types.TargetGraphProject
	for i, p := range ws.DescribeGraph().Projects {
		if p.Path == "web" {
			web = &ws.DescribeGraph().Projects[i]
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
		src := "// export fun ci composes the gate\nexport fun build(args: [str]) > void {}\n"
		assert.False(t, declares(t, src), "ci in a comment must not count as declaring ci")
	})

	t.Run("real declaration counts", func(t *testing.T) {
		t.Parallel()
		src := "export fun ci(args: [str]) > void {}\n"
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

func TestDescribeSpells_ShapeAndOrder(t *testing.T) {
	// Not parallel: mutates global spell registry.
	const name = "zzz-describe-spells-test"
	spell := types.NewSpell(name, types.WithTargets("build", "test"))
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(name) })

	ws := newWorkspace(t)
	out := ws.DescribeSpells()

	assert.NotEmpty(t, out.Definition, "DescribeSpells: Definition is empty")
	assert.NotZero(t, out.Count, "DescribeSpells: Count == 0, expected at least the test spell")
	assert.Len(t, out.Spells, out.Count, "DescribeSpells: len(Spells) != Count")

	// Entries must be sorted by name.
	for i := 1; i < len(out.Spells); i++ {
		assert.LessOrEqualf(t, out.Spells[i-1].Name, out.Spells[i].Name,
			"DescribeSpells: Spells not sorted at [%d]=%q, [%d]=%q",
			i-1, out.Spells[i-1].Name, i, out.Spells[i].Name)
	}

	// The test spell must appear (zzz-* sorts last).
	require.NotEmpty(t, out.Spells)
	assert.Equal(t, name, out.Spells[len(out.Spells)-1].Name,
		"DescribeSpells: expected test spell as last entry (zzz-prefix sorts last)")
}

func TestDescribeTargets_CanonicalCIFirst(t *testing.T) {
	// Not parallel: mutates global spell registry.
	const spellName = "zzz-targets-spell"
	spell := types.NewSpell(spellName, types.WithTargets("zzz-target-a", "zzz-target-b"))
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	ws := newWorkspaceCustom(t, WithWorkspaceRegistry(reg))

	out := ws.DescribeTargets()

	require.NotZero(t, out.Count, "DescribeTargets: Count == 0")
	assert.Equal(t, "ci", out.Targets[0].Name, "DescribeTargets: first entry")
	assert.Equal(t, "canonical", out.Targets[0].Kind, "DescribeTargets: ci.Kind")

	byName := make(map[string]types.TargetEntry, len(out.Targets))
	for _, e := range out.Targets {
		byName[e.Name] = e
	}
	for _, target := range []string{"zzz-target-a", "zzz-target-b"} {
		e, ok := byName[target]
		require.Truef(t, ok, "DescribeTargets: expected spell target %q in output", target)
		assert.Equalf(t, "spell", e.Kind, "DescribeTargets: %q.Kind", target)
		assert.Containsf(t, e.Spells, spellName, "DescribeTargets: %q.Spells", target)
	}
}

func TestDescribeTarget_Charms(t *testing.T) {
	const spellName = "zzz-charm-spell"
	s := types.NewSpell(spellName,
		types.WithTargets("lint"),
		types.WithTargetCharms(map[string][]string{"lint": {"write", "debug"}}),
	)
	project.DefaultSpellRegistry().RegisterSpell(s)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	ws := newWorkspaceCustom(t, WithWorkspaceRegistry(reg))

	out, err := ws.DescribeTarget(types.Target{Name: "lint"})
	require.NoError(t, err, "DescribeTarget")
	var got []string
	for _, e := range out.Targets {
		if e.Target == "lint" {
			got = e.Charms
		}
	}
	assert.Equal(t, []string{"debug", "write"}, got, "DescribeTarget(lint).Charms (sorted union across spells)")
}

func TestDescribeCharms_InverseIndex(t *testing.T) {
	// Not parallel: mutates global spell registry.
	const spellName = "zzz-describe-charms-spell"
	s := types.NewSpell(spellName,
		types.WithTargets("lint"),
		types.WithTargetCharms(map[string][]string{"lint": {"write", "debug"}}),
	)
	project.DefaultSpellRegistry().RegisterSpell(s)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	ws := newWorkspaceCustom(t, WithWorkspaceRegistry(reg))

	out := ws.DescribeCharms([]string{"write"})
	assert.NotEmpty(t, out.Definition, "DescribeCharms: Definition is empty")

	byName := make(map[string]types.CharmEntry, len(out.Charms))
	for _, c := range out.Charms {
		byName[c.Name] = c
	}

	// Reserved built-ins always appear, documented, even where nothing declares them.
	for _, name := range types.ReservedCharms() {
		e, ok := byName[name]
		require.Truef(t, ok, "DescribeCharms: reserved charm %q missing", name)
		assert.Truef(t, e.Builtin, "DescribeCharms: %q.Builtin", name)
		assert.NotEmptyf(t, e.Doc, "DescribeCharms: %q.Doc", name)
	}

	// A spell-declared charm is indexed back to the target that declares it.
	for _, name := range []string{"write", "debug"} {
		e, ok := byName[name]
		require.Truef(t, ok, "DescribeCharms: declared charm %q missing", name)
		assert.Falsef(t, e.Builtin, "DescribeCharms: %q.Builtin should be false", name)
		require.Lenf(t, e.Declarations, 1, "DescribeCharms: %q declarations", name)
		d := e.Declarations[0]
		assert.Equal(t, ".", d.Project, "declaration project")
		assert.Equal(t, "lint", d.Target, "declaration target")
		assert.Equal(t, spellName, d.Spell, "declaration spell")
	}

	// The workspace default is flagged; a non-default charm is not.
	assert.True(t, byName["write"].Default, "DescribeCharms: write should be marked default")
	assert.False(t, byName["debug"].Default, "DescribeCharms: debug should not be default")

	// Entries are sorted by name.
	for i := 1; i < len(out.Charms); i++ {
		assert.LessOrEqualf(t, out.Charms[i-1].Name, out.Charms[i].Name,
			"DescribeCharms: not sorted at [%d]=%q,[%d]=%q", i-1, out.Charms[i-1].Name, i, out.Charms[i].Name)
	}
}

func TestDescribeTargets_CustomTargets(t *testing.T) {
	t.Parallel()
	const customTarget = "zzz-custom-target"
	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithTarget(customTarget))
	ws := newWorkspaceCustom(t, WithWorkspaceRegistry(reg))

	out := ws.DescribeTargets()

	byName := make(map[string]types.TargetEntry, len(out.Targets))
	for _, e := range out.Targets {
		byName[e.Name] = e
	}
	e, ok := byName[customTarget]
	require.Truef(t, ok, "DescribeTargets: custom target %q not found in output", customTarget)
	assert.Equal(t, "custom", e.Kind, "DescribeTargets: Kind")
	assert.Contains(t, e.Projects, ".", "DescribeTargets: Projects")
}

func TestDescribeProjects_Inventory(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	out := ws.DescribeProjects()

	assert.NotEmpty(t, out.Definition, "DescribeProjects: Definition is empty")
	wantPaths := []string{".", "api", "extensions/drape", "extensions/lattice", "web/studio"}
	assert.Equal(t, len(wantPaths), out.Count, "DescribeProjects: Count")
	assert.Equal(t, ws.Root(), out.Workspace, "DescribeProjects: Workspace")
	byPath := make(map[string]types.ProjectEntry, len(out.Projects))
	for _, e := range out.Projects {
		byPath[e.Path] = e
	}
	for _, p := range wantPaths {
		_, ok := byPath[p]
		assert.Truef(t, ok, "DescribeProjects: project %q missing from output", p)
	}
}

func TestDescribeTarget_FanOut(t *testing.T) {
	t.Parallel()
	// A bare target ":build" should fan out to every project.
	ws := newWorkspace(t)
	out, err := ws.DescribeTarget(types.Target{Name: "build"})
	require.NoError(t, err, "DescribeTarget")
	assert.NotEmpty(t, out.Definition, "DescribeTarget: Definition is empty")
	wantProjects := []string{".", "api", "extensions/drape", "extensions/lattice", "web/studio"}
	assert.Equal(t, len(wantProjects), out.Count, "DescribeTarget: Count")
	byProject := make(map[string]types.EvaluatedTargetEntry, len(out.Targets))
	for _, e := range out.Targets {
		byProject[e.Project] = e
	}
	for _, p := range wantProjects {
		e, ok := byProject[p]
		require.Truef(t, ok, "DescribeTarget: project %q missing from output", p)
		assert.Equalf(t, "build", e.Target, "DescribeTarget: project %q target", p)
		assert.NotEmptyf(t, e.Dir, "DescribeTarget: project %q Dir is empty", p)
	}
}

func TestDescribeTarget_SingleProject(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	out, err := ws.DescribeTarget(types.Target{Path: "api", Name: "test"})
	require.NoError(t, err, "DescribeTarget")
	require.Equal(t, 1, out.Count, "DescribeTarget: Count")
	e := out.Targets[0]
	assert.Equal(t, "api", e.Project, "DescribeTarget: Project")
	assert.Equal(t, "test", e.Target, "DescribeTarget: Target")
}

func TestDescribeTarget_UnknownProject(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	_, err := ws.DescribeTarget(types.Target{Path: "does-not-exist", Name: "build"})
	assert.Error(t, err, "DescribeTarget: expected error for unknown project")
}

func TestDescribeTarget_WithSpellAndPolicy(t *testing.T) {
	// Not parallel: mutates global spell registry.
	const spellName = "zzz-dt-spell"
	spell := types.NewSpell(
		spellName,
		types.WithTargets("my-target"),
		types.WithSources("**/*.zzz"),
		types.WithClaims("**/*.zzz"),
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

	out, err := ws.DescribeTarget(types.Target{Name: "my-target"})
	require.NoError(t, err, "DescribeTarget")
	require.NotZero(t, out.Count, "DescribeTarget: Count == 0")
	e := out.Targets[0]

	// Spell entry must be present.
	require.NotEmpty(t, e.Spells, "DescribeTarget: Spells is empty, expected at least one entry")
	assert.Equal(t, spellName, e.Spells[0].Name, "DescribeTarget: Spells[0].Name")

	// EffectiveClaims must be non-empty (spell declared claims).
	assert.NotEmpty(t, e.Spells[0].EffectiveClaims, "DescribeTarget: Spells[0].EffectiveClaims is empty, expected \"**/*.zzz\"")

	// Policy must be present with the volatility-retry flag set.
	require.NotNil(t, e.Policy, "DescribeTarget: Policy is nil, want TrackVolatile=true")
	assert.True(t, e.Policy.RetryOnVolatile, "DescribeTarget: Policy.RetryOnVolatile = false, want true")
}

func TestDescribeEvaluatedProjects_Shape(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	out := ws.DescribeEvaluatedProjects()

	assert.NotEmpty(t, out.Definition, "DescribeEvaluatedProjects: Definition is empty")
	wantPaths := []string{".", "api", "extensions/drape", "extensions/lattice", "web/studio"}
	assert.Equal(t, len(wantPaths), out.Count, "DescribeEvaluatedProjects: Count")
	assert.Equal(t, ws.Root(), out.Workspace, "DescribeEvaluatedProjects: Workspace")
	byPath := make(map[string]types.EvaluatedProjectEntry, len(out.Projects))
	for _, e := range out.Projects {
		byPath[e.Path] = e
	}
	for _, p := range wantPaths {
		_, ok := byPath[p]
		assert.Truef(t, ok, "DescribeEvaluatedProjects: project %q missing from output", p)
	}
}

func TestDescribeEvaluatedProjects_WorkspaceRootedSources(t *testing.T) {
	// Not parallel: mutates global spell registry.
	const spellName = "zzz-ep-spell"
	spell := types.NewSpell(spellName, types.WithSources("**/*.ep"))
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

	out := ws.DescribeEvaluatedProjects()

	var apiEntry *types.EvaluatedProjectEntry
	for i := range out.Projects {
		if out.Projects[i].Path == "api" {
			apiEntry = &out.Projects[i]
			break
		}
	}
	require.NotNil(t, apiEntry, "DescribeEvaluatedProjects: \"api\" project missing from output")

	// Sources must be workspace-rooted ("api/**/*.ep"), not project-relative.
	assert.NotContains(t, apiEntry.Sources, "**/*.ep",
		"DescribeEvaluatedProjects: Sources contains project-relative glob, want workspace-rooted \"api/**/*.ep\"")
	assert.Contains(t, apiEntry.Sources, "api/**/*.ep",
		"DescribeEvaluatedProjects: expected \"api/**/*.ep\" in Sources")
}

func TestDescribeWorkspaces_SingleWorkspace(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	cfg := types.WorkspaceConfig{CacheDir: "/tmp/cache-test", Concurrency: 4}
	out := ws.DescribeWorkspaces(cfg)

	assert.Equal(t, 1, out.Count, "DescribeWorkspaces: Count")
	require.Len(t, out.Workspaces, 1, "DescribeWorkspaces: len(Workspaces)")
	entry := out.Workspaces[0]
	assert.Equal(t, ws.Root(), entry.Root, "Root")
	assert.Equal(t, cfg.CacheDir, entry.CacheDir, "CacheDir")
	assert.Equal(t, cfg.Concurrency, entry.Concurrency, "Concurrency")
	assert.NotZero(t, entry.ProjectCount, "ProjectCount = 0, want > 0")
	assert.NotEmpty(t, out.Definition, "Definition is empty")
}

// TestDescribeFiles_Classification covers the roles end to end: a declared
// output, a declared source, an unclaimed path, and nested-project ownership.
// Globs come from registered spells, the same channel real projects declare
// them through.
func TestDescribeFiles_Classification(t *testing.T) {
	// Not parallel: mutates the global spell registry.
	const rootSpell, webSpell = "zzz-df-root", "zzz-df-web"
	project.DefaultSpellRegistry().RegisterSpell(
		types.NewSpell(rootSpell, types.WithSources("docs/**/*.md"), types.WithSpellOutputs("GEN.md", "gen/**")))
	project.DefaultSpellRegistry().RegisterSpell(
		types.NewSpell(webSpell, types.WithSources("**/*.ts"), types.WithSpellOutputs("dist/**")))
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

	out := ws.DescribeFiles([]string{"GEN.md", "docs/guide.md", "web/dist/app.js", "web/app.ts", "scratch.tmp", "./web/magusfile.buzz"})
	require.Equal(t, 6, out.Count)
	require.NotEmpty(t, out.Definition)
	byPath := map[string]types.FileEntry{}
	for _, f := range out.Files {
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

	// A ./ prefix normalizes away; magusfiles always count as sources.
	assert.Equal(t, "source", byPath["web/magusfile.buzz"].Role)
}
