package hostmem

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
