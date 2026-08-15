package ledger

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func unit(id string) types.DelegationUnit {
	return types.DelegationUnit{
		ID:             id,
		Goal:           "goal for " + id,
		Checkpoint:     "abc123",
		OwnedPaths:     []string{"internal/" + id},
		ForbiddenPaths: []string{"MAGUS.md"},
		Tier:           "standard",
		Validation:     "magus run test",
		State:          types.StateDeclared,
	}
}

func TestStoreRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		puts []types.DelegationUnit
		want []types.DelegationUnit
	}{
		{
			name: "an unwritten ledger is empty, not an error",
		},
		{
			name: "rows come back in the order they were first recorded",
			puts: []types.DelegationUnit{unit("a"), unit("b"), unit("c")},
			want: []types.DelegationUnit{unit("a"), unit("b"), unit("c")},
		},
		{
			name: "a second put on the same id replaces the row in place",
			puts: []types.DelegationUnit{
				unit("a"), unit("b"),
				{ID: "a", Goal: "revised", State: types.StatePass},
			},
			want: []types.DelegationUnit{
				{ID: "a", Goal: "revised", State: types.StatePass},
				unit("b"),
			},
		},
		{
			name: "a read-only row carries no paths by design",
			puts: []types.DelegationUnit{{ID: "scout", Goal: "inventory", ReadOnly: true, State: types.StateRunning}},
			want: []types.DelegationUnit{{ID: "scout", Goal: "inventory", ReadOnly: true, State: types.StateRunning}},
		},
		{
			name: "no_return is stored as its own terminal state",
			puts: []types.DelegationUnit{{ID: "a", State: types.StateNoReturn}},
			want: []types.DelegationUnit{{ID: "a", State: types.StateNoReturn}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := NewStore(t.TempDir())
			for _, u := range tt.puts {
				stored, err := s.Put(u)
				require.NoError(t, err)
				assert.NotZero(t, stored.Created, "the store stamps Created")
				assert.NotZero(t, stored.Updated, "the store stamps Updated")
			}
			got, err := s.List()
			require.NoError(t, err)
			require.Len(t, got, len(tt.want))
			for i, w := range tt.want {
				// Timestamps are the store's, not the fixture's; assert them separately
				// above and compare the declared facts here.
				w.Created, w.Updated = got[i].Created, got[i].Updated
				assert.Equal(t, w, got[i])
			}
		})
	}
}

func TestStorePutRequiresAnID(t *testing.T) {
	t.Parallel()

	s := NewStore(t.TempDir())
	_, err := s.Put(types.DelegationUnit{Goal: "no id"})
	require.ErrorIs(t, err, ErrNoID, "a row with no id could never be updated or referred to again")

	got, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, got, "a rejected put writes nothing")
}

func TestStorePutPreservesCreatedOnUpdate(t *testing.T) {
	t.Parallel()

	s := NewStore(t.TempDir())
	first, err := s.Put(unit("a"))
	require.NoError(t, err)

	second, err := s.Put(types.DelegationUnit{ID: "a", State: types.StateFail})
	require.NoError(t, err)
	assert.Equal(t, first.Created, second.Created, "an update keeps the row's original creation time")

	// A caller-supplied timestamp is a fact about the caller's clock, so the store
	// ignores it rather than recording a time the row was not written at.
	third, err := s.Put(types.DelegationUnit{ID: "a", Created: 1, Updated: 1})
	require.NoError(t, err)
	assert.Equal(t, first.Created, third.Created)
	assert.NotEqual(t, int64(1), third.Updated)
}

// TestStoreUpdateMergesUnderOneLock is what Update exists for. Two writers advancing
// different fields of one row - a state machine and a checkpoint recorder - each
// read-modify-write the same file, and a merge that reads with List and writes with Put
// releases the lock in between: the second write then reverts the first one's field.
func TestStoreUpdateMergesUnderOneLock(t *testing.T) {
	t.Parallel()

	s := NewStore(t.TempDir())
	_, err := s.Put(unit("a"))
	require.NoError(t, err)

	const rounds = 25
	var wg sync.WaitGroup
	errs := make(chan error, 2*rounds)
	for _, apply := range []func(*types.DelegationUnit){
		func(u *types.DelegationUnit) { u.State = types.StatePass },
		func(u *types.DelegationUnit) { u.Checkpoint = "deadbeef" },
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				_, uerr := s.Update("a", apply)
				errs <- uerr
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}

	got, err := s.List()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, types.StatePass, got[0].State, "the state advance survived the concurrent checkpoint write")
	assert.Equal(t, "deadbeef", got[0].Checkpoint, "and the checkpoint survived the concurrent state advance")
	assert.Equal(t, "goal for a", got[0].Goal, "neither merge erased the row it did not name")
}

// TestStoreUpdateCreatesTheRowItMerges keeps declaring a unit and advancing one the same
// call: a merge onto an id nothing has written yet starts from a zero row carrying the id.
func TestStoreUpdateCreatesTheRowItMerges(t *testing.T) {
	t.Parallel()

	s := NewStore(t.TempDir())
	stored, err := s.Update("fresh", func(u *types.DelegationUnit) { u.State = types.StateRunning })
	require.NoError(t, err)
	assert.Equal(t, "fresh", stored.ID)
	assert.Equal(t, types.StateRunning, stored.State)
	assert.NotZero(t, stored.Created)

	_, err = s.Update("", func(*types.DelegationUnit) {})
	require.ErrorIs(t, err, ErrNoID, "a merge with no id has no row to address")
}

func TestStoreClear(t *testing.T) {
	t.Parallel()

	s := NewStore(t.TempDir())
	require.NoError(t, s.Clear(), "clearing a ledger that was never written is not an error")

	_, err := s.Put(unit("a"))
	require.NoError(t, err)
	_, err = s.Put(unit("b"))
	require.NoError(t, err)

	require.NoError(t, s.Clear())
	got, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, got, "a fresh plan starts from an empty ledger")

	// One plan per workspace: nothing was archived, so a put after a clear is row one.
	_, err = s.Put(unit("c"))
	require.NoError(t, err)
	got, err = s.List()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "c", got[0].ID)
}

func TestStorePersistsAcrossStores(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first := NewStore(dir)
	_, err := first.Put(unit("a"))
	require.NoError(t, err)

	// The point of the store: a plan outlives the process that declared it.
	second := NewStore(dir)
	got, err := second.List()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "a", got[0].ID)
	assert.Equal(t, []string{"internal/a"}, got[0].OwnedPaths)

	_, err = os.Stat(filepath.Join(dir, "ledger", "units.json"))
	assert.NoError(t, err, "the ledger lives at <stateDir>/ledger/units.json")
}

func TestStoreListReturnsCopies(t *testing.T) {
	t.Parallel()

	s := NewStore(t.TempDir())
	_, err := s.Put(unit("a"))
	require.NoError(t, err)

	got, err := s.List()
	require.NoError(t, err)
	got[0].OwnedPaths[0] = "mutated"
	got[0].Goal = "mutated"

	again, err := s.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"internal/a"}, again[0].OwnedPaths, "a caller mutating a returned row cannot reach the store")
	assert.Equal(t, "goal for a", again[0].Goal)
}

func TestStoreReportsUnreadableLedger(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "ledger"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ledger", "units.json"), []byte("{not json"), 0o644))

	s := NewStore(dir)
	_, err := s.List()
	assert.Error(t, err, "a corrupt ledger is reported, never silently read as empty")
}
