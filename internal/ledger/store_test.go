package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
				// The replacement carries no owned paths, so the row's paths were
				// released by it - see TestStorePutRecordsReleasedPaths.
				{
					ID: "a", Goal: "revised", State: types.StatePass,
					Releases: []types.DelegationRelease{{Path: "internal/a", Digest: types.DigestAbsent}},
				},
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

			ctx := t.Context()
			s := NewStore(Location{CacheDir: t.TempDir(), Root: t.TempDir()})
			for _, u := range tt.puts {
				stored, err := s.Put(ctx, u)
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
				require.Len(t, got[i].Releases, len(w.Releases))
				for j := range w.Releases {
					w.Releases[j].ReleasedAt = got[i].Releases[j].ReleasedAt
				}
				assert.Equal(t, w, got[i])
			}
		})
	}
}

func TestStorePutRequiresAnID(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := NewStore(Location{CacheDir: t.TempDir(), Root: t.TempDir()})
	_, err := s.Put(ctx, types.DelegationUnit{Goal: "no id"})
	require.ErrorIs(t, err, ErrNoID, "a row with no id could never be updated or referred to again")

	got, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, got, "a rejected put writes nothing")
}

func TestStorePutPreservesCreatedOnUpdate(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := NewStore(Location{CacheDir: t.TempDir(), Root: t.TempDir()})
	first, err := s.Put(ctx, unit("a"))
	require.NoError(t, err)

	second, err := s.Put(ctx, types.DelegationUnit{ID: "a", State: types.StateFail})
	require.NoError(t, err)
	assert.Equal(t, first.Created, second.Created, "an update keeps the row's original creation time")

	// A caller-supplied timestamp is a fact about the caller's clock, so the store
	// ignores it rather than recording a time the row was not written at.
	third, err := s.Put(ctx, types.DelegationUnit{ID: "a", Created: 1, Updated: 1})
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

	ctx := t.Context()
	s := NewStore(Location{CacheDir: t.TempDir(), Root: t.TempDir()})
	_, err := s.Put(ctx, unit("a"))
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
				_, uerr := s.Update(ctx, "a", apply)
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

// TestStoreUpdateSurvivesSeparateStores is the CROSS-PROCESS half, and the one the
// in-process mutex cannot cover: two Stores on one directory share no mutex, exactly as
// the CLI, the daemon, and a registering worker do not. Only the file lock stops their
// read-modify-writes from interleaving, and the symptom when it does is a DROPPED ROW -
// the loser read the ledger before the winner appended, so its write puts back a file
// that never held the winner's unit.
//
// Two Stores in one process is as close as a Go test gets to two processes: the flock is
// per open handle and each Store opens its own, so the kernel arbitrates between them the
// same way. What it cannot reproduce is a lock the OS declines to make exclusive within a
// process, which is why this is a regression test for the drop and not a proof of the
// lock.
func TestStoreUpdateSurvivesSeparateStores(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dir := t.TempDir()
	root := t.TempDir()

	const rounds = 30
	var wg sync.WaitGroup
	errs := make(chan error, 2*rounds)
	for w := range 2 {
		s := NewStore(Location{CacheDir: dir, Root: root})
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range rounds {
				_, err := s.Put(ctx, unit(fmt.Sprintf("w%d-%d", w, i)))
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}

	got, err := NewStore(Location{CacheDir: dir, Root: root}).List()
	require.NoError(t, err)
	ids := make([]string, len(got))
	for i, u := range got {
		ids[i] = u.ID
	}
	assert.Len(t, ids, 2*rounds, "every row both writers appended is still there: %v", ids)
	for w := range 2 {
		for i := range rounds {
			assert.Contains(t, ids, fmt.Sprintf("w%d-%d", w, i))
		}
	}
}

// TestStoreUpdateCreatesTheRowItMerges keeps declaring a unit and advancing one the same
// call: a merge onto an id nothing has written yet starts from a zero row carrying the id.
func TestStoreUpdateCreatesTheRowItMerges(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := NewStore(Location{CacheDir: t.TempDir(), Root: t.TempDir()})
	stored, err := s.Update(ctx, "fresh", func(u *types.DelegationUnit) { u.State = types.StateRunning })
	require.NoError(t, err)
	assert.Equal(t, "fresh", stored.ID)
	assert.Equal(t, types.StateRunning, stored.State)
	assert.NotZero(t, stored.Created)

	_, err = s.Update(ctx, "", func(*types.DelegationUnit) {})
	require.ErrorIs(t, err, ErrNoID, "a merge with no id has no row to address")
}

// TestStorePutRecordsReleasedPaths is the early-release rule the skill teaches, seen
// from the store: a worker that has finished editing a contested path shrinks its
// owned_paths, and the row then has to say WHICH version of that path the waiting unit
// inherits. A release with no digest would leave the waiter guessing.
func TestStorePutRecordsReleasedPaths(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "kept.go"), []byte("package kept\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "released.go"), []byte("package released\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg"), 0o755))

	s := NewStore(Location{CacheDir: t.TempDir(), Root: root})
	_, err := s.Put(ctx, types.DelegationUnit{
		ID:         "u1",
		OwnedPaths: []string{"kept.go", "released.go", "pkg", "gone.go"},
		State:      types.StateRunning,
	})
	require.NoError(t, err)
	before, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, before[0].Releases, "declaring a unit releases nothing")

	stored, err := s.Put(ctx, types.DelegationUnit{
		ID:         "u1",
		OwnedPaths: []string{"kept.go"},
		State:      types.StateRunning,
	})
	require.NoError(t, err)

	require.Len(t, stored.Releases, 3, "every path the put dropped is recorded, in the order it was owned")
	assert.Equal(t, "released.go", stored.Releases[0].Path)
	assert.Equal(t,
		"sha256:"+hashOf(t, filepath.Join(root, "released.go")),
		stored.Releases[0].Digest,
		"the digest is the file's content at the moment it was released")
	assert.NotZero(t, stored.Releases[0].ReleasedAt)
	// A directory has no single content digest and a path with nothing on disk has no
	// content at all; both say so by name rather than by a hash-shaped value.
	assert.Equal(t, types.DelegationRelease{
		Path: "pkg", Digest: types.DigestDir, ReleasedAt: stored.Releases[1].ReleasedAt,
	}, stored.Releases[1])
	assert.Equal(t, types.DelegationRelease{
		Path: "gone.go", Digest: types.DigestAbsent, ReleasedAt: stored.Releases[2].ReleasedAt,
	}, stored.Releases[2])
	for _, r := range stored.Releases {
		assert.NotEqual(t, "kept.go", r.Path, "a path the unit still owns was not released")
	}
}

// A release is the store's to say, not the caller's: a put that does not name
// owned_paths releases nothing, and one that owns a path again stops claiming to have
// released it.
func TestStoreReleasesFollowTheOwnedSet(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("one\n"), 0o644))

	s := NewStore(Location{CacheDir: t.TempDir(), Root: root})
	_, err := s.Put(ctx, types.DelegationUnit{ID: "u1", OwnedPaths: []string{"a.go"}})
	require.NoError(t, err)

	released, err := s.Update(ctx, "u1", func(u *types.DelegationUnit) { u.OwnedPaths = nil })
	require.NoError(t, err)
	require.Len(t, released.Releases, 1)
	first := released.Releases[0].Digest

	advanced, err := s.Update(ctx, "u1", func(u *types.DelegationUnit) { u.State = types.StateRunning })
	require.NoError(t, err)
	assert.Equal(t, released.Releases, advanced.Releases, "a state advance neither releases nor forgets anything")

	// Owned again, edited, released again: the row keeps ONE entry, carrying the version
	// the next agent actually inherits.
	_, err = s.Update(ctx, "u1", func(u *types.DelegationUnit) { u.OwnedPaths = []string{"a.go"} })
	require.NoError(t, err)
	reowned, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, reowned[0].Releases, "a path the unit owns again is not a path it released")

	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("two\n"), 0o644))
	again, err := s.Update(ctx, "u1", func(u *types.DelegationUnit) { u.OwnedPaths = nil })
	require.NoError(t, err)
	require.Len(t, again.Releases, 1)
	assert.NotEqual(t, first, again.Releases[0].Digest, "the second release digests the file as it stands now")
}

