package main

import (
	"testing"

	"github.com/egladman/magus/internal/render"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEncodeFragmentDeterminism confirms that render.EncodeFragmentRaw produces
// byte-for-byte identical output for the same input across two calls. This relies
// on gzip.NewWriter leaving the header ModTime at its zero value by default, so the
// compressed stream is deterministic - a necessary property for stable #data= URL
// fragments in MAGUS.md. The test exercises the shared encoder that both the render
// package (per-project MAGUS.md deep links) and cmd/magus (graph open) use, proving
// browser wire-format parity is preserved when a single implementation is used.
func TestEncodeFragmentDeterminism(t *testing.T) {
	payload := []byte(`{"projects":[{"path":"pkg/foo","engine":"buzz","nodes":[{"name":"build","dependencies":["fmt"]},{"name":"fmt"}]}]}`)

	first, err := render.EncodeFragmentRaw(payload)
	require.NoError(t, err, "first EncodeFragmentRaw")

	second, err := render.EncodeFragmentRaw(payload)
	require.NoError(t, err, "second EncodeFragmentRaw")

	assert.Equal(t, first, second, "EncodeFragmentRaw must be deterministic:\n  first:  %s\n  second: %s", first, second)
}
