package std

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

func TestINIParse(t *testing.T) {
	ctx := context.Background()

	// The .npmrc shape: flat key=value, no section header at all.
	got, err := INIParse(ctx, "minimum-release-age=14400\nregistry=https://r.example/\n")
	require.NoError(t, err)
	global := got[""]
	assert.Equal(t, "14400", global["minimum-release-age"])
	// A '#' inside a value is content, not a comment: truncating a URL fragment
	// here would be silent corruption.
	assert.Equal(t, "https://r.example/", global["registry"])
}

func TestINIParseSections(t *testing.T) {
	got, err := INIParse(context.Background(), `
; a comment
top = 1

[user]
name = Eli
email = "  spaced  "

[core]
editor=vim
`)
	require.NoError(t, err)

	global := got[""]
	assert.Equal(t, "1", global["top"])

	user := got["user"]
	assert.Equal(t, "Eli", user["name"])
	// Quotes are how significant whitespace is written, so they are stripped and
	// the spaces kept.
	assert.Equal(t, "  spaced  ", user["email"])

	core := got["core"]
	assert.Equal(t, "vim", core["editor"])
}

func TestINIParseLastKeyWins(t *testing.T) {
	got, err := INIParse(context.Background(), "k=1\nk=2\n")
	require.NoError(t, err)
	assert.Equal(t, "2", got[""]["k"], "matches how git and npm resolve a repeated key")
}

func TestINIParseRejectsGarbage(t *testing.T) {
	// A dropped setting the caller believes it read is worse than an error.
	_, err := INIParse(context.Background(), "[ok]\nthis is not a setting\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line 2")
}

func TestINIParseAlwaysHasGlobalSection(t *testing.T) {
	got, err := INIParse(context.Background(), "[only]\nk=v\n")
	require.NoError(t, err)
	_, ok := got[""]
	assert.True(t, ok, "the global section must exist so a caller need not check for it")
}

func TestINIStringify(t *testing.T) {
	ctx := context.Background()
	got, err := INIStringify(ctx, map[string]map[string]string{
		"":     {"top": "1"},
		"user": {"name": "Eli", "email": "e@x"},
		"core": {"editor": "vim"},
	})
	require.NoError(t, err)
	// Global first with no header, then sections sorted, keys sorted - byte-stable
	// so a generated config never shows phantom drift.
	assert.Equal(t, "top=1\n\n[core]\neditor=vim\n\n[user]\nemail=e@x\nname=Eli\n", got)
}

func TestINIStringifyIsStable(t *testing.T) {
	ctx := context.Background()
	in := map[string]map[string]string{"s": {"a": "1", "b": "2", "c": "3", "d": "4"}}
	first, err := INIStringify(ctx, in)
	require.NoError(t, err)
	for range 20 {
		again, err := INIStringify(ctx, in)
		require.NoError(t, err)
		require.Equal(t, first, again, "map iteration order must not reach the output")
	}
}

func TestINIRoundTrip(t *testing.T) {
	ctx := context.Background()
	const src = "top=1\n\n[core]\neditor=vim\n\n[user]\nname=Eli\n"

	parsed, err := INIParse(ctx, src)
	require.NoError(t, err)
	out, err := INIStringify(ctx, parsed)
	require.NoError(t, err)
	assert.Equal(t, src, out)
}
