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

	tool, ok := d.Tools["docker"]
	probe := tool.Ready
	require.True(t, ok, "docker spell declares no docker tool")
	require.NotEmpty(t, probe.Bin, "docker declares no readiness probe")
	assert.Equal(t, "docker", probe.Bin)
	assert.Equal(t, []string{"info"}, probe.Args,
		"`docker --version` is client-only and cannot detect a stopped daemon")

	gated := d.Tools["hadolint"].Ready.Bin != ""
	assert.False(t, gated, "linting a Dockerfile must not wait on the docker daemon")
}

// Every op resolves its probe through the bin it already declares, so no op restates
// which tool it uses.
func TestReadinessResolvesThroughOpBin(t *testing.T) {
	d := spellruntime.Builtins()["docker"]
	for name, op := range d.Ops {
		gated := d.Tools[op.Command.Bin].Ready.Bin != ""
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
		for tool, tl := range s.Tools {
			assert.Empty(t, tl.Ready.Bin,
				"%s: %s is self-contained and needs no readiness probe", name, tool)
		}
	}
}
