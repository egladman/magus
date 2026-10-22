package builtin

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaults(t *testing.T) {
	got := Defaults()
	assert.Len(t, got, 119)
	assert.NotContains(t, got, "stdin-closed")
	assert.NotContains(t, got, "spawn-principal-for-economy")
	want := map[string]Decision{
		"compat-marker":        Advise,
		"read-navigation":      Deny,
		"brief-command":        Deny,
		"stage-all":            Deny,
		"whole-tree":           Deny,
		"lease-write":          Deny,
		"lease-checkout":       Deny,
		"push-ungated":         Deny,
		"human-only":           Deny,
		"lease-state":          Advise,
		"leased-path":          Advise,
		"split-run":            Advise,
		"push-gate":            Advise,
		"revert-classify":      Advise,
		"graph-pipe":           Deny,
		"capture-filter":       Deny,
		"gate-repeat":          Deny,
		"sleep-poll":           Deny,
		"busy-wait":            Deny,
		"process-poll":         Deny,
		"read-paging":          Deny,
		"brief-plain-commands": Deny,
		"monitor-duplicate":    Deny,
		"monitor-pile":         Deny,
		"spawn-waiter":         Deny,
		"spawn-nested-waiter":  Deny,
		"heredoc-write":        Deny,
		"hooks-off":            Deny,
		"spawn-duplicate":      Deny,
		"background-left":      Deny,
		"background-unbounded": Deny,
		"unjudged-tool":        Deny,
		"unread-command":       Deny,
		"guard-unfinished":     Advise,

		"orchestrator-groundwork": Deny,
		"spawn-above-parent-tier": Deny,

		"root-context-budget":    Off,
		"root-heavy-command":     Off,
		"root-result-cap":        Off,
		"root-repeat":            Off,
		"spawn-names-model":      Off,
		"spawn-above-model-tier": Off,
		"stall":                  Off,

		"model-tier-context-cap": Deny,
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
	want["compat-marker"] = Setting{Decision: Advise, Markers: []string{"compat(until:", "Deprecated:"}}
	assert.Equal(t, want, got)
}

// A workspace policy that fails to load leaves the built-in defaults, so the timer rules
// must refuse there, and no profile level may soften them.
func TestTimerRulesDenyWhenNoWorkspacePolicyLoads(t *testing.T) {
	timers := []string{"busy-wait", "process-poll", "sleep-poll"}

	resolved, err := Resolve(nil)
	require.NoError(t, err)
	for _, name := range timers {
		assert.Equal(t, Deny, Defaults()[name], name)
		assert.Equal(t, Setting{Decision: Deny}, resolved[name], name)
	}

	for _, level := range types.GuardProfile("").Values() {
		declared, err := ResolveProfile(types.GuardProfile(level), nil)
		require.NoError(t, err, level)
		profiled, err := Resolve(declared)
		require.NoError(t, err, level)
		for _, name := range timers {
			assert.Equal(t, Deny, profiled[name].Decision, "%s at %s", name, level)
		}
	}
}

// A rule's own markers fill a declaration that names none, so turning the rule on never
// turns it deaf; a declared list replaces them.
func TestResolveFillsTheMarkersARuleTakes(t *testing.T) {
	got, err := Resolve(map[string]Setting{"compat-marker": {Decision: Advise}})
	require.NoError(t, err)
	assert.Equal(t, []string{"compat(until:", "Deprecated:"}, got["compat-marker"].Markers)
	got["compat-marker"].Markers[0] = "edited"

	declared, err := Resolve(map[string]Setting{"compat-marker": {Decision: Off, Markers: []string{"legacy:"}}})
	require.NoError(t, err)
	assert.Equal(t, Setting{Decision: Off, Markers: []string{"legacy:"}}, declared["compat-marker"])

	again, err := Resolve(nil)
	require.NoError(t, err)
	assert.Equal(t, "compat(until:", again["compat-marker"].Markers[0], "Resolve hands out a copy")
}

func TestResolveChecksMarkers(t *testing.T) {
	for _, tc := range []struct {
		name string
		rule string
		s    Setting
		want string
	}{
		{"markers on a rule without it", "stage-all", Setting{Decision: Advise, Markers: []string{"x"}},
			`built-in rule "stage-all" takes no markers parameter; only compat-marker does`},
		{"a blank marker", "compat-marker", Setting{Decision: Advise, Markers: []string{"compat(until:", " "}},
			`built-in rule "compat-marker": a marker is blank; give the text to notice, or leave markers out`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolve(map[string]Setting{tc.rule: tc.s})
			var de *types.DiagnosticError
			require.ErrorAs(t, err, &de)
			assert.Equal(t, types.GuardRuleMisdeclared, de.Code)
			assert.Equal(t, tc.want, de.Msg)
		})
	}
}

