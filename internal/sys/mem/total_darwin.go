//go:build darwin

package mem

import (
	"context"
	"sync"

	"golang.org/x/sys/unix"
)

// TotalBytes reads hw.memsize, or 0 when it cannot. See total_linux.go for why
// this is the machine's total rather than what is free now.
//
// A sysctl read rather than a fork of sysctl(8): the value is the same and the
// cost is a syscall instead of a process. ctx is unused for that reason, and kept
// so the signature matches the other platforms and the callers that pass one.
//
// Cached: the answer is a property of the machine and cannot change while the
// process runs, while the call sites are not one-shot. The cache holds the first
// successful reading only, so a transient failure during startup does not pin 0
// (UNKNOWN) for the life of the process.
func TotalBytes(_ context.Context) int64 {
	totalOnce.mu.Lock()
	defer totalOnce.mu.Unlock()
	if totalOnce.bytes > 0 {
		return totalOnce.bytes
	}
	totalOnce.bytes = readTotalBytes()
	return totalOnce.bytes
}

var totalOnce struct {
	mu    sync.Mutex
	bytes int64
}

func readTotalBytes() int64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil || n > 1<<62 {
		return 0
	}
	return int64(n)
}
