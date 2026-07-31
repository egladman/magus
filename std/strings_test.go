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
