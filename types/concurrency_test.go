package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each profile is a width relative to the machine, never below one slot. balanced is
// the pre-profile default exactly, so an unset profile changes nothing on upgrade.
func TestConcurrencyProfileWidth(t *testing.T) {
	cases := []struct {
		profile ConcurrencyProfile
		cores   int
		want    int
	}{
		{"", 10, 8}, {ProfileBalanced, 10, 8}, {ProfileBalanced, 4, 4},
		{ProfileConservative, 10, 5}, {ProfileConservative, 1, 1},
		{ProfileAggressive, 10, 10}, {ProfileAggressive, 32, 32},
		{ProfileBalanced, 0, 1},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, tc.profile.Width(tc.cores), "%q on %d cores", tc.profile, tc.cores)
	}
}

// UnmarshalText is the one door from a name to a profile, so it is where a misspelling
// stops, and an empty name stays unset.
func TestConcurrencyProfileUnmarshalText(t *testing.T) {
	for _, name := range []string{"", "conservative", "balanced", "aggressive"} {
		var p ConcurrencyProfile
		require.NoError(t, p.UnmarshalText([]byte(name)), name)
		assert.Equal(t, ConcurrencyProfile(name), p)
	}

	p := ProfileAggressive
	err := p.UnmarshalText([]byte("turbo"))
	require.EqualError(t, err, `unknown concurrency profile "turbo" (want one of [conservative balanced aggressive])`)
	assert.Equal(t, ProfileAggressive, p, "a refused name leaves the profile as it was")
}
