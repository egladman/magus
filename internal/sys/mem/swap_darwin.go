//go:build darwin

package mem

import (
	"context"
	"encoding/binary"

	"golang.org/x/sys/unix"
)

// xswUsageSize and xswUsedOffset locate xsu_used inside struct xsw_usage, whose
// shape (u_int64_t total, avail, used; u_int32_t pagesize; boolean_t encrypted)
// puts the used figure 16 bytes in and totals 32. Verified against the SDK header
// on darwin/arm64 rather than assumed: a wrong offset here reads a neighbouring
// field and reports a swap figure that is confidently wrong.
const (
	xswUsageSize  = 32
	xswUsedOffset = 16
)

// SwapUsedBytes reads the used half of vm.swapusage, or 0 when it cannot be read.
//
// Darwin is the platform that needs this most. AvailableBytes counts free,
// inactive and speculative pages, and none of them fall when the memory
// compressor and the swap file absorb pressure, so a machine that is thrashing
// can still report headroom that looks survivable. Swap is the reading that moves.
//
// A sysctl read rather than a fork of sysctl(8), which is what this used to do:
// the watchdog calls this every two seconds for the life of an invocation, so the
// fork was a process per tick to read 32 bytes. ctx is unused now and kept for the
// signature the watchdog calls through.
func SwapUsedBytes(_ context.Context) int64 {
	raw, err := unix.SysctlRaw("vm.swapusage")
	if err != nil || len(raw) < xswUsageSize {
		return 0
	}
	used := binary.NativeEndian.Uint64(raw[xswUsedOffset:])
	if used > 1<<62 {
		return 0
	}
	return int64(used)
}
