//go:build linux

package forecast

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// hostMemoryBytes reads MemTotal from /proc/meminfo, or 0 when it cannot.
//
// MemTotal rather than MemAvailable: this figure is a property of the machine
// class the shards will run on, not of the machine doing the planning at the
// moment it plans. The planner runs on its own runner with its own transient
// load, and budgeting from whatever happened to be free there would make the
// shard plan depend on the planner's mood.
func hostMemoryBytes() int64 {
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
