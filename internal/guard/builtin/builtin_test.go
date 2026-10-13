package builtin

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaults(t *testing.T) {
	got := Defaults()
	assert.Len(t, got, 101)
	want := map[string]Decision{
		"read-navigation":      Advise,
		"brief-command":        Advise,
		"stage-all":            Advise,
		"whole-tree":           Deny,
		"lease-write":          Deny,
		"push-ungated":         Deny,
		"lease-state":          Advise,
		"push-gate":            Advise,
		"sleep-poll":           Advise,
		"read-paging":          Advise,
		"brief-plain-commands": Advise,
		"monitor-duplicate":    Advise,
		"monitor-pile":         Advise,
		"spawn-waiter":         Advise,
		"spawn-nested-waiter":  Advise,
		"heredoc-write":        Deny,
		"hooks-off":            Deny,
		"spawn-duplicate":      Deny,
		"background-left":      Deny,
		"background-unbounded": Deny,
	}
	have := map[string]Decision{}
	for name := range want {
		have[name] = got[name]
	}
	assert.Equal(t, want, have)

	got["whole-tree"] = Off
	assert.Equal(t, Deny, Defaults()["whole-tree"], "Defaults hands out a copy")
}

func TestResolveFillsDefaults(t *testing.T) {
	got, err := Resolve(map[string]Setting{
		"read-navigation": {Decision: Deny, Lines: 120},
		"whole-tree":      {Decision: Off},
	})
	require.NoError(t, err)

	want := map[string]Setting{}
	for name, d := range Defaults() {
		want[name] = Setting{Decision: d}
	}
	want["read-navigation"] = Setting{Decision: Deny, Lines: 120}
	want["whole-tree"] = Setting{Decision: Off}
	assert.Equal(t, want, got)
}

func TestResolveRefusesMisdeclarations(t *testing.T) {
	type coded struct {
		Code types.DiagnosticCode
		Msg  string
	}
	for _, tc := range []struct {
		name     string
		declared map[string]Setting
		want     coded
	}{
		{"unknown name with a near one", map[string]Setting{"read-navigaton": {Decision: Deny}},
			coded{types.GuardRuleMisdeclared, `unknown built-in rule "read-navigaton"; did you mean "read-navigation"?`}},
		{"unknown name with nothing near", map[string]Setting{"frobnicate": {Decision: Deny}},
			coded{types.GuardRuleMisdeclared, "unknown built-in rule \"frobnicate\"; `magus describe rules` lists every name"}},
		{"bad decision", map[string]Setting{"stage-all": {Decision: "block"}},
			coded{types.GuardRuleMisdeclared, `built-in rule "stage-all": decision "block" is not off, advise or deny`}},
		{"empty decision", map[string]Setting{"stage-all": {}},
			coded{types.GuardRuleMisdeclared, `built-in rule "stage-all": decision "" is not off, advise or deny`}},
		{"lines on a rule without it", map[string]Setting{"stage-all": {Decision: Deny, Lines: 50}},
			coded{types.GuardRuleMisdeclared, `built-in rule "stage-all" takes no lines parameter; only read-navigation does`}},
		{"lines below 1", map[string]Setting{"read-navigation": {Decision: Deny, Lines: -3}},
			coded{types.GuardRuleMisdeclared, `built-in rule "read-navigation": lines is -3; give 1 or more, or leave it out`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.declared)
			assert.Nil(t, got)
			var de *types.DiagnosticError
			require.ErrorAs(t, err, &de)
			assert.Equal(t, tc.want, coded{de.Code, de.Msg})
		})
	}
}

// A notice that reports on the workspace's own rules cannot be set to anything: off would
// hide that a rule judged nothing, deny would refuse a call over it, and advise is the
// value it already has.
func TestResolveRefusesAFixedRule(t *testing.T) {
	for name, what := range map[string]string{
		"workspace-command-failed": "a command rule",
		"workspace-spawn-failed":   "a spawn rule",
		"workspace-write-failed":   "a write rule",
		"guard-unfinished":         "a guard check",
	} {
		for _, d := range []Decision{Off, Advise, Deny} {
			t.Run(name+"/"+string(d), func(t *testing.T) {
				got, err := Resolve(map[string]Setting{name: {Decision: d}})
				assert.Nil(t, got)
				var de *types.DiagnosticError
				require.ErrorAs(t, err, &de)
				assert.Equal(t, types.GuardRuleMisdeclared, de.Code)
				assert.Equal(t, `built-in rule "`+name+`" cannot be set; remove it from the declaration`, de.Msg)
				assert.Equal(t, "it is how a workspace learns that "+what+
					" of its own judged nothing, so it can be neither silenced nor promoted", types.DiagnosticRationale(err))
			})
		}
	}
}

