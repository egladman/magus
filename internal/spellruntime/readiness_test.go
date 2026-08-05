package spellruntime_test

import (
	"testing"

	"github.com/egladman/magus/internal/spellruntime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The docker spell drives two binaries and only one of them talks to a daemon. That
// asymmetry is the whole reason readiness is keyed by TOOL rather than by spell:
// a spell-scoped probe would make a Dockerfile lint wait on a service it never uses.
func TestDockerReadinessIsScopedToTheDaemonBackedTool(t *testing.T) {
	d, ok := spellruntime.Builtins()["docker"]
	require.True(t, ok, "docker spell not registered")

	probe, ok := d.ReadinessProbes["docker"]
	require.True(t, ok, "docker declares no readiness probe")
	assert.Equal(t, "docker", probe.Bin)
	assert.Equal(t, []string{"info"}, probe.Args,
		"`docker --version` is client-only and cannot detect a stopped daemon")

	_, gated := d.ReadinessProbes["hadolint"]
	assert.False(t, gated, "linting a Dockerfile must not wait on the docker daemon")
}

// Every op resolves its probe through the bin it already declares, so no op restates
// which tool it uses.
func TestReadinessResolvesThroughOpBin(t *testing.T) {
	d := spellruntime.Builtins()["docker"]
	for name, op := range d.Ops {
		_, gated := d.ReadinessProbes[op.Command.Bin]
		if op.Command.Bin == "docker" {
			assert.True(t, gated, "op %q runs docker and should be gated", name)
		}
	}
}

// A spell that declares nothing behaves exactly as before.
func TestSpellsWithoutReadinessAreUngated(t *testing.T) {
	for _, name := range []string{"go", "rust", "typescript"} {
		s, ok := spellruntime.Builtins()[name]
		require.True(t, ok, name)
		assert.Empty(t, s.ReadinessProbes, "%s is self-contained and needs no probe", name)
	}
}
