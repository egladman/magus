package types_test

import (
	"context"
	"sync"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPeakRSSTakesTheMaximumNotTheSum(t *testing.T) {
	t.Parallel()
	ctx := types.WithPeakRSS(context.Background())

	// A target that forks a compiler, a linker and a test binary reached its
	// high-water mark in whichever one peaked. Summing would describe a machine
	// that never existed, and would make every multi-process target look
	// unschedulable.
	types.RecordPeakRSS(ctx, 200<<20)
	types.RecordPeakRSS(ctx, 4<<30)
	types.RecordPeakRSS(ctx, 512<<20)

	got, seen := types.PeakRSS(ctx)
	assert.True(t, seen)
	assert.Equal(t, int64(4<<30), got, "want the peak, not the total")
}

func TestPeakRSSDistinguishesUnmeasuredFromZero(t *testing.T) {
	t.Parallel()

	// The whole reason PeakRSS returns a bool. Windows and wasm cannot report
	// this, and a process that never started reports nothing either - all of
	// which arrive as 0. A planner that reads 0 as "cheap" would co-schedule
	// precisely the targets it knows least about.
	ctx := types.WithPeakRSS(context.Background())
	got, seen := types.PeakRSS(ctx)
	assert.False(t, seen, "nothing reported: the figure is unknown, not zero")
	assert.Zero(t, got)

	types.RecordPeakRSS(ctx, 0)
	types.RecordPeakRSS(ctx, -1)
	_, seen = types.PeakRSS(ctx)
	assert.False(t, seen, "a non-positive report is not a measurement")

	types.RecordPeakRSS(ctx, 1)
	got, seen = types.PeakRSS(ctx)
	assert.True(t, seen)
	assert.Equal(t, int64(1), got)
}

func TestPeakRSSWithoutACollectorIsANoOp(t *testing.T) {
	t.Parallel()
	// Every exec outside a target run takes this path, so it must not panic and
	// must not claim a measurement.
	ctx := context.Background()
	types.RecordPeakRSS(ctx, 1<<30)
	got, seen := types.PeakRSS(ctx)
	assert.False(t, seen)
	assert.Zero(t, got)
}

func TestPeakRSSNestedCollectorsAreIndependent(t *testing.T) {
	t.Parallel()
	outer := types.WithPeakRSS(context.Background())
	types.RecordPeakRSS(outer, 1<<30)

	inner := types.WithPeakRSS(outer)
	types.RecordPeakRSS(inner, 8<<30)

	// An inner unit's peak does not leak outward: the outer figure would
	// otherwise describe work the outer unit did not do.
	got, _ := types.PeakRSS(outer)
	assert.Equal(t, int64(1<<30), got)
	got, _ = types.PeakRSS(inner)
	assert.Equal(t, int64(8<<30), got)
}

func TestPeakRSSIsConcurrencySafe(t *testing.T) {
	t.Parallel()
	// Targets fan out across spells, so reports race by construction.
	ctx := types.WithPeakRSS(context.Background())
	var wg sync.WaitGroup
	for i := 1; i <= 64; i++ {
		wg.Add(1)
		go func(n int64) {
			defer wg.Done()
			types.RecordPeakRSS(ctx, n<<20)
		}(int64(i))
	}
	wg.Wait()

	got, seen := types.PeakRSS(ctx)
	require.True(t, seen)
	assert.Equal(t, int64(64<<20), got)
}
