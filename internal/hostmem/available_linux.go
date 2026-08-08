//go:build linux

package hostmem

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// Available reads MemAvailable from /proc/meminfo, or 0 when it cannot.
//
// MemAvailable, not MemFree: the kernel's own estimate of what a new allocation
// could get without swapping, which counts reclaimable page cache. MemFree on a
// CI runner mid-build sits near zero on every healthy run, because the build just
// read a toolchain into cache - a watchdog on MemFree would fire constantly and
// teach its reader to ignore it.
func Available() int64 {
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
