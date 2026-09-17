package guard

import (
	"strings"
	"testing"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/hint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuzzWriteRequiresTheBuzzSkill pins the session behavior. The shared half is exercised
// through the spawn rule in spawn_test.go; what matters here is that the same mechanism is
// reached and that the deny names a skill the reader can actually load.
func TestBuzzWriteRequiresTheBuzzSkill(t *testing.T) {
	t.Parallel()

	t.Run("the first Buzz write of a session is denied and names the skill", func(t *testing.T) {
		g := hint.NewGate(t.TempDir(), "buzz-a")
		reason := denyBuzzWriteWithoutSkill(g, true, "spells/typescript/spell.buzz")
		require.NotEmpty(t, reason)
		assert.Contains(t, reason, buzzWriteSkill.String())
		assert.NotContains(t, reason, agent.FullTwinName(buzzWriteSkill.String()),
			"one shipped harness installs the short form alone")
	})

	t.Run("loading the skill clears it for the rest of the session", func(t *testing.T) {
		g := hint.NewGate(t.TempDir(), "buzz-b")
		require.True(t, recordSkillLoad(g, buzzWriteSkill.String()))
		assert.Empty(t, denyBuzzWriteWithoutSkill(g, true, "magusfile.buzz"))
	})

	t.Run("the multi-agent brief does not clear it", func(t *testing.T) {
		g := hint.NewGate(t.TempDir(), "buzz-c")
		recordSkillLoad(g, multiAgentSkill.String())
		assert.NotEmpty(t, denyBuzzWriteWithoutSkill(g, true, "tools/host-schemas.buzz"),
			"the two rules gate different skills and must not satisfy each other")
	})

	t.Run("a host that cannot report a skill load is not held to having reported one", func(t *testing.T) {
		g := hint.NewGate(t.TempDir(), "buzz-d")
		assert.Empty(t, denyBuzzWriteWithoutSkill(g, false, "magusfile.buzz"))
		assert.NotEmpty(t, denyBuzzWriteWithoutSkill(g, true, "magusfile.buzz"))
	})
}

// TestSkillMarkerRefusesANameThatIsNotOne pins the one MarkerKind in the tree built from
// input magus did not write. A kind is a filename component that hint.MarkerPath joins
// unhashed, and the gate then creates directories under it, exclusively creates a file at
// it, and sweeps it with os.Remove, so a traversing name reaches all three outside the
// advisories directory.
func TestSkillMarkerRefusesANameThatIsNotOne(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"../../../../tmp/pwn",
		"..",
		"magus/../../escape",
		"magus buzz write",
		"Magus-Buzz-Write",
		"magus.buzz.write",
		"",
		strings.Repeat("a", 129),
	} {
		assert.Empty(t, skillMarker(name), "%q must not become a marker", name)
	}

	// The shipped shapes still record, twin included: a filter that rejected those would
	// deny the gate forever with no load able to clear it.
	assert.NotEmpty(t, skillMarker(multiAgentSkill.String()))
	assert.NotEmpty(t, skillMarker(agent.FullTwinName(multiAgentSkill.String())))
	assert.NotEmpty(t, skillMarker(buzzWriteSkill.String()))

	// And the refusal reaches the recorder, so a traversing name marks nothing at all.
	g := hint.NewGate(t.TempDir(), "session-traversal")
	assert.False(t, recordSkillLoad(g, "../../../../tmp/pwn"))
	assert.NotEmpty(t, denySpawnWithoutBrief(g, true),
		"a name that cannot be a skill must not clear a gate that wants one")
}

// TestANamespacedSkillStillClearsItsGate pins the other half of the charset rule. Hosts
// namespace a plugin-installed skill, and rejecting that form outright would leave the
// gate denied while the reader did exactly what the deny asked.
func TestANamespacedSkillStillClearsItsGate(t *testing.T) {
	t.Parallel()

	g := hint.NewGate(t.TempDir(), "session-namespaced")
	require.True(t, recordSkillLoad(g, "some-plugin:"+multiAgentSkill.String()))
	assert.Empty(t, denySpawnWithoutBrief(g, true))

	// The namespace is stripped, never walked: a separator that could traverse is still
	// refused, however it is dressed up as a namespace.
	bad := hint.NewGate(t.TempDir(), "session-namespaced-bad")
	assert.False(t, recordSkillLoad(bad, "plugin:../../escape"))
	assert.NotEmpty(t, denySpawnWithoutBrief(bad, true))
}