func TestFixedRulesAreCompiledRules(t *testing.T) {
	for name := range fixed {
		assert.Contains(t, defaults, name)
		assert.Equal(t, Advise, defaults[name], name)
		assert.True(t, Fixed(name))
	}
	assert.False(t, Fixed("stage-all"))

	got, err := Resolve(nil)
	require.NoError(t, err)
	for name := range fixed {
		assert.Equal(t, Setting{Decision: Advise}, got[name], "an undeclared fixed rule resolves to its default")
	}
}

func TestResolveReportsEveryMisdeclaration(t *testing.T) {
	_, err := Resolve(map[string]Setting{
		"stage-all":  {Decision: "block"},
		"whole-tre":  {Decision: Off},
		"whole-tree": {Decision: Off},
	})
	var joined interface{ Unwrap() []error }
	require.ErrorAs(t, err, &joined)
	var msgs []string
	for _, e := range joined.Unwrap() {
		var de *types.DiagnosticError
		require.ErrorAs(t, e, &de)
		msgs = append(msgs, de.Msg)
	}
	assert.Equal(t, []string{
		`built-in rule "stage-all": decision "block" is not off, advise or deny`,
		`unknown built-in rule "whole-tre"; did you mean "whole-tree"?`,
	}, msgs)
}

func TestStricter(t *testing.T) {
	settings := []Setting{
		{Decision: Off, Lines: 200},
		{Decision: Off},
		{Decision: Advise, Lines: 200},
		{Decision: Advise, Lines: 120},
		{Decision: Advise},
		{Decision: Deny, Lines: 200},
		{Decision: Deny, Lines: 120},
		{Decision: Deny},
	}
	// want[i][j] indexes settings, which run from least to most strict: an unset Lines
	// judges every whole read, so it is stricter than any threshold.
	want := [][]int{
		{0, 1, 2, 3, 4, 5, 6, 7},
		{1, 1, 2, 3, 4, 5, 6, 7},
		{2, 2, 2, 3, 4, 5, 6, 7},
		{3, 3, 3, 3, 4, 5, 6, 7},
		{4, 4, 4, 4, 4, 5, 6, 7},
		{5, 5, 5, 5, 5, 5, 6, 7},
		{6, 6, 6, 6, 6, 6, 6, 7},
		{7, 7, 7, 7, 7, 7, 7, 7},
	}
	for i, a := range settings {
		for j, b := range settings {
			assert.Equal(t, settings[want[i][j]], Stricter(a, b), "Stricter(%+v, %+v)", a, b)
		}
	}
}

// testRule gives one compiled rule every parameter for the length of a test, since no
// shipped rule takes tokens, calls or chars yet.
func testRule(t *testing.T) string {
	t.Helper()
	const name = "test-cost-rule"
	defaults[name] = Advise
	params[name] = []string{"tokens", "calls", "chars"}
	t.Cleanup(func() {
		delete(defaults, name)
		delete(params, name)
	})
	return name
}

