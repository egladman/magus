package spell

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// TestConflicts covers the overridden-charm detector: a destructive overlap on the
// same argv position is a conflict (the alphabetically-later charm wins), while
// disjoint edits, a lone charm, and an inert charm are not.
func TestConflicts(t *testing.T) {
	base := []string{"lint", "."}
	charms := map[string]types.Charm{
		// two charms fight over /0; "rw" sorts after "fmt", so rw wins and fmt is lost.
		"fmt": {Ops: []types.PatchOp{{Op: "replace", Path: "/0", Value: "format"}}},
		"rw":  {Ops: []types.PatchOp{{Op: "replace", Path: "/0", Value: "fix"}}},
		// disjoint appends: both survive regardless of order.
		"debug": {Ops: []types.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
		"trace": {Ops: []types.PatchOp{{Op: "add", Path: "/-", Value: "-x"}}},
		// inert: declares no ops.
		"noop": {},
	}

	t.Run("destructive overlap is a conflict", func(t *testing.T) {
		got, err := Conflicts(base, charms, []string{"rw", "fmt"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "fmt", got[0].Name, "the losing charm is reported")
		assert.Equal(t, "rw", got[0].OverriddenBy, "the winner is named")
	})

	t.Run("disjoint appends do not conflict", func(t *testing.T) {
		got, err := Conflicts(base, charms, []string{"debug", "trace"})
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("disjoint positions do not conflict", func(t *testing.T) {
		got, err := Conflicts(base, charms, []string{"rw", "debug"})
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("a single charm cannot conflict", func(t *testing.T) {
		got, err := Conflicts(base, charms, []string{"rw"})
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("an inert charm is not a conflict", func(t *testing.T) {
		got, err := Conflicts(base, charms, []string{"noop", "rw"})
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}
