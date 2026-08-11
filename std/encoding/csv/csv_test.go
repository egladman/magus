package csv

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCSVParse(t *testing.T) {
	ctx := context.Background()

	got, err := CSVParse(ctx, "a,b\n1,2\n", ",", "")
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"a", "b"}, {"1", "2"}}, got)

	// A quoted field carrying the delimiter is the case a hand-rolled split gets
	// wrong, and the reason this module exists.
	got, err = CSVParse(ctx, "name,note\nx,\"a,b\"\n", ",", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"x", "a,b"}, got[1])

	// An embedded newline inside quotes stays one row.
	got, err = CSVParse(ctx, "a\n\"line1\nline2\"\n", ",", "")
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, "line1\nline2", got[1][0])
}

func TestCSVParseTSV(t *testing.T) {
	got, err := CSVParse(context.Background(), "a\tb\n1\t2\n", "\t", "")
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"a", "b"}, {"1", "2"}}, got)
}

func TestCSVParseComment(t *testing.T) {
	got, err := CSVParse(context.Background(), "# note\na,b\n", ",", "#")
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"a", "b"}}, got)
}

func TestCSVParseRejectsRaggedRows(t *testing.T) {
	// Silently accepting this is how a shifted column reaches a report.
	_, err := CSVParse(context.Background(), "a,b\n1\n", ",", "")
	require.Error(t, err)
}

func TestCSVDelimiterMustBeOneCharacter(t *testing.T) {
	ctx := context.Background()
	_, err := CSVParse(ctx, "a,b\n", "||", "")
	require.Error(t, err, "a multi-character delimiter must not be silently truncated")
	_, err = CSVStringify(ctx, [][]string{{"a"}}, "||")
	require.Error(t, err)
}

func TestCSVStringify(t *testing.T) {
	ctx := context.Background()

	got, err := CSVStringify(ctx, [][]string{{"a", "b"}, {"1", "2"}}, ",")
	require.NoError(t, err)
	assert.Equal(t, "a,b\n1,2\n", got)

	// A field containing the delimiter comes back quoted, so the output reparses.
	got, err = CSVStringify(ctx, [][]string{{"x", "a,b"}}, ",")
	require.NoError(t, err)
	assert.Equal(t, "x,\"a,b\"\n", got)
}

func TestCSVRoundTrip(t *testing.T) {
	ctx := context.Background()
	const src = "name,note\nx,\"a,b\"\ny,plain\n"

	parsed, err := CSVParse(ctx, src, ",", "")
	require.NoError(t, err)
	out, err := CSVStringify(ctx, parsed, ",")
	require.NoError(t, err)
	assert.Equal(t, src, out, "parse then stringify must reproduce the input")
}