func TestResolveChecksEachParameter(t *testing.T) {
	rule := testRule(t)
	for _, tc := range []struct {
		name string
		rule string
		s    Setting
		want string
	}{
		{"tokens on a rule without it", "stage-all", Setting{Decision: Advise, Tokens: 1000},
			`built-in rule "stage-all" takes no tokens parameter; only test-cost-rule does`},
		{"tokens below 1", rule, Setting{Decision: Advise, Tokens: -1},
			`built-in rule "test-cost-rule": tokens is -1; give 1 or more, or leave it out`},
		{"tokens past every window", rule, Setting{Decision: Advise, Tokens: 1_000_001},
			`built-in rule "test-cost-rule": tokens is 1000001, past the largest context window any known model has (1000000); give 1000000 or less, or leave it out`},
		{"calls below 2", rule, Setting{Decision: Deny, Calls: 1},
			`built-in rule "test-cost-rule": calls is 1; give 2 or more, or leave it out`},
		{"chars below 1000", rule, Setting{Decision: Advise, Chars: 999},
			`built-in rule "test-cost-rule": chars is 999; give 1000 or more, or leave it out`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolve(map[string]Setting{tc.rule: tc.s})
			var de *types.DiagnosticError
			require.ErrorAs(t, err, &de)
			assert.Equal(t, types.GuardRuleMisdeclared, de.Code)
			assert.Equal(t, tc.want, de.Msg)
		})
	}

	ok := Setting{Decision: Deny, Tokens: 1_000_000, Calls: 2, Chars: 1000}
	got, err := Resolve(map[string]Setting{rule: ok})
	require.NoError(t, err)
	assert.Equal(t, ok, got[rule])
}

func TestResolveNamesNoTakerForAnUnusedParameter(t *testing.T) {
	_, err := Resolve(map[string]Setting{"stage-all": {Decision: Deny, Calls: 3}})
	var de *types.DiagnosticError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, `built-in rule "stage-all" takes no calls parameter; no rule does`, de.Msg)
}

// At one decision each threshold takes the lower of the two, and an unset one yields:
// unset defers to the rule's own figure, which neither side stated.
func TestStricterMergesThresholds(t *testing.T) {
	a := Setting{Decision: Advise, Tokens: 200_000, Calls: 8}
	b := Setting{Decision: Advise, Tokens: 150_000, Chars: 20_000}
	want := Setting{Decision: Advise, Tokens: 150_000, Calls: 8, Chars: 20_000}
	assert.Equal(t, want, Stricter(a, b))
	assert.Equal(t, want, Stricter(b, a))

	deny := Setting{Decision: Deny, Tokens: 900_000}
	assert.Equal(t, deny, Stricter(a, deny), "a stricter decision wins whole")
}

// Every level is defined, and each row resolves as a declaration would, so none sets a
// fixed rule or a parameter its rule does not take.
func TestProfilesAreDefinedAndResolve(t *testing.T) {
	for _, level := range types.GuardProfile("").Values() {
		row, ok := profiles[types.GuardProfile(level)]
		require.True(t, ok, level)
		_, err := Resolve(row)
		require.NoError(t, err, level)
	}
	assert.Len(t, profiles, len(types.GuardProfile("").Values()))
}

// A profile sets the rules it names; a rule the workspace declares overrides its
// profile's setting, and every other rule keeps its default.
func TestResolveProfileExpandsThenDeclared(t *testing.T) {
	rule := testRule(t)
	saved := profiles[types.GuardProfileStrict]
	profiles[types.GuardProfileStrict] = map[string]Setting{
		rule:        {Decision: Deny, Calls: 8},
		"stage-all": {Decision: Deny},
	}
	t.Cleanup(func() { profiles[types.GuardProfileStrict] = saved })

	declared := map[string]Setting{"stage-all": {Decision: Off}}
	expanded, err := ResolveProfile(types.GuardProfileStrict, declared)
	require.NoError(t, err)
	got, err := Resolve(expanded)
	require.NoError(t, err)
	assert.Equal(t, Setting{Decision: Deny, Calls: 8}, got[rule])
	assert.Equal(t, Setting{Decision: Off}, got["stage-all"], "the declaration overrides the profile")
	assert.Equal(t, Setting{Decision: Advise}, got["raw-tool"], "a rule neither names keeps its default")
	assert.Len(t, declared, 1, "ResolveProfile leaves declared alone")
	assert.Len(t, profiles[types.GuardProfileStrict], 2, "ResolveProfile leaves the row alone")

	light, err := ResolveProfile("", nil)
	require.NoError(t, err)
	assert.Equal(t, profiles[types.GuardProfileLight], light, "unset is light")

	_, err = ResolveProfile("max", nil)
	var de *types.DiagnosticError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, types.GuardRuleMisdeclared, de.Code)
	assert.Equal(t, `unknown guard profile "max"; give one of off, light, standard, strict`, de.Msg)
}
