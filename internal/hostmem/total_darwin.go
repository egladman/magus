//go:build darwin

package hostmem

import (
	"os/exec"
	"strconv"
	"strings"
)

// Total reads hw.memsize, or 0 when it cannot. See the linux file for
// why this is the machine's total rather than what is free right now.
func Total() int64 {
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return n
}
