package bindings

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A rule's name rides its verdict to the guard; an unnamed verdict carries none.
func TestGuardVerdictCarriesTheRuleName(t *testing.T) {
	rule, err := loadCommandRule(t, `
import "magus";

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    if (req.command == "named") {
        return GuardVerdict{decision = "deny", reason = "Lead with the outcome.", rule = "pull-request-text"};
    }
    return magus\guard.deny("unnamed");
});
`)
	require.NoError(t, err)
	facts := hint.NewGate(t.TempDir(), "claude-code/s1")
	cases := map[string]types.GuardVerdict{
		"named": {Decision: types.GuardDeny, Reason: "Lead with the outcome.", Rule: "pull-request-text"},
		"other": {Decision: types.GuardDeny, Reason: "unnamed"},
	}
	for command, want := range cases {
		got, err := rule(t.Context(), types.CommandRequest{Command: command}, facts)
		require.NoError(t, err, command)
		assert.Equal(t, want, got, command)
	}
}

// deny's and advise's opts name the rule.
func TestVerdictRuleOpt(t *testing.T) {
	opts := vm.NewMap()
	opts.MapSet("rule", vm.StringValue("pull-request-text"))
	got, err := verdictRuleOpt("deny", opts)
	require.NoError(t, err)
	assert.Equal(t, "pull-request-text", got)

	_, err = verdictRuleOpt("advise", vm.StringValue("pull-request-text"))
	require.ErrorContains(t, err, `magus\guard.advise: opts must be a {rule: str} record`)
}

// A name that cannot report as workspace:<name> is a misdeclaration, whether it reaches the
// guard through deny's opts or a GuardVerdict the rule built itself.
func TestGuardVerdictRuleNameIsValidated(t *testing.T) {
	guardMap := vm.NewMap()
	registerVerdictMembers(context.Background(), nil, nil, guardMap)
	deny := requireDirect(t, guardMap, "deny")
	opts := func(key string, v vm.Value) vm.Value {
		m := vm.NewMap()
		m.MapSet(key, v)
		return m
	}

	err := callVoidDirect(t, deny, vm.StringValue("why"), opts("rule", vm.StringValue("Pull_Request")))
	require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
	require.ErrorContains(t, err, `guard rule name "Pull_Request" is not lowercase letters and digits joined by single hyphens, as in "pull-request"`)

	err = callVoidDirect(t, deny, vm.StringValue("why"), opts("rule", vm.StringValue("command")))
	require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))

	err = callVoidDirect(t, deny, vm.StringValue("why"), opts("rule", vm.IntValue(1)))
	require.ErrorContains(t, err, `magus\guard.deny: "rule" must be a string`)

	err = callVoidDirect(t, deny, vm.StringValue("why"), opts("name", vm.StringValue("x")))
	require.ErrorContains(t, err, `magus\guard.deny: unknown opts field "name"`)

	literal := vm.NewMap()
	literal.MapSet("decision", vm.StringValue("deny"))
	literal.MapSet("reason", vm.StringValue("why"))
	literal.MapSet("rule", vm.StringValue("NotKebab"))
	_, err = decodeGuardVerdict(`magus\guard.command`, literal)
	require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
}
