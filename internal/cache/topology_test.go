package cache

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"runtime"
	"testing"
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

// BenchmarkCPUGroups measures the cached-lookup cost of cpuGroups.
// The sync.OnceValue wrapper means the first call pays sysfs read
// cost; subsequent calls are an atomic load. The hash worker pool
// hits the cached path on every hashFiles invocation after the
// process's first.
func BenchmarkCPUGroups(b *testing.B) {
	_ = cpuGroups() // warm the OnceValue
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = cpuGroups()
	}
}

// BenchmarkPinThread measures the cost of a single pin+unpin round
// trip on the current OS. On Linux this is two sched_setaffinity
// syscalls (capture-prev + set + restore). On other OSes both are
// no-ops and this benchmark documents the zero-cost fallback.
func BenchmarkPinThread(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	cpus := cpuGroups()[0]
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		unpin, _ := pinThread(cpus)
		unpin()
	}
}
