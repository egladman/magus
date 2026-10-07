package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
)

// sedInPlace is a recoverable deny on its own line, and nothing else.
const sedInPlace = `sed -i 's/a/b/' f.go`

func withSetting(rule string, d builtin.Decision) Dependencies {
	return Dependencies{Builtins: map[string]builtin.Setting{rule: {Decision: d}}}
}

func TestGradeDeny(t *testing.T) {
	t.Parallel()
	deny := ShellVerdict{Deny: "use the editor", Rule: denyRule{Name: denyRuleSedInPlace}}

	for _, tt := range []struct {
		decision builtin.Decision
		want     ShellVerdict
	}{
		{builtin.Deny, deny},
		{builtin.Advise, ShellVerdict{Context: "use the editor", Kind: hint.MarkerKind(denyRuleSedInPlace), demoted: true}},
		{builtin.Off, ShellVerdict{}},
	} {
		got := withSetting(string(denyRuleSedInPlace), tt.decision).grade(deny)
		assert.Equal(t, tt.want, got, tt.decision)
		assert.Equal(t, got, withSetting(string(denyRuleSedInPlace), tt.decision).grade(got), "grading is idempotent: %s", tt.decision)
	}
}

func TestGradeAdvisory(t *testing.T) {
	t.Parallel()
	advice := ShellVerdict{Context: "read the map", Kind: advisorySourceRead, Brief: "map"}

	for _, tt := range []struct {
		decision builtin.Decision
		want     ShellVerdict
	}{
		{builtin.Advise, advice},
		{builtin.Deny, ShellVerdict{Deny: "read the map", Rule: denyRule{Name: denyRuleName(advisorySourceRead)}}},
		{builtin.Off, ShellVerdict{}},
	} {
		assert.Equal(t, tt.want, withSetting(string(advisorySourceRead), tt.decision).grade(advice), tt.decision)
	}
}

func TestGradeDefaultsAndUnknownRules(t *testing.T) {
	t.Parallel()
	var deps Dependencies

	assert.True(t, deps.grade(ShellVerdict{Deny: "x", Rule: denyRule{Name: denyRuleSedInPlace}}).demoted, "nil Builtins grades by the defaults")
	whole := ShellVerdict{Deny: "x", Rule: denyRule{Name: denyRuleWholeTree}}
	assert.Equal(t, whole, deps.grade(whole))
	uncatalogued := ShellVerdict{Deny: "x", Rule: denyRule{Name: "not-a-rule"}}
	assert.Equal(t, uncatalogued, deps.grade(uncatalogued), "a rule the table does not carry keeps its decision")
}

func TestEvaluateGradesEachSite(t *testing.T) {
	t.Parallel()

	advised := Evaluate(Dependencies{}, sedInPlace)
	assert.Empty(t, advised.Deny)
	assert.True(t, advised.demoted)
	assert.Equal(t, hint.MarkerKind(denyRuleSedInPlace), advised.Kind)

	assert.Equal(t, denyRuleSedInPlace, Evaluate(withSetting(string(denyRuleSedInPlace), builtin.Deny), sedInPlace).Rule.Name)
	assert.Equal(t, ShellVerdict{}, Evaluate(withSetting(string(denyRuleSedInPlace), builtin.Off), sedInPlace))
}

func TestDemotedDenyDoesNotHideALaterDeny(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		sedInPlace + " && git reset --hard",
		"git add -A && git reset --hard",
		"git add -A && git stash",
	} {
		v := Evaluate(Dependencies{}, command)
		assert.Equal(t, denyRuleWholeTree, v.Rule.Name, command)
		assert.NotEmpty(t, v.Deny, command)
	}
}

