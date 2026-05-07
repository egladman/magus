//go:build !linux

package cache

// discoverCPUGroups returns nil on non-Linux platforms; cpuGroups falls back to one group.
func discoverCPUGroups() [][]int { return nil }

// pinThread is a no-op on non-Linux platforms.
func pinThread(_ []int) (unpin func(), err error) {
	return func() {}, nil
}
