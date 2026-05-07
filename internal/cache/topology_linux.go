package cache

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

// discoverCPUGroups groups CPUs by shared LLC via sysfs (index3/L3 → index2/L2 fallback).
// Returns nil on single-LLC systems or when sysfs is unreadable.
func discoverCPUGroups() [][]int {
	n := runtime.NumCPU()
	if n <= 1 {
		return nil
	}

	byList := make(map[string][]int, 4)
	order := make([]string, 0, 4)
	for cpu := range n {
		list, ok := readSharedCPUList(cpu)
		if !ok {
			return nil
		}
		if _, seen := byList[list]; !seen {
			order = append(order, list)
		}
		byList[list] = append(byList[list], cpu)
	}
	if len(order) <= 1 {
		return nil
	}
	out := make([][]int, 0, len(order))
	for _, k := range order {
		out = append(out, byList[k])
	}
	return out
}

// readSharedCPUList returns the LLC shared_cpu_list for cpu (index3 → index2 fallback).
func readSharedCPUList(cpu int) (string, bool) {
	for _, idx := range [...]int{3, 2} {
		path := fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cache/index%d/shared_cpu_list", cpu, idx)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		return strings.TrimSpace(string(data)), true
	}
	return "", false
}

// pinThread pins the calling OS thread to cpus via sched_setaffinity and returns
// a restore function. Caller must hold runtime.LockOSThread. The Go runtime recycles
// OS threads, so restoring the prior affinity mask is required. unpin is never nil.
func pinThread(cpus []int) (unpin func(), err error) {
	var prev unix.CPUSet
	if err := unix.SchedGetaffinity(0, &prev); err != nil {
		return func() {}, err
	}
	var next unix.CPUSet
	for _, c := range cpus {
		if c < 0 || c >= 1024 {
			continue
		}
		next.Set(c)
	}
	if next.Count() == 0 {
		return func() {}, nil
	}
	if err := unix.SchedSetaffinity(0, &next); err != nil {
		return func() {}, err
	}
	return func() { _ = unix.SchedSetaffinity(0, &prev) }, nil
}
