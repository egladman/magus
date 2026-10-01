package mem

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

// The v1 sentinel case is why UsableBytes takes a minimum rather than trying to
// recognize either cgroup version's spelling of unlimited: a ceiling larger than
// the machine describes no ceiling at all.
func TestNarrowToLimit(t *testing.T) {
	const total = 16 << 30
	for _, tc := range []struct {
		name         string
		total, limit int64
		want         int64
	}{
		{"no limit is the machine", total, 0, total},
		{"a real container ceiling wins", total, 4 << 30, 4 << 30},
		{"a ceiling above the machine is no ceiling", total, 9223372036854771712, total},
		{"a ceiling equal to the machine changes nothing", total, total, total},
		{"an unmeasurable host inside a measured container", 0, 4 << 30, 4 << 30},
		{"both unknown stays unknown", 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, narrowToLimit(tc.total, tc.limit))
		})
	}
}

// BudgetMB is the arithmetic machine-wide admission is sized from, so an unmeasurable
// host must read as "no budget to arbitrate" rather than as a budget of nothing: the
// difference between admitting everything and refusing everything. Balanced,
// conservative and the zero value all keep the same fraction; only aggressive reads
// differently, in BudgetMB_Aggressive below.
func TestBudgetMB(t *testing.T) {
	assert.Equal(t, 12288, BudgetMB(16<<30, types.ProfileBalanced), "three quarters of the machine")
	assert.Equal(t, 12288, BudgetMB(16<<30, types.ProfileConservative), "conservative keeps the same fraction as balanced")
	assert.Equal(t, 12288, BudgetMB(16<<30, ""), "an unset profile keeps the fraction, same as balanced")
	assert.Equal(t, 0, BudgetMB(0, types.ProfileBalanced), "an unmeasurable host has no budget to arbitrate")
	assert.Equal(t, 0, BudgetMB(-1, types.ProfileBalanced))
}

// TestBudgetMB_Aggressive pins the profile's whole point: no percentage reservation,
// only the fixed kernel/page-cache floor comes off the top.
func TestBudgetMB_Aggressive(t *testing.T) {
	assert.Equal(t, 16384-aggressiveFloorMB, BudgetMB(16<<30, types.ProfileAggressive),
		"every usable megabyte minus the fixed floor, not a fraction of the machine")
	assert.Equal(t, 0, BudgetMB(0, types.ProfileAggressive), "an unmeasurable host still has no budget to arbitrate")
	assert.Equal(t, 0, BudgetMB(-1, types.ProfileAggressive))
	assert.Equal(t, 1, BudgetMB(256<<20, types.ProfileAggressive),
		"a measurable host under the floor gets a nominal budget of 1 MB, never 0: 0 reads as unmeasurable/unlimited")
}

// UsableBytes never exceeds the machine, whatever this host reports.
func TestUsableNeverExceedsTotal(t *testing.T) {
	ctx := context.Background()
	if total := TotalBytes(ctx); total > 0 {
		assert.LessOrEqual(t, UsableBytes(ctx), total)
	} else {
		assert.Zero(t, TotalBytes(ctx), "an unmeasurable host reports UNKNOWN")
	}
}
