package golang

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/spells"
)

func TestGoFuzzOp(t *testing.T) {
	op, ok := spell.Builtins()["go"].Ops["go-fuzz"]
	require.True(t, ok, "the go spell declares go-fuzz")

	assert.Equal(t, spells.Command{
		Bin:     "go",
		Args:    []string{"test", "-trimpath", "-run", "^$"},
		EnvKeys: []string{"GOOS", "GOARCH", "GOARM", "GOAMD64"},
		// A randomized run is never replayable; go-fuzz asks its target for skip_cache.
		External: spells.ExternalReads,
		Hints:    op.Hints,
	}, op.Command)
	assert.NotEmpty(t, op.Hints, "go-fuzz shares go-test's module failure advice")

	// The package, target name and fuzztime ride in as call-site args, so a bare run
	// carries no default package pattern that go would refuse beside -fuzz.
	assert.Empty(t, op.DefaultArgs)
	assert.Empty(t, op.TrailingArgs)
}

func TestGoFuzzMatchesGoTestPolicy(t *testing.T) {
	ops := spell.Builtins()["go"].Ops
	fuzz, test := ops["go-fuzz"], ops["go-test"]

	assert.Equal(t, test.EnvKeys, fuzz.EnvKeys, "the same platform env keys the cache key already carries")
	assert.Equal(t, test.Hints, fuzz.Hints)
	assert.Equal(t, test.Bin, fuzz.Bin)
}
