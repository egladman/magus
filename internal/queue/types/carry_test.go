package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rebase carries under every policy, code under none, and the zero policy is rebase
// alone.
func TestCarryPolicyAllows(t *testing.T) {
	for name, tc := range map[string]struct {
		policy CarryPolicy
		want   map[CarryTier]bool
	}{
		"zero":    {nil, map[CarryTier]bool{CarryRebase: true}},
		"default": {DefaultCarryPolicy(), map[CarryTier]bool{CarryRebase: true, CarryGenerated: true, CarryProse: true, CarryCommentOnly: true}},
		"code":    {CarryPolicy{CarryCode, CarryProse}, map[CarryTier]bool{CarryRebase: true, CarryProse: true}},
	} {
		t.Run(name, func(t *testing.T) {
			got := map[CarryTier]bool{}
			for _, tier := range carryTiers {
				if tc.policy.Allows(tier) {
					got[tier] = true
				}
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCheckCarryTier(t *testing.T) {
	for _, name := range []string{"rebase", "generated", "prose", "comment-only"} {
		require.NoError(t, CheckCarryTier(name), name)
	}
	assert.EqualError(t, CheckCarryTier("code"), `"code" never carries an approval: a reviewer has to see a code change (want some of rebase, generated, prose, comment-only)`)
	assert.EqualError(t, CheckCarryTier("docs"), `"docs" is not an approval carry tier (want some of rebase, generated, prose, comment-only)`)
	assert.EqualError(t, CheckCarryTier(""), `"" is not an approval carry tier (want some of rebase, generated, prose, comment-only)`)
}
