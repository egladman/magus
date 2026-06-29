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

// TestWithDefaultCharms covers the magus.yaml default_charms merge: defaults are
// applied to a run, per-run charms stack on top (exact dups dropped), and the
// --no-default-charms escape ignores the defaults.
func TestWithDefaultCharms(t *testing.T) {
	cases := []struct {
		name      string
		perRun    []string
		defaults  []string
		noDefault bool
		want      []string
	}{
		{"defaults applied to a bare run", nil, []string{"rw"}, false, []string{"rw"}},
		{"per-run stacks on defaults", []string{"debug"}, []string{"rw"}, false, []string{"rw", "debug"}},
		{"exact duplicate dropped", []string{"rw"}, []string{"rw"}, false, []string{"rw"}},
		{"no-default-charms ignores defaults", []string{"debug"}, []string{"rw"}, true, []string{"debug"}},
		{"no defaults is identity", []string{"debug"}, nil, false, []string{"debug"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, withDefaultCharms(c.perRun, c.defaults, c.noDefault))
		})
	}
}
