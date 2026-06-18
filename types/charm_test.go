package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCharmsStack(t *testing.T) {
	ctx := context.Background()
	assert.False(t, HasCharm(ctx, "write"), "empty context should carry no charms")

	// Multiple charms coexist (stacking) and order is insignificant.
	ctx = WithCharms(ctx, []string{"write", "debug"})
	assert.True(t, HasCharm(ctx, "write"))
	assert.True(t, HasCharm(ctx, "debug"))
	assert.False(t, HasCharm(ctx, "verbose"), "a charm that was not set must be absent")

	// An empty set is a no-op and must not clobber existing charms.
	assert.True(t, HasCharm(WithCharms(ctx, nil), "write"), "WithCharms(nil) must preserve existing charms")
}

// TestHasCharmNormalizes documents that charm matching is case- and
// separator-insensitive on both sides: an active charm stored in one spelling
// matches a query in another, mirroring target-name normalization.
func TestHasCharmNormalizes(t *testing.T) {
	// Active charm declared with odd casing/separator; queried canonically.
	ctx := WithCharms(context.Background(), []string{"No_Cache"})
	assert.True(t, HasCharm(ctx, "no-cache"), "no-cache query must match active No_Cache")

	// And the reverse: canonical active, odd-cased query.
	ctx = WithCharms(context.Background(), []string{"write"})
	assert.True(t, HasCharm(ctx, "WRITE"), "WRITE query must match active write charm")
}

// TestReservedCharms locks in the built-in charm set the typo guard exempts and
// the doctor collision check enumerates: recognition is casing/separator-blind,
// and ReservedCharms hands back an independent copy callers cannot mutate.
func TestReservedCharms(t *testing.T) {
	for _, name := range []string{"rw", "cd", "gha", "RW", "CD", "GHA"} {
		assert.Truef(t, IsReservedCharm(name), "IsReservedCharm(%q)", name)
	}
	assert.False(t, IsReservedCharm("container"))

	got := ReservedCharms()
	require.Equal(t, []string{"rw", "cd", "gha"}, got)
	got[0] = "mutated"
	assert.Equal(t, "rw", ReservedCharms()[0], "ReservedCharms() must return an independent copy")
}

// TestParseTargetNormalizesCharms locks in that the "target:charm" suffix is
// canonicalized at the parse boundary, so everything downstream (cache key, ci
// strip, typo guard) sees one spelling.
func TestParseTargetNormalizesCharms(t *testing.T) {
	got, err := ParseTarget("format:Write,No_Cache")
	require.NoError(t, err)
	assert.Equal(t, []string{"write", "no-cache"}, got.Charms)
}
