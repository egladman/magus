package gen

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A defined type over a basic kind matches no case in AnyVal's type switch, because a
// type switch matches on identity. Before this was handled, every such field crossed as
// NULL: `doctor().checks[0].status` read null rather than "ok", and a magusfile told to
// branch on status was branching on nothing.
func TestAnyValNamedStringType(t *testing.T) {
	got, ok := AnyVal(map[string]any{"status": types.DoctorOK}).MapGet("status")
	require.True(t, ok, "status key missing")
	require.True(t, got.IsStr(), "a named string crossed as %s, not a string", got.Kind())
	assert.Equal(t, "ok", got.AsString())
}

// The same hole, one type over: TargetRun.state is types.TargetRunState.
func TestAnyValNamedStringOnTargetRun(t *testing.T) {
	got, ok := AnyVal(map[string]any{"state": types.TargetRunPassed}).MapGet("state")
	require.True(t, ok)
	require.True(t, got.IsStr(), "a named string crossed as %s", got.Kind())
	assert.Equal(t, "passed", got.AsString())
}

// Reflection covers every basic kind, not just string, so the next defined type does
// not reintroduce the same silent null.
func TestAnyValNamedBasicKinds(t *testing.T) {
	type namedInt int
	type namedBool bool
	assert.Equal(t, int64(7), AnyVal(namedInt(7)).AsInt())
	assert.True(t, AnyVal(namedBool(true)).AsBool())

	// A shape the boundary cannot represent still yields null rather than guessing.
	assert.True(t, AnyVal(struct{ X int }{1}).IsNull())
}
