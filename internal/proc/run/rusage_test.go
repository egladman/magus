//go:build !windows && !wasm

package run

import (
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMaxRSSBytesIsPlausibleForRealProcess pins the UNIT, which is the entire risk in this
// code: ru_maxrss is bytes on darwin and kilobytes on Linux, same field name, 1000x apart.
// A conversion applied on the wrong platform produces a number that still looks like a
// memory figure, so nothing catches it by eye.
//
// The assertion is a plausibility band rather than an exact value, because a process's
// peak RSS is not reproducible. It has to be tight enough to fail a 1024x error in EITHER
// direction, which a generous band does not: a shell really peaks around 2MB, so reading
// kilobytes as bytes lands under 1MB (caught by the floor) and multiplying bytes as though
// they were kilobytes lands near 2GB (caught only by a ceiling well under 100GB). One
// gigabyte is the ceiling: absurd for `sh -c echo hi`, and below any mis-scaled value.
func TestMaxRSSBytesIsPlausibleForRealProcess(t *testing.T) {
	res, err := Exec(context.Background(), "sh", []string{"-c", "echo hi"}, ExecOptions{Capture: true})
	require.NoError(t, err)
	require.True(t, res.Started)

	const oneMB, oneGB = 1 << 20, 1 << 30
	assert.Greater(t, res.MaxRSSBytes, int64(oneMB),
		"a real process holds more than a megabyte; a value this small means kilobytes were reported as bytes")
	assert.Less(t, res.MaxRSSBytes, int64(oneGB),
		"a shell does not hold a gigabyte; a value this large means bytes were multiplied as though they were kilobytes")
	t.Logf("peak rss on %s: %d bytes (%.1f MB)", runtime.GOOS,
		res.MaxRSSBytes, float64(res.MaxRSSBytes)/(1<<20))
}

// TestMaxRSSBytesZeroWhenNeverStarted proves an unmeasured run reports 0 rather than a
// confident number. Zero means UNKNOWN, and a caller must be able to tell that apart from
// a process that genuinely used nothing.
func TestMaxRSSBytesZeroWhenNeverStarted(t *testing.T) {
	res, err := Exec(context.Background(), "definitely-not-a-real-binary-xyz", nil, ExecOptions{})
	require.Error(t, err)
	assert.False(t, res.Started)
	assert.Zero(t, res.MaxRSSBytes, "a process that never ran has no peak to report")
}