// Of two lists at one decision the union stands, since more markers notice more.
func TestStricterJoinsMarkers(t *testing.T) {
	a := Setting{Decision: Advise, Markers: []string{"compat(until:", "Deprecated:"}}
	b := Setting{Decision: Advise, Markers: []string{"Deprecated:", "legacy:"}}
	assert.Equal(t, []string{"compat(until:", "Deprecated:", "legacy:"}, Stricter(a, b).Markers)
	assert.Equal(t, []string{"Deprecated:", "legacy:", "compat(until:"}, Stricter(b, a).Markers)
	assert.Equal(t, Setting{Decision: Off}, Stricter(Setting{Decision: Off}, Setting{Decision: Off}))
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
		{"removed rule", map[string]Setting{"stdin-closed": {Decision: Deny}},
			coded{types.GuardRuleMisdeclared, `built-in rule "stdin-closed" was removed; delete it from the declaration`}},
		{"renamed rule", map[string]Setting{"spawn-principal-for-economy": {Decision: Advise}},
			coded{types.GuardRuleMisdeclared, `built-in rule "spawn-principal-for-economy" was renamed to "spawn-above-model-tier"; declare it under that name`}},
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

func TestResolveChecksEachParameter(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		rule string
		s    Setting
		want string
	}{
		{"tokens on a rule without it", "stage-all", Setting{Decision: Advise, Tokens: 1000},
			`built-in rule "stage-all" takes no tokens parameter; only root-context-budget does`},
		{"tokens below 1", rootContextBudget, Setting{Decision: Advise, Tokens: -1},
			`built-in rule "root-context-budget": tokens is -1; give 1 or more, or leave it out`},
		{"calls below 2", rootHeavyCommand, Setting{Decision: Advise, Calls: 1},
			`built-in rule "root-heavy-command": calls is 1; give 2 or more, or leave it out`},
		{"chars below 1000", rootResultCap, Setting{Decision: Advise, Chars: 999},
			`built-in rule "root-result-cap": chars is 999; give 1000 or more, or leave it out`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolve(map[string]Setting{tc.rule: tc.s})
			var de *types.DiagnosticError
			require.ErrorAs(t, err, &de)
			assert.Equal(t, types.GuardRuleMisdeclared, de.Code)
			assert.Equal(t, tc.want, de.Msg)
		})
	}

	ok := map[string]Setting{
		rootContextBudget: {Decision: Advise, Tokens: 1_000_000},
		rootHeavyCommand:  {Decision: Advise, Calls: 2},
		rootResultCap:     {Decision: Advise, Chars: 1000},
	}
	got, err := Resolve(ok)
	require.NoError(t, err)
	for name, s := range ok {
		assert.Equal(t, s, got[name], "each parameter at its floor loads")
	}
}

// No window is built in to bound tokens by: a value no model could reach loads, since only
// the workspace's own model rows know the largest window.
func TestResolveDoesNotBoundTokensByAGuessedWindow(t *testing.T) {
	t.Parallel()
	got, err := Resolve(map[string]Setting{rootContextBudget: {Decision: Advise, Tokens: 50_000_000}})
	require.NoError(t, err)
	assert.Equal(t, 50_000_000, got[rootContextBudget].Tokens)
}

// Every parameter a rule takes is one the parser can set, or markers.
func TestParamsNameOnlyKnownParameters(t *testing.T) {
	t.Parallel()
	for rule, names := range params {
		for _, name := range names {
			if name == "markers" {
				continue
			}
			var s Setting
			assert.True(t, s.SetParam(name, 1), "%s takes %s, which no parameter spec names", rule, name)
		}
	}
}

func TestSetParam(t *testing.T) {
	t.Parallel()
	var s Setting
	for i, p := range paramSpecs {
		require.True(t, s.SetParam(p.name, i+1), p.name)
	}
	assert.Equal(t, Setting{Lines: 1, Tokens: 2, Calls: 3, Chars: 4}, s)
	assert.False(t, s.SetParam("markers", 1), "markers are a list, not a number")
	assert.False(t, s.SetParam("decision", 1))
}

