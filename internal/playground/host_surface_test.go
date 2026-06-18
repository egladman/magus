package playground

import (
	"context"
	"slices"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMagusSurfaceMatchesBindings is the drift guard between the two host
// implementations of the magus.* surface: the real Buzz bindings
// (internal/interp/bindings) and this package's recording dry-run host
// (buildMagus). A magusfile referencing a member the playground omits would fail
// to evaluate, so the playground must implement every member the real bindings
// register. When a binding is added or removed in the real host without mirroring
// it here, this test fails instead of the playground silently breaking.
func TestMagusSurfaceMatchesBindings(t *testing.T) {
	realTop, realTarget := bindings.MagusModuleKeys()
	require.NotEmpty(t, realTop, "bindings.MagusModuleKeys returned no top-level members")

	m := buildMagus(buzz.NewSession(context.Background(), buzz.WithEmbedded()), newRecorder())
	have := keySet(m)
	for _, k := range realTop {
		assert.True(t, have[k], "playground magus.* is missing %q (registered by the real bindings); add a stub in buildMagus", k)
	}

	tv, ok := m.MapGet("target")
	require.True(t, ok, "playground magus.target is missing")
	haveTarget := keySet(tv)
	for _, k := range realTarget {
		assert.True(t, haveTarget[k], "playground magus.target.* is missing %q (registered by the real bindings)", k)
	}

	// And the inverse: the playground must not expose members the real host dropped
	// (e.g. the removed depends_on/dispatch), which would teach a dead API.
	for _, k := range m.MapKeys() {
		assert.True(t, slices.Contains(realTop, k), "playground magus.%s has no counterpart in the real bindings; remove it or it teaches a dead API", k)
	}
}

func keySet(m buzz.Value) map[string]bool {
	s := map[string]bool{}
	for _, k := range m.MapKeys() {
		s[k] = true
	}
	return s
}
