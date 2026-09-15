package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/hint"
)

// A precedent hunt is one distinctive name; an output filter is not. The second shape
// was 26% of grep invocations in the mining, so firing on it would train the reader to
// skip the family.
func TestPrecedentIdentClassifier(t *testing.T) {
	ident := func(command string) string {
		cmds, ok := ParseCommands(command)
		require.True(t, ok, "fixture must parse")
		return precedentIdent(cmds)
	}

	assert.Equal(t, "HandleRequest", ident("rg HandleRequest"))
	assert.Equal(t, "parse_config", ident("rg parse_config"))
	assert.Equal(t, "buildStep", ident("grep -r buildStep internal/"))

	assert.Empty(t, ident("go test ./... | grep FAIL"), "an output filter is not a precedent hunt")
	assert.Empty(t, ident("rg needle"), "a lowercase run is as likely to be prose")
	assert.Empty(t, ident("rg Foo"), "under the length floor")
	assert.Empty(t, ident("cat internal/hint/hint.go"), "reading a known path asks nothing of the graph")
}

// TestSymbolSearchDeniesOnlyWhatRefsReplaces pins the condition the deny rests on. refs
// replaces a grep exactly when the name is an indexed symbol AND the index can be
// trusted; every other answer leaves the advisory in place, because a deny that routes
// nowhere takes a capability away. Raw text is the standing case: no index holds a
// string literal, so grep stays the only tool for it.
func TestSymbolSearchDeniesOnlyWhatRefsReplaces(t *testing.T) {
	verdict := func(defined, definitive bool) ShellVerdict {
		return Evaluate(Dependencies{
			SymbolDefined: func(string) (bool, bool) { return defined, definitive },
		}, "grep -r HandleRequest internal/")
	}

	denied := verdict(true, true)
	assert.NotEmpty(t, denied.Deny, "an indexed symbol under a current index is the case refs replaces exactly")
	assert.Equal(t, denyRuleSymbolSearch, denied.Rule.Name)
	assert.Equal(t, "HandleRequest", denied.Rule.Arg, "the deny names the symbol so the trail can count it")
	assert.Contains(t, denied.Deny, "HandleRequest", "a deny that does not carry the replacement is a lost turn")

	stale := verdict(true, false)
	assert.Empty(t, stale.Deny, "a stale index answers unknown, which cannot justify taking grep away")
	assert.Equal(t, advisoryPrecedent, stale.Kind)

	absent := verdict(false, true)
	assert.Empty(t, absent.Deny, "nothing replaces a search for text no index holds")
	assert.Equal(t, advisoryPrecedent, absent.Kind)

	unwired := Evaluate(Dependencies{}, "grep -r HandleRequest internal/")
	assert.Empty(t, unwired.Deny, "a caller that supplies no resolver keeps the advisory it had")
}

// The property nothing may erode: a DENY carries its whole reason on every invocation,
// whatever the marker directory says. The gate is not consulted on that arm at all, and
// this asserts it against a spent marker for every enrolled kind at once.
func TestDenyIgnoresEverySpentAdvisoryMarker(t *testing.T) {
	base := t.TempDir()
	gate := hint.NewGate(base, "shared")
	for _, kind := range []hint.MarkerKind{
		advisoryStaleBinary, advisoryCodeSearch, advisoryDocSearch, advisorySourceRead, advisoryPrecedent,
		advisoryStageClassify, advisoryUnleasedWrite, advisorySkillSource, advisoryRegenSource,
		advisoryGraphStale, advisoryFocus, advisoryNewFile,
	} {
		require.NotEmpty(t, gate.Once(kind, "x"), "fixture: spend every family")
		require.Empty(t, gate.Once(kind, "x"), "fixture: the family is now spent")
	}

	// The deny arm reads Deny, which no gate call touches; a Kind on a deny would be a
	// contradiction, so this also asserts the verdict carries none.
	for _, command := range []string{"git stash", "go build ./...", "magus ls | head -5"} {
		v := Evaluate(testDependencies(), command)
		require.NotEmpty(t, v.Deny, "fixture %q must deny", command)
		assert.Empty(t, v.Kind, "a deny carries no advisory kind, so nothing can hold it to one firing")
		assert.Empty(t, v.Brief, "a deny has no degraded form: the caller cannot see past a refusal")
	}
}
