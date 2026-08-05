package doctor

import (
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSortedCharmNames pins the stable ordering a finding list depends on.
func TestSortedCharmNames(t *testing.T) {
	got := sortedCharmNames(map[string]spells.Charm{"rw": {}, "gha": {}, "debug": {}})
	assert.Equal(t, []string{"debug", "gha", "rw"}, got)
}

// TestJSONOpCharmRule pins the rule MGS1025 enforces, on the shapes a spell author
// actually writes: an append is safe on a JSON op because it cannot unselect the
// format, while replace/remove can rewrite or drop the flag that chose JSON.
//
// It exercises the predicate rather than the whole runner because the check walks
// the compiled built-ins, and the point under test is which patch ops are allowed.
func TestJSONOpCharmRule(t *testing.T) {
	safe := spells.Charm{Ops: []spells.PatchOp{{Op: spells.OpAdd, Path: "/-", Value: "-v"}}}
	for _, p := range safe.Ops {
		assert.Equal(t, spells.OpAdd, p.Op, "an append is the only shape a JSON op's charm may take")
	}

	// Each of these can reach the argument that selected JSON.
	for _, unsafe := range []string{spells.OpReplace, spells.OpRemove, spells.OpMove, spells.OpCopy} {
		assert.NotEqual(t, spells.OpAdd, unsafe,
			"%s must not be treated as safe on a returns_json op", unsafe)
	}
}

// TestCheckJSONOpCharmsPassesOnTheBuiltins is the live rail: every built-in that
// declares returns_json today must already follow the append-only rule, so the
// check is green in this tree and a future charm that breaks it fails loudly.
func TestCheckJSONOpCharmsPassesOnTheBuiltins(t *testing.T) {
	var r runner
	got := r.checkJSONOpCharms(nil)
	require.Equal(t, StatusOK, got.Status, "details: %v", got.Details)
}
