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

// ReadPressure reads the kernel's pressure level and the compressor's segments.
// It returns why a new step must not start, "" when nothing says so. An error
// means the reading is missing or partial, and may come with a reason:
// unreadable segments leave the level to answer alone.
func ReadPressure(_ context.Context) (string, error) {
	level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level")
	if err != nil {
		return "", fmt.Errorf("read kern.memorystatus_vm_pressure_level: %w", err)
	}
	segments, limit, err := compressorSegments(sysctlCount)
	return darwinPressure(level, segments, limit), err
}

// compressorSegments is 0, 0 and an error when either count cannot be read.
func compressorSegments(count func(string) (int64, error)) (segments, limit int64, err error) {
	if segments, err = count("vm.compressor.segment.total"); err == nil {
		limit, err = count("vm.compressor.segment.limit")
	}
	if err != nil {
		return 0, 0, fmt.Errorf("compressor segments are unread, so the kernel's level answers alone: %w", err)
	}
	return segments, limit, nil
}

// sysctlCount reads an integer sysctl of either width.
func sysctlCount(name string) (int64, error) {
	raw, err := unix.SysctlRaw(name)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", name, err)
	}
	switch len(raw) {
	case 4:
		return int64(binary.NativeEndian.Uint32(raw)), nil
	case 8:
		return int64(binary.NativeEndian.Uint64(raw) & (1<<63 - 1)), nil
	default:
		return 0, fmt.Errorf("read %s: %d bytes is no integer", name, len(raw))
	}
}

// darwinPressure judges one reading. A limit of 0 means unknown segments.
func darwinPressure(level uint32, segments, limit int64) string {
	if limit > 0 && segments*100 >= limit*compressorBadPercent {
		return fmt.Sprintf("the memory compressor holds %d%% of its segment limit (%d of %d), and a kernel panic on 2026-10-31 came at 100%%",
			segments*100/limit, segments, limit)
	}
	if level >= pressureLevelCritical {
		return fmt.Sprintf("the kernel reports critical memory pressure (kern.memorystatus_vm_pressure_level %d)", level)
	}
	return ""
}
