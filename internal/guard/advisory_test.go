package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// TestAdviseInstalledSkillWrite pins the discriminator: the STAMP decides, not
// the path. Both files below sit in the same directory under a magus-* name,
// and only one of them is magus's to overwrite.
func TestAdviseInstalledSkillWrite(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) string {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
		return path
	}

	installed := write(".claude/skills/magus-run/SKILL.md", "---\nname: magus-run\nmetadata:\n  source: magus\n---\n\n# Running work\n")
	got := adviseInstalledSkillWrite(installed)
	assert.Contains(t, got.Say, "INSTALLED skill")
	assert.Contains(t, got.Why, "magus-workspace-rules")

	// A workspace's own skill must draw silence: calling a hand-written file
	// generated is worse than saying nothing.
	local := write(fmt.Sprintf(".claude/skills/%s/SKILL.md", agent.LocalSkillName), fmt.Sprintf("---\nname: %s\nmetadata:\n  source: workspace\n---\n\n# Our rules\n", agent.LocalSkillName))
	assert.Empty(t, adviseInstalledSkillWrite(local))

	// An embedded source has no frontmatter, and its prose may quote the stamp.
	source := write("internal/agent/skills/magus-workspace-rules/SKILL.md",
		"# Adapting the agent integration\n\nIf a file's frontmatter says `source: magus`, it is not yours.\n\nsource: magus\n")
	assert.Empty(t, adviseInstalledSkillWrite(source))
	quoted := write(".claude/skills/team-rules/SKILL.md", "---\nname: team-rules\n---\n\nsource: magus\n")
	assert.Empty(t, adviseInstalledSkillWrite(quoted), "a stamp in the body is prose")

	// Not a skill file, not in a skill directory, and not there at all.
	assert.Empty(t, adviseInstalledSkillWrite(write(".claude/skills/magus-run/README.md", "source: magus")))
	assert.Empty(t, adviseInstalledSkillWrite(write("docs/SKILL.md", "source: magus")))
	assert.Empty(t, adviseInstalledSkillWrite(filepath.Join(dir, ".claude", "skills", "magus-vcs-hygiene", "SKILL.md")))
}

// TestSymbolSearchRefusesWhateverTheIndexState pins the condition the deny
// rests on. A current index that defines the name refuses with refs. A stale
// one does too: an index goes stale on the first edit, and refs refreshes it.
// A current index that holds no such name lets the search run, and only a
// workspace with no index at all is advised.
func TestSymbolSearchRefusesWhateverTheIndexState(t *testing.T) {
	verdict := func(defined, definitive bool) ShellVerdict {
		return Evaluate(strict(Dependencies{
			SymbolDefined: func(string) (bool, bool) { return defined, definitive },
		}), "grep -r HandleRequest internal/")
	}

	denied := verdict(true, true)
	assert.Equal(t, denyRule{Name: denyRuleSymbolSearch, Arg: "HandleRequest"}, denied.Rule,
		"the deny names the symbol so the trail can count it")
	assert.Contains(t, denied.Deny, "refs HandleRequest --occurrences", "a deny that does not carry the replacement is a lost turn")
	assert.Contains(t, denied.Deny, "Classified: `HandleRequest` is a name (an identifier's shape, defined in the index)")
	assert.NotContains(t, denied.Deny, "graph build")

	stale := verdict(true, false)
	assert.Equal(t, denyRuleSymbolSearch, stale.Rule.Name, "a stale index still knows the name, so the search is refused")
	assert.Contains(t, stale.Deny, "The symbol index is older than the sources it covers; refs refreshes it as it answers.")
	assert.NotContains(t, stale.Deny, "graph build", "refs refreshes it, so nobody is sent away to rebuild")
	require.Len(t, stale.Next, 1, "refs alone")
	assert.Equal(t, hint.Refs.With("HandleRequest", "--occurrences"), stale.Next[0].Run)
	assert.Contains(t, stale.Lead, "Classified:", "the lead replaces the deny, so it carries the classification")

	absent := verdict(false, true)
	assert.Empty(t, absent.Deny, "nothing replaces a search for a name the current index does not hold")
	assert.Empty(t, absent.Context)

	unwired := Evaluate(strict(Dependencies{}), "grep -r HandleRequest internal/")
	assert.Empty(t, unwired.Deny, "with no index at all a deny would route nowhere")
	assert.Equal(t, advisoryPrecedent, unwired.Kind)
	assert.Empty(t, unwired.Brief, "a repeat saying the index is still missing changes nothing, so it goes quiet")
}

