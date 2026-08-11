package std

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSortStrings(t *testing.T) {
	got, err := SortStrings(context.Background(), []string{"charlie", "alice", "bob"})
	require.NoError(t, err)
	assert.Equal(t, []string{"alice", "bob", "charlie"}, got)
}

func TestSortReturnsANewList(t *testing.T) {
	// The whole reason these return rather than mutate: a caller holding a
	// `final` list from vcs.changed_files should not have to copy it first.
	in := []string{"b", "a"}
	got, err := SortStrings(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "a"}, in, "the input must be untouched")
	assert.Equal(t, []string{"a", "b"}, got)
}

func TestSortNatural(t *testing.T) {
	ctx := context.Background()

	// The case lexicographic ordering gets wrong.
	got, err := SortNatural(ctx, []string{"file10", "file2", "file1"})
	require.NoError(t, err)
	assert.Equal(t, []string{"file1", "file2", "file10"}, got)

	// Lexicographic disagrees, which is the point.
	lex, err := SortStrings(ctx, []string{"file10", "file2", "file1"})
	require.NoError(t, err)
	assert.Equal(t, []string{"file1", "file10", "file2"}, lex)

	// Multiple numeric segments.
	got, err = SortNatural(ctx, []string{"a2b10", "a2b2", "a10b1"})
	require.NoError(t, err)
	assert.Equal(t, []string{"a2b2", "a2b10", "a10b1"}, got)

	// A digit sorts before a letter at the same position.
	got, err = SortNatural(ctx, []string{"fileA", "file2"})
	require.NoError(t, err)
	assert.Equal(t, []string{"file2", "fileA"}, got)

	// Leading zeros compare by value, and the order stays total.
	got, err = SortNatural(ctx, []string{"x01", "x1", "x2"})
	require.NoError(t, err)
	assert.Equal(t, []string{"x1", "x01", "x2"}, got)

	// A long digit run must not overflow anything.
	got, err = SortNatural(ctx, []string{"r99999999999999999999999", "r2"})
	require.NoError(t, err)
	assert.Equal(t, []string{"r2", "r99999999999999999999999"}, got)
}

func TestSortSemver(t *testing.T) {
	ctx := context.Background()

	// The ordering a lexicographic sort gets wrong: v1.10.0 is newer than v1.9.0.
	got, err := SortSemver(ctx, []string{"v1.10.0", "v1.9.0", "v1.2.3"})
	require.NoError(t, err)
	assert.Equal(t, []string{"v1.2.3", "v1.9.0", "v1.10.0"}, got)

	// A prerelease precedes its release.
	got, err = SortSemver(ctx, []string{"v2.0.0", "v2.0.0-rc1"})
	require.NoError(t, err)
	assert.Equal(t, []string{"v2.0.0-rc1", "v2.0.0"}, got)

	// Tags with and without the leading v both sort.
	got, err = SortSemver(ctx, []string{"1.10.0", "v1.9.0"})
	require.NoError(t, err)
	assert.Equal(t, []string{"v1.9.0", "1.10.0"}, got)
}

func TestSortSemverPutsInvalidLast(t *testing.T) {
	// A stray tag must be visible at the end, not silently reordering releases.
	got, err := SortSemver(context.Background(),
		[]string{"nightly", "v1.0.0", "latest", "v0.9.0"})
	require.NoError(t, err)
	assert.Equal(t, []string{"v0.9.0", "v1.0.0", "latest", "nightly"}, got)
}

func TestSortEmptyAndSingle(t *testing.T) {
	ctx := context.Background()
	for _, fn := range []func(context.Context, []string) ([]string, error){
		SortStrings, SortNatural, SortSemver,
	} {
		got, err := fn(ctx, nil)
		require.NoError(t, err)
		assert.Empty(t, got)

		got, err = fn(ctx, []string{"only"})
		require.NoError(t, err)
		assert.Equal(t, []string{"only"}, got)
	}
}