func TestResolveNamesEveryTakerOfAParameter(t *testing.T) {
	_, err := Resolve(map[string]Setting{"stage-all": {Decision: Deny, Calls: 3}})
	var de *types.DiagnosticError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, `built-in rule "stage-all" takes no calls parameter; only root-heavy-command, root-repeat, stall do`, de.Msg)
}

// A rule that only ever explains cannot be declared to refuse.
func TestResolveRefusesADenyOnARuleThatNeverDenies(t *testing.T) {
	for _, name := range []string{"compat-marker", "leased-path", "split-run", "root-context-budget", "root-heavy-command", "root-repeat", "root-result-cap", "spawn-above-model-tier", "stall"} {
		_, err := Resolve(map[string]Setting{name: {Decision: Deny}})
		var de *types.DiagnosticError
		require.ErrorAs(t, err, &de, name)
		assert.Equal(t, types.GuardRuleMisdeclared, de.Code)
		assert.Equal(t, `built-in rule "`+name+`" is a notice and never refuses; give off or advise`, de.Msg)
		assert.NotEmpty(t, types.DiagnosticRationale(err), name)

		_, err = Resolve(map[string]Setting{name: {Decision: Advise}})
		require.NoError(t, err, name)
	}
}

func TestVerdicts(t *testing.T) {
	adviseOnly := []types.GuardDecision{types.GuardAdvise}
	both := []types.GuardDecision{types.GuardAdvise, types.GuardDeny}
	assert.Equal(t, adviseOnly, Verdicts("stall"), "a rule that never refuses")
	assert.Equal(t, adviseOnly, Verdicts("guard-unfinished"), "a fixed rule")
	assert.Equal(t, both, Verdicts("stage-all"), "a recoverable rule a workspace may lower")
	assert.Equal(t, both, Verdicts("graph-pipe"), "an advisory that names a command")
	assert.Equal(t, adviseOnly, Verdicts("leased-path"), "a notice")
	assert.Equal(t, both, Verdicts("lease-write"), "a deny rule")
	assert.Equal(t, adviseOnly, Verdicts("root-repeat"), "a meter only advises")
	assert.Equal(t, both, Verdicts("spawn-names-model"), "an off rule a level raises to deny")
	assert.Equal(t, both, Verdicts("orchestrator-groundwork"), "a rule no level names")
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

// At one decision the higher level's figures stand, since they are the lower; a level
// beats none, which reads the rule's own figures.
func TestStricterKeepsTheHigherLevel(t *testing.T) {
	standard := Setting{Decision: Advise, Profile: types.GuardProfileStandard}
	strict := Setting{Decision: Advise, Profile: types.GuardProfileStrict}
	declared := Setting{Decision: Advise}
	assert.Equal(t, strict, Stricter(standard, strict))
	assert.Equal(t, strict, Stricter(strict, standard))
	assert.Equal(t, standard, Stricter(declared, standard))
	assert.Equal(t, standard, Stricter(standard, declared))
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
	t.Parallel()
	strict := len(profiles[types.GuardProfileStrict])
	declared := map[string]Setting{rootRepeat: {Decision: Off}}
	expanded, err := ResolveProfile(types.GuardProfileStrict, declared)
	require.NoError(t, err)
	got, err := Resolve(expanded)
	require.NoError(t, err)
	assert.Equal(t, Setting{Decision: Advise, Profile: types.GuardProfileStrict}, got[rootContextBudget], "a level stamps its rows")
	assert.Equal(t, Setting{Decision: Off}, got[rootRepeat], "the declaration overrides the profile")
	assert.Equal(t, Setting{Decision: Deny}, got["raw-tool"], "a rule neither names keeps its default")
	assert.Len(t, declared, 1, "ResolveProfile leaves declared alone")
	assert.Len(t, profiles[types.GuardProfileStrict], strict, "ResolveProfile leaves the row alone")
	for _, s := range profiles[types.GuardProfileStrict] {
		assert.Empty(t, s.Profile, "ResolveProfile stamps a copy, never the row")
	}

	light, err := ResolveProfile("", nil)
	require.NoError(t, err)
	assert.Len(t, light, len(profiles[types.GuardProfileLight]), "unset is light")
	for name, s := range light {
		assert.Equal(t, types.GuardProfileLight, s.Profile, name)
	}

	_, err = ResolveProfile("max", nil)
	var de *types.DiagnosticError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, types.GuardRuleMisdeclared, de.Code)
	assert.Equal(t, `unknown guard profile "max"; give one of off, light, standard, strict`, de.Msg)
}
