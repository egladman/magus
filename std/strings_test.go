package std

import (
	"context"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStringsCase(t *testing.T) {
	ctx := context.Background()
	const in = "hello_world-test case"

	caseFn := func(fn func(context.Context, string) (string, error)) string {
		got, err := fn(ctx, in)
		require.NoError(t, err)
		return got
	}

	t.Run("camel", func(t *testing.T) {
		assert.Equal(t, "helloWorldTestCase", caseFn(StringsCamelCase))
	})
	t.Run("snake", func(t *testing.T) {
		assert.Equal(t, "hello_world_test_case", caseFn(StringsSnakeCase))
	})
	t.Run("kebab", func(t *testing.T) {
		assert.Equal(t, "hello-world-test-case", caseFn(StringsKebabCase))
	})
	t.Run("pascal", func(t *testing.T) {
		assert.Equal(t, "HelloWorldTestCase", caseFn(StringsPascalCase))
	})
}

func TestStringsCapitalize(t *testing.T) {
	got, err := StringsCapitalize(context.Background(), "hELLO")
	require.NoError(t, err)
	assert.Equal(t, "Hello", got)
}

func TestStringsWords(t *testing.T) {
	got, err := StringsWords(context.Background(), "fooBarBaz")
	require.NoError(t, err)
	assert.Equal(t, []string{"foo", "Bar", "Baz"}, got)
}

func TestStringsEllipsis(t *testing.T) {
	got, err := StringsEllipsis(context.Background(), "abcdefgh", 5)
	require.NoError(t, err)
	assert.Equal(t, "ab...", got)
}

// TestKebabCaseMatchesNormalize makes an equivalence the docs rely on into an
// enforced one. types.Normalize (hand-rolled, no lo dependency) is what resolves
// every target, charm and spell op; strings\kebabCase (samber/lo) is what a Buzz
// author can call, and it is the version the runnable examples in
// docs/concepts/targets.md execute in the browser playground - the plain
// playground does not wire the magus module.
//
// They agree today by construction: types.kebabCase is documented as mirroring
// lo.KebabCase's word-boundary regexes. Agreement by construction is not agreement
// by contract, and a doc example demonstrating the wrong function would be a lie
// nobody would catch. The cases below ARE the published table.
func TestKebabCaseMatchesNormalize(t *testing.T) {
	for _, in := range []string{
		"go-build", "go_build", "goBuild", "GoBuild", "Go_Build",
		"HTTPServer", "build2", "go--build", "build",
		"image_build_static", "no_cache", "NoCache", "WRITE",
	} {
		got, err := StringsKebabCase(context.Background(), in)
		require.NoErrorf(t, err, "StringsKebabCase(%q)", in)
		assert.Equalf(t, types.Normalize(in), got,
			"strings\\kebabCase(%q) must match types.Normalize - the docs demonstrate the former to teach the latter", in)
	}
}

func TestStringsUpperFirst(t *testing.T) {
	ctx := context.Background()
	// Unlike capitalize, the remainder keeps its casing - that difference is the
	// whole reason both exist.
	got, err := StringsUpperFirst(ctx, "hELLO")
	require.NoError(t, err)
	assert.Equal(t, "HELLO", got)

	empty, err := StringsUpperFirst(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, "", empty)
}

func TestStringsCompare(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"a", "b", -1},
		{"b", "a", 1},
		{"a", "a", 0},
		// A prefix sorts before the longer string it prefixes, which is the case
		// the hand-rolled strLess in docs/lib/text.buzz existed to get right.
		{"abc", "abcd", -1},
		{"", "a", -1},
	} {
		got, err := StringsCompare(ctx, tc.a, tc.b)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got, "compare(%q, %q)", tc.a, tc.b)
	}
}

func TestStringsTrimAffix(t *testing.T) {
	ctx := context.Background()

	got, err := StringsTrimPrefix(ctx, "v1.2.3", "v")
	require.NoError(t, err)
	assert.Equal(t, "1.2.3", got)

	// An absent affix is not an error; the string comes back untouched.
	got, err = StringsTrimPrefix(ctx, "1.2.3", "v")
	require.NoError(t, err)
	assert.Equal(t, "1.2.3", got)

	got, err = StringsTrimSuffix(ctx, "main.go", ".go")
	require.NoError(t, err)
	assert.Equal(t, "main", got)

	got, err = StringsTrimSuffix(ctx, "main.go", ".rs")
	require.NoError(t, err)
	assert.Equal(t, "main.go", got)
}

func TestStringsPad(t *testing.T) {
	ctx := context.Background()

	got, err := StringsPadLeft(ctx, "7", 4, "0")
	require.NoError(t, err)
	assert.Equal(t, "0007", got)

	got, err = StringsPadRight(ctx, "name", 8, " ")
	require.NoError(t, err)
	assert.Equal(t, "name    ", got)

	// Already at or over the width: returned unchanged, never truncated.
	got, err = StringsPadLeft(ctx, "12345", 4, "0")
	require.NoError(t, err)
	assert.Equal(t, "12345", got)

	// A multi-rune pad is cut to land exactly on the requested width.
	got, err = StringsPadLeft(ctx, "x", 5, "ab")
	require.NoError(t, err)
	assert.Equal(t, "abab"+"x", got)

	// Width counts runes, not bytes, so a non-ASCII string pads as it reads.
	got, err = StringsPadRight(ctx, "é", 3, "-")
	require.NoError(t, err)
	assert.Equal(t, "é--", got)

	_, err = StringsPadLeft(ctx, "x", 4, "")
	require.Error(t, err, "an empty pad can never reach the width")
}

func TestStringsLines(t *testing.T) {
	ctx := context.Background()

	got, err := StringsLines(ctx, "a\nb\nc")
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, got)

	// A trailing newline is the normal shape of command output; it must not
	// produce a phantom empty final line.
	got, err = StringsLines(ctx, "a\nb\n")
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, got)

	got, err = StringsLines(ctx, "a\r\nb\r\n")
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, got)

	got, err = StringsLines(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, []string{}, got)

	// An interior blank line is real content and is kept.
	got, err = StringsLines(ctx, "a\n\nb\n")
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "", "b"}, got)
}

func TestStringsFields(t *testing.T) {
	ctx := context.Background()

	// The case str.split(" ") gets wrong: runs of spaces yield empty elements.
	got, err := StringsFields(ctx, "go version  go1.25.0   darwin/arm64")
	require.NoError(t, err)
	assert.Equal(t, []string{"go", "version", "go1.25.0", "darwin/arm64"}, got)

	got, err = StringsFields(ctx, "   ")
	require.NoError(t, err)
	assert.Equal(t, []string{}, got)
}

func TestStringsSplitN(t *testing.T) {
	ctx := context.Background()

	// The reason split_n exists: a value that itself contains the separator.
	got, err := StringsSplitN(ctx, "KEY=a=b=c", "=", 2)
	require.NoError(t, err)
	assert.Equal(t, []string{"KEY", "a=b=c"}, got)

	got, err = StringsSplitN(ctx, "a=b=c", "=", -1)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, got)

	got, err = StringsSplitN(ctx, "a=b", "=", 0)
	require.NoError(t, err)
	assert.Equal(t, []string{}, got)
}

func TestStringsCollapseWs(t *testing.T) {
	ctx := context.Background()

	got, err := StringsCollapseWs(ctx, "  one\n\ttwo   three \r\n")
	require.NoError(t, err)
	assert.Equal(t, "one two three", got)

	got, err = StringsCollapseWs(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, "", got)
}
