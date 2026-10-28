//go:build darwin && noffi

package mem

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
)

// availableBytes is available_mach_darwin.go's reading by a vm_stat(1) fork, for the
// static (noffi) build that keeps dlopen out: the same counters at a process per
// sample. A partial parse yields 0 rather than a figure wrong in the tight direction,
// and CommandContext keeps a hung fork from blocking the watch goroutine.
func availableBytes(ctx context.Context) int64 {
	out, err := exec.CommandContext(ctx, "vm_stat").Output()
	if err != nil {
		return 0
	}
	// No default: a wrong page size is wrong by a factor of four on Apple Silicon
	// (16K pages), and always in the direction that invents an emergency.
	var pageSize int64
	var pages int64
	var fields int
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
		fields++
	}
	// All three fields, or none: a truncated vm_stat that yielded only "Pages free"
	// reports a fraction of what is actually available, which reads as a crisis.
	if fields != 3 || pageSize <= 0 {
		return 0
	}
	return pages * pageSize
}
