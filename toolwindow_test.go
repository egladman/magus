package magus

import (
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// key mirrors how probeTools records a version, so the tests exercise the real lookup
// rather than a convenient stand-in.
func key(project, spell, tool string) string { return project + "\x00" + spell + "\x00" + tool }

func projectWith(path string, bounds map[string]spells.VersionBounds, sp *spells.Spell) *types.Project {
	return &types.Project{Path: path, Dir: "/tmp/" + path, ToolBounds: bounds, ResolvedSpells: []*spells.Spell{sp}}
}

func tsSpell(tool string, supported spells.VersionBounds) *spells.Spell {
	return spells.NewSpell("typescript", spells.WithTools(map[string]spells.Tool{
		tool: {Probe: spells.Command{Bin: tool, Args: []string{"--version"}}, Supported: supported},
	}))
}

// The gate has to fire for a project whose targets never dispatch a spell op, which is
// the whole reason it moved out of op dispatch: every TypeScript project here runs its
// own pnpm scripts, so the op-level check could never reach them.
func TestToolWindowFailsWithoutAnyOpDispatch(t *testing.T) {
	p := projectWith("libs/textsearch", map[string]spells.VersionBounds{"node": {Min: "22", Below: "25"}}, tsSpell("node", spells.VersionBounds{}))
	err := checkToolWindows([]*types.Project{p}, map[string]string{key("libs/textsearch", "typescript", "node"): "v26.5.0"})
	require.Error(t, err)
	assert.ErrorIs(t, err, types.ToolTooNew)
	assert.Contains(t, err.Error(), "node v26.5.0 is newer than the supported range (below 25)")
}

// The spell's requirement and the project's policy intersect, narrower winning, so a
// version inside one but outside the other still fails.
func TestToolWindowIntersectsSpellAndProject(t *testing.T) {
	p := projectWith(".", map[string]spells.VersionBounds{"go": {Min: "1.26"}}, spells.NewSpell("go", spells.WithTools(map[string]spells.Tool{
		"go": {Probe: spells.Command{Bin: "go"}, Supported: spells.VersionBounds{Min: "1.21"}},
	})))
	// 1.24 satisfies the spell's 1.21 floor but not the project's 1.26 one.
	err := checkToolWindows([]*types.Project{p}, map[string]string{key(".", "go", "go"): "v1.24.0"})
	require.Error(t, err)
	assert.ErrorIs(t, err, types.ToolTooOld)

	assert.NoError(t, checkToolWindows([]*types.Project{p}, map[string]string{key(".", "go", "go"): "v1.26.5"}))
}

// Every violation is reported, not just the first: a toolchain mismatch usually has more
// than one tool to fix, and one-per-rebuild is the experience this replaces.
func TestToolWindowReportsEveryViolation(t *testing.T) {
	a := projectWith("console", map[string]spells.VersionBounds{"node": {Below: "25"}}, tsSpell("node", spells.VersionBounds{}))
	b := projectWith("docs", map[string]spells.VersionBounds{"node": {Below: "25"}}, tsSpell("node", spells.VersionBounds{}))
	err := checkToolWindows([]*types.Project{a, b}, map[string]string{
		key("console", "typescript", "node"): "v26.5.0",
		key("docs", "typescript", "node"):    "v26.5.0",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "console:")
	assert.Contains(t, err.Error(), "docs:")
}

// An unread version is never a violation. That covers an absent binary, output carrying
// no version, and probing switched off entirely - magus must not fail on a comparison it
// could not make.
func TestToolWindowSkipsUnreadVersions(t *testing.T) {
	p := projectWith("console", map[string]spells.VersionBounds{"node": {Min: "99"}}, tsSpell("node", spells.VersionBounds{}))
	assert.NoError(t, checkToolWindows([]*types.Project{p}, map[string]string{}))
}

// A tool nobody constrained is never probed against anything.
func TestToolWindowIgnoresUnconstrainedTools(t *testing.T) {
	p := projectWith("console", nil, tsSpell("node", spells.VersionBounds{}))
	assert.NoError(t, checkToolWindows([]*types.Project{p}, map[string]string{key("console", "typescript", "node"): "v26.5.0"}))
}
