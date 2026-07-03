package types

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpellVersionProbe covers the toolchain version-probe accessor: a spell
// with no probe reports false and returns ("", nil); a spell with a probe
// surfaces its result (and error) verbatim.
func TestSpellVersionProbe(t *testing.T) {
	none := NewSpell("none")
	assert.False(t, none.HasVersionProbe(), "HasVersionProbe should be false for a spell with no probe")
	v, err := none.ProbeVersion(context.Background(), "/dir")
	assert.NoError(t, err)
	assert.Empty(t, v)

	probed := NewSpell("go", WithVersionProbe(func(_ context.Context, dir string) (string, error) {
		return "ver@" + dir, nil
	}))
	assert.True(t, probed.HasVersionProbe(), "HasVersionProbe should be true for a spell with a probe")
	v, err = probed.ProbeVersion(context.Background(), "/d")
	assert.NoError(t, err)
	assert.Equal(t, "ver@/d", v)

	boom := errors.New("boom")
	failing := NewSpell("rs", WithVersionProbe(func(_ context.Context, _ string) (string, error) {
		return "", boom
	}))
	_, err = failing.ProbeVersion(context.Background(), "/d")
	assert.ErrorIs(t, err, boom)
}

func TestNewSpellAccessors(t *testing.T) {
	s := NewSpell(
		"go",
		WithSources("**/*.go"),
		WithClaims("**/*.go"),
		WithSpellOutputs("bin/**"),
		WithTargets("build", "test"),
		WithOpaque(),
	)
	assert.Equal(t, "go", s.Name())
	assert.Equal(t, []string{"build", "test"}, s.Targets())
	assert.True(t, s.Opaque())
}

// WithTargetSources must defensively clone its input so a caller mutating the
// original map after construction cannot corrupt the spell's internal state.
func TestWithTargetSourcesClonesInput(t *testing.T) {
	src := map[string][]string{"test": {"testdata/**"}}
	s := NewSpell("go", WithTargetSources(src))

	// Mutate the caller's map after construction.
	src["test"] = []string{"hacked"}
	src["lint"] = []string{"added"}

	assert.Equal(t, []string{"testdata/**"}, s.TargetSources()["test"], "input map must be cloned")
	assert.Nil(t, s.TargetSources()["lint"], "keys added after construction must not leak in")
}

func TestSpellNilInvokerIsNoop(t *testing.T) {
	// A spell with no invoke function is a graceful no-op, not a panic.
	s := NewSpell("noop")
	resp, err := s.Invoke(t.Context(), InvokeRequest{Target: "build"})
	assert.NoError(t, err)
	assert.Equal(t, InvokeResponse{}, resp)
}

func TestSpellImplementsSpellDriver(t *testing.T) {
	require.Implements(t, (*SpellDriver)(nil), NewSpell("x"))
}

func TestSpellServiceTargets(t *testing.T) {
	s := NewSpell("node", WithTargets("build", "serve"), WithServiceTargets("serve"))
	assert.True(t, s.IsServiceTarget("serve"), "serve is a service op")
	assert.False(t, s.IsServiceTarget("build"), "build is a command op")
	assert.False(t, s.IsServiceTarget("missing"), "unknown target is not a service")

	// A spell with no service targets never reports one.
	assert.False(t, NewSpell("go", WithTargets("build")).IsServiceTarget("build"))
}
