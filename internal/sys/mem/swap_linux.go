//go:build linux

package mem

import (
	"bufio"
	"context"
	"io"
	"os"
	"strconv"
	"strings"
)

// SwapUsedBytes reports SwapTotal minus SwapFree from /proc/meminfo, or 0 when
// either line is missing or unparsable.
//
// A machine with swap disabled reports a total of 0 and so a used of 0, which is
// the right answer: there is no swap for pressure to show up in, and the watchdog
// has only AvailableBytes to go on.
//
// ctx is unused here; see TotalBytes.
func SwapUsedBytes(_ context.Context) int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	return parseSwapUsedMeminfo(f)
}

// parseSwapUsedMeminfo is SwapUsedBytes with the file injected, so its tests drive
// a truncated and a swapless /proc/meminfo instead of the one this machine happens
// to have.
func parseSwapUsedMeminfo(r io.Reader) int64 {
	var total, free int64
	var seen int
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		var dst *int64
		switch {
		case strings.HasPrefix(line, "SwapTotal:"):
			dst = &total
		case strings.HasPrefix(line, "SwapFree:"):
			dst = &free
		default:
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
		*dst = kb * 1024
		seen++
	}
	// Both lines or neither: a total read without a free would report the whole
	// swap device as in use, which reads as a machine at the edge of death.
	if seen != 2 || total <= 0 {
		return 0
	}
	return max(total-free, 0)
}
