package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/internal/doctor"
)

// TestStatusGlyph maps every documented status to its plain (uncoloured) marker and
// confirms the unknown-status fallback.
func TestStatusGlyph(t *testing.T) {
	assert.Equal(t, "[pass]", statusGlyph(doctor.StatusOK, false))
	assert.Equal(t, "[fail]", statusGlyph(doctor.StatusFail, false))
	assert.Equal(t, "[?]", statusGlyph("", false))
	assert.Equal(t, "[?]", statusGlyph("unknown", false))
	assert.Equal(t, "[?]", statusGlyph("OK", false)) // case-sensitive by design
	// Coloured variant wraps the marker in ANSI but preserves the label.
	assert.Contains(t, statusGlyph(doctor.StatusFail, true), "[fail]")
	assert.Contains(t, statusGlyph(doctor.StatusFail, true), "\x1b[31m")
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

// TestDropCharms covers the --without-charm subtraction: it removes a named charm
// however it entered the set, matches by normalized name, and is an identity when
// nothing is dropped.
func TestDropCharms(t *testing.T) {
	cases := []struct {
		name   string
		charms []string
		drop   []string
		want   []string
	}{
		{"drop nothing is identity", []string{"rw", "debug"}, nil, []string{"rw", "debug"}},
		{"drop one from the set", []string{"rw", "debug"}, []string{"debug"}, []string{"rw"}},
		{"drop by normalized name", []string{"rw"}, []string{"RW"}, []string{}},
		{"drop a name not present", []string{"rw"}, []string{"gha"}, []string{"rw"}},
		{"empty charms stays empty", nil, []string{"rw"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, dropCharms(c.charms, c.drop))
		})
	}
}

// TestSplitCharmList covers the comma-separated --without-charm value parse.
func TestSplitCharmList(t *testing.T) {
	assert.Nil(t, splitCharmList(""))
	assert.Equal(t, []string{"rw"}, splitCharmList("rw"))
	assert.Equal(t, []string{"rw", "debug"}, splitCharmList("rw, debug"))
	assert.Equal(t, []string{"rw", "debug"}, splitCharmList(" rw ,, debug,"))
}
