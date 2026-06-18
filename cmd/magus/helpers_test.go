package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/internal/doctor"
)

// TestStatusGlyph maps every documented status to its glyph and
// confirms the unknown-status fallback.
func TestStatusGlyph(t *testing.T) {
	assert.Equal(t, "[ok]", statusGlyph(doctor.StatusOK))
	assert.Equal(t, "[warn]", statusGlyph(doctor.StatusWarn))
	assert.Equal(t, "[fail]", statusGlyph(doctor.StatusFail))
	assert.Equal(t, "[?]", statusGlyph(""))
	assert.Equal(t, "[?]", statusGlyph("unknown"))
	assert.Equal(t, "[?]", statusGlyph("OK")) // case-sensitive by design
}

// TestCanonicalTarget covers the short-alias expansions and the passthrough.
func TestCanonicalTarget(t *testing.T) {
	assert.Equal(t, "format", canonicalTarget("fmt"))
	assert.Equal(t, "generate", canonicalTarget("gen"))
	assert.Equal(t, "build", canonicalTarget("build"))
	assert.Equal(t, "", canonicalTarget(""))
}
