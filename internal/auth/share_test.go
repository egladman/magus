package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// The console's share picker opens on this lifetime too (desktop/share.ts).
func TestDefaultShareLifetimeIsFifteenMinutes(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 15*time.Minute, DefaultShareTTL)
}

func TestMintShareVerifiesOnlyItsOwnSecret(t *testing.T) {
	t.Parallel()
	secret, tok, err := MintShare(job.AccessConsole, 10*time.Minute)
	require.NoError(t, err)
	kind, ok := credentialKind(secret.Reveal())
	require.True(t, ok)
	assert.Equal(t, job.KindShare, kind)

	now := time.Now()
	assert.True(t, tok.Verify(secret.Reveal(), now))
	other, _, err := MintShare(job.AccessConsole, 10*time.Minute)
	require.NoError(t, err)
	assert.False(t, tok.Verify(other.Reveal(), now), "another link's secret")
	assert.False(t, tok.Verify(secret.Reveal(), tok.Expires.Add(time.Second)), "after expiry")
	assert.False(t, ShareToken{}.Verify(secret.Reveal(), now), "the zero token verifies nothing")
	assert.Equal(t, job.Credential{Kind: job.KindShare, ID: tok.ID(), Grant: job.AccessViewer}, tok.Credential())
}

// The share door follows the minting rule: a minter below the share's grant is refused, and
// the lifetime is bounded to a day with no clamp at either end.
func TestMintShareFollowsTheMintingRule(t *testing.T) {
	t.Parallel()
	for _, minter := range []job.Access{{}, job.AccessConnector, {Tokens: job.LevelWrite}} {
		_, _, err := MintShare(minter, time.Hour)
		assert.ErrorIs(t, err, ErrExceedsGrant, minter.String())
	}
	for _, minter := range []job.Access{job.AccessViewer, job.AccessConsole, job.AccessOperator} {
		_, _, err := MintShare(minter, time.Hour)
		require.NoError(t, err, minter.String())
	}
	for _, ttl := range []time.Duration{0, -time.Minute, 59 * time.Second, MaxShareTTL + time.Second, 90 * 24 * time.Hour} {
		_, _, err := MintShare(job.AccessOperator, ttl)
		assert.ErrorIs(t, err, ErrShareLifetime, ttl.String())
		assert.ErrorIs(t, err, types.TokenLifetimeOutOfRange, "one code for a token or a link outside its bound: %s", ttl)
	}
	_, tok, err := MintShare(job.AccessOperator, MaxShareTTL)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(MaxShareTTL), tok.Expires, time.Second)
	assert.Equal(t, 24*time.Hour, MaxShareTTL, "the owner's decision: a share link lives a day at most")
}
