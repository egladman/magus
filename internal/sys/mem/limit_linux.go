//go:build linux

package mem

import (
	"context"
	"os"
	"strconv"
	"strings"
)

// cgroupLimitPaths are read in order: cgroup v2 first, then v1.
//
// The unqualified paths, not one resolved through /proc/self/cgroup, because
// every container runtime that imposes a memory limit also gives the container
// its own cgroup namespace, which is what makes these paths the container's own
// limit rather than the host's root. A process in a non-namespaced sub-cgroup
// reads its parent's figure and is admitted against a budget that is too large,
// which is the behavior magus had everywhere before this existed.
var cgroupLimitPaths = []string{
	"/sys/fs/cgroup/memory.max",                   // v2
	"/sys/fs/cgroup/memory/memory.limit_in_bytes", // v1
}

// LimitBytes reports the memory ceiling this process actually runs under, or 0
// when there is none to read.
//
// Both files are world-readable, so this needs no elevated privileges; a kernel
// without cgroups, a host outside a container, and a sandbox that hides the
// mount all fall through to 0 rather than erroring.
func LimitBytes(_ context.Context) int64 {
	for _, path := range cgroupLimitPaths {
		if n := readCgroupLimit(path); n > 0 {
			return n
		}
	}
	return 0
}

// readCgroupLimit parses one limit file, or 0 when it is absent, unparseable, or
// says unlimited.
//
// The two cgroup versions spell unlimited differently: v2 writes the literal
// "max", while v1 writes a sentinel near the top of the address space that
// varies by kernel and page size. Rather than matching either sentinel, an
// absurd figure is rejected by UsableBytes taking the smaller of this and the
// machine's own total, so a value larger than the machine reads as no limit.
func readCgroupLimit(path string) int64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	text := strings.TrimSpace(string(raw))
	if text == "max" {
		return 0
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
