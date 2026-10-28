//go:build darwin

package mem

import (
	"context"
	"sync"

	"golang.org/x/sys/unix"
)

// totalBytes reads hw.memsize, or 0 when it cannot. See total_linux.go for why
// this is the machine's total rather than what is free now.
//
// Cached after the first successful reading only, so a transient startup failure
// does not pin 0 (UNKNOWN) for the life of the process. ctx is unused: a sysctl is
// a syscall, not a fork.
func totalBytes(_ context.Context) int64 {
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
