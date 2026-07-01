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

func TestParseBuzzProjectOpts_TargetWeight(t *testing.T) {
	pol := vm.NewMap()
	pol.MapSet("weight", vm.IntValue(4))
	p := applyOpts(t, targetsOpts("lint", pol))
	assert.Equal(t, 4, p.TargetPolicies["lint"].Weight)
}

func TestParseBuzzProjectOpts_TargetWeightNonPositiveIgnored(t *testing.T) {
	pol := vm.NewMap()
	pol.MapSet("weight", vm.IntValue(0))
	p := applyOpts(t, targetsOpts("lint", pol))
	assert.Equal(t, 0, p.TargetPolicies["lint"].Weight, "weight <= 0 sets no policy")
}
