package broker

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/types"
)

func TestAdmissionLogRecordsASeatedWaitAndARefusal(t *testing.T) {
	addr := testAddr(t)
	store := file.Log[types.MachineAdmission]{Path: filepath.Join(t.TempDir(), "admissions.jsonl"), Keep: 100}
	serve(t, addr, WithCapacity(0, 1), WithAdmissionLog(store))
	holder, waiter := dial(t, addr), dial(t, addr)

	held, err := holder.Claim(t.Context(), types.MachineClaim{Project: ".", Target: "vet", Slots: 1, PID: 100}, nil)
	require.NoError(t, err)
	require.True(t, held.Granted)
	waiting := claimAsync(t, t.Context(), addr, waiter, types.MachineClaim{Project: ".", Target: "test", Slots: 1, PID: 200, WaitBoundMS: 60_000})
	requirePending(t, waiting)
	time.Sleep(5 * time.Millisecond) // a wait the record can measure
	holder.Release(t.Context(), held.ID)
	require.True(t, requireAnswer(t, waiting).v.Granted)

	refused, err := holder.Request(t.Context(), types.MachineClaim{Project: ".", Target: "lint", Slots: 1, PID: 300, WaitedMS: 30_000, WaitBoundMS: 30_000})
	require.NoError(t, err)
	require.False(t, refused.Granted)

	var got []types.MachineAdmission
	require.Eventually(t, func() bool {
		got, err = store.Load()
		return err == nil && len(got) == 2
	}, 5*time.Second, 5*time.Millisecond)

	assert.Equal(t, types.AdmissionSeated, got[0].Outcome)
	assert.Equal(t, "test", got[0].Target)
	assert.Positive(t, got[0].WaitedMS, "a seated wait says how long it waited")
	assert.Equal(t, int64(60_000), got[0].WaitBoundMS)
	assert.Equal(t, 1, got[0].Slots)

	assert.Equal(t, types.AdmissionRefused, got[1].Outcome)
	assert.Equal(t, "lint", got[1].Target)
	assert.Equal(t, int64(30_000), got[1].WaitedMS)
	assert.Equal(t, 1, got[1].BudgetSlots)
	require.Len(t, got[1].Holders, 1)
	assert.Equal(t, "test", got[1].Holders[0].Target)

	assert.Len(t, waiters(t, addr), 0)
}

func TestAdmissionLogIsNotWrittenForAGrantThatNeverWaited(t *testing.T) {
	addr := testAddr(t)
	store := file.Log[types.MachineAdmission]{Path: filepath.Join(t.TempDir(), "admissions.jsonl"), Keep: 100}
	serve(t, addr, WithCapacity(0, 4), WithAdmissionLog(store))
	c := dial(t, addr)

	v, err := c.Claim(t.Context(), types.MachineClaim{Project: ".", Target: "vet", Slots: 1}, nil)
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
	held, err := holder.Claim(t.Context(), types.MachineClaim{Target: "vet", Slots: 1, PID: 100}, nil)
	require.NoError(t, err)
	require.True(t, held.Granted)
	first := claimAsync(t, t.Context(), addr, dial(t, addr), types.MachineClaim{Target: "test", Slots: 1, PID: 200})
	requirePending(t, first)

	heard := make(chan types.MachineVerdict, 4)
	second := dial(t, addr)
	go func() {
		_, _ = second.Claim(t.Context(), types.MachineClaim{Target: "lint", Slots: 1, PID: 300},
			func(v types.MachineVerdict) { heard <- v })
	}()
	select {
	case v := <-heard:
		assert.Equal(t, 1, v.Ahead)
	case <-time.After(5 * time.Second):
		t.Fatal("the second waiter heard no parked frame")
	}
}