func hashOf(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func TestStoreClear(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := NewStore(Location{CacheDir: t.TempDir(), Root: t.TempDir()})
	require.NoError(t, s.Clear(ctx), "clearing a ledger that was never written is not an error")

	_, err := s.Put(ctx, unit("a"))
	require.NoError(t, err)
	_, err = s.Put(ctx, unit("b"))
	require.NoError(t, err)

	require.NoError(t, s.Clear(ctx))
	got, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, got, "a fresh plan starts from an empty ledger")

	// One plan per workspace: nothing was archived, so a put after a clear is row one.
	_, err = s.Put(ctx, unit("c"))
	require.NoError(t, err)
	got, err = s.List()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "c", got[0].ID)
}

func TestStorePersistsAcrossStores(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dir := t.TempDir()
	first := NewStore(Location{CacheDir: dir, Root: t.TempDir()})
	_, err := first.Put(ctx, unit("a"))
	require.NoError(t, err)

	// The point of the store: a plan outlives the process that declared it.
	second := NewStore(Location{CacheDir: dir, Root: t.TempDir()})
	got, err := second.List()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "a", got[0].ID)
	assert.Equal(t, []string{"internal/a"}, got[0].OwnedPaths)

	_, err = os.Stat(filepath.Join(dir, "ledger", "units.json"))
	assert.NoError(t, err, "the ledger lives at <cacheDir>/ledger/units.json")
}

func TestStoreListReturnsCopies(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := NewStore(Location{CacheDir: t.TempDir(), Root: t.TempDir()})
	_, err := s.Put(ctx, unit("a"))
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

	s := NewStore(Location{CacheDir: dir, Root: t.TempDir()})
	_, err := s.List()
	assert.Error(t, err, "a corrupt ledger is reported, never silently read as empty")
}
