//go:build darwin

package mem

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// The entire risk in reading a struct by offset is reading the WRONG field, which
// still yields a number that looks like a memory figure. This pins the layout by
// checking the relation the three fields must satisfy rather than any single value,
// so it holds on a machine with no swap in use as well as one that is paging.
func TestSwapUsageLayout(t *testing.T) {
	raw, err := unix.SysctlRaw("vm.swapusage")
	require.NoError(t, err)
	require.Len(t, raw, xswUsageSize, "struct xsw_usage is 32 bytes on darwin")

	total := binary.NativeEndian.Uint64(raw[0:])
	avail := binary.NativeEndian.Uint64(raw[8:])
	used := binary.NativeEndian.Uint64(raw[xswUsedOffset:])
	pageSize := binary.NativeEndian.Uint32(raw[24:])

	assert.LessOrEqual(t, used, total, "used cannot exceed total; the offsets are wrong if it does")
	assert.LessOrEqual(t, avail, total)
	assert.Equal(t, total, used+avail, "the three fields must account for each other exactly")
	assert.NotZero(t, pageSize&(pageSize-1) == 0, "swap page size is a power of two")

	assert.Equal(t, int64(used), SwapUsedBytes(context.Background()),
		"SwapUsedBytes must return the field this test located")
}

// A negative or absurd figure is UNKNOWN rather than a reading: this is a watchdog
// input, and one wrong in the alarming direction invents an emergency.
func TestSwapUsedIsPlausible(t *testing.T) {
	used := SwapUsedBytes(context.Background())
	assert.GreaterOrEqual(t, used, int64(0))
	if total := TotalBytes(context.Background()); total > 0 {
		assert.Less(t, used, total*64, "swap in use is not orders of magnitude past RAM")
	}
}
