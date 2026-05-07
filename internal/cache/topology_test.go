package cache

import (
	"runtime"
	"testing"
)

// TestCPUGroupsNonEmpty checks the portable contract: cpuGroups never
// returns nil and the union of its groups covers at least one CPU.
func TestCPUGroupsNonEmpty(t *testing.T) {
	groups := cpuGroups()
	if len(groups) == 0 {
		t.Fatal("cpuGroups returned no groups")
	}
	total := 0
	for i, g := range groups {
		if len(g) == 0 {
			t.Fatalf("group %d is empty", i)
		}
		total += len(g)
	}
	if total < 1 {
		t.Fatalf("total CPUs across groups = %d, want ≥ 1", total)
	}
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
	if unpin == nil {
		t.Fatal("pinThread returned nil unpin function")
	}
	unpin()
}
