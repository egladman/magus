package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWorkspaceRegistry(t *testing.T) {
	r := NewWorkspaceRegistry()
	require.NotNil(t, r)
}

// wiringWorkspace is a workspace value a test binds, comparable like the *magus.Magus a
// load binds.
type wiringWorkspace struct {
	types.WorkspaceReader
	name string
}

// unkeyableWorkspace holds a slice, so no sync.Map can key it.
type unkeyableWorkspace struct {
	types.WorkspaceReader
	ids []string
}

// Unknown wiring is an error, never an empty list, and a workspace whose magusfile wired
// nothing reads as wired-but-empty, distinct from it.
func TestWiredHarnessesTellsUnknownFromNone(t *testing.T) {
	unbound := &wiringWorkspace{name: "unbound"}
	_, err := WiredHarnesses(unbound)
	require.ErrorIs(t, err, ErrNoHarnessWiring)
	_, err = WiredHarnesses(nil)
	require.ErrorIs(t, err, ErrNoHarnessWiring)
	_, err = WiredHarnesses(unkeyableWorkspace{ids: []string{"x"}})
	require.ErrorIs(t, err, ErrNoHarnessWiring, "a value no load could have bound is unknown, not a panic")

	none := &wiringWorkspace{name: "none"}
	BindRegistry(none, NewWorkspaceRegistry())
	t.Cleanup(func() { UnbindRegistry(none) })
	ids, err := WiredHarnesses(none)
	require.NoError(t, err)
	assert.Equal(t, []string{}, ids, "wired, and empty")

	wired := &wiringWorkspace{name: "wired"}
	reg := NewWorkspaceRegistry()
	BindRegistry(wired, reg)
	reg.AddHarness("codex")
	reg.AddHarness("claude-code")
	ids, err = WiredHarnesses(wired)
	require.NoError(t, err)
	assert.Equal(t, []string{"codex", "claude-code"}, ids, "read live, in wiring order")

	UnbindRegistry(wired)
	_, err = WiredHarnesses(wired)
	require.ErrorIs(t, err, ErrNoHarnessWiring, "a closed workspace is unbound")
}

func TestContextWithRegistry_RoundTrip(t *testing.T) {
	r := NewWorkspaceRegistry()
	ctx := ContextWithRegistry(context.Background(), r)
	got := WorkspaceRegistryFromContext(ctx)
	assert.Same(t, r, got)
}

func TestWorkspaceRegistryFromContext_MissingReturnsNil(t *testing.T) {
	got := WorkspaceRegistryFromContext(context.Background())
	assert.Nil(t, got)
}

// A re-run of the same magusfile replaces the rule rather than stacking a second one.
func TestWorkspaceRegistry_SpawnRuleReplaces(t *testing.T) {
	r := NewWorkspaceRegistry()
	assert.Nil(t, r.SpawnRule())

	verdict := func(reason string) SpawnRule {
		return func(context.Context, types.SpawnRequest, hint.Gate) (types.GuardVerdict, error) {
			return types.GuardVerdict{Decision: types.GuardDeny, Reason: reason}, nil
		}
	}
	r.SetSpawnRule(verdict("first"))
	r.SetSpawnRule(verdict("second"))
	got, err := r.SpawnRule()(context.Background(), types.SpawnRequest{}, hint.Gate{})
	require.NoError(t, err)
	assert.Equal(t, types.GuardVerdict{Decision: types.GuardDeny, Reason: "second"}, got)
}

func TestWorkspaceRegistry_WriteRuleReplaces(t *testing.T) {
	r := NewWorkspaceRegistry()
	assert.Nil(t, r.WriteRule())

	verdict := func(reason string) WriteRule {
		return func(context.Context, types.WriteRequest, hint.Gate) (types.GuardVerdict, error) {
			return types.GuardVerdict{Decision: types.GuardDeny, Reason: reason}, nil
		}
	}
	r.SetWriteRule(verdict("first"))
	r.SetWriteRule(verdict("second"))
	got, err := r.WriteRule()(context.Background(), types.WriteRequest{}, hint.Gate{})
	require.NoError(t, err)
	assert.Equal(t, types.GuardVerdict{Decision: types.GuardDeny, Reason: "second"}, got)
}

func TestWorkspaceRegistry_CommandRuleReplaces(t *testing.T) {
	r := NewWorkspaceRegistry()
	assert.Nil(t, r.CommandRule())

	verdict := func(reason string) CommandRule {
		return func(context.Context, types.CommandRequest, hint.Gate) (types.GuardVerdict, error) {
			return types.GuardVerdict{Decision: types.GuardAdvise, Reason: reason}, nil
		}
	}
	r.SetCommandRule(verdict("first"))
	r.SetCommandRule(verdict("second"))
	got, err := r.CommandRule()(context.Background(), types.CommandRequest{}, hint.Gate{})
	require.NoError(t, err)
	assert.Equal(t, types.GuardVerdict{Decision: types.GuardAdvise, Reason: "second"}, got)
}

