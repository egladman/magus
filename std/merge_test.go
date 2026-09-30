package std

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeShallowReplacesANestedObject(t *testing.T) {
	base := map[string]any{
		"kept": true,
		"hooks": map[string]any{
			"before": []any{"old"},
			"after":  []any{"theirs"},
		},
	}
	overlay := map[string]any{
		"hooks": map[string]any{"before": []any{"magus"}},
	}

	got, err := MergeShallow(context.Background(), base, overlay)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"kept":  true,
		"hooks": map[string]any{"before": []any{"magus"}},
	}, got)
	assert.Equal(t, []any{"old"}, base["hooks"].(map[string]any)["before"], "base is not modified")
}

func TestMergeDeepKeepsSiblingKeysAndReplacesArrays(t *testing.T) {
	// 2^53+1 is the integer a float cannot hold. It sits beside the object
	// being patched, so a deep merge has to copy it through unchanged.
	const large int64 = 9007199254740993
	base := map[string]any{
		"large": large,
		"hooks": map[string]any{
			"before": []any{"old"},
			"after":  []any{map[string]any{"command": "theirs"}},
		},
	}
	overlay := map[string]any{
		"hooks": map[string]any{"before": []any{"magus"}},
	}

	got, err := MergeDeep(context.Background(), base, overlay)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"large": large,
		"hooks": map[string]any{
			"before": []any{"magus"},
			"after":  []any{map[string]any{"command": "theirs"}},
		},
	}, got)
	assert.Equal(t, []any{"old"}, base["hooks"].(map[string]any)["before"], "base is not modified")
}

func TestMergeReplacesWhenEitherSideIsNotAnObject(t *testing.T) {
	ctx := context.Background()
	replaced, err := MergeDeep(ctx, map[string]any{"k": "v"}, []any{"x"})
	require.NoError(t, err)
	assert.Equal(t, []any{"x"}, replaced)

	nulled, err := MergeShallow(ctx, map[string]any{"k": "v"}, nil)
	require.NoError(t, err)
	assert.Nil(t, nulled)
}

func TestMergeJSONKeepsAnIntegerPastTheFloatRange(t *testing.T) {
	got, err := MergeJSON(context.Background(),
		`{"large":9007199254740993,"hooks":{"before":["old"],"after":[{"command":"theirs"}]}}`,
		`{"hooks":{"before":["magus"]}}`)
	require.NoError(t, err)
	assert.Equal(t, `{
  "hooks": {
    "after": [
      {
        "command": "theirs"
      }
    ],
    "before": [
      "magus"
    ]
  },
  "large": 9007199254740993
}
`, got)
}

func TestMergeJSONRejectsADocumentThatIsNotJSON(t *testing.T) {
	_, err := MergeJSON(context.Background(), `{"ok":true}`, `not json`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "merge.json: overlay")
}
