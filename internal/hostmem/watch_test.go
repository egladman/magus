package hostmem

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const gb = int64(1) << 30

// drive runs Watch over a fixed sequence of readings and returns what it emitted.
// The sampler cancels the context once the sequence is exhausted, so the test
// neither sleeps for a fixed duration nor races the ticker.
func drive(t *testing.T, total int64, readings []int64) []string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var got []string
	i := 0
	sample := func() int64 {
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

	done := make(chan struct{})
	go func() {
		defer close(done)
		watch(ctx, total, sample, func(s string) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, s)
		}, time.Millisecond)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Watch did not return after the sampler exhausted its readings")
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
	assert.Contains(t, got[0], "900MB available of 16384MB total")
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
		Watch(context.Background(), 16*gb, nil, nil)
		Watch(context.Background(), 16*gb, func() int64 { return 1 }, nil)
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
