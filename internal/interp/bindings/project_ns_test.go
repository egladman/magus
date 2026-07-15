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

func TestParseBuzzProjectOpts_TargetSlotsNonPositiveErrors(t *testing.T) {
	pol := vm.NewMap()
	pol.MapSet("slots", vm.IntValue(0))
	_, err := parseBuzzProjectOpts(context.Background(), targetsOpts("lint", pol))
	assert.ErrorContains(t, err, `targets["lint"].slots must be >= 1`)
}

// A non-int slots value must be a load error, not reinterpreted: AsInt reads a
// float's raw bits as an int, which would otherwise flow a garbage value into
// the policy.
func TestParseBuzzProjectOpts_TargetSlotsNonIntErrors(t *testing.T) {
	pol := vm.NewMap()
	pol.MapSet("slots", vm.FloatValue(2.5))
	_, err := parseBuzzProjectOpts(context.Background(), targetsOpts("lint", pol))
	assert.ErrorContains(t, err, `targets["lint"].slots must be a whole number`)
}

func TestParseBuzzProjectOpts_Sources(t *testing.T) {
	opts := vm.NewMap()
	opts.MapSet("sources", vm.ListValue([]vm.Value{vm.StrValue("docs/**"), vm.StrValue("../proto/**/*.proto")}))
	p := applyOpts(t, opts)
	assert.Equal(t, []string{"docs/**", "../proto/**/*.proto"}, p.Sources)
}

func TestParseBuzzProjectOpts_UnknownTopLevelKeyErrors(t *testing.T) {
	opts := vm.NewMap()
	opts.MapSet("depend_on", vm.ListValue([]vm.Value{vm.StrValue("api")}))
	_, err := parseBuzzProjectOpts(context.Background(), opts)
	assert.ErrorContains(t, err, `unknown option "depend_on"`)
	assert.ErrorContains(t, err, `did you mean "depends_on"`)
}

func TestParseBuzzProjectOpts_UnknownTargetPolicyKeyErrors(t *testing.T) {
	pol := vm.NewMap()
	pol.MapSet("skip_cache", vm.BoolValue(true))
	_, err := parseBuzzProjectOpts(context.Background(), targetsOpts("lint", pol))
	assert.ErrorContains(t, err, `unknown option "skip_cache"`)
	assert.ErrorContains(t, err, `did you mean "skipCache"`)
}