// An advisory not held by its own marker is still not repeated word for word to one
// caller, and the subagents of a session are each their own caller.
func TestAdviceRepeatedOncePerCaller(t *testing.T) {
	t.Parallel()
	g := hint.NewGate(t.TempDir(), "s")
	rule := string(advisoryEchoOnSuccess)
	assert.False(t, adviceRepeated(g, "", rule, "drop the echo"))
	assert.True(t, adviceRepeated(g, "", rule, "drop the echo"))
	assert.False(t, adviceRepeated(g, "", rule, "drop the other echo"), "new text is a new fact")
	assert.False(t, adviceRepeated(g, "a1", rule, "drop the echo"), "a subagent is told for itself")
	assert.False(t, adviceRepeated(g, "", string(advisorySourceRead), "x"), "an enrolled advisory is its gate's to hold")
	assert.False(t, adviceRepeated(g, "", string(advisorySourceRead), "x"))
}

// The property nothing may erode: a DENY carries its whole reason on every invocation,
// whatever the marker directory says. The gate is not consulted on that arm at all, and
// this asserts it against a spent marker for every enrolled kind at once.
func TestDenyIgnoresEverySpentAdvisoryMarker(t *testing.T) {
	base := t.TempDir()
	gate := hint.NewGate(base, "shared")
	for _, kind := range []hint.MarkerKind{
		advisorySourceRead, advisoryPrecedent,
		advisoryStageClassify, advisoryUnleasedWrite, advisorySkillSource, advisoryRegenSource,
		advisoryGraphStale, advisoryFocus, advisoryNewFile,
	} {
		require.NotEmpty(t, gate.Once(kind, "x"), "fixture: spend every family")
		require.Empty(t, gate.Once(kind, "x"), "fixture: the family is now spent")
	}

	// The deny arm reads Deny, which no gate call touches; a Kind on a deny would be a
	// contradiction, so this also asserts the verdict carries none.
	for _, command := range []string{"git stash", "go build ./...", "magus ls | head -5"} {
		v := Evaluate(strict(testDependencies()), command)
		require.NotEmpty(t, v.Deny, "fixture %q must deny", command)
		assert.Empty(t, v.Kind, "a deny carries no advisory kind, so nothing can hold it to one firing")
		assert.Empty(t, v.Brief, "a deny has no degraded form: the caller cannot see past a refusal")
	}
}

// requireAdvisedOnce pins what a rule that once refused does with the default settings: v
// refuses nothing, it is an advisory keyed on rule, and a session hears it once.
func requireAdvisedOnce(t *testing.T, v ShellVerdict, rule denyRuleName) {
	t.Helper()
	require.Empty(t, v.Deny, "a rule set to advise does not refuse")
	require.True(t, v.demoted, "a rule set to advise advises")
	assert.Equal(t, hint.MarkerKind(rule), v.Kind)
	assert.NotEmpty(t, v.Context)

	held := heldAdvice{v: v}
	gate := hint.NewGate(t.TempDir(), "s1")
	first := types.GuardJudgment{Decision: "pass"}
	whys := map[string]string{}
	held.speak(gate, &first, whys)
	assert.Equal(t, types.GuardJudgment{Decision: "advise", Rule: string(rule), Context: v.Context}, first)
	assert.Equal(t, v.Why, whys[v.Context], "the rationale is noted for the stored advice")

	again := types.GuardJudgment{Decision: "pass"}
	held.speak(gate, &again, whys)
	assert.Equal(t, "pass", again.Decision, "once per session")
}

func TestSymbolSearchAdvisesWhenSetToAdvise(t *testing.T) {
	deps := advising(Dependencies{SymbolDefined: func(string) (bool, bool) { return true, true }}, string(denyRuleSymbolSearch))
	requireAdvisedOnce(t, Evaluate(deps, "grep -r HandleRequest internal/"), denyRuleSymbolSearch)
}
