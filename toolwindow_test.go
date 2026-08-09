package magus

import (
	"context"
	"errors"
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

// A batch with one violation of each kind must not let the project NAMES decide the code.
// The code was previously recovered by sniffing the sorted messages for "older than", so
// it turned on which project label sorted first - an alphabetical accident.
func TestToolWindowCodeDoesNotTurnOnProjectOrder(t *testing.T) {
	old := projectWith("zz", map[string]spells.VersionBounds{"node": {Min: "22"}}, tsSpell("node", spells.VersionBounds{}))
	recent := projectWith("aa", map[string]spells.VersionBounds{"node": {Below: "25"}}, tsSpell("node", spells.VersionBounds{}))
	versions := map[string]string{
		key("zz", "typescript", "node"): "v18.0.0",
		key("aa", "typescript", "node"): "v26.5.0",
	}

	// "aa" sorts first and is the too-NEW one, so a code read off the sorted messages
	// would say MGS3006 while the too-old violation sits in the same payload.
	err := checkToolWindows([]*types.Project{old, recent}, versions)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "older than the supported range (min 22)")
	assert.Contains(t, err.Error(), "newer than the supported range (below 25)")
	assert.ErrorIs(t, err, types.ToolTooOld, "one error carries one code, and too-old wins a mixed batch")

	// Reversing the projects must change nothing. One error, one code, and neither the
	// iteration order nor the alphabet gets a say in which.
	flipped := checkToolWindows([]*types.Project{recent, old}, versions)
	require.Error(t, flipped)
	assert.Equal(t, err.Error(), flipped.Error())
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

// probedSpell is tsSpell with the version prober injected, so these tests exercise the
// real probeTools path without forking anything. The counter is what proves memoization,
// which is the only reason probeTools threads two outputs out of one pass.
func probedSpell(name, tool string, key spells.VersionKey, out func(dir string) (string, error), calls *int) *spells.Spell {
	return spells.NewSpell(name,
		spells.WithTools(map[string]spells.Tool{
			tool: {Probe: spells.Command{Bin: tool, Args: []string{"--version"}}, Key: key},
		}),
		spells.WithVersionProber(func(_ context.Context, _ spells.Command, dir string) (string, error) {
			*calls++
			return out(dir)
		}),
	)
}

// The cache key wants a narrowed token and the window gate wants the whole version. Both
// come out of one probe, and the two must not disagree about what was read.
func TestProbeToolsRecordsTheKeyTokenAndTheFullVersion(t *testing.T) {
	calls := 0
	sp := probedSpell("typescript", "node", spells.VersionKey{}, func(string) (string, error) { return "v22.14.0", nil }, &calls)
	p := &types.Project{Path: "console", Dir: "/tmp/console", ResolvedSpells: []*spells.Spell{sp}}

	full := map[string]string{}
	got := (&Magus{}).probeTools(t.Context(), []*types.Project{p}, full)

	assert.Equal(t, 1, calls)
	assert.Equal(t, map[string]string{key("console", "typescript", "node"): "v22.14.0"}, full)
	require.Contains(t, got, "console")
	assert.Equal(t, []string{"typescript:node:v22.14.0"}, got["console"], "the default key narrows nothing; the gate reads the same probe")
}

// A probe that fails records UNPROBED for the cache key and NOTHING for the gate. Those
// have to differ: a key must change so a later successful probe misses, while the gate
// must not invent a version and fail a build over a comparison it could not make.
func TestProbeToolsLeavesAFailedProbeOutOfTheGate(t *testing.T) {
	calls := 0
	sp := probedSpell("typescript", "node", spells.VersionKey{}, func(string) (string, error) {
		return "", errors.New("exec: node: not found")
	}, &calls)
	p := &types.Project{
		Path: "console", Dir: "/tmp/console",
		ToolBounds:     map[string]spells.VersionBounds{"node": {Min: "22"}},
		ResolvedSpells: []*spells.Spell{sp},
	}

	full := map[string]string{}
	got := (&Magus{}).probeTools(t.Context(), []*types.Project{p}, full)

	assert.Equal(t, []string{"typescript:node:UNPROBED"}, got["console"])
	assert.Empty(t, full)
	assert.NoError(t, checkToolWindows([]*types.Project{p}, full), "an absent tool is not a version violation")
}

// Output carrying no version is the same contract as a failed probe for the gate's
// purposes: nothing to compare, so nothing to fail. It differs for the cache key, which
// still has to record what it saw.
func TestProbeToolsLeavesUnparsableOutputOutOfTheGate(t *testing.T) {
	calls := 0
	sp := probedSpell("typescript", "node", spells.VersionKey{}, func(string) (string, error) { return "nightly", nil }, &calls)
	p := &types.Project{Path: "console", Dir: "/tmp/console", ResolvedSpells: []*spells.Spell{sp}}

	full := map[string]string{}
	(&Magus{}).probeTools(t.Context(), []*types.Project{p}, full)
	assert.Empty(t, full)
}

// Memoization is on (spell, dir, tool), so two projects in one directory cost one spawn.
// Without it the gate would multiply forks by the number of projects sharing a tree.
func TestProbeToolsMemoizesAcrossProjectsSharingADir(t *testing.T) {
	calls := 0
	sp := probedSpell("go", "go", spells.VersionKey{}, func(string) (string, error) { return "go1.26.5", nil }, &calls)
	a := &types.Project{Path: "a", Dir: "/tmp/same", ResolvedSpells: []*spells.Spell{sp}}
	b := &types.Project{Path: "b", Dir: "/tmp/same", ResolvedSpells: []*spells.Spell{sp}}

	full := map[string]string{}
	(&Magus{}).probeTools(t.Context(), []*types.Project{a, b}, full)

	assert.Equal(t, 1, calls, "one spawn for the shared (spell, dir, tool)")
	// Both projects still get their own gate entry, or the second would go unchecked.
	assert.Equal(t, "v1.26.5", full[key("a", "go", "go")])
	assert.Equal(t, "v1.26.5", full[key("b", "go", "go")])
}

// A declared constant token is the documented way to key a tool without spawning. It must
// stay out of the gate: the author typed it to invalidate a cache, and it is not a reading
// of anything installed.
func TestProbeToolsDoesNotGateOnADeclaredConstant(t *testing.T) {
	sp := spells.NewSpell("typescript", spells.WithTools(map[string]spells.Tool{
		"node": {Key: spells.VersionKey{Const: "pinned-1"}},
	}))
	p := &types.Project{Path: "console", Dir: "/tmp/console", ResolvedSpells: []*spells.Spell{sp}}
	full := map[string]string{}
	got := (&Magus{}).probeTools(t.Context(), []*types.Project{p}, full)
	assert.Equal(t, []string{"typescript:node:pinned-1"}, got["console"], "the constant still keys the cache")
	assert.Empty(t, full, "but it is not a reading of anything installed, so the gate never sees it")
}

// MAGUS_CACHE_TOOL_VERSION=off short-circuits the probe pass, and the window gate reads
// what that pass records - so switching off tool-version CACHE KEYING also switches off
// the version gate. Pinned because it is not obvious from either name, and because a gate
// that can be disabled by a cache knob should at least be disabled on purpose.
func TestProbeToolsOffAlsoDisablesTheWindowGate(t *testing.T) {
	t.Setenv("MAGUS_CACHE_TOOL_VERSION", "off")
	calls := 0
	sp := probedSpell("typescript", "node", spells.VersionKey{}, func(string) (string, error) { return "v26.5.0", nil }, &calls)
	p := &types.Project{
		Path: "console", Dir: "/tmp/console",
		ToolBounds:     map[string]spells.VersionBounds{"node": {Below: "25"}},
		ResolvedSpells: []*spells.Spell{sp},
	}

	full := map[string]string{}
	assert.Nil(t, (&Magus{}).probeTools(t.Context(), []*types.Project{p}, full))
	assert.Equal(t, 0, calls)
	assert.NoError(t, checkToolWindows([]*types.Project{p}, full))
}

// mode=workspace probes once at the root instead of per project dir. The gate still keys
// by project path, so a per-project window is enforced against the root's reading.
func TestProbeToolsWorkspaceModeProbesTheRootDir(t *testing.T) {
	t.Setenv("MAGUS_CACHE_TOOL_VERSION", "workspace")
	var seen []string
	calls := 0
	sp := probedSpell("typescript", "node", spells.VersionKey{}, func(dir string) (string, error) {
		seen = append(seen, dir)
		return "v26.5.0", nil
	}, &calls)
	p := &types.Project{
		Path: "console", Dir: "/tmp/console",
		ToolBounds:     map[string]spells.VersionBounds{"node": {Below: "25"}},
		ResolvedSpells: []*spells.Spell{sp},
	}

	m := &Magus{ws: &types.Workspace{Root: "/tmp/root"}}
	full := map[string]string{}
	m.probeTools(t.Context(), []*types.Project{p}, full)

	assert.Equal(t, []string{"/tmp/root"}, seen)
	assert.ErrorIs(t, checkToolWindows([]*types.Project{p}, full), types.ToolTooNew)
}

// toolVersionsByProject is probeTools with the gate output discarded. A nil map must not
// panic on the write path, which is the only thing separating the two.
func TestToolVersionsByProjectTakesTheNilGateMap(t *testing.T) {
	calls := 0
	sp := probedSpell("typescript", "node", spells.VersionKey{}, func(string) (string, error) { return "v22.14.0", nil }, &calls)
	p := &types.Project{Path: "console", Dir: "/tmp/console", ResolvedSpells: []*spells.Spell{sp}}
	assert.Equal(t, []string{"typescript:node:v22.14.0"}, (&Magus{}).toolVersionsByProject(t.Context(), []*types.Project{p})["console"])
}
