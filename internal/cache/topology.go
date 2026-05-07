package cache

import (
	"runtime"
	"sync"
)

// cpuGroups returns LLC-sharing CPU groups for NUMA-aware worker pinning.
// Returns one group (all CPUs) on single-LLC or unsupported platforms.
// Cached via sync.OnceValue; discovery touches sysfs once per process.
var cpuGroups = sync.OnceValue(func() [][]int {
	g := discoverCPUGroups()
	if len(g) <= 1 {
		n := runtime.NumCPU()
		cpus := make([]int, n)
		for i := range cpus {
			cpus[i] = i
		}
		return [][]int{cpus}
	}
	return g
})
