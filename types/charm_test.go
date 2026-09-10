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
	for _, name := range []string{"rw", "cd", "gha", "update", "RW", "CD", "GHA", "UPDATE"} {
		assert.Truef(t, IsReservedCharm(name), "IsReservedCharm(%q)", name)
	}
	// The compat alias is reserved too, or the typo guard would report a run that
	// spells the charm the old way as an undeclared charm rather than running it.
	for _, name := range []string{"relock", "RELOCK"} {
		assert.Truef(t, IsReservedCharm(name), "IsReservedCharm(%q)", name)
	}
	assert.False(t, IsReservedCharm("container"))

	got := ReservedCharms()
	require.Equal(t, []string{"rw", "cd", "gha", "update"}, got,
		"the alias is accepted but not enumerated: `describe charm` lists one spelling")
	got[0] = "mutated"
	assert.Equal(t, "rw", ReservedCharms()[0], "ReservedCharms() must return an independent copy")
}

func TestReservedCharmDoc(t *testing.T) {
	assert.Equal(t,
		"mutate in place: flip check-only targets (format, lint, generate) to write; stripped from ci",
		ReservedCharmDoc("RW"), "casing-insensitive lookup for rw")
	assert.Equal(t,
		"continuous-delivery: a target reads it to publish its artifact; survives into ci",
		ReservedCharmDoc("cd"))
	assert.Equal(t,
		"GitHub Actions output: swap a tool's reporter to inline workflow annotations; survives into ci",
		ReservedCharmDoc("gha"))
	assert.Equal(t,
		"move pinned upstream state forward (a lockfile, a scanner database) instead of verifying it; stripped from ci",
		ReservedCharmDoc("update"))
	assert.Equal(t, ReservedCharmDoc("update"), ReservedCharmDoc("relock"),
		"the alias resolves to the same charm, so it must describe the same thing")
	assert.Empty(t, ReservedCharmDoc("container"), "a non-reserved charm has no built-in doc")
}

// TestParseTargetNormalizesCharms locks in that the "target:charm" suffix is
// canonicalized at the parse boundary, so everything downstream (cache key, ci
// strip, typo guard) sees one spelling.
func TestParseTargetNormalizesCharms(t *testing.T) {
	got, err := ParseTarget("format:Write,No_Cache")
	require.NoError(t, err)
	assert.Equal(t, []string{"write", "no-cache"}, got.Charms)
}

// TestParseTargetResolvesUpdateAlias covers the compat alias at the one boundary that
// can canonicalize it. Everything downstream reads Charms: a spell's charm arm is named
// `update`, RunCI strips `update`, and the cache keys `update`, so the alias has to be
// gone by the time any of them look. DeclaredCharms carries the raw spelling, which is
// how the CLI knows to teach the new one.
func TestParseTargetResolvesUpdateAlias(t *testing.T) {
	got, err := ParseTarget("format:relock")
	require.NoError(t, err)
	assert.Equal(t, []string{CharmUpdate}, got.Charms)
	assert.Equal(t, []string{"relock"}, got.DeclaredCharms)

	// It stacks and normalizes like any other charm name.
	got, err = ParseTarget("format:rw,RELOCK")
	require.NoError(t, err)
	assert.Equal(t, []string{CharmReadWrite, CharmUpdate}, got.Charms)

	// The canonical spelling teaches nothing, so it must leave DeclaredCharms empty:
	// a hint that fires on correct input is a hint everyone learns to ignore.
	got, err = ParseTarget("format:update")
	require.NoError(t, err)
	assert.Equal(t, []string{CharmUpdate}, got.Charms)
	assert.Empty(t, got.DeclaredCharms)
}

// TestHasCharmResolvesUpdateAlias covers the other half: a charm set that reaches the
// context without passing through ParseTarget (default_charms, MAGUS_DEFAULT_CHARMS, a
// programmatic caller) is canonicalized on store, so a spell testing has_charm("update")
// answers true for a workspace that still writes relock.
func TestHasCharmResolvesUpdateAlias(t *testing.T) {
	ctx := WithCharms(context.Background(), []string{"relock"})
	assert.True(t, HasCharm(ctx, CharmUpdate), "a stored alias must answer to the canonical query")
	assert.True(t, HasCharm(ctx, "relock"), "and to the alias, so an old spell keeps working")
	assert.Equal(t, []string{CharmUpdate}, CharmsFromContext(ctx), "the stored set is canonical")
}
