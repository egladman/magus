package types

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPeakRSSKernelFloorTakesTheMaximum(t *testing.T) {
	t.Parallel()
	ctx := WithExecCost(context.Background())

	// A kernel figure carries no time, so it cannot say whether two execs
	// overlapped; only the curve sums. Without samples the floor alone is the peak.
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

func TestStepCurveSumsConcurrentExecs(t *testing.T) {
	t.Parallel()
	ctx := WithExecCost(context.Background())
	c := ctx.Value(execCostKey{}).(*execCost)
	step := c.start
	c.now = func() time.Time { return step.Add(10 * time.Second) }

	// Ten seconds in twenty buckets is 500ms each. Exec a peaks at 2GiB in the
	// bucket [1s, 1.5s), and exec b, started a second in, holds 3GiB there too.
	RecordCurve(ctx, step, []MemorySample{
		{At: time.Second, Bytes: 1 << 30},
		{At: 1200 * time.Millisecond, Bytes: 2 << 30},
		{At: 5 * time.Second, Bytes: 512 << 20},
	})
	RecordCurve(ctx, step.Add(time.Second), []MemorySample{
		{At: 100 * time.Millisecond, Bytes: 3 << 30},
		{At: 20 * time.Second, Bytes: 256 << 20},
	})
	RecordPeakRSS(ctx, 4<<30)

	var want MemoryCurve
	want[2] = 5 << 10
	want[10] = 512
	want[MemoryCurveBuckets-1] = 256
	assert.Equal(t, want, Curve(ctx), "a bucket sums each exec's largest sample in it")
	assert.Equal(t, int64(5<<30), PeakRSS(ctx), "concurrent execs add, above the kernel floor")

	RecordPeakRSS(ctx, 8<<30)
	assert.Equal(t, int64(8<<30), PeakRSS(ctx), "the kernel figure floors the curve's peak")
}
