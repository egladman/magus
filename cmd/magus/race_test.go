package main

import (
	"github.com/egladman/magus/internal/cli"
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

// TestRaceDocNamesEveryMode keeps the registry's --race help honest about the
// modes resolveRace accepts. The help used to be COMPUTED from raceModes; moving
// the flag into the command registry made it a static string, so this is what
// stops a new mode from being accepted and undocumented.
func TestRaceDocNamesEveryMode(t *testing.T) {
	for _, c := range cli.All {
		for _, f := range c.Flags {
			if f.Name != "race" {
				continue
			}
			for _, mode := range raceModes {
				assert.Contains(t, f.Doc, mode,
					"magus %s --race help does not name the %q mode", c.Name, mode)
			}
		}
	}
}
