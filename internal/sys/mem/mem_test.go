// mem_test.go deliberately has no mem.go beside it. Both readings it covers are
// platform-split across total_{darwin,linux,other}.go and available_*.go, and what
// these tests assert is the RELATIONSHIP between the two, which belongs to neither
// file. Splitting it per reader would lose the only assertion worth making on an
// arbitrary host.

package mem

import (
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTotalAndAvailableAgree checks the two readings against each other rather
// than against a fixed number, because the only honest expectation on an
// arbitrary host is a relationship: memory that can be handed out right now
// cannot exceed the memory the machine has.
//
// On a platform with no implementation both are 0 (UNKNOWN) and there is nothing
// to assert - which is itself the contract, so the test states it rather than
// skipping silently.
func TestTotalAndAvailableAgree(t *testing.T) {
	total, avail := TotalBytes(context.Background()), AvailableBytes(context.Background())

	switch runtime.GOOS {
	case "linux", "darwin":
		assert.Positive(t, total, "TotalBytes must read the machine's memory on %s", runtime.GOOS)
		assert.Positive(t, avail, "AvailableBytes must read the machine's free memory on %s", runtime.GOOS)
		assert.LessOrEqual(t, avail, total,
			"available (%d) cannot exceed total (%d); the darwin page-size parse is the likely culprit",
			avail, total)
	default:
		assert.Zero(t, total, "an unimplemented platform reports UNKNOWN, not a guess")
		assert.Zero(t, avail, "an unimplemented platform reports UNKNOWN, not a guess")
	}
}

// TestAvailableIsNotFreeMemory guards the distinction the watchdog depends on.
// MemFree on a busy machine sits near zero because the page cache holds what was
// just read; MemAvailable counts that cache as reclaimable. A watchdog wired to
// the wrong one fires on every healthy run.
//
// A machine genuinely under memory pressure would fail this, which is acceptable:
// it is a developer machine or a CI runner at rest, and a real failure here is
// worth looking at rather than tuning away.
func TestAvailableIsNotFreeMemory(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no reading on this platform")
	}
	total, avail := TotalBytes(context.Background()), AvailableBytes(context.Background())
	assert.Greater(t, avail, total/100,
		"available (%d) is under 1%% of total (%d) - either this host is genuinely "+
			"thrashing, or the reading picked up free rather than available memory",
		avail, total)
}

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
// host must read as "no budget to arbitrate" rather than as a budget of nothing - the
// difference between admitting everything and refusing everything.
func TestBudgetMB(t *testing.T) {
	assert.Equal(t, 12288, BudgetMB(16<<30), "three quarters of the machine")
	assert.Equal(t, 0, BudgetMB(0), "an unmeasurable host has no budget to arbitrate")
	assert.Equal(t, 0, BudgetMB(-1))
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
