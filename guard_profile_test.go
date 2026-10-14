package magus

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// profileRules isolates the test and returns rules with no approval authority, so the
// working tree is the whole workspace policy, and the user tier's config directory. This
// test binary links no Buzz engine, so rules are registered directly rather than loaded.
func profileRules(t *testing.T, level types.GuardProfile) (*GuardRules, string) {
	t.Helper()
	testkit.Isolate(t)
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	disabled := false
	rules := &GuardRules{root: t.TempDir(), opts: types.VCSOptions{Enabled: &disabled}, registry: workspace.NewWorkspaceRegistry()}
	if level != "" {
		rules.registry.SetProfile(level)
	}
	return rules, filepath.Join(home, "magus")
}

func userAgents(t *testing.T, dir, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magus.yaml"), []byte(body), 0o600))
}

func TestGuardProfileUnsetIsLight(t *testing.T) {
	rules, _ := profileRules(t, "")
	assert.Equal(t, types.GuardProfileLight, EffectiveGuardProfile(t.Context(), rules, ""))

	approved, err := LoadApprovedProfile(t.Context(), rules.root)
	require.NoError(t, err)
	assert.Equal(t, types.GuardProfileLight, approved, "no approval authority is light")
	assert.Equal(t, types.GuardProfileLight, EffectiveGuardProfile(t.Context(), nil, rules.root))
}

// A person's agents.profile raises the workspace's level and never lowers it, including
// below the light a workspace that declares nothing gets.
func TestGuardProfileUserTierOnlyTightens(t *testing.T) {
	rules, user := profileRules(t, types.GuardProfileStandard)
	assert.Equal(t, types.GuardProfileStandard, EffectiveGuardProfile(t.Context(), rules, ""))

	userAgents(t, user, "agents:\n  profile: strict\n")
	assert.Equal(t, types.GuardProfileStrict, EffectiveGuardProfile(t.Context(), rules, ""))
	userAgents(t, user, "agents:\n  profile: off\n")
	assert.Equal(t, types.GuardProfileStandard, EffectiveGuardProfile(t.Context(), rules, ""))

	unset, user := profileRules(t, "")
	userAgents(t, user, "agents:\n  profile: off\n")
	assert.Equal(t, types.GuardProfileLight, EffectiveGuardProfile(t.Context(), unset, ""))

	off, user := profileRules(t, types.GuardProfileOff)
	assert.Equal(t, types.GuardProfileOff, EffectiveGuardProfile(t.Context(), off, ""), "a workspace may declare off")
	userAgents(t, user, "agents:\n  profile: standard\n")
	assert.Equal(t, types.GuardProfileStandard, EffectiveGuardProfile(t.Context(), off, ""))
}

// With no level that sets anything, a declaration still applies, and a magus.yaml that
// does not load is reported beside the answer, never in place of it.
func TestEffectiveBuiltinsReadsTheUserTier(t *testing.T) {
	rules, user := profileRules(t, types.GuardProfileOff)
	got, err := rules.EffectiveBuiltins(t.Context())
	require.NoError(t, err)
	assert.Nil(t, got, "no level sets a rule yet, so nothing is declared")

	rules.registry.SetBuiltins(map[string]builtin.Setting{"whole-tree": {Decision: builtin.Off}})
	got, err = rules.EffectiveBuiltins(t.Context())
	require.NoError(t, err)
	assert.Equal(t, builtin.Setting{Decision: builtin.Off}, got["whole-tree"])

	userAgents(t, user, "agents:\n  billing: oauth\n")
	got, err = rules.EffectiveBuiltins(t.Context())
	require.Error(t, err)
	assert.Equal(t, builtin.Setting{Decision: builtin.Off}, got["whole-tree"], "the declaration still applies")
	assert.Equal(t, builtin.Setting{Decision: builtin.Off}, GuardBuiltins(t.Context(), rules, "")["whole-tree"])
}

// A workspace whose level sets nothing still hands the hook the light level's settings, not
// the bare defaults nil stands for.
func TestGuardBuiltinsFallsBackToTheLightLevel(t *testing.T) {
	rules, _ := profileRules(t, types.GuardProfileOff)
	got := GuardBuiltins(t.Context(), rules, "")
	require.NotNil(t, got, "the compiled-in light row resolves")
	assert.Equal(t, lightBuiltins(), got)
	assert.True(t, slices.ContainsFunc(slices.Collect(maps.Values(got)), func(s builtin.Setting) bool {
		return s.Profile == types.GuardProfileLight
	}), "the light row's settings are in it")
}

// The approved side alone resolves the declaration over the defaults when no working tree
// loaded; with no approval authority that is the defaults.
func TestLoadApprovedBuiltinsWithoutAnAuthority(t *testing.T) {
	rules, user := profileRules(t, "")
	got, err := LoadApprovedBuiltins(t.Context(), rules.root)
	require.NoError(t, err)
	assert.Equal(t, builtin.Setting{Decision: builtin.Deny}, got["whole-tree"])

	userAgents(t, user, "agents:\n  profile: max\n")
	_, err = LoadApprovedBuiltins(t.Context(), rules.root)
	require.Error(t, err, "a magus.yaml that does not load is an error")
}