// TestASkillGateStandsDownWithNowhereToRecord pins the second way this rule could deny
// with no action that clears it. A gate with no cache dir cannot write the marker a load
// would leave, so every retry is denied identically however many times the skill loads.
func TestASkillGateStandsDownWithNowhereToRecord(t *testing.T) {
	t.Parallel()

	g := hint.NewGate("", "session-no-cache-dir")
	assert.False(t, recordSkillLoad(g, multiAgentSkill.String()),
		"nothing can be recorded, and reporting otherwise would claim a marker exists")
	assert.Empty(t, denySpawnWithoutBrief(g, true))
	assert.Empty(t, denyBuzzWriteWithoutSkill(g, true, "magusfile.buzz"))
}

// TestOnlyBuzzSourceIsGated pins the extension test. Buzz has no directory of its own, so
// a workspace is free to put a spell anywhere; every other file in the tree passes untouched
// however close it sits to one that does not.
func TestOnlyBuzzSourceIsGated(t *testing.T) {
	t.Parallel()

	gated := []string{
		"magusfile.buzz",
		"spells/go/spell.buzz",
		"/abs/path/tools/host-schemas.buzz",
		"docs/render.BUZZ",
	}
	for _, p := range gated {
		assert.NotEmpty(t, denyBuzzWriteWithoutSkill(hint.NewGate(t.TempDir(), "gated"), true, p), p)
	}

	passed := []string{
		"internal/guard/buzz.go",
		"magusfile.buzz.md",
		"spells/go/spell.buzz.bak",
		"notes/buzz",
		".buzz/cache/entry.json",
		"",
	}
	for _, p := range passed {
		assert.Empty(t, denyBuzzWriteWithoutSkill(hint.NewGate(t.TempDir(), "passed"), true, p), p)
	}
}

// TestBuzzAuthoringOnACommandLineRequiresTheSkill covers the road the write rule cannot
// see. Authoring Buzz through `magus buzz -e` or a heredoc reaches no write tool, so the
// envelope carries no path and the rule above never runs; the language is just as unknown
// either way.
func TestBuzzAuthoringOnACommandLineRequiresTheSkill(t *testing.T) {
	t.Parallel()

	authors := map[string]string{
		"inline eval":               `magus buzz -e 'import "std"; fun main() > void {}'`,
		"inline eval, long flag":    `magus buzz --eval 'fun main() > void {}'`,
		"inline eval via ./magus":   `./magus buzz -e 'fun main() > void {}'`,
		"heredoc into a spell":      "cat > spells/ts/spell.buzz <<'EOF'\nfun main() > void {}\nEOF",
		"append to a magusfile":     "echo 'x' >> magusfile.buzz",
		"heredoc inside a subshell": "sh -c \"cat > tools/x.buzz <<'EOF'\nfun main() > void {}\nEOF\"",
		"uppercase extension":       "cat > Tools/X.BUZZ <<'EOF'\nEOF",
	}
	for name, command := range authors {
		t.Run(name, func(t *testing.T) {
			g := hint.NewGate(t.TempDir(), "buzz-cmd-"+name)
			reason := denyBuzzAuthorWithoutSkill(g, true, command, DialectBash)
			require.NotEmpty(t, reason, "must demand the skill")
			assert.Contains(t, reason, buzzWriteSkill.String())
		})
	}

	// Running Buzz that already exists is READING it, which is how the language gets
	// learned. `magus buzz -t x.buzz` names a .buzz path as an operand, so a rule built on
	// write CANDIDATES rather than redirect targets would refuse exactly the act it wants.
	reads := map[string]string{
		"running a file":     "magus buzz magusfile.buzz",
		"testing a file":     "magus buzz -t spells/go/spell.buzz",
		"testing embedded":   "magus buzz -t --embedded magusfile.buzz",
		"redirect elsewhere": "magus describe target ci -o json > out.json",
		"no buzz at all":     "magus run go-build .",
	}
	for name, command := range reads {
		t.Run(name, func(t *testing.T) {
			g := hint.NewGate(t.TempDir(), "buzz-read-"+name)
			assert.Empty(t, denyBuzzAuthorWithoutSkill(g, true, command, DialectBash))
		})
	}

	t.Run("loading the skill clears the command road too", func(t *testing.T) {
		const command = `magus buzz -e 'fun main() > void {}'`
		g := hint.NewGate(t.TempDir(), "buzz-cmd-cleared")
		require.NotEmpty(t, denyBuzzAuthorWithoutSkill(g, true, command, DialectBash))
		require.True(t, recordSkillLoad(g, buzzWriteSkill.String()))
		assert.Empty(t, denyBuzzAuthorWithoutSkill(g, true, command, DialectBash),
			"the write road and the command road share one marker: reading the skill once is enough")
	})

	t.Run("the buzz deny never displaces another", func(t *testing.T) {
		held := ShellVerdict{Deny: "something else", Rule: denyRule{Name: denyRuleCacheDirWrite}}
		assert.Equal(t, held, rankBuzzAuthor(held, "buzz reason"),
			"there is nothing to learn before a command that is refused anyway")
	})
}
