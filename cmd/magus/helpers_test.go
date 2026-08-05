package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

// TestStatusGlyph maps every documented status to its plain (uncoloured) marker and
// confirms the unknown-status fallback.
func TestStatusGlyph(t *testing.T) {
	assert.Equal(t, "[pass]", statusGlyph(types.DoctorOK, false))
	assert.Equal(t, "[fail]", statusGlyph(types.DoctorFail, false))
	assert.Equal(t, "[?]", statusGlyph("", false))
	assert.Equal(t, "[?]", statusGlyph("unknown", false))
	assert.Equal(t, "[?]", statusGlyph("OK", false)) // case-sensitive by design
	// Coloured variant wraps the marker in ANSI but preserves the label.
	assert.Contains(t, statusGlyph(types.DoctorFail, true), "[fail]")
	assert.Contains(t, statusGlyph(types.DoctorFail, true), "\x1b[31m")
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

// TestMergeDriverRefreshSuppression pins the guard that keeps the merge driver from
// rewriting the tracked .gitattributes. loadMagus refreshes the merge-driver registration
// on the memoizing path, and that write lands in the working tree - which, when the caller
// IS the merge driver running inside the VCS's index manipulation, is the dirty-tree
// failure that stops `git rebase --continue`. Every other caller must still refresh.
func TestMergeDriverRefreshSuppression(t *testing.T) {
	assert.False(t, skipMergeDriverRefresh(context.Background()),
		"an ordinary command must keep the merge-driver registration honest")
	assert.True(t, skipMergeDriverRefresh(withoutMergeDriverRefresh(context.Background())),
		"a load from inside the merge driver must not rewrite .gitattributes")
	// The marker must not leak backwards onto the parent context.
	parent := context.Background()
	_ = withoutMergeDriverRefresh(parent)
	assert.False(t, skipMergeDriverRefresh(parent), "suppression must not escape the derived context")
}
