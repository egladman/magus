//go:build linux

package hostmem

import (
	"bufio"
	"context"
	"os"
	"strconv"
	"strings"
)

// AvailableBytes reads MemAvailable from /proc/meminfo, or 0 when it cannot.
//
// MemAvailable, not MemFree: the kernel's estimate of what a new allocation could
// get without swapping, which counts reclaimable page cache. MemFree on a runner
// mid-build sits near zero on every healthy run, so a watchdog wired to it would
// fire constantly.
//
// ctx is unused here; see TotalBytes.
func AvailableBytes(_ context.Context) int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "MemAvailable:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}
