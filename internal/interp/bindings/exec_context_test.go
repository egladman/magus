package bindings

import (
	"testing"

	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// execCtxValue builds what ctx.withEnv/withCwd produce: a marked map carrying only
// execution overrides.
func execCtxValue(env map[string]string, cwd string) vm.Value {
	m := vm.NewMap()
	m.MapSet(ctxMarker, vm.BoolValue(true))
	if env != nil {
		e := vm.NewMap()
		for k, v := range env {
			e.MapSet(k, vm.StrValue(v))
		}
		m.MapSet("env", e)
	}
	if cwd != "" {
		m.MapSet("cwd", vm.StrValue(cwd))
	}
	return m
}

// TestCtxOverridesReadFromAMarkedContext pins that a leading magus\Exec is recognized
// and its overrides extracted. This is asserted at the binding rather than through a
// subprocess deliberately: an earlier round of this was "verified" by shelling out to
// an op that did not exist, so the assertion passed because nothing ran. A unit test on
// the extractor cannot pass for that reason.
func TestCtxOverridesReadFromAMarkedContext(t *testing.T) {
	args := []vm.Value{execCtxValue(map[string]string{"CGO_ENABLED": "0"}, "sub")}
	got, consumed := ctxOverridesFromBuzz(args, 0)

	require.Equal(t, 1, consumed, "a marked context must be consumed as argument one")
	assert.Equal(t, map[string]string{"CGO_ENABLED": "0"}, got.env, "env override")
	assert.Equal(t, "sub", got.cwd, "cwd override")
}

// TestPlainOptsTableIsNotAContext is the other half: an ordinary {args: [...]} table
// must NOT be mistaken for a context, or the required-ctx check would accept the old
// call form and silently read opts as overrides.
func TestPlainOptsTableIsNotAContext(t *testing.T) {
	o := vm.NewMap()
	o.MapSet("args", vm.StrValue("-race"))
	o.MapSet("env", vm.NewMap())

	_, consumed := ctxOverridesFromBuzz([]vm.Value{o}, 0)
	assert.Zero(t, consumed, "an unmarked map is an opts table, never a context")
}
