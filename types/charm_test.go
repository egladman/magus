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
	assert.False(t, IsReservedCharm("container"))

	got := ReservedCharms()
	require.Equal(t, []string{"rw", "cd", "gha", "update"}, got)
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

// TestParseTargetRecordsNonCanonicalCharmSpelling covers DeclaredCharms, which carries
// the raw spelling so the CLI can teach the canonical one. Everything downstream reads
// Charms instead: a spell's charm arm, RunCI's strip and the cache key all want one
// spelling, so the raw form has to be kept beside them rather than among them.
func TestParseTargetRecordsNonCanonicalCharmSpelling(t *testing.T) {
	got, err := ParseTarget("format:UPDATE")
	require.NoError(t, err)
	assert.Equal(t, []string{CharmUpdate}, got.Charms)
	assert.Equal(t, []string{"UPDATE"}, got.DeclaredCharms)

	// The canonical spelling teaches nothing, so it must leave DeclaredCharms empty:
	// a hint that fires on correct input is a hint everyone learns to ignore.
	got, err = ParseTarget("format:update")
	require.NoError(t, err)
	assert.Equal(t, []string{CharmUpdate}, got.Charms)
	assert.Empty(t, got.DeclaredCharms)
}

// TestWithCharmsCanonicalizesOnStore covers the charm sets that never pass through
// ParseTarget (default_charms, MAGUS_DEFAULT_CHARMS, a programmatic caller), so a spell
// testing has_charm("update") answers true whatever casing the workspace wrote.
func TestWithCharmsCanonicalizesOnStore(t *testing.T) {
	ctx := WithCharms(context.Background(), []string{"UPDATE"})
	assert.True(t, HasCharm(ctx, CharmUpdate), "a stored charm must answer to the canonical query")
	assert.True(t, HasCharm(ctx, "UPDATE"), "and to the spelling the caller used")
	assert.Equal(t, []string{CharmUpdate}, CharmsFromContext(ctx), "the stored set is canonical")
}

// relock became update with no alias, so the old spelling has to stop a run by code
// rather than match nothing and run without the grant.
func TestRenamedCharmError(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"relock", "RELOCK"} {
		err := RenamedCharmError(name)
		require.ErrorIs(t, err, CharmRenamed, name)
		var d *DiagnosticError
		require.ErrorAs(t, err, &d)
		assert.Equal(t, CharmRenamed, d.Code)
	}
	for _, name := range []string{CharmUpdate, CharmReadWrite, "rellock"} {
		assert.NoError(t, RenamedCharmError(name), name)
	}
}
