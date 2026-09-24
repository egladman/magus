package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestMintShareVerifiesOnlyItsOwnSecret(t *testing.T) {
	t.Parallel()
	secret, tok, err := MintShare(types.GrantConsole, 10*time.Minute)
	require.NoError(t, err)
	class, ok := classOf(secret)
	require.True(t, ok)
	assert.Equal(t, types.ClassShare, class)

	now := time.Now()
	assert.True(t, tok.Verify(secret, now))
	other, _, err := MintShare(types.GrantConsole, 10*time.Minute)
	require.NoError(t, err)
	assert.False(t, tok.Verify(other, now), "another link's secret")
	assert.False(t, tok.Verify(secret, tok.Expires.Add(time.Second)), "after expiry")
	assert.False(t, ShareToken{}.Verify(secret, now), "the zero token verifies nothing")
	assert.Equal(t, types.Credential{Class: types.ClassShare, ID: tok.ID(), Grant: types.GrantViewer}, tok.Credential())
}

// The share door follows the minting rule: a minter below the share's grant is refused, and
// the lifetime is bounded to a day with no clamp at either end.
func TestMintShareFollowsTheMintingRule(t *testing.T) {
	t.Parallel()
	for _, minter := range []types.Grant{{}, types.GrantConnector, {Tokens: types.LevelWrite}} {
		_, _, err := MintShare(minter, time.Hour)
		assert.ErrorIs(t, err, ErrExceedsGrant, minter.String())
	}
	for _, minter := range []types.Grant{types.GrantViewer, types.GrantConsole, types.GrantOperator} {
		_, _, err := MintShare(minter, time.Hour)
		assert.NoError(t, err, minter.String())
	}
	for _, ttl := range []time.Duration{0, -time.Minute, 59 * time.Second, MaxShareTTL + time.Second, 90 * 24 * time.Hour} {
		_, _, err := MintShare(types.GrantOperator, ttl)
		assert.ErrorIs(t, err, ErrShareLifetime, ttl.String())
		assert.ErrorIs(t, err, types.TokenLifetimeOutOfRange, "one code for a token or a link outside its bound: %s", ttl)
	}
	_, tok, err := MintShare(types.GrantOperator, MaxShareTTL)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(MaxShareTTL), tok.Expires, time.Second)
	assert.Equal(t, 24*time.Hour, MaxShareTTL, "the owner's decision: a share link lives a day at most")
}
