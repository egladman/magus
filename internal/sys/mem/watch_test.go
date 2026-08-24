package mem

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const gb = int64(1) << 30

// drive runs Watch over a fixed sequence of headroom readings and returns what it
// emitted. Swap stays unreadable, so these cases exercise the headroom trigger
// alone; driveSwap is the swap-side twin.
func drive(t *testing.T, total int64, readings []int64) []string {
	t.Helper()
	return driveSwap(t, total, readings, nil)
}

// driveSwap runs Watch over paired headroom and swap readings and returns what it
// emitted. The sampler cancels the context once the sequence is exhausted, so the
// test neither sleeps for a fixed duration nor races the ticker.
func driveSwap(t *testing.T, total int64, readings, swaps []int64) []string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var got []string
	i := 0
	sample := func(context.Context) int64 {
		mu.Lock()
		defer mu.Unlock()
		if i >= len(readings) {
			cancel()
			return 0
		}
		v := readings[i]
		i++
		return v
	}

	swapAt := 0
	swap := func(context.Context) int64 {
		mu.Lock()
		defer mu.Unlock()
		if swapAt >= len(swaps) {
			return 0
		}
		v := swaps[swapAt]
		swapAt++
		return v
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		watch(ctx, total, sample, swap, func(r Reading) {
			mu.Lock()
			defer mu.Unlock()
			line := fmt.Sprintf("%dMB of %dMB swap+%dMB",
				r.AvailableBytes>>20, r.TotalBytes>>20, r.SwapGrowthBytes>>20)
			if r.SwapTriggered {
				line += " [swap]"
			}
			got = append(got, line)
		}, time.Millisecond)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("watch did not return after the sampler exhausted its readings")
	}
	mu.Lock()
	defer mu.Unlock()
	return got
}

// A healthy run says nothing at all. This is the case that runs every day, and a
// watchdog that narrates it is one nobody reads when it matters.
func TestWatchIsSilentWhenHeadroomIsFine(t *testing.T) {
	got := drive(t, 16*gb, []int64{14 * gb, 13 * gb, 9 * gb, 8 * gb})
	assert.Empty(t, got, "nothing here is below an eighth of 16GB")
}

// The case the CI-side shell loop could not catch: a sharp drop between what
// would have been two 30-second samples.
func TestWatchReportsASuddenDrop(t *testing.T) {
	got := drive(t, 16*gb, []int64{14 * gb, 900 << 20})
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "900MB of 16384MB")
}

// Once talking, it re-reports only on a further material drop - not every tick.
func TestWatchRepeatsOnlyOnAFurtherDrop(t *testing.T) {
	got := drive(t, 16*gb, []int64{
		1900 << 20, // first report
		1880 << 20, // 20MB lower: not material, stays quiet
		1800 << 20, // still inside the 250MB band
		1600 << 20, // 300MB below the floor: reports
		1610 << 20, // recovered slightly: quiet
	})
	require.Len(t, got, 2, "got: %v", got)
	assert.Contains(t, got[0], "1900MB")
	assert.Contains(t, got[1], "1600MB")
}

// UNKNOWN is not an emergency. A host magus cannot measure gets silence.
func TestWatchSaysNothingWithoutAReading(t *testing.T) {
	assert.Empty(t, drive(t, 0, []int64{1 << 20}), "total unknown")
	assert.Empty(t, drive(t, 16*gb, []int64{0, 0}), "samples unknown")
}

// A nil callback must not panic a build; the watchdog is instrumentation and may
// never be the reason a run fails.
func TestWatchToleratesNilArguments(t *testing.T) {
	assert.NotPanics(t, func() {
		watch(context.Background(), 16*gb, nil, nil, nil, time.Millisecond)
		watch(context.Background(), 16*gb, func(context.Context) int64 { return 1 }, nil, nil, time.Millisecond)
	})
}

