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
	for _, name := range []string{"", "reported", "inferred"} {
		var s ThreadSource
		require.NoError(t, s.UnmarshalText([]byte(name)), name)
		assert.Equal(t, ThreadSource(name), s)
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
	var s ThreadSource
	require.EqualError(t, s.UnmarshalText([]byte("guessed")), `unknown thread source "guessed" (want one of [reported inferred])`)
	var b AgentBilling
	require.EqualError(t, b.UnmarshalText([]byte("oauth")), `unknown agent billing "oauth" (want one of [metered quota])`)
	var p GuardProfile
	require.EqualError(t, p.UnmarshalText([]byte("max")), `unknown guard profile "max" (want one of [off light standard strict])`)
}

// A profile renders and encodes as written, so the two never disagree; Effective names the
// level an unset one applies.
func TestGuardProfileRendersAsWritten(t *testing.T) {
	assert.Empty(t, GuardProfile("").String())
	text, err := GuardProfile("").MarshalText()
	require.NoError(t, err)
	assert.Empty(t, text)
	assert.Equal(t, GuardProfileLight, GuardProfile("").Effective())
	assert.Equal(t, GuardProfileOff, GuardProfileOff.Effective())
	assert.Equal(t, "unset", AgentBilling("").String())
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

// The receiver is a level that applies, so unset is light; the argument raises it, so
// unset says nothing and off never lowers it.
func TestGuardProfileStricter(t *testing.T) {
	for _, tc := range []struct {
		p, o GuardProfile
		want GuardProfile
	}{
		{"", "", GuardProfileLight},
		{GuardProfileOff, "", GuardProfileOff},
		{"", GuardProfileOff, GuardProfileLight},
		{GuardProfileOff, GuardProfileLight, GuardProfileLight},
		{GuardProfileStrict, GuardProfileOff, GuardProfileStrict},
		{GuardProfileLight, GuardProfileStandard, GuardProfileStandard},
		{GuardProfileOff, "max", GuardProfileOff},
	} {
		assert.Equal(t, tc.want, tc.p.Stricter(tc.o), "%q.Stricter(%q)", tc.p, tc.o)
	}
}

func TestModelPricingPriceMultiplier(t *testing.T) {
	stepped := ModelPricing{LongContext: &PriceStep{Above: 100_000, Multiplier: 5}}
	assert.InDelta(t, 1.0, stepped.PriceMultiplier(100_000), 0, "the step is above the threshold, not at it")
	assert.InDelta(t, 5.0, stepped.PriceMultiplier(100_001), 0)
	assert.InDelta(t, 1.0, ModelPricing{}.PriceMultiplier(900_000), 0, "a nil LongContext is no step")
}
