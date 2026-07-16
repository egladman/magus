package dry

import (
	"context"
	"slices"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMagusSurfaceMatchesBindings is the drift guard between the two host
// implementations of the magus.* surface: the real Buzz bindings
// (internal/interp/bindings) and this package's tracing dry-run host (buildMagus). A
// magusfile referencing a member the playground omits would fail to evaluate, so the
// playground must implement every member the real bindings register. Adding or
// removing a binding without mirroring it here fails this test instead of silently
// breaking the playground.
func TestMagusSurfaceMatchesBindings(t *testing.T) {
	realTop := bindings.MagusModuleKeys()
	require.NotEmpty(t, realTop, "bindings.MagusModuleKeys returned no members")

	m := buildMagus(buzz.NewSession(context.Background(), buzz.WithEmbedded()), newTracer())
	have := keySet(m)
	for _, k := range realTop {
		assert.True(t, have[k], "playground magus.* is missing %q (registered by the real bindings); add a stub in buildMagus", k)
	}

	// And the inverse: the playground must not expose members the real host dropped
	// (e.g. the removed magus.target namespace), which would teach a dead API.
	for _, k := range m.MapKeys() {
		assert.True(t, slices.Contains(realTop, k), "playground magus.%s has no counterpart in the real bindings; remove it or it teaches a dead API", k)
	}
}

func keySet(m vm.Value) map[string]bool {
	s := map[string]bool{}
	for _, k := range m.MapKeys() {
		s[k] = true
	}
	return s
}