// A run that dips, recovers, then collapses again must report the second collapse.
// It is the one that kills the machine, and an earlier floor left in place silences
// it: once floor is 100MB, `avail > floor-250MB` is true for every possible reading.
func TestWatchReportsACollapseAfterRecovery(t *testing.T) {
	got := drive(t, 16*gb, []int64{
		100 << 20, // first collapse: reports
		9 * gb,    // recovered, above the threshold
		1 << 30,   // collapses again: must report, not be measured against 100MB
	})
	require.Len(t, got, 2, "got: %v", got)
	assert.Contains(t, got[0], "100MB")
	assert.Contains(t, got[1], "1024MB")
}

// The false positive that would make the swap trigger useless: a machine up for
// weeks carries gigabytes of swap that predate the run. A level threshold would
// fire on every run there; growth since the run started fires on none.
func TestWatchIgnoresSwapItDidNotCause(t *testing.T) {
	got := driveSwap(t, 16*gb,
		[]int64{14 * gb, 13 * gb, 12 * gb},
		[]int64{6 * gb, 6 * gb, 6 * gb})
	assert.Empty(t, got, "flat swap is the machine's history, not this run's doing")
}

// The blindness this trigger exists to fix: on darwin, free+inactive+speculative
// pages do not fall while the compressor and swap absorb the pressure, so a run can
// drive a machine into paging with headroom that still reads as fine.
func TestWatchReportsSwapGrowthWithHealthyHeadroom(t *testing.T) {
	got := driveSwap(t, 16*gb,
		[]int64{14 * gb, 13 * gb},
		[]int64{2 * gb, 3 * gb})
	require.Len(t, got, 1, "got: %v", got)
	assert.Contains(t, got[0], "swap+1024MB")
}

func TestWatchRepeatsSwapOnlyOnFurtherGrowth(t *testing.T) {
	got := driveSwap(t, 16*gb,
		[]int64{14 * gb, 13 * gb, 13 * gb, 13 * gb},
		[]int64{1 * gb, 2 * gb, 2*gb + (100 << 20), 3 * gb})
	require.Len(t, got, 2, "got: %v", got)
	assert.Contains(t, got[0], "swap+1024MB")
	assert.Contains(t, got[1], "swap+2048MB")
}

// The case the growth model exists for, and the one the original guard missed: a
// CLEAN machine this run drives into swap. Waiting for a non-zero reading before
// taking a baseline made the first swap the run caused the baseline, so the growth
// measured as none and nothing was ever reported.
func TestWatchReportsSwapOnAMachineThatStartedClean(t *testing.T) {
	got := driveSwap(t, 16*gb,
		[]int64{14 * gb, 13 * gb},
		[]int64{0, 2 * gb})
	require.Len(t, got, 1, "got: %v", got)
	assert.Contains(t, got[0], "swap+2048MB")
	assert.Contains(t, got[0], "[swap]")
}

// A headroom warning routinely carries a little swap growth. It must still read as a
// headroom warning, or the reader is told the run paged out 0MB.
func TestWatchDoesNotCallASmallDriftASwapReport(t *testing.T) {
	got := driveSwap(t, 16*gb,
		[]int64{14 * gb, 900 << 20},
		[]int64{1 * gb, 1*gb + (8 << 20)})
	require.Len(t, got, 1, "got: %v", got)
	assert.NotContains(t, got[0], "[swap]", "8MB of drift is not why this fired")
}

// The floor is maintained on every tick, including one the swap trigger fired on.
// Skipping it there leaves the next headroom warning measured against a stale
// baseline, so a real collapse goes unreported.
func TestWatchKeepsTheFloorCurrentThroughASwapReport(t *testing.T) {
	got := driveSwap(t, 16*gb,
		[]int64{1900 << 20, 1000 << 20, 900 << 20},
		[]int64{0, 2 * gb, 2 * gb})
	require.Len(t, got, 2, "got: %v", got)
	assert.Contains(t, got[0], "1900MB", "the first headroom report")
	assert.Contains(t, got[1], "1000MB", "the swap tick, which also lowered the floor")
}