func TestDemotedDenyOutranksAdvisories(t *testing.T) {
	t.Parallel()

	v := Evaluate(Dependencies{}, sedInPlace+" && git commit -m x")
	assert.True(t, v.demoted, "the held advice outranks the commit advisory")
	assert.Equal(t, hint.MarkerKind(denyRuleSedInPlace), v.Kind)

	off := Evaluate(withSetting(string(denyRuleSedInPlace), builtin.Off), sedInPlace+" && git commit -m x")
	assert.Equal(t, advisoryStageClassify, off.Kind, "an off rule leaves the line to the advisory")
}

func TestDemotedDenyKeepsThePushGate(t *testing.T) {
	t.Parallel()

	v := Evaluate(Dependencies{}, sedInPlace+" && git push")
	assert.Equal(t, advisoryPushGate, v.Rule.Name, "Judge upgrades the push gate, so no advice may hide it")
}

func TestStrongerRanks(t *testing.T) {
	t.Parallel()
	deny := ShellVerdict{Deny: "d", Rule: denyRule{Name: denyRuleWholeTree}}
	demoted := ShellVerdict{Context: "h", Kind: "sed-in-place", demoted: true}
	push := ShellVerdict{Context: "p", Rule: denyRule{Name: advisoryPushGate}}
	advisory := ShellVerdict{Context: "a", Kind: advisorySourceRead}

	for _, tt := range []struct {
		name       string
		a, b, want ShellVerdict
	}{
		{"deny over demoted", demoted, deny, deny},
		{"deny kept over demoted", deny, demoted, deny},
		{"demoted over advisory", advisory, demoted, demoted},
		{"demoted kept over advisory", demoted, advisory, demoted},
		{"advisory over silence", ShellVerdict{}, advisory, advisory},
		{"push gate kept over demoted", push, demoted, push},
		{"first demoted kept", demoted, ShellVerdict{Context: "h2", Kind: "raw-tool", demoted: true}, demoted},
	} {
		assert.Equal(t, tt.want, stronger(tt.a, tt.b), tt.name)
	}
}

func TestRankGraded(t *testing.T) {
	t.Parallel()
	advisory := ShellVerdict{Context: "a", Kind: advisorySourceRead}
	deny := ShellVerdict{Deny: "d", Rule: denyRule{Name: denyRuleWholeTree}}

	got := Dependencies{}.rankGraded(advisory, denyRuleSiblingCheckout, "relocated", rankSiblingCheckout)
	assert.True(t, got.demoted, "a demoted sibling checkout outranks an advisory")
	assert.Equal(t, deny, Dependencies{}.rankGraded(deny, denyRuleSiblingCheckout, "relocated", rankSiblingCheckout))

	strictDeps := withSetting(string(denyRuleSiblingCheckout), builtin.Deny)
	assert.Equal(t, denyRuleSiblingCheckout, strictDeps.rankGraded(advisory, denyRuleSiblingCheckout, "relocated", rankSiblingCheckout).Rule.Name)
	assert.Equal(t, advisory, withSetting(string(denyRuleSiblingCheckout), builtin.Off).rankGraded(advisory, denyRuleSiblingCheckout, "relocated", rankSiblingCheckout))
}

func TestReadNavigationLinesComeFromTheWorkspace(t *testing.T) {
	t.Parallel()
	root := readFixture(t)
	deps := readDeps(root)

	// No lines declared: every whole read of a mapped file is judged, however short.
	v, ok := readVerdictAt(deps, root, `cat internal/store/small.go`, DialectBash)
	require.True(t, ok)
	assert.Equal(t, denyRuleReadNavigation, v.Rule.Name)

	deps.Builtins = map[string]builtin.Setting{string(denyRuleReadNavigation): {Decision: builtin.Advise, Lines: 120}}
	_, ok = readVerdictAt(deps, root, `cat internal/store/small.go`, DialectBash)
	assert.False(t, ok, "under the declared lines")
	v, ok = readVerdictAt(deps, root, `cat internal/store/store.go`, DialectBash)
	require.True(t, ok)
	assert.Equal(t, denyRuleReadNavigation, v.Rule.Name, "past the declared lines")
}
