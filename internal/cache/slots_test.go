package cache

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// atomicStoreMax stores v into dst iff it is greater than the current value.
func atomicStoreMax(dst *atomic.Int32, v int32) {
	for {
		cur := dst.Load()
		if v <= cur || dst.CompareAndSwap(cur, v) {
			return
		}
	}
}

// TestRunAllSlotsThrottles verifies that a step whose Slots equals the whole
// slot budget runs alone: while it holds every slot no other step can be in fn,
// yet lighter steps still saturate the budget when the heavy one is idle.
func TestRunAllSlotsThrottles(t *testing.T) {
	root, c := openCache(t)

	var running atomic.Int32
	var duringHeavy atomic.Int32 // peak concurrency observed while the heavy step is in fn
	var lightPeak atomic.Int32   // peak concurrency observed among the light steps

	heavy := depStep(root, "heavy")
	heavy.NoCache = true
	heavy.Slots = 2 // == WithConcurrency below, so it holds every slot

	steps := []Step{heavy}
	for _, p := range []string{"l1", "l2", "l3"} {
		s := depStep(root, p)
		s.NoCache = true
		steps = append(steps, s)
	}

	fn := func(_ context.Context, s Step) error {
		cur := running.Add(1)
		defer running.Add(-1)
		if s.ProjectPath == "heavy" {
			time.Sleep(30 * time.Millisecond)
			atomicStoreMax(&duringHeavy, running.Load())
			return nil
		}
		atomicStoreMax(&lightPeak, cur)
		time.Sleep(15 * time.Millisecond)
		return nil
	}

	_, err := c.RunAll(context.Background(), steps, fn, WithConcurrency(2))
	require.NoError(t, err, "RunAll")

	assert.Equal(t, int32(1), duringHeavy.Load(), "no step may run while a slots==budget step holds every slot")
	assert.Equal(t, int32(2), lightPeak.Load(), "light steps should saturate the 2-slot budget when the heavy step is idle")
}
