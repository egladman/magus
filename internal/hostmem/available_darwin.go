//go:build darwin

package hostmem

import (
	"os/exec"
	"strconv"
	"strings"
)

// Available approximates MemAvailable on macOS as (free + inactive + speculative)
// pages, or 0 when vm_stat cannot be read or parsed.
//
// Darwin publishes no single MemAvailable equivalent. Inactive pages are the
// closest analogue to Linux's reclaimable page cache: they hold content the VM
// can evict on demand. Wired and active pages are excluded because they cannot be
// handed to a new allocation without paging.
//
// This is a watchdog input, not an accounting figure. It is allowed to be
// approximate; it is not allowed to be confidently wrong in the tight direction,
// which is why anything unparsable yields 0 (UNKNOWN, watchdog stays silent)
// rather than a partial sum that would read as an emergency.
func Available() int64 {
	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0
	}
	var pageSize int64 = 4096
	var pages int64
	var sawAny bool
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Mach Virtual Memory Statistics:") {
			// The header carries the page size: "... (page size of 16384 bytes)".
			if i := strings.Index(line, "page size of "); i >= 0 {
				rest := line[i+len("page size of "):]
				if j := strings.Index(rest, " "); j > 0 {
					if n, err := strconv.ParseInt(rest[:j], 10, 64); err == nil && n > 0 {
						pageSize = n
					}
				}
			}
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(name) {
		case "Pages free", "Pages inactive", "Pages speculative":
		default:
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(value), "."), 10, 64)
		if err != nil {
			return 0
		}
		pages += n
		sawAny = true
	}
	if !sawAny {
		return 0
	}
	return pages * pageSize
}
