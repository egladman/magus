package types

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPeakRSSTakesTheMaximumNotTheSum(t *testing.T) {
	t.Parallel()
	ctx := WithExecCost(context.Background())

	// A target that forks a compiler, a linker and a test binary reached its
	// high-water mark in whichever one peaked. Summing would describe a machine
	// that never existed, and would make every multi-process target look
	// unschedulable.
	RecordPeakRSS(ctx, 200<<20)
	RecordPeakRSS(ctx, 4<<30)
	RecordPeakRSS(ctx, 512<<20)

	got := PeakRSS(ctx)
	assert.Equal(t, int64(4<<30), got, "want the peak, not the total")
}

func TestPeakRSSDistinguishesUnmeasuredFromZero(t *testing.T) {
	t.Parallel()

	// The whole reason PeakRSS returns a bool. Windows and wasm cannot report
	// this, and a process that never started reports nothing either, all of
	// which arrive as 0. A planner that reads 0 as "cheap" would co-schedule
	// precisely the targets it knows least about.
	ctx := WithExecCost(context.Background())
	got := PeakRSS(ctx)
	assert.Zero(t, got)

	RecordPeakRSS(ctx, 0)
	RecordPeakRSS(ctx, -1)
	assert.Zero(t, PeakRSS(ctx), "a non-positive figure is not a reading")

	RecordPeakRSS(ctx, 1)
	got = PeakRSS(ctx)
	assert.Equal(t, int64(1), got)
}

func TestPeakRSSWithoutACollectorIsANoOp(t *testing.T) {
	t.Parallel()
	// Every exec outside a target run takes this path, so it must not panic and
	// must not claim a measurement.
	ctx := context.Background()
	RecordPeakRSS(ctx, 1<<30)
	got := PeakRSS(ctx)
	assert.Zero(t, got)
}

func TestPeakRSSNestedCollectorsAreIndependent(t *testing.T) {
	t.Parallel()
	outer := WithExecCost(context.Background())
	RecordPeakRSS(outer, 1<<30)

	inner := WithExecCost(outer)
	RecordPeakRSS(inner, 8<<30)

	// An inner unit's peak does not leak outward: the outer figure would
	// otherwise describe work the outer unit did not do.
	got := PeakRSS(outer)
	assert.Equal(t, int64(1<<30), got)
	got = PeakRSS(inner)
	assert.Equal(t, int64(8<<30), got)
}

func TestPeakRSSIsConcurrencySafe(t *testing.T) {
	t.Parallel()
	// Targets fan out across spells, so reports race by construction.
	ctx := WithExecCost(context.Background())
	var wg sync.WaitGroup
	for i := 1; i <= 64; i++ {
		wg.Add(1)
		go func(n int64) {
			defer wg.Done()
			RecordPeakRSS(ctx, n<<20)
		}(int64(i))
	}
	wg.Wait()

	got := PeakRSS(ctx)
	assert.Equal(t, int64(64<<20), got)
}

func TestCPUMsSumsAcrossProcesses(t *testing.T) {
	t.Parallel()
	ctx := WithExecCost(context.Background())
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() { RecordCPUMs(ctx, 10) })
	}
	wg.Wait()
	RecordCPUMs(ctx, 0)
	RecordCPUMs(ctx, -5)

	assert.Equal(t, int64(640), CPUMs(ctx), "the sum of every positive report")
	assert.Zero(t, PeakRSS(ctx), "CPU time is not a peak")
}

func TestCPUMsNestedCollectorsAreIndependent(t *testing.T) {
	t.Parallel()
	outer := WithExecCost(context.Background())
	RecordCPUMs(outer, 100)
	inner := WithExecCost(outer)
	RecordCPUMs(inner, 7)

	assert.Equal(t, int64(100), CPUMs(outer))
	assert.Equal(t, int64(7), CPUMs(inner))
	assert.Zero(t, CPUMs(context.Background()), "no collector, no figure")
}
