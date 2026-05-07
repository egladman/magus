package magus

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// newWorkspaceCustom creates a single-project workspace at a temp dir and
// returns it after applying opts. The root project has an empty magusfile.tl.
func newWorkspaceCustom(t *testing.T, opts ...Option) types.WorkspaceRepository {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "magusfile.tl"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := Inspect(context.Background(), root, opts...)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
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

	if out.Definition == "" {
		t.Error("DescribeSpells: Definition is empty")
	}
	if out.Count == 0 {
		t.Error("DescribeSpells: Count == 0, expected at least the test spell")
	}
	if len(out.Spells) != out.Count {
		t.Errorf("DescribeSpells: len(Spells)=%d != Count=%d", len(out.Spells), out.Count)
	}

	// Entries must be sorted by name.
	for i := 1; i < len(out.Spells); i++ {
		if out.Spells[i].Name < out.Spells[i-1].Name {
			t.Errorf("DescribeSpells: Spells not sorted at [%d]=%q, [%d]=%q",
				i-1, out.Spells[i-1].Name, i, out.Spells[i].Name)
		}
	}

	// The test spell must appear (zzz-* sorts last).
	if last := out.Spells[len(out.Spells)-1]; last.Name != name {
		t.Errorf("DescribeSpells: expected %q as last entry (zzz-prefix sorts last), got %q", name, last.Name)
	}
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

	if out.Count == 0 {
		t.Fatal("DescribeTargets: Count == 0")
	}
	if out.Targets[0].Name != "ci" {
		t.Errorf("DescribeTargets: first entry = %q, want \"ci\"", out.Targets[0].Name)
	}
	if out.Targets[0].Kind != "canonical" {
		t.Errorf("DescribeTargets: ci.Kind = %q, want \"canonical\"", out.Targets[0].Kind)
	}

	byName := make(map[string]types.TargetEntry, len(out.Targets))
	for _, e := range out.Targets {
		byName[e.Name] = e
	}
	for _, target := range []string{"zzz-target-a", "zzz-target-b"} {
		e, ok := byName[target]
		if !ok {
			t.Errorf("DescribeTargets: expected spell target %q in output", target)
			continue
		}
		if e.Kind != "spell" {
			t.Errorf("DescribeTargets: %q.Kind = %q, want \"spell\"", target, e.Kind)
		}
		if !slices.Contains(e.Spells, spellName) {
			t.Errorf("DescribeTargets: %q.Spells = %v, want to contain %q", target, e.Spells, spellName)
		}
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
	if err != nil {
		t.Fatalf("DescribeTarget: %v", err)
	}
	var got []string
	for _, e := range out.Targets {
		if e.Target == "lint" {
			got = e.Charms
		}
	}
	if want := []string{"debug", "write"}; !slices.Equal(got, want) {
		t.Errorf("DescribeTarget(lint).Charms = %v, want %v (sorted union across spells)", got, want)
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
	if !ok {
		t.Fatalf("DescribeTargets: custom target %q not found in output", customTarget)
	}
	if e.Kind != "custom" {
		t.Errorf("DescribeTargets: %q.Kind = %q, want \"custom\"", customTarget, e.Kind)
	}
	if !slices.Contains(e.Projects, ".") {
		t.Errorf("DescribeTargets: %q.Projects = %v, want to contain \".\"", customTarget, e.Projects)
	}
}

func TestDescribeProjects_Inventory(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	out := ws.DescribeProjects()

	if out.Definition == "" {
		t.Error("DescribeProjects: Definition is empty")
	}
	wantPaths := []string{".", "api", "extensions/drape", "extensions/lattice", "web/studio"}
	if out.Count != len(wantPaths) {
		t.Errorf("DescribeProjects: Count = %d, want %d", out.Count, len(wantPaths))
	}
	if out.Workspace != ws.Root() {
		t.Errorf("DescribeProjects: Workspace = %q, want %q", out.Workspace, ws.Root())
	}
	byPath := make(map[string]types.ProjectEntry, len(out.Projects))
	for _, e := range out.Projects {
		byPath[e.Path] = e
	}
	for _, p := range wantPaths {
		if _, ok := byPath[p]; !ok {
			t.Errorf("DescribeProjects: project %q missing from output", p)
		}
	}
}

func TestDescribeTarget_FanOut(t *testing.T) {
	t.Parallel()
	// A bare target ":build" should fan out to every project.
	ws := newWorkspace(t)
	out, err := ws.DescribeTarget(types.Target{Name: "build"})
	if err != nil {
		t.Fatalf("DescribeTarget: %v", err)
	}
	if out.Definition == "" {
		t.Error("DescribeTarget: Definition is empty")
	}
	wantProjects := []string{".", "api", "extensions/drape", "extensions/lattice", "web/studio"}
	if out.Count != len(wantProjects) {
		t.Errorf("DescribeTarget: Count = %d, want %d", out.Count, len(wantProjects))
	}
	byProject := make(map[string]types.EvaluatedTargetEntry, len(out.Targets))
	for _, e := range out.Targets {
		byProject[e.Project] = e
	}
	for _, p := range wantProjects {
		e, ok := byProject[p]
		if !ok {
			t.Errorf("DescribeTarget: project %q missing from output", p)
			continue
		}
		if e.Target != "build" {
			t.Errorf("DescribeTarget: project %q target = %q, want \"build\"", p, e.Target)
		}
		if e.Dir == "" {
			t.Errorf("DescribeTarget: project %q Dir is empty", p)
		}
	}
}

func TestDescribeTarget_SingleProject(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	out, err := ws.DescribeTarget(types.Target{Path: "api", Name: "test"})
	if err != nil {
		t.Fatalf("DescribeTarget: %v", err)
	}
	if out.Count != 1 {
		t.Fatalf("DescribeTarget: Count = %d, want 1", out.Count)
	}
	e := out.Targets[0]
	if e.Project != "api" {
		t.Errorf("DescribeTarget: Project = %q, want \"api\"", e.Project)
	}
	if e.Target != "test" {
		t.Errorf("DescribeTarget: Target = %q, want \"test\"", e.Target)
	}
}

func TestDescribeTarget_UnknownProject(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	_, err := ws.DescribeTarget(types.Target{Path: "does-not-exist", Name: "build"})
	if err == nil {
		t.Fatal("DescribeTarget: expected error for unknown project, got nil")
	}
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
		WithTarget("my-target", TrackFlake()),
	)
	ws := newWorkspaceCustom(t, WithWorkspaceRegistry(reg))

	out, err := ws.DescribeTarget(types.Target{Name: "my-target"})
	if err != nil {
		t.Fatalf("DescribeTarget: %v", err)
	}
	if out.Count == 0 {
		t.Fatal("DescribeTarget: Count == 0")
	}
	e := out.Targets[0]

	// Spell entry must be present.
	if len(e.Spells) == 0 {
		t.Fatal("DescribeTarget: Spells is empty, expected at least one entry")
	}
	if e.Spells[0].Name != spellName {
		t.Errorf("DescribeTarget: Spells[0].Name = %q, want %q", e.Spells[0].Name, spellName)
	}

	// EffectiveClaims must be non-empty (spell declared claims).
	if len(e.Spells[0].EffectiveClaims) == 0 {
		t.Error("DescribeTarget: Spells[0].EffectiveClaims is empty, expected \"**/*.zzz\"")
	}

	// Policy must reflect TrackFlake.
	if e.Policy == nil {
		t.Fatal("DescribeTarget: Policy is nil, want TrackFlake=true")
	}
	if !e.Policy.TrackFlake {
		t.Error("DescribeTarget: Policy.TrackFlake = false, want true")
	}
}

func TestDescribeEvaluatedProjects_Shape(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	out := ws.DescribeEvaluatedProjects()

	if out.Definition == "" {
		t.Error("DescribeEvaluatedProjects: Definition is empty")
	}
	wantPaths := []string{".", "api", "extensions/drape", "extensions/lattice", "web/studio"}
	if out.Count != len(wantPaths) {
		t.Errorf("DescribeEvaluatedProjects: Count = %d, want %d", out.Count, len(wantPaths))
	}
	if out.Workspace != ws.Root() {
		t.Errorf("DescribeEvaluatedProjects: Workspace = %q, want %q", out.Workspace, ws.Root())
	}
	byPath := make(map[string]types.EvaluatedProjectEntry, len(out.Projects))
	for _, e := range out.Projects {
		byPath[e.Path] = e
	}
	for _, p := range wantPaths {
		if _, ok := byPath[p]; !ok {
			t.Errorf("DescribeEvaluatedProjects: project %q missing from output", p)
		}
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
	for _, rel := range []string{"magusfile.tl", "api/magusfile.tl"} {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	reg := NewWorkspaceRegistry()
	reg.RegisterProject("api", WithSpell(spellName))
	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	out := ws.DescribeEvaluatedProjects()

	var apiEntry *types.EvaluatedProjectEntry
	for i := range out.Projects {
		if out.Projects[i].Path == "api" {
			apiEntry = &out.Projects[i]
			break
		}
	}
	if apiEntry == nil {
		t.Fatal("DescribeEvaluatedProjects: \"api\" project missing from output")
	}

	// Sources must be workspace-rooted ("api/**/*.ep"), not project-relative.
	for _, src := range apiEntry.Sources {
		if src == "**/*.ep" {
			t.Errorf("DescribeEvaluatedProjects: Sources contains project-relative %q, want workspace-rooted \"api/**/*.ep\"", src)
		}
		if src == "api/**/*.ep" {
			return // pass
		}
	}
	t.Errorf("DescribeEvaluatedProjects: expected \"api/**/*.ep\" in Sources, got %v", apiEntry.Sources)
}

func TestDescribeWorkspaces_SingleWorkspace(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	cfg := types.WorkspaceConfig{CacheDir: "/tmp/cache-test", Concurrency: 4}
	out := ws.DescribeWorkspaces(cfg)

	if out.Count != 1 {
		t.Errorf("DescribeWorkspaces: Count = %d, want 1", out.Count)
	}
	if len(out.Workspaces) != 1 {
		t.Fatalf("DescribeWorkspaces: len(Workspaces) = %d, want 1", len(out.Workspaces))
	}
	entry := out.Workspaces[0]
	if entry.Root != ws.Root() {
		t.Errorf("Root = %q, want %q", entry.Root, ws.Root())
	}
	if entry.CacheDir != cfg.CacheDir {
		t.Errorf("CacheDir = %q, want %q", entry.CacheDir, cfg.CacheDir)
	}
	if entry.Concurrency != cfg.Concurrency {
		t.Errorf("Concurrency = %d, want %d", entry.Concurrency, cfg.Concurrency)
	}
	if entry.ProjectCount == 0 {
		t.Error("ProjectCount = 0, want > 0")
	}
	if out.Definition == "" {
		t.Error("Definition is empty")
	}
}
