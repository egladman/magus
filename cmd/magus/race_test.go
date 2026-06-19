package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRace(t *testing.T) {
	// assertRace resolves input and asserts the resulting enabled/replay state.
	assertRace := func(t *testing.T, input string, wantEnabled, wantReplay bool) {
		t.Helper()
		opts, err := resolveRace(input)
		require.NoError(t, err)
		assert.Equal(t, wantEnabled, opts.Enabled)
		assert.Equal(t, wantReplay, opts.Replay)
	}

	t.Run("flag absent = disabled", func(t *testing.T) { assertRace(t, "", false, false) })
	t.Run("watch alone", func(t *testing.T) { assertRace(t, "watch", true, false) })
	t.Run("replay alone (orthogonal — no watch)", func(t *testing.T) { assertRace(t, "replay", false, true) })
	t.Run("both", func(t *testing.T) { assertRace(t, "watch,replay", true, true) })
	t.Run("order-independent", func(t *testing.T) { assertRace(t, "replay,watch", true, true) })
	t.Run("whitespace tolerated", func(t *testing.T) { assertRace(t, "watch , replay", true, true) })
	t.Run("empty trailing part ignored", func(t *testing.T) { assertRace(t, "watch,", true, false) })
	t.Run("empty leading part ignored", func(t *testing.T) { assertRace(t, ",replay", false, true) })
	t.Run("idempotent", func(t *testing.T) { assertRace(t, "watch,watch", true, false) })
	t.Run("watch,replay,watch", func(t *testing.T) { assertRace(t, "watch,replay,watch", true, true) })

	// Unknown or non-mode tokens must error.
	for _, input := range []string{"off", "on", "true", "bogus", "watch,bogus"} {
		t.Run("error/"+input, func(t *testing.T) {
			_, err := resolveRace(input)
			assert.Error(t, err)
		})
	}
}
