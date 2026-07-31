package magus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	graph, err := ws.DescribeGraph(context.Background())
	require.NoError(t, err, "DescribeGraph")
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

func TestDescribeSpells_ShapeAndOrder(t *testing.T) {
	// Not parallel: mutates global spell registry.
	const name = "zzz-describe-spells-test"
	spell := spells.NewSpell(name, spells.WithTargets("build", "test"))
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(name) })

	ws := newWorkspace(t)
	out, err := ws.DescribeSpells(context.Background())
	require.NoError(t, err, "DescribeSpells")

	require.NotEmpty(t, out, "DescribeSpells: expected at least the test spell")

	// Entries must be sorted by name.
	for i := 1; i < len(out); i++ {
		assert.LessOrEqualf(t, out[i-1].Name, out[i].Name,
			"DescribeSpells: Spells not sorted at [%d]=%q, [%d]=%q",
			i-1, out[i-1].Name, i, out[i].Name)
	}

	// The test spell must appear (zzz-* sorts last).
	require.NotEmpty(t, out)
	assert.Equal(t, name, out[len(out)-1].Name,
		"DescribeSpells: expected test spell as last entry (zzz-prefix sorts last)")
}

func TestDescribeTargets_CanonicalCIFirst(t *testing.T) {
	// Not parallel: mutates global spell registry.
	const spellName = "zzz-targets-spell"
	spell := spells.NewSpell(spellName, spells.WithTargets("zzz-target-a", "zzz-target-b"))
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	ws := newWorkspaceCustom(t, WithWorkspaceRegistry(reg))

	out, err := ws.DescribeTargets(context.Background())
	require.NoError(t, err, "DescribeTargets")

	require.NotEmpty(t, out, "DescribeTargets: no targets")
	assert.Equal(t, "ci", out[0].Name, "DescribeTargets: first entry")
	assert.Equal(t, "canonical", out[0].Kind, "DescribeTargets: ci.Kind")

	byName := make(map[string]types.TargetEntry, len(out))
	for _, e := range out {
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
	s := spells.NewSpell(spellName,
		spells.WithTargets("lint"),
		spells.WithTargetCharms(map[string][]string{"lint": {"write", "debug"}}),
	)
	project.DefaultSpellRegistry().RegisterSpell(s)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	ws := newWorkspaceCustom(t, WithWorkspaceRegistry(reg))

	out, err := ws.DescribeTarget(context.Background(), types.Target{Name: "lint"})
	require.NoError(t, err, "DescribeTarget")
	var got []string
	for _, e := range out {
		if e.Target == "lint" {
			got = e.Charms
		}
	}
	assert.Equal(t, []string{"debug", "write"}, got, "DescribeTarget(lint).Charms (sorted union across spells)")
}

func TestDescribeCharms_InverseIndex(t *testing.T) {
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
	ws := newWorkspaceCustom(t, WithWorkspaceRegistry(reg))

	charms, err := ws.DescribeCharms(context.Background(), []string{"write"})
	require.NoError(t, err, "DescribeCharms")

	byName := make(map[string]types.CharmEntry, len(charms))
	for _, c := range charms {
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
	for i := 1; i < len(charms); i++ {
		assert.LessOrEqualf(t, charms[i-1].Name, charms[i].Name,
			"DescribeCharms: not sorted at [%d]=%q,[%d]=%q", i-1, charms[i-1].Name, i, charms[i].Name)
	}
}

// TestDescribeTargets_CustomTargets exercises this package's own test build,
// which deliberately does not link the Buzz interpreter (see doc.go and
// register_test.go) — so validateTargetPolicies (A4) cannot see this project's
// magusfile at all and its "policy names an unknown target" enforcement does not
// apply here (see the interp.Available guard in load()). The end-to-end version
// of that check, exercised through a real magusfile via the linked interpreter,
// lives in cmd/magus (which blank-imports interp/bindings): see
// TestInspect_TargetPolicyNamingUnknownTarget in cmd/magus/project_options_test.go.
func TestDescribeTargets_CustomTargets(t *testing.T) {
	t.Parallel()
	const customTarget = "zzz-custom-target"
	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithTarget(customTarget))
	ws := newWorkspaceCustom(t, WithWorkspaceRegistry(reg))

	out, err := ws.DescribeTargets(context.Background())
	require.NoError(t, err, "DescribeTargets")

	byName := make(map[string]types.TargetEntry, len(out))
	for _, e := range out {
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
	out, err := ws.DescribeProjects(context.Background())
	require.NoError(t, err, "DescribeProjects")

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
	out, err := ws.DescribeTarget(context.Background(), types.Target{Name: "build"})
	require.NoError(t, err, "DescribeTarget")
	wantProjects := []string{".", "api", "extensions/drape", "extensions/lattice", "web/studio"}
	assert.Len(t, out, len(wantProjects), "DescribeTarget: one entry per project")
	byProject := make(map[string]types.EvaluatedTargetEntry, len(out))
	for _, e := range out {
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
	out, err := ws.DescribeTarget(context.Background(), types.Target{Path: "api", Name: "test"})
	require.NoError(t, err, "DescribeTarget")
	require.Len(t, out, 1, "DescribeTarget: one entry")
	e := out[0]
	assert.Equal(t, "api", e.Project, "DescribeTarget: Project")
	assert.Equal(t, "test", e.Target, "DescribeTarget: Target")
}

func TestDescribeTarget_UnknownProject(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	_, err := ws.DescribeTarget(context.Background(), types.Target{Path: "does-not-exist", Name: "build"})
	assert.Error(t, err, "DescribeTarget: expected error for unknown project")
}

func TestDescribeTarget_WithSpellAndPolicy(t *testing.T) {
	// Not parallel: mutates global spell registry.
	const spellName = "zzz-dt-spell"
	spell := spells.NewSpell(
		spellName,
		spells.WithTargets("my-target"),
		spells.WithSources("**/*.zzz"),
		spells.WithClaims("**/*.zzz"),
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

	out, err := ws.DescribeTarget(context.Background(), types.Target{Name: "my-target"})
	require.NoError(t, err, "DescribeTarget")
	require.NotEmpty(t, out, "DescribeTarget: no entries")
	e := out[0]

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
	out, err := ws.DescribeEvaluatedProjects(context.Background())
	require.NoError(t, err, "DescribeEvaluatedProjects")

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

	out, err := ws.DescribeEvaluatedProjects(context.Background())
	require.NoError(t, err, "DescribeEvaluatedProjects")

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
	out, err := ws.DescribeWorkspaces(context.Background(), cfg)
	require.NoError(t, err, "DescribeWorkspaces")

	require.Len(t, out, 1, "DescribeWorkspaces: one entry")
	entry := out[0]
	assert.Equal(t, ws.Root(), entry.Root, "Root")
	assert.Equal(t, cfg.CacheDir, entry.CacheDir, "CacheDir")
	assert.Equal(t, cfg.Concurrency, entry.Concurrency, "Concurrency")
	assert.NotZero(t, entry.ProjectCount, "ProjectCount = 0, want > 0")
}

// TestDescribeFiles_Classification covers the roles end to end: a declared
// output, a declared source, an unclaimed path, and nested-project ownership.
// Globs come from registered spells, the same channel real projects declare
// them through.
func TestDescribeFiles_Classification(t *testing.T) {
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

	out, err := ws.DescribeFiles(context.Background(), []string{"GEN.md", "docs/guide.md", "web/dist/app.js", "web/app.ts", "scratch.tmp", "./web/magusfile.buzz"})
	require.NoError(t, err, "DescribeFiles")
	require.Len(t, out, 6)
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

	// A ./ prefix normalizes away; magusfiles always count as sources.
	assert.Equal(t, "source", byPath["web/magusfile.buzz"].Role)
}

// TestDescribeMethods_HonorCancelledContext pins that a context cancelled BEFORE a
// Describe* method's outer walk begins is REPORTED, not silently swallowed: the
// method returns a non-nil error satisfying errors.Is(err, context.Canceled)
// together with the zero value of its result type. A truncated-but-returned result
// (e.g. Count: 3 out of 50 projects) is indistinguishable from a genuinely small
// workspace, which is the exact bug describeCancelled exists to prevent - so the
// cancelled case here asserts BOTH halves: the error, and that nothing rides along
// with it. Table-driven over all nine methods.
//
// DescribeCharms and DescribeTargets build one unconditional entry (the reserved
// charms; the canonical "ci" target) before their per-project walk starts, so their
// cases use a dedicated spell+target fixture: the live control proves the per-project
// walk actually ran (the fixture's charm/target is present), which the cancelled case
// then proves never happens (the whole result, baseline included, is the zero value).
//
// Each case also runs the same call against a live context as a positive control:
// without it, a method broken to unconditionally error would pass the cancelled case
// for the wrong reason.
func TestDescribeMethods_HonorCancelledContext(t *testing.T) {
	// Not parallel: mutates the global spell registry.
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	liveCtx := context.Background()

	ws := newWorkspace(t)

	// DescribeCharms/DescribeTargets need a project that actually USES a declared
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

	hasCharm := func(entries []types.CharmEntry, name string) bool {
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
			name: "DescribeSpells",
			cancelled: func(t *testing.T) {
				out, err := ws.DescribeSpells(cancelledCtx)
				require.Error(t, err, "DescribeSpells")
				assert.ErrorIs(t, err, context.Canceled, "DescribeSpells error")
				assert.Nil(t, out, "DescribeSpells entries")
			},
			live: func(t *testing.T) {
				out, err := ws.DescribeSpells(liveCtx)
				require.NoError(t, err, "DescribeSpells")
				assert.NotEmpty(t, out)
			},
		},
		{
			name: "DescribeCharms",
			cancelled: func(t *testing.T) {
				out, err := spellWs.DescribeCharms(cancelledCtx, nil)
				require.Error(t, err, "DescribeCharms")
				assert.ErrorIs(t, err, context.Canceled, "DescribeCharms error")
				assert.Nil(t, out, "DescribeCharms entries")
			},
			live: func(t *testing.T) {
				out, err := spellWs.DescribeCharms(liveCtx, nil)
				require.NoError(t, err, "DescribeCharms")
				assert.True(t, hasCharm(out, customCharm), "declared charm missing from a live walk")
			},
		},
		{
			name: "DescribeTargets",
			cancelled: func(t *testing.T) {
				out, err := spellWs.DescribeTargets(cancelledCtx)
				require.Error(t, err, "DescribeTargets")
				assert.ErrorIs(t, err, context.Canceled, "DescribeTargets error")
				assert.Nil(t, out, "DescribeTargets entries")
			},
			live: func(t *testing.T) {
				out, err := spellWs.DescribeTargets(liveCtx)
				require.NoError(t, err, "DescribeTargets")
				assert.True(t, hasTarget(out, customTarget), "custom target missing from a live walk")
			},
		},
		{
			name: "DescribeProjects",
			cancelled: func(t *testing.T) {
				out, err := ws.DescribeProjects(cancelledCtx)
				require.Error(t, err, "DescribeProjects")
				assert.ErrorIs(t, err, context.Canceled, "DescribeProjects error")
				assert.Zero(t, out, "DescribeProjects result")
			},
			live: func(t *testing.T) {
				out, err := ws.DescribeProjects(liveCtx)
				require.NoError(t, err, "DescribeProjects")
				assert.NotEmpty(t, out.Projects, "Projects")
				assert.NotZero(t, out.Count, "Count")
			},
		},
		{
			name: "DescribeEvaluatedProjects",
			cancelled: func(t *testing.T) {
				out, err := ws.DescribeEvaluatedProjects(cancelledCtx)
				require.Error(t, err, "DescribeEvaluatedProjects")
				assert.ErrorIs(t, err, context.Canceled, "DescribeEvaluatedProjects error")
				assert.Zero(t, out, "DescribeEvaluatedProjects result")
			},
			live: func(t *testing.T) {
				out, err := ws.DescribeEvaluatedProjects(liveCtx)
				require.NoError(t, err, "DescribeEvaluatedProjects")
				assert.NotEmpty(t, out.Projects, "Projects")
				assert.NotZero(t, out.Count, "Count")
			},
		},
		{
			name: "DescribeFiles",
			cancelled: func(t *testing.T) {
				out, err := ws.DescribeFiles(cancelledCtx, []string{"magusfile.buzz", "api/magusfile.buzz"})
				require.Error(t, err, "DescribeFiles")
				assert.ErrorIs(t, err, context.Canceled, "DescribeFiles error")
				assert.Nil(t, out, "DescribeFiles entries")
			},
			live: func(t *testing.T) {
				out, err := ws.DescribeFiles(liveCtx, []string{"magusfile.buzz", "api/magusfile.buzz"})
				require.NoError(t, err, "DescribeFiles")
				assert.Len(t, out, 2)
			},
		},
		{
			name: "DescribeGraph",
			cancelled: func(t *testing.T) {
				out, err := ws.DescribeGraph(cancelledCtx)
				require.Error(t, err, "DescribeGraph")
				assert.ErrorIs(t, err, context.Canceled, "DescribeGraph error")
				assert.Zero(t, out, "DescribeGraph result")
			},
			live: func(t *testing.T) {
				out, err := ws.DescribeGraph(liveCtx)
				require.NoError(t, err, "DescribeGraph")
				assert.NotEmpty(t, out.Projects)
			},
		},
		{
			name: "DescribeTarget",
			cancelled: func(t *testing.T) {
				out, err := ws.DescribeTarget(cancelledCtx, types.Target{Name: "build"})
				require.Error(t, err, "DescribeTarget")
				assert.ErrorIs(t, err, context.Canceled, "DescribeTarget error")
				assert.Nil(t, out, "DescribeTarget entries")
			},
			live: func(t *testing.T) {
				out, err := ws.DescribeTarget(liveCtx, types.Target{Name: "build"})
				require.NoError(t, err, "DescribeTarget")
				assert.NotEmpty(t, out)
			},
		},
		{
			name: "DescribeWorkspaces",
			cancelled: func(t *testing.T) {
				cfg := types.WorkspaceConfig{CacheDir: "/tmp/cache-test", Concurrency: 4}
				out, err := ws.DescribeWorkspaces(cancelledCtx, cfg)
				require.Error(t, err, "DescribeWorkspaces")
				assert.ErrorIs(t, err, context.Canceled, "DescribeWorkspaces error")
				assert.Nil(t, out, "DescribeWorkspaces entries")
			},
			live: func(t *testing.T) {
				cfg := types.WorkspaceConfig{CacheDir: "/tmp/cache-test", Concurrency: 4}
				out, err := ws.DescribeWorkspaces(liveCtx, cfg)
				require.NoError(t, err, "DescribeWorkspaces")
				assert.Len(t, out, 1)
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

// TestDescribeTarget_ReportsPerTargetOutputs pins that a target's description
// carries the target's OWN declared outputs. It read the project-wide globs
// before, so a target whose outputs come from ctx.outputs described itself as
// producing nothing - the described plan disagreed with what the cache keys and
// snapshots, which is the one thing this command exists to show.
func TestDescribeTarget_ReportsPerTargetOutputs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const mf = `export fun generate(ctx: magus\Context, args: [str]) > void {
    ctx.outputs("GEN.md");
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(mf), 0o644))

	m, err := Open(context.Background(), root)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	out, err := m.DescribeTarget(context.Background(), types.Target{Name: "generate"})
	require.NoError(t, err, "DescribeTarget")
	require.Len(t, out, 1)
	assert.Equal(t, []string{"GEN.md"}, out[0].Outputs,
		"a per-target ctx.outputs glob belongs in that target's own description")
}

// TestDescribeFiles_PerTargetOutputs pins the classification against the whole
// declared set, not the project-wide globs alone. A file declared only by a
// per-target ctx.outputs is generated by every other consumer's reckoning (clean
// removes it, the merge driver regenerates it), so reporting it as a source told
// an agent it was safe to hand-edit the one file it must never hand-edit. This
// repo's own root MAGUS.md is declared exactly this way.
func TestDescribeFiles_PerTargetOutputs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const mf = `export fun generate(ctx: magus\Context, args: [str]) > void {
    ctx.outputs("GEN.md");
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(mf), 0o644))

	m, err := Open(context.Background(), root)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	out, err := m.DescribeFiles(context.Background(), []string{"GEN.md"})
	require.NoError(t, err, "DescribeFiles")
	require.Len(t, out, 1)
	assert.Equal(t, types.FileEntry{
		Path:     "GEN.md",
		Project:  ".",
		Role:     "output",
		OutputOf: []string{"."},
		Hint:     out[0].Hint,
	}, out[0])
	assert.Contains(t, out[0].Hint, "generated")
}
