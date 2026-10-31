//go:build darwin

package mem

import (
	"context"
	"encoding/binary"
	"fmt"

	"golang.org/x/sys/unix"
)

const (
	// pressureLevelCritical is the top of the kernel's level: 1, 2 or 4.
	// Level 2, warning, is a machine using its memory.
	pressureLevelCritical = 4

	// compressorBadPercent is a starting figure. The 2026-10-31 panic read
	// 100% of segments, with compressed pages at 73% and "OK". A healthy
	// machine read 8.5%.
	compressorBadPercent = 75
)

// ReadPressure reads the kernel's pressure level and the compressor's
// segments. An error means the level cannot be read. Unreadable segments
// leave the level to answer alone.
func ReadPressure(_ context.Context) (Pressure, error) {
	level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level")
	if err != nil {
		return Pressure{}, fmt.Errorf("read kern.memorystatus_vm_pressure_level: %w", err)
	}
	return darwinPressure(level, sysctlCount("vm.compressor.segment.total"), sysctlCount("vm.compressor.segment.limit")), nil
}

// sysctlCount reads an integer sysctl of either width, or 0 when it cannot.
func sysctlCount(name string) int64 {
	raw, err := unix.SysctlRaw(name)
	if err != nil {
		return 0
	}
	switch len(raw) {
	case 4:
		return int64(binary.NativeEndian.Uint32(raw))
	case 8:
		return int64(binary.NativeEndian.Uint64(raw) & (1<<63 - 1))
	default:
		return 0
	}
}

// darwinPressure judges one reading. A limit of 0 means unknown segments.
func darwinPressure(level uint32, segments, limit int64) Pressure {
	if limit > 0 && segments*100 >= limit*compressorBadPercent {
		return Pressure{Bad: true, State: fmt.Sprintf("the memory compressor holds %d%% of its segment limit (%d of %d), and a kernel panic on 2026-10-31 came at 100%%",
			segments*100/limit, segments, limit)}
	}
	if level >= pressureLevelCritical {
		return Pressure{Bad: true, State: fmt.Sprintf("the kernel reports critical memory pressure (kern.memorystatus_vm_pressure_level %d)", level)}
	}
	return Pressure{State: fmt.Sprintf("kernel pressure level %d, compressor at %d of %d segments", level, segments, limit)}
}
