package bindings

import (
	"context"
	"testing"

	"github.com/egladman/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// applyOpts runs the parsed project options against a fresh project so tests can
// assert the resulting policy fields.
func applyOpts(t *testing.T, opts vm.Value) *types.Project {
	t.Helper()
	got, err := parseBuzzProjectOpts(context.Background(), opts)
	require.NoError(t, err)
	p := &types.Project{Path: "."}
	for _, o := range got {
		require.NoError(t, o(p))
	}
	return p
}

// targetsOpts builds a `{"targets": {name: policy}}` options map.
func targetsOpts(name string, policy vm.Value) vm.Value {
	targets := vm.NewMap()
	targets.MapSet(name, policy)
	opts := vm.NewMap()
	opts.MapSet("targets", targets)
	return opts
}

func TestParseBuzzProjectOpts_TargetSlots(t *testing.T) {
	pol := vm.NewMap()
	pol.MapSet("slots", vm.IntValue(4))
	p := applyOpts(t, targetsOpts("lint", pol))
	assert.Equal(t, 4, p.TargetPolicies["lint"].Slots)
}

func TestParseBuzzProjectOpts_TargetSlotsNonPositiveIgnored(t *testing.T) {
	pol := vm.NewMap()
	pol.MapSet("slots", vm.IntValue(0))
	p := applyOpts(t, targetsOpts("lint", pol))
	assert.Equal(t, 0, p.TargetPolicies["lint"].Slots, "slots <= 0 sets no policy")
}

// A non-int slots value must be ignored, not reinterpreted: AsInt reads a float's
// raw bits as an int, which would otherwise flow a garbage value into the policy.
func TestParseBuzzProjectOpts_TargetSlotsNonIntIgnored(t *testing.T) {
	pol := vm.NewMap()
	pol.MapSet("slots", vm.FloatValue(2.5))
	p := applyOpts(t, targetsOpts("lint", pol))
	assert.Equal(t, 0, p.TargetPolicies["lint"].Slots, "non-int slots sets no policy")
}
