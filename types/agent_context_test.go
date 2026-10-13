package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProfileAndAgentEnumsUnmarshalText(t *testing.T) {
	for _, name := range []string{"", "main", "sub", "unknown"} {
		var th AgentThread
		require.NoError(t, th.UnmarshalText([]byte(name)), name)
		assert.Equal(t, AgentThread(name), th)
	}
	for _, name := range []string{"", "metered", "quota"} {
		var b AgentBilling
		require.NoError(t, b.UnmarshalText([]byte(name)), name)
		assert.Equal(t, AgentBilling(name), b)
	}
	for _, name := range []string{"", "off", "light", "standard", "strict"} {
		var p GuardProfile
		require.NoError(t, p.UnmarshalText([]byte(name)), name)
		assert.Equal(t, GuardProfile(name), p)
	}

	var th AgentThread
	require.EqualError(t, th.UnmarshalText([]byte("root")), `unknown agent thread "root" (want one of [main sub unknown])`)
	var b AgentBilling
	require.EqualError(t, b.UnmarshalText([]byte("oauth")), `unknown agent billing "oauth" (want one of [metered quota])`)
	var p GuardProfile
	require.EqualError(t, p.UnmarshalText([]byte("max")), `unknown guard profile "max" (want one of [off light standard strict])`)
}

func TestProfileAndAgentEnumsRenderUnset(t *testing.T) {
	assert.Equal(t, "unknown", AgentThread("").String())
	assert.Equal(t, "unset", AgentBilling("").String())
	assert.Equal(t, "light", GuardProfile("").String())
}

// Unset is light, so a workspace that declares nothing ranks with one that declares light,
// above off and below standard.
func TestGuardProfileRank(t *testing.T) {
	assert.Equal(t, 0, GuardProfileOff.Rank())
	assert.Equal(t, 1, GuardProfile("").Rank())
	assert.Equal(t, 1, GuardProfileLight.Rank())
	assert.Equal(t, 2, GuardProfileStandard.Rank())
	assert.Equal(t, 3, GuardProfileStrict.Rank())
	assert.Equal(t, -1, GuardProfile("max").Rank())
}

func TestStricterProfile(t *testing.T) {
	for _, tc := range []struct {
		in   []GuardProfile
		want GuardProfile
	}{
		{nil, GuardProfileLight},
		{[]GuardProfile{""}, GuardProfileLight},
		{[]GuardProfile{GuardProfileOff}, GuardProfileOff},
		{[]GuardProfile{GuardProfileOff, ""}, GuardProfileOff},
		{[]GuardProfile{GuardProfile("").Level(), GuardProfileOff}, GuardProfileLight},
		{[]GuardProfile{GuardProfileStrict, GuardProfileOff}, GuardProfileStrict},
		{[]GuardProfile{GuardProfileStandard, GuardProfileLight}, GuardProfileStandard},
		{[]GuardProfile{"max", GuardProfileOff}, GuardProfileOff},
		{[]GuardProfile{"max"}, GuardProfileLight},
	} {
		assert.Equal(t, tc.want, StricterProfile(tc.in...), "%v", tc.in)
	}
}

func TestModelFactsFactor(t *testing.T) {
	stepped := ModelFacts{PriceStep: 100_000, StepFactor: 5}
	assert.InDelta(t, 1.0, stepped.Factor(100_000), 0, "the step is above the threshold, not at it")
	assert.InDelta(t, 5.0, stepped.Factor(100_001), 0)
	assert.InDelta(t, 1.0, ModelFacts{}.Factor(900_000), 0)
}
