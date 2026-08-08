//go:build linux

package hostmem

import (
	"bufio"
	"context"
	"os"
	"strconv"
	"strings"
)

// TotalBytes reads MemTotal from /proc/meminfo, or 0 when it cannot.
//
// MemTotal, not MemAvailable: the shard planner budgets against the machine class
// the shards will run on, not against whatever happened to be free on the machine
// doing the planning.
//
// ctx is unused here (reading /proc cannot block meaningfully) and present so the
// signature matches darwin's, which forks a subprocess.
func TotalBytes(_ context.Context) int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
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
