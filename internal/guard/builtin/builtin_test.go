package builtin

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaults(t *testing.T) {
	got := Defaults()
	assert.Len(t, got, 80)
	assert.Equal(t, map[string]Decision{
		"read-navigation": Advise,
		"brief-command":   Advise,
		"stage-all":       Advise,
		"whole-tree":      Deny,
		"lease-write":     Deny,
		"push-ungated":    Deny,
		"lease-state":     Advise,
		"push-gate":       Advise,
	}, map[string]Decision{
		"read-navigation": got["read-navigation"],
		"brief-command":   got["brief-command"],
		"stage-all":       got["stage-all"],
		"whole-tree":      got["whole-tree"],
		"lease-write":     got["lease-write"],
		"push-ungated":    got["push-ungated"],
		"lease-state":     got["lease-state"],
		"push-gate":       got["push-gate"],
	})

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
