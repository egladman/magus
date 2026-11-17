package broker

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/libs/safetyscissors/budget"
)

func TestAdmissionLogRecordsASeatedWaitAndARefusal(t *testing.T) {
	addr := testAddr(t)
	store := file.Log[budget.Admission]{Path: filepath.Join(t.TempDir(), "admissions.jsonl"), Keep: 100}
	serve(t, addr, WithCapacity(0, 1), WithAdmissionLog(store))
	holder, waiter := dial(t, addr), dial(t, addr)

	held, err := holder.Claim(t.Context(), budget.Claim{Project: ".", Target: "vet", Slots: 1, PID: 100}, nil)
	require.NoError(t, err)
	require.True(t, held.Granted)
	waiting := claimAsync(t, t.Context(), addr, waiter, budget.Claim{Project: ".", Target: "test", Slots: 1, PID: 200, WaitBound: time.Minute})
	requirePending(t, waiting)
	time.Sleep(5 * time.Millisecond) // a wait the record can measure
	holder.Release(t.Context(), held.ID)
	require.True(t, requireAnswer(t, waiting).v.Granted)

	refused, err := holder.Request(t.Context(), budget.Claim{Project: ".", Target: "lint", Slots: 1, PID: 300, WaitedMs: 30_000, WaitBound: 30 * time.Second})
	require.NoError(t, err)
	require.False(t, refused.Granted)

	var got []budget.Admission
	require.Eventually(t, func() bool {
		got, err = store.Load()
		return err == nil && len(got) == 2
	}, 5*time.Second, 5*time.Millisecond)

	assert.Equal(t, budget.AdmissionSeated, got[0].Outcome)
	assert.Equal(t, "test", got[0].Target)
	assert.Positive(t, got[0].WaitedMs, "a seated wait says how long it waited")
	assert.Equal(t, time.Minute, got[0].WaitBound)
	assert.Equal(t, 1, got[0].Slots)

	assert.Equal(t, budget.AdmissionRefused, got[1].Outcome)
	assert.Equal(t, "lint", got[1].Target)
	assert.Equal(t, int64(30_000), got[1].WaitedMs)
	assert.Equal(t, 1, got[1].BudgetSlots)
	require.Len(t, got[1].Holders, 1)
	assert.Equal(t, "test", got[1].Holders[0].Target)

	assert.Len(t, waiters(t, addr), 0)
}

func TestAdmissionLogIsNotWrittenForAGrantThatNeverWaited(t *testing.T) {
	addr := testAddr(t)
	store := file.Log[budget.Admission]{Path: filepath.Join(t.TempDir(), "admissions.jsonl"), Keep: 100}
	serve(t, addr, WithCapacity(0, 4), WithAdmissionLog(store))
	c := dial(t, addr)

	v, err := c.Claim(t.Context(), budget.Claim{Project: ".", Target: "vet", Slots: 1}, nil)
	require.NoError(t, err)
	require.True(t, v.Granted)
	assert.Equal(t, 1, v.Slots, "the broker names the slots it granted")

	got, err := store.Load()
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestAParkedClaimHearsHowManyWaitAheadOfIt(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 1))
	holder := dial(t, addr)
	held, err := holder.Claim(t.Context(), budget.Claim{Target: "vet", Slots: 1, PID: 100}, nil)
	require.NoError(t, err)
	require.True(t, held.Granted)
	first := claimAsync(t, t.Context(), addr, dial(t, addr), budget.Claim{Target: "test", Slots: 1, PID: 200})
	requirePending(t, first)

	heard := make(chan budget.Verdict, 4)
	second := dial(t, addr)
	go func() {
		_, _ = second.Claim(t.Context(), budget.Claim{Target: "lint", Slots: 1, PID: 300},
			func(v budget.Verdict) { heard <- v })
	}()
	select {
	case v := <-heard:
		assert.Equal(t, 1, v.Ahead)
	case <-time.After(5 * time.Second):
		t.Fatal("the second waiter heard no parked frame")
	}
}