func TestWorkspaceRegistry_RegisterProject_ProjectPaths(t *testing.T) {
	r := NewWorkspaceRegistry()
	r.RegisterProject("api")
	r.RegisterProject("web")
	r.RegisterProject("cmd/tool")

	assert.Equal(t, []string{"api", "cmd/tool", "web"}, r.ProjectPaths())
}

func TestWorkspaceRegistry_ProjectPaths_Empty(t *testing.T) {
	r := NewWorkspaceRegistry()
	assert.Empty(t, r.ProjectPaths())
}

func TestWorkspaceRegistry_RegisterProject_AccumulatesOptions(t *testing.T) {
	r := NewWorkspaceRegistry()
	r.RegisterProject("api", WithOutputs("dist/api"))
	r.RegisterProject("api", WithOutputs("dist/api-extra"))

	// Both registrations should be stored (two calls to RegisterProject same path).
	// We verify via ProjectPaths that "api" appears exactly once.
	assert.Equal(t, []string{"api"}, r.ProjectPaths())
}

// rootedWorkspace is a minimal WorkspaceRepository: Apply only reads Root, All,
// and Get, so the embedded interface stays nil and any other method would panic
// (a signal that Apply started calling more methods).
type rootedWorkspace struct {
	types.WorkspaceRepository
	root     string
	projects map[string]*types.Project
}

func newRootedWorkspace(root string, projects ...*types.Project) *rootedWorkspace {
	m := make(map[string]*types.Project, len(projects))
	for _, p := range projects {
		m[p.Path] = p
	}
	return &rootedWorkspace{root: root, projects: m}
}

func (f *rootedWorkspace) Root() string { return f.root }

func (f *rootedWorkspace) Get(path string) *types.Project { return f.projects[path] }

func (f *rootedWorkspace) All() []*types.Project {
	out := make([]*types.Project, 0, len(f.projects))
	for _, p := range f.projects {
		out = append(out, p)
	}
	return out
}

func TestApply_AppliesRegisteredOptions(t *testing.T) {
	p := &types.Project{Path: "api"}
	w := newRootedWorkspace("/repo", p)

	r := NewWorkspaceRegistry()
	r.RegisterProject("api", WithOutputs("dist/**"), WithName("API"))

	require.NoError(t, r.Apply(w))
	assert.Equal(t, &types.Project{Path: "api", Name: "API", Outputs: types.MustParseGlobs("dist/**")}, p)
}

func TestApply_UnknownProjectErrorsWithHint(t *testing.T) {
	w := newRootedWorkspace("/repo",
		&types.Project{Path: "api"},
		&types.Project{Path: "web"},
	)

	r := NewWorkspaceRegistry()
	r.RegisterProject("frontend", WithOutputs("dist/**"))

	err := r.Apply(w)
	require.Error(t, err)
	assert.ErrorIs(t, err, types.ErrUnknownProject)
	// The hint lists the known projects, sorted, so the caller sees the spelling.
	assert.Contains(t, err.Error(), "known projects: api, web")
	assert.Contains(t, err.Error(), "omit the path")
}

func TestApply_PropagatesOptionError(t *testing.T) {
	p := &types.Project{Path: "api"}
	w := newRootedWorkspace("/repo", p)

	r := NewWorkspaceRegistry()
	// An invalid watch-ignore regex makes the option return an error.
	r.RegisterProject("api", WithWatchIgnore(IgnoreRegex("(")))

	err := r.Apply(w)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WithWatchIgnore")
}

func TestApply_ResolvesSpellsAndUnionsDeps(t *testing.T) {
	const spellName = "workspace_apply_test_spell"
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(
		spellName,
		spells.WithDependsOn(func(string) []string { return []string{"shared/lib"} }),
	))
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	p := &types.Project{Path: "api", Dir: "/repo/api", Spells: []string{spellName}, DependsOn: []string{"other"}}
	w := newRootedWorkspace("/repo", p)

	r := NewWorkspaceRegistry()
	r.RegisterProject("api") // no opts; spell resolution runs over all projects

	require.NoError(t, r.Apply(w))
	require.Len(t, p.ResolvedSpells, 1)
	assert.Equal(t, spellName, p.ResolvedSpells[0].Name())
	// Spell-declared deps are unioned in, sorted, and de-duplicated.
	assert.Equal(t, []string{"other", "shared/lib"}, p.DependsOn)
}

