package guard

import (
	"testing"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/hint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpawnRequiresTheMultiAgentBrief pins the one thing this rule may read. It asks
// whether a marker exists, never what the prompt says, because Evaluate hands it spawns
// whose prose deliberately goes unjudged.
func TestSpawnRequiresTheMultiAgentBrief(t *testing.T) {
	t.Parallel()

	t.Run("the first spawn of a session is denied and names the skill", func(t *testing.T) {
		g := hint.NewGate(t.TempDir(), "session-a")
		reason := denySpawnWithoutBrief(g, true)
		require.NotEmpty(t, reason)
		assert.Contains(t, reason, multiAgentSkill.String())
	})

	t.Run("loading the skill clears it for the rest of the session", func(t *testing.T) {
		g := hint.NewGate(t.TempDir(), "session-b")
		require.True(t, recordSkillLoad(g, multiAgentSkill.String()))
		assert.Empty(t, denySpawnWithoutBrief(g, true))
	})

	t.Run("a different skill does not clear it", func(t *testing.T) {
		g := hint.NewGate(t.TempDir(), "session-c")
		recordSkillLoad(g, "magus-run")
		assert.NotEmpty(t, denySpawnWithoutBrief(g, true),
			"any skill standing in for the brief would make the rule unfalsifiable")
	})

	t.Run("two sessions do not share the marker", func(t *testing.T) {
		dir := t.TempDir()
		recordSkillLoad(hint.NewGate(dir, "session-d"), multiAgentSkill.String())
		assert.NotEmpty(t, denySpawnWithoutBrief(hint.NewGate(dir, "session-e"), true),
			"the marker is keyed on the session, so one session's load is not another's")
	})

	t.Run("a host reporting no session is not guarded here", func(t *testing.T) {
		assert.Empty(t, denySpawnWithoutBrief(hint.NewGate(t.TempDir(), ""), true),
			"with no session pointer every spawn everywhere would share one bucket: the first denies and the rest pass")
		assert.False(t, recordSkillLoad(hint.NewGate(t.TempDir(), ""), multiAgentSkill.String()))
	})

	t.Run("an empty skill name records nothing", func(t *testing.T) {
		assert.False(t, recordSkillLoad(hint.NewGate(t.TempDir(), "session-f"), ""))
	})
}

// TestSpawnRuleStandsDownWhereLoadsGoUnobserved is the host-agnosticism guarantee, and the
// most important case here.
//
// Four harnesses ship and they wire hooks differently: one matches a skill tool, one wires
// four other matchers, one uses its own event names, and one wires no hook config at all.
// On a host that reports no skill load, a marker can never appear, so a rule that denied
// anyway would deny EVERY spawn for the life of the session with nothing the reader could
// do. Standing down is the only honest answer, and false is the default.
func TestSpawnRuleStandsDownWhereLoadsGoUnobserved(t *testing.T) {
	t.Parallel()

	g := hint.NewGate(t.TempDir(), "unobserved-host")
	assert.Empty(t, denySpawnWithoutBrief(g, false),
		"a host that cannot report a skill load must not be held to having reported one")
	assert.NotEmpty(t, denySpawnWithoutBrief(g, true),
		"the same session denies once the wiring declares it observes loads")
}

// TestEitherCopyClearsTheBrief pins that the twins are interchangeable as EVIDENCE: they
// carry the same rules, so a session that loaded the full one has read the brief.
func TestEitherCopyClearsTheBrief(t *testing.T) {
	t.Parallel()

	g := hint.NewGate(t.TempDir(), "session-full")
	require.True(t, recordSkillLoad(g, agent.FullTwinName(multiAgentSkill.String())))
	assert.Empty(t, denySpawnWithoutBrief(g, true))
}

// TestDenyNamesOnlyTheAlwaysInstalledCopy pins that the reason never points at the full
// twin. One shipped harness installs the short form alone, so naming the twin there would
// send the reader after a skill that is not on disk.
func TestDenyNamesOnlyTheAlwaysInstalledCopy(t *testing.T) {
	t.Parallel()

	reason := denySpawnWithoutBrief(hint.NewGate(t.TempDir(), "session-g"), true)
	require.NotEmpty(t, reason)
	assert.NotContains(t, reason, agent.FullTwinName(multiAgentSkill.String()))
}
