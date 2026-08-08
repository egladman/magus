//go:build darwin

package hostmem

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// TotalBytes reads hw.memsize, or 0 when it cannot. See total_linux.go for why
// this is the machine's total rather than what is free now.
//
// Cached: the answer is a property of the machine and cannot change while the
// process runs, while the call sites are not one-shot - buildStep consults it per
// target, so `magus describe` over a large workspace would otherwise fork sysctl
// once per target for a constant.
//
// The cache holds the first successful reading only. A failure caches nothing, so
// a transient fork failure during startup does not pin 0 (UNKNOWN) for the life of
// the process.
func TotalBytes(ctx context.Context) int64 {
	totalOnce.mu.Lock()
	defer totalOnce.mu.Unlock()
	if totalOnce.bytes > 0 {
		return totalOnce.bytes
	}
	totalOnce.bytes = readTotalBytes(ctx)
	return totalOnce.bytes
}

var totalOnce struct {
	mu    sync.Mutex
	bytes int64
}

func readTotalBytes(ctx context.Context) int64 {
	out, err := exec.CommandContext(ctx, "sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return n
}
