package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// UnmarshalText is the one door from a name to a spawn kind, so it is where a
// misspelling stops, and an empty name stays unset.
func TestSpawnKindUnmarshalText(t *testing.T) {
	for _, name := range []string{"", "spawn", "continue"} {
		var k SpawnKind
		require.NoError(t, k.UnmarshalText([]byte(name)), name)
		assert.Equal(t, SpawnKind(name), k)
	}

	k := SpawnKindContinue
	require.EqualError(t, k.UnmarshalText([]byte("resume")), `unknown spawn kind "resume" (want one of [spawn continue])`)
	assert.Equal(t, SpawnKindContinue, k, "a refused name leaves the kind as it was")

	assert.Equal(t, "unset", SpawnKind("").String())
	assert.Equal(t, "continue", SpawnKindContinue.String())
}
