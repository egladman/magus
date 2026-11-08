package cache

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// An inline member holds its own claim and slots while it runs and leaves
// no cache entry. Its cost stays off the collector of the body it runs in.
func TestRunInlineClaimsSlotsAndMemoryWithoutACacheEntry(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "")
	b, _ := testBudget(t, 10_000, 8)
	c, err := Open(t.Context(), t.TempDir(), WithMachineAdmission(&budgetAdmitter{budget: b}, 0))
	require.NoError(t, err)
	member := Step{ProjectPath: ".", Target: "conventions", MemoryMB: 4000, Slots: 3, Under: ".:lint"}
	composer := types.WithExecCost(t.Context())

	var held types.MachineSnapshot
	var slots int
	var peak int64
	err = c.RunInline(composer, member, func(ctx context.Context) error {
		held, slots = b.Snapshot(), SlotsHeld(ctx)
		types.RecordPeakRSS(ctx, 4096)
		peak = types.PeakRSS(ctx)
		return nil
	}, WithLimiter(NewLimiter(8)))
	require.NoError(t, err)

	assert.Equal(t, 4000, held.HeldMB)
	assert.Equal(t, 3, held.HeldSlots)
	require.Len(t, held.Holders, 1)
	assert.Equal(t, "conventions", held.Holders[0].Target)
	assert.Equal(t, 3, slots, "the limiter seated the member at its own width")
	assert.Equal(t, int64(4096), peak, "the member has a collector of its own")
	assert.Zero(t, types.PeakRSS(composer), "and the composer's never hears of it")
	assert.Zero(t, b.Snapshot().HeldMB, "the claim went back")
	assert.Equal(t, Stats{}, c.Stats(), "nothing was keyed, replayed or stored")
}

// A member under a ctx still holding a claim rides it, as any step beneath
// a holder does: a second claim queued behind its own composer never seats.
func TestRunInlineRidesAClaimItsComposerStillHolds(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "")
	b, _ := testBudget(t, 10_000, 8)
	c, err := Open(t.Context(), t.TempDir(), WithMachineAdmission(&budgetAdmitter{budget: b}, 0))
	require.NoError(t, err)
	lim := WithLimiter(NewLimiter(8))
	composer := Step{ProjectPath: ".", Target: "lint", MemoryMB: 9000, NoCache: true}
	member := Step{ProjectPath: ".", Target: "conventions", MemoryMB: 4000, Under: ".:lint"}

	var heldMB int
	_, err = c.RunAside(t.Context(), composer, func(ctx context.Context) error {
		return c.RunInline(ctx, member, func(context.Context) error {
			heldMB = b.Snapshot().HeldMB
			return nil
		}, lim)
	}, lim)
	require.NoError(t, err)
	assert.Equal(t, 9000, heldMB, "the member rode the composer's claim")
}

// BenchmarkInlineMemberAdmission is what every ctx.needs member now pays:
// a claim, its slots and the inflight record, then their release.
func BenchmarkInlineMemberAdmission(b *testing.B) {
	b.Setenv("MAGUS_LEVEL", "")
	budget := NewMachineBudget(1<<20, 1<<10)
	c, err := Open(b.Context(), b.TempDir(), WithMachineAdmission(&budgetAdmitter{budget: budget}, 0))
	require.NoError(b, err)
	lim := WithLimiter(NewLimiter(8))
	member := Step{ProjectPath: ".", Target: "conventions", MemoryMB: 4000, Slots: 2, Under: ".:lint"}
	body := func(context.Context) error { return nil }
	for b.Loop() {
		if err := c.RunInline(b.Context(), member, body, lim); err != nil {
			b.Fatal(err)
		}
	}
}
