//go:build darwin && noffi

package mem

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
)

// AvailableBytes approximates MemAvailable as (free + inactive + speculative)
// pages, or 0 when vm_stat cannot be read or parsed.
//
// Darwin publishes no MemAvailable equivalent. Inactive pages are the closest
// analogue to Linux's reclaimable cache; wired and active pages are excluded
// because a new allocation cannot have them without paging.
//
// By forking vm_stat(1) and parsing it, which is what the whole package used to
// do. The dynamic build asks the kernel directly instead
// (available_mach_darwin.go), but that needs dlopen, and `noffi` exists to keep
// dlopen out of the archives magus ships as static. vm_stat prints
// host_statistics64, so this is the same source at the cost of a process per sample.
//
// A watchdog input, so it may be approximate. It may not be confidently wrong in
// the tight direction, which is why a partial parse yields 0.
//
// CommandContext, not Command: the watchdog calls this every two seconds for the
// life of an invocation, and a fork that hangs would block the watch goroutine
// where ctx.Done() cannot reach it.
func AvailableBytes(ctx context.Context) int64 {
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
