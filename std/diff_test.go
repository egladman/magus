package std

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiffUnifiedEmptyWhenIdentical(t *testing.T) {
	// The empty result doubles as the check, so a caller needs no separate compare.
	got, err := DiffUnified(context.Background(), "a\nb\n", "a\nb\n", "", "", 0)
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestDiffUnified(t *testing.T) {
	got, err := DiffUnified(context.Background(), "one\ntwo\nthree\n", "one\n2\nthree\n", "", "", 0)
	require.NoError(t, err)

	assert.Contains(t, got, "-two")
	assert.Contains(t, got, "+2")
	assert.Contains(t, got, "--- a")
	assert.Contains(t, got, "+++ b")
	// Unchanged context is kept, which is what makes it readable as a patch.
	assert.Contains(t, got, " one")
}

func TestDiffUnifiedLabels(t *testing.T) {
	got, err := DiffUnified(context.Background(), "x\n", "y\n",
		"docs/reference/buzz/term.md", "regenerated", 0)
	require.NoError(t, err)
	assert.Contains(t, got, "--- docs/reference/buzz/term.md")
	assert.Contains(t, got, "+++ regenerated")
}

func TestDiffUnifiedAgainstEmpty(t *testing.T) {
	// Comparing against "" must report every line added, not a phantom first line.
	got, err := DiffUnified(context.Background(), "", "a\nb\n", "", "", 0)
	require.NoError(t, err)
	assert.Contains(t, got, "+a")
	assert.Contains(t, got, "+b")
	// No removed LINE - checked line-wise, since the "--- a" header legitimately
	// starts with dashes.
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "---") {
			continue
		}
		assert.False(t, strings.HasPrefix(line, "-"), "unexpected removal: %q", line)
	}
}

func TestDiffUnifiedContext(t *testing.T) {
	a := strings.Repeat("same\n", 20) + "old\n" + strings.Repeat("same\n", 20)
	b := strings.Repeat("same\n", 20) + "new\n" + strings.Repeat("same\n", 20)

	tight, err := DiffUnified(context.Background(), a, b, "", "", 1)
	require.NoError(t, err)
	wide, err := DiffUnified(context.Background(), a, b, "", "", 5)
	require.NoError(t, err)
	assert.Less(t, len(tight), len(wide), "a larger context must keep more lines")
}

func TestDiffEqualNormalizesLineEndingsAndTrailingNewline(t *testing.T) {
	ctx := context.Background()

	// The two differences that are not differences for a text file.
	eq, err := DiffEqual(ctx, "a\nb\n", "a\r\nb\r\n")
	require.NoError(t, err)
	assert.True(t, eq, "CRLF must not read as drift")

	eq, err = DiffEqual(ctx, "a\nb", "a\nb\n")
	require.NoError(t, err)
	assert.True(t, eq, "a trailing newline must not read as drift")

	eq, err = DiffEqual(ctx, "a\nb\n", "a\nc\n")
	require.NoError(t, err)
	assert.False(t, eq, "a real change must still read as drift")
}

func TestDiffStat(t *testing.T) {
	ctx := context.Background()

	got, err := DiffStat(ctx, "a\nb\n", "a\nb\n")
	require.NoError(t, err)
	assert.Equal(t, "", got, "identical inputs summarize as nothing")

	got, err = DiffStat(ctx, "a\n", "a\nb\nc\n")
	require.NoError(t, err)
	assert.Equal(t, "2 added, 0 removed", got)

	got, err = DiffStat(ctx, "a\nb\nc\n", "a\n")
	require.NoError(t, err)
	assert.Equal(t, "0 added, 2 removed", got)

	// A replaced line counts on both sides, matching how a patch reads.
	got, err = DiffStat(ctx, "a\nb\n", "a\nB\n")
	require.NoError(t, err)
	assert.Equal(t, "1 added, 1 removed", got)
}

func TestPathMatches(t *testing.T) {
	ctx := context.Background()

	// ** crosses separators; * does not. That difference is the whole reason a
	// caller reaches for a glob rather than a prefix check.
	ok, err := PathMatch(ctx, "**/*.go", "internal/interp/bindings/modules.go")
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = PathMatch(ctx, "*.go", "internal/modules.go")
	require.NoError(t, err)
	assert.False(t, ok, "* must not cross a separator")

	ok, err = PathMatch(ctx, "std/*.go", "std/net.go")
	require.NoError(t, err)
	assert.True(t, ok)

	// A malformed pattern raises rather than reporting "no match", so a typo is
	// not read as "nothing changed".
	_, err = PathMatch(ctx, "[unterminated", "any")
	require.Error(t, err)
}

func TestPathMatchesAny(t *testing.T) {
	ctx := context.Background()
	patterns := []string{"**/*.md", "**/*.go"}

	ok, err := PathMatchAny(ctx, patterns, "docs/x.md")
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = PathMatchAny(ctx, patterns, "std/net.go")
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = PathMatchAny(ctx, patterns, "magus.yaml")
	require.NoError(t, err)
	assert.False(t, ok)

	// An empty pattern set matches nothing rather than everything.
	ok, err = PathMatchAny(ctx, nil, "anything")
	require.NoError(t, err)
	assert.False(t, ok)
}
