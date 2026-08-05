package std

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isValid is the predicate the module was missing: magusfile.buzz previously called
// parse purely for its side effect of raising.
func TestSemverIsValid(t *testing.T) {
	ctx := context.Background()
	for _, v := range []string{"1.2.3", "v1.2.3", "1.2", "1.2.3-rc1+build"} {
		ok, err := SemverIsValid(ctx, v)
		require.NoError(t, err, "isValid must never error, only report")
		assert.True(t, ok, "%q should be valid", v)
	}
	for _, v := range []string{"", "not a version", "x86_64"} {
		ok, err := SemverIsValid(ctx, v)
		require.NoError(t, err)
		assert.False(t, ok, "%q should be invalid", v)
	}
}

// Input stays lenient - matching parse - while output is always canonical, so two
// results are directly comparable.
func TestSemverCanonicalLenientInStrictOut(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ in, want string }{
		{"1.2.3", "v1.2.3"},
		{"v1.2.3", "v1.2.3"},
		{"1.2", "v1.2.0"},
		{"1.2.3+build", "v1.2.3"},
	} {
		got, err := SemverCanonical(ctx, tc.in)
		require.NoError(t, err, tc.in)
		assert.Equal(t, tc.want, got, tc.in)
	}
	_, err := SemverCanonical(ctx, "nonsense")
	assert.Error(t, err)
}

// The prefix functions return the same token the cache keys on, which is why they are
// strings rather than the ints parse() already provides.
func TestSemverMajorMinorPrefixes(t *testing.T) {
	ctx := context.Background()
	maj, err := SemverMajor(ctx, "1.2.3")
	require.NoError(t, err)
	assert.Equal(t, "v1", maj)

	mm, err := SemverMajorMinor(ctx, "1.2.3")
	require.NoError(t, err)
	assert.Equal(t, "v1.2", mm)

	// Two versions sharing a major share a token; that is the grouping the cache uses.
	other, err := SemverMajor(ctx, "1.9.9")
	require.NoError(t, err)
	assert.Equal(t, maj, other)

	_, err = SemverMajor(ctx, "nope")
	assert.Error(t, err)
}

// satisfies covers the range form compare() structurally cannot express.
func TestSemverSatisfiesRanges(t *testing.T) {
	ctx := context.Background()
	ok, err := SemverSatisfies(ctx, "1.5.0", ">= 1.2, < 2.0")
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = SemverSatisfies(ctx, "2.0.0", ">= 1.2, < 2.0")
	require.NoError(t, err)
	assert.False(t, ok)

	ok, err = SemverSatisfies(ctx, "1.2.9", "^1.2.3")
	require.NoError(t, err)
	assert.True(t, ok)

	_, err = SemverSatisfies(ctx, "1.0.0", "not a constraint")
	assert.Error(t, err)
}