func TestApply_UnknownSpellErrorsAndLeavesResolvedNil(t *testing.T) {
	p := &types.Project{Path: "api", Spells: []string{"no_such_spell"}}
	w := newRootedWorkspace("/repo", p)

	r := NewWorkspaceRegistry()
	r.RegisterProject("api")

	err := r.Apply(w)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `spell "no_such_spell" not registered`)
	// Index-alignment invariant: a missing spell leaves the resolved view absent
	// rather than shorter than Spells.
	assert.Nil(t, p.ResolvedSpells)
}

func TestApply_EmptyRegistryIsNoop(t *testing.T) {
	p := &types.Project{Path: "api"}
	w := newRootedWorkspace("/repo", p)
	require.NoError(t, NewWorkspaceRegistry().Apply(w))
	assert.Empty(t, p.Outputs)
	assert.Nil(t, p.ResolvedSpells)
}

func TestRemoteBackend_RoundTrip(t *testing.T) {
	r := NewWorkspaceRegistry()
	assert.Empty(t, r.RemoteBackend())

	r.SetRemoteBackend("aws-s3")
	assert.Equal(t, "aws-s3", r.RemoteBackend())

	// Last writer wins.
	r.SetRemoteBackend("gcs-cache")
	assert.Equal(t, "gcs-cache", r.RemoteBackend())
}

func testModelTierSet(t *testing.T, enum string) *types.ModelTierSet {
	t.Helper()
	set, err := types.NewModelTierSet(types.ModelTierDeclaration{
		Enum:    enum,
		Site:    "magusfile.buzz:1",
		Cases:   []string{"low", "high"},
		Default: 0,
		Bounds:  make([]types.ModelTierBounds, 2),
		Skills:  []types.SkillVariant{types.SkillVariantFull, types.SkillVariantShort},
	})
	require.NoError(t, err)
	return set
}

// The default builds on first ask and once; a declaration, or turning tiers off, wins over it.
func TestWorkspaceRegistry_ModelTiers(t *testing.T) {
	t.Run("no declaration and no default is nil", func(t *testing.T) {
		assert.Nil(t, NewWorkspaceRegistry().ModelTiers(t.Context()))
	})

	t.Run("the default builds once, on the first ask", func(t *testing.T) {
		r := NewWorkspaceRegistry()
		builds := 0
		shipped := testModelTierSet(t, "Shipped")
		r.SetDefaultModelTiers(func() (*types.ModelTierSet, error) { builds++; return shipped, nil })
		assert.Zero(t, builds, "registering the default builds nothing")
		assert.Same(t, shipped, r.ModelTiers(t.Context()))
		assert.Same(t, shipped, r.ModelTiers(t.Context()))
		assert.Equal(t, 1, builds)
	})

	t.Run("a declaration replaces the default and a re-run replaces the declaration", func(t *testing.T) {
		r := NewWorkspaceRegistry()
		r.SetDefaultModelTiers(func() (*types.ModelTierSet, error) { return testModelTierSet(t, "Shipped"), nil })
		first, second := testModelTierSet(t, "First"), testModelTierSet(t, "Second")
		r.SetModelTiers(first)
		assert.Same(t, first, r.ModelTiers(t.Context()))
		r.SetModelTiers(second)
		assert.Same(t, second, r.ModelTiers(t.Context()))
	})

	t.Run("a declaration of nil turns tiers off", func(t *testing.T) {
		r := NewWorkspaceRegistry()
		r.SetDefaultModelTiers(func() (*types.ModelTierSet, error) {
			return nil, errors.New("the default must not build when tiers are off")
		})
		r.SetModelTiers(nil)
		assert.Nil(t, r.ModelTiers(t.Context()))
	})

	t.Run("a default that fails to build answers nil", func(t *testing.T) {
		r := NewWorkspaceRegistry()
		r.SetDefaultModelTiers(func() (*types.ModelTierSet, error) { return nil, errors.New("broken") })
		assert.Nil(t, r.ModelTiers(t.Context()))
	})
}

func TestWorkspaceRegistry_ModelTierMatrixReplaces(t *testing.T) {
	r := NewWorkspaceRegistry()
	assert.Nil(t, r.ModelTierMatrix())

	matrix := func(reason string) types.ModelTierMatrix {
		return func(context.Context, types.ModelTierRequest) (*types.JobModelTier, error) {
			return &types.JobModelTier{Reason: reason}, nil
		}
	}
	r.SetModelTierMatrix(matrix("first"))
	r.SetModelTierMatrix(matrix("second"))
	got, err := r.ModelTierMatrix()(context.Background(), types.ModelTierRequest{})
	require.NoError(t, err)
	assert.Equal(t, &types.JobModelTier{Reason: "second"}, got)
}

func TestRegistryProfile(t *testing.T) {
	r := NewWorkspaceRegistry()
	assert.Empty(t, r.Profile(), "nothing declared is unset, which the guard reads as light")
	r.SetProfile(types.GuardProfileStrict)
	assert.Equal(t, types.GuardProfileStrict, r.Profile())
}
