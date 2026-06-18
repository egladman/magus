package cache

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCPUGroupsNonEmpty checks the portable contract: cpuGroups never
// returns nil and the union of its groups covers at least one CPU.
func TestCPUGroupsNonEmpty(t *testing.T) {
	groups := cpuGroups()
	require.NotEmpty(t, groups, "cpuGroups returned no groups")
	total := 0
	for i, g := range groups {
		require.NotEmptyf(t, g, "group %d is empty", i)
		total += len(g)
	}
	assert.GreaterOrEqual(t, total, 1, "total CPUs across groups should be ≥ 1")
	t.Logf("detected %d LLC group(s) across %d CPU(s) (NumCPU=%d)",
		len(groups), total, runtime.NumCPU())
}

// TestPinThreadNoCrash exercises pinThread on the current OS and
// verifies the unpin path is callable. Best-effort: errors are
// acceptable (e.g., restricted sched_setaffinity in some sandboxes)
// because hash.go treats pinning as best-effort.
func TestPinThreadNoCrash(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	groups := cpuGroups()
	unpin, err := pinThread(groups[0])
	if err != nil {
		t.Logf("pinThread returned err (acceptable, best-effort): %v", err)
	}
	require.NotNil(t, unpin, "pinThread returned nil unpin function")
	unpin()
}
