package std

import (
	"context"
	"testing"

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
