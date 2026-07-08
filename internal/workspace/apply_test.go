package workspace

import (
	"testing"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWorkspace is a minimal WorkspaceRepository: Apply only reads Root, All,
// and Get, so the embedded interface stays nil and any other method would panic
// (a signal that Apply's surface widened).
type fakeWorkspace struct {
	types.WorkspaceRepository
	root     string
	projects map[string]*types.Project
}

func newFakeWorkspace(root string, projects ...*types.Project) *fakeWorkspace {
	m := make(map[string]*types.Project, len(projects))
	for _, p := range projects {
		m[p.Path] = p
	}
	return &fakeWorkspace{root: root, projects: m}
}

func (f *fakeWorkspace) Root() string { return f.root }

func (f *fakeWorkspace) Get(path string) *types.Project { return f.projects[path] }

func (f *fakeWorkspace) All() []*types.Project {
	out := make([]*types.Project, 0, len(f.projects))
	for _, p := range f.projects {
		out = append(out, p)
	}
	return out
}

func TestApply_AppliesRegisteredOptions(t *testing.T) {
	p := &types.Project{Path: "api"}
	w := newFakeWorkspace("/repo", p)

	r := NewWorkspaceRegistry()
	r.RegisterProject("api", WithOutputs("dist/**"), WithExclusive())

	require.NoError(t, r.Apply(w))
	assert.Equal(t, []string{"dist/**"}, p.Outputs)
	assert.True(t, p.Exclusive)
}

func TestApply_UnknownProjectErrorsWithHint(t *testing.T) {
	w := newFakeWorkspace("/repo",
		&types.Project{Path: "api"},
		&types.Project{Path: "web"},
	)

	r := NewWorkspaceRegistry()
	r.RegisterProject("frontend", WithExclusive())

	err := r.Apply(w)
	require.Error(t, err)
	assert.ErrorIs(t, err, types.ErrUnknownProject)
	// The hint lists the known projects, sorted, so the caller sees the spelling.
	assert.Contains(t, err.Error(), "known projects: api, web")
	assert.Contains(t, err.Error(), "omit the path")
}

func TestApply_PropagatesOptionError(t *testing.T) {
	p := &types.Project{Path: "api"}
	w := newFakeWorkspace("/repo", p)

	r := NewWorkspaceRegistry()
	// An invalid watch-ignore regex makes the option return an error.
	r.RegisterProject("api", WithWatchIgnore(IgnoreRegex("(")))

	err := r.Apply(w)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WithWatchIgnore")
}

func TestApply_ResolvesSpellsAndUnionsDeps(t *testing.T) {
	const spellName = "workspace_apply_test_spell"
	project.DefaultSpellRegistry().RegisterSpell(types.NewSpell(
		spellName,
		types.WithSpellDependsOn(func(string) []string { return []string{"shared/lib"} }),
	))
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	p := &types.Project{Path: "api", Dir: "/repo/api", Spells: []string{spellName}, DependsOn: []string{"other"}}
	w := newFakeWorkspace("/repo", p)

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
	w := newFakeWorkspace("/repo", p)

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
	w := newFakeWorkspace("/repo", p)
	require.NoError(t, NewWorkspaceRegistry().Apply(w))
	assert.Empty(t, p.Outputs)
	assert.Nil(t, p.ResolvedSpells)
}

func TestRemoteBackend_RoundTrip(t *testing.T) {
	r := NewWorkspaceRegistry()
	assert.Empty(t, r.RemoteBackend())

	r.SetRemoteBackend("s3-cache")
	assert.Equal(t, "s3-cache", r.RemoteBackend())

	// Last writer wins.
	r.SetRemoteBackend("gcs-cache")
	assert.Equal(t, "gcs-cache", r.RemoteBackend())
}
