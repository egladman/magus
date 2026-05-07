package cache

import (
	"runtime"
	"testing"
)

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
