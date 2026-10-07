package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/hint"
)

// TestSymbolSearchRefusesWhateverTheIndexState pins the condition the deny rests on. A
// current index that defines the name refuses with refs. A stale one refuses too, serving
// the rebuild first: the fail-open advice it used to give is what let every symbol search
// through, since an index goes stale on the first edit. A current index that holds no such
// name lets the search run, and only a workspace with no index at all is advised.
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
	assert.Contains(t, stale.Deny, hint.GraphBuild.With("--silent")+"`, then `"+hint.Refs.With("HandleRequest", "--occurrences"))
	assert.Contains(t, stale.Deny, "The symbol index is older than the sources it covers")
	require.Len(t, stale.Next, 2, "the rebuild, then refs")
	assert.Equal(t, hint.GraphBuild.With("--silent"), stale.Next[0].Run)
	assert.Contains(t, stale.Lead, "Classified:", "the lead replaces the deny, so it carries the classification")

	absent := verdict(false, true)
	assert.Empty(t, absent.Deny, "nothing replaces a search for a name the current index does not hold")
	assert.Empty(t, absent.Context)

	unwired := Evaluate(strict(Dependencies{}), "grep -r HandleRequest internal/")
	assert.Empty(t, unwired.Deny, "with no index at all a deny would route nowhere")
	assert.Equal(t, advisoryPrecedent, unwired.Kind)
	assert.Contains(t, unwired.Brief, "no symbol index exists yet")
	assert.Contains(t, unwired.Brief, hint.GraphBuild.With("--silent"))
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
	require.Empty(t, v.Deny, "the default does not refuse")
	require.True(t, v.demoted, "the default advises")
	assert.Equal(t, hint.MarkerKind(rule), v.Kind)
	assert.NotEmpty(t, v.Context)

	held := heldAdvice{v: v}
	gate := hint.NewGate(t.TempDir(), "s1")
	first := Verdict{Decision: "pass"}
	held.speak(gate, &first)
	assert.Equal(t, Verdict{Decision: "advise", Rule: string(rule), Context: v.Context}, first)

	again := Verdict{Decision: "pass"}
	held.speak(gate, &again)
	assert.Equal(t, "pass", again.Decision, "once per session")
}

func TestSymbolSearchAdvisesByDefault(t *testing.T) {
	deps := Dependencies{SymbolDefined: func(string) (bool, bool) { return true, true }}
	requireAdvisedOnce(t, Evaluate(deps, "grep -r HandleRequest internal/"), denyRuleSymbolSearch)
}
