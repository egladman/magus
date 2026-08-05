package bindings

import (
	"context"
	"testing"

	vm "github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// includePolicy builds `{"cache": {"include": {axis: {"enabled": on}}}}`.
func includePolicy(axis string, on bool) vm.Value {
	flag := vm.NewMap()
	flag.MapSet("enabled", vm.BoolValue(on))
	include := vm.NewMap()
	include.MapSet(axis, flag)
	cache := vm.NewMap()
	cache.MapSet("include", include)
	pol := vm.NewMap()
	pol.MapSet("cache", cache)
	return pol
}

// The nested form mirrors magus.yaml's cache.include.*.enabled, so one decision reads
// the same way wherever it is written.
func TestTargetCacheIncludeOverride(t *testing.T) {
	p := applyOpts(t, targetsOpts("image", includePolicy("arch", false)))
	pol := p.TargetPolicies["image"]

	require.NotNil(t, pol.IncludeArch, "arch override not decoded")
	assert.False(t, *pol.IncludeArch)
	assert.Nil(t, pol.IncludeOS, "an unmentioned axis must inherit, not default")
}

// A misspelled nesting level would leave the target inheriting the workspace answer,
// which looks identical to a cache that works - so it is a load error.
func TestTargetCacheIncludeRejectsWrongShape(t *testing.T) {
	bad := vm.NewMap()
	cache := vm.NewMap()
	cache.MapSet("includes", vm.NewMap()) // plural typo
	bad.MapSet("cache", cache)
	_, err := parseBuzzProjectOpts(context.Background(), targetsOpts("image", bad))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "include")

	noEnabled := vm.NewMap()
	c2 := vm.NewMap()
	inc := vm.NewMap()
	inc.MapSet("os", vm.NewMap()) // present but empty
	c2.MapSet("include", inc)
	noEnabled.MapSet("cache", c2)
	_, err = parseBuzzProjectOpts(context.Background(), targetsOpts("image", noEnabled))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "enabled")
}
