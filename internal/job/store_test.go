package job

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/store/migrate"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// tempLocation places a ledger entirely under temp directories, so no test resolves the real
// user state directory. Two stores built from ONE returned value share a leases file;
// two calls never do, whatever roots they are given.
//
// The actor is pinned UNBOUND rather than resolved, so that a developer running the suite
// from a worktree bound to a lease, or under a BAGGAGE that names one, grades these
// fixtures the same way CI does. The rules that turn on a bound actor pass their own.
func tempLocation(t *testing.T, root string) Location {
	t.Helper()
	return Location{StateBase: t.TempDir(), CacheDir: t.TempDir(), Root: root, Actor: &Actor{}}
}

// boundStore is a Store acting as the worker leased to id, sharing loc's ledger file.
func boundStore(loc Location, id string) *Store {
	loc.Actor = &Actor{Lease: id}
	return NewStore(loc)
}

func tempStore(t *testing.T, root string) *Store {
	t.Helper()
	return NewStore(tempLocation(t, root))
}

func lease(id string) types.Job {
	return types.Job{
		ID:         id,
		Agent:      &agent.Spec{Instructions: "goal for " + id, Model: agent.Model{Tier: "bounded"}, Worker: agent.Worker{Type: agent.WorkerTypeResumed}},
		Checkpoint: "abc123",
		WritePaths: []string{"internal/" + id},
		DenyPaths:  []string{"MAGUS.md"},
		Validation: "magus run test",
		State:      types.StateDeclared,
	}
}

// seed writes a row whole, the way a fixture means it: every field this test declared and
// nothing carried over from a previous one.
func seed(t *testing.T, s *Store, row types.Job) types.Job {
	t.Helper()
	stored, err := s.Update(t.Context(), row.ID, func(cur *types.Job) { *cur = row })
	require.NoError(t, err)
	return stored
}

func TestSoftPathsClaimNothingOfTheirOwn(t *testing.T) {
	t.Parallel()

	s := NewStore(tempLocation(t, t.TempDir()))
	soft := func(id string, hard, soft []string) types.Job {
		return types.Job{ID: id, State: types.StateRunning, WritePaths: append(slices.Clone(hard), soft...), SoftPaths: soft}
	}
	seed(t, s, soft("A", []string{"a.go"}, []string{"a_test.go"}))
	seed(t, s, soft("B", []string{"b.go"}, []string{"a_test.go"}))
	rows, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, types.NewJobList(rows).Overlaps, "two rows extending one test file overlap nowhere")

	seed(t, s, soft("C", []string{"a_test.go"}, nil))
	rows, err = s.List()
	require.NoError(t, err)
	overlaps := types.NewJobList(rows).Overlaps
	assert.Empty(t, overlaps, "a hard claim on a soft path is no overlap either")

	seed(t, s, soft("D", []string{"a.go"}, nil))
	rows, err = s.List()
	require.NoError(t, err)
	assert.Len(t, types.NewJobList(rows).Overlaps, 1, "hard against hard still overlaps")

	t.Run("dropping an unwritten soft path records no release", func(t *testing.T) {
		t.Parallel()
		kept, err := s.Update(t.Context(), "A", func(cur *types.Job) { cur.WritePaths, cur.SoftPaths = nil, nil })
		require.NoError(t, err)
		assert.Equal(t, []string{"a.go"}, releasedPaths(kept), "a.go was held; a_test.go never was")
	})
}

func releasedPaths(row types.Job) []string {
	out := make([]string, len(row.Releases))
	for i, r := range row.Releases {
		out[i] = r.Path
	}
	return out
}

// stamped copies the fields the store writes itself from got onto want, so a whole-row
// comparison covers every other field.
func stamped(want, got types.Job) types.Job {
	want.Requires, want.RegisteredBy, want.Created, want.Updated = got.Requires, got.RegisteredBy, got.Created, got.Updated
	want.Authority = got.Authority
	want.Generation = got.Generation
	return want
}

func TestStoreRoundTrip(t *testing.T) {
	t.Parallel()

	stamped := func(row types.Job, generation int) types.Job {
		row.Generation = generation
		return row
	}
	tests := []struct {
		name string
		puts []types.Job
		want []types.Job
	}{
		{
			name: "an unwritten ledger is empty, not an error",
		},
		{
			name: "rows come back in the order they were first recorded",
			puts: []types.Job{lease("a"), lease("b"), lease("c")},
			want: []types.Job{stamped(lease("a"), 1), stamped(lease("b"), 1), stamped(lease("c"), 1)},
		},
		{
			name: "a second put on the same id replaces the row in place",
			puts: []types.Job{
				lease("a"), lease("b"),
				{ID: "a", Agent: agent.Instructed("revised"), State: types.StatePass},
			},
			want: []types.Job{
				// The replacement carries no write paths, so the row's paths were
				// released by it; see TestStorePutRecordsReleasedPaths.
				{
					ID: "a", Agent: agent.Instructed("revised"), State: types.StatePass, Generation: 2,
					Releases: []types.JobRelease{{Path: "internal/a", Digest: types.DigestAbsent}},
				},
				stamped(lease("b"), 1),
			},
		},
		{
			name: "a read-only row carries no paths by design",
			puts: []types.Job{{ID: "scout", Agent: agent.Instructed("inventory"), ReadOnly: true, State: types.StateRunning}},
			want: []types.Job{{ID: "scout", Agent: agent.Instructed("inventory"), ReadOnly: true, State: types.StateRunning, Generation: 1}},
		},
		{
			name: "no_return is stored as its own terminal state",
			puts: []types.Job{{ID: "a", State: types.StateNoReturn}},
			want: []types.Job{{ID: "a", State: types.StateNoReturn, Generation: 1}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := tempStore(t, t.TempDir())
			for _, u := range tt.puts {
				stored := seed(t, s, u)
				assert.NotZero(t, stored.Created, "the store stamps Created")
				assert.NotZero(t, stored.Updated, "the store stamps Updated")
			}
			got, err := s.List()
			require.NoError(t, err)
			require.Len(t, got, len(tt.want))
			for i, w := range tt.want {
				// Timestamps are the store's, not the fixture's;
				// assert them separately and compare the declared facts here.
				assert.Equal(t, trail.LocalOrigin(t.Context()), got[i].RegisteredBy, "the store records the OS account that created the row")
				w.Created, w.Updated, w.RegisteredBy = got[i].Created, got[i].Updated, got[i].RegisteredBy
				require.Len(t, got[i].Releases, len(w.Releases))
				for j := range w.Releases {
					w.Releases[j].ReleasedAt = got[i].Releases[j].ReleasedAt
				}
				assert.Equal(t, w, got[i])
			}
		})
	}
}

// TestStoreIsPrivateToItsOwner pins the modes the first write creates: rows
// carry a repository's plan and its checkout paths.
func TestStoreIsPrivateToItsOwner(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	_, err := s.Update(t.Context(), "row", func(cur *types.Job) { cur.Agent = agent.Instructed("private") })
	require.NoError(t, err)

	info, err := os.Stat(filepath.Dir(s.path))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	info, err = os.Stat(s.path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestStorePutRequiresAnID(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := tempStore(t, t.TempDir())
	row := types.Job{Agent: agent.Instructed("no id")}
	_, err := s.Update(ctx, row.ID, func(cur *types.Job) { *cur = row })
	require.ErrorIs(t, err, ErrNoID, "a row with no id could never be updated or referred to again")

	got, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, got, "a rejected put writes nothing")
}

func TestStorePutPreservesCreatedOnUpdate(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	first := seed(t, s, lease("a"))

	second := seed(t, s, types.Job{ID: "a", State: types.StateFail})
	assert.Equal(t, first.Created, second.Created, "an update keeps the row's original creation time")

	// A caller-supplied timestamp is a fact about the caller's clock, so the store
	// ignores it rather than recording a time the row was not written at.
	third := seed(t, s, types.Job{ID: "a", Created: 1, Updated: 1})
	assert.Equal(t, first.Created, third.Created)
	assert.NotEqual(t, int64(1), third.Updated)
}

// TestStoreUpdateMergesUnderOneLock is what Update exists for. Two writers advancing
// different fields of one row (a state machine and a checkpoint recorder) each
// read-modify-write the same file, and a merge that reads with List and writes back the
// whole row releases the lock in between: the second write then reverts the first one's
// field.
func TestStoreUpdateMergesUnderOneLock(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := tempStore(t, t.TempDir())
	seed(t, s, lease("a"))

	const rounds = 25
	var wg sync.WaitGroup
	errs := make(chan error, 2*rounds)
	for _, apply := range []func(*types.Job){
		func(u *types.Job) { u.State = types.StatePass },
		func(u *types.Job) { u.Checkpoint = "deadbeef" },
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
	assert.Equal(t, "goal for a", got[0].Instructions(), "neither merge erased the row it did not name")
}

// TestStoreUpdateSurvivesSeparateStores is the CROSS-PROCESS half, and the one the
// in-process mutex cannot cover: two Stores on one directory share no mutex, exactly as
// the CLI, the server, and a registering worker do not. Only the file lock stops their
// read-modify-writes from interleaving, and the symptom when it does is a DROPPED ROW:
// the loser read the ledger before the winner appended, so its write puts back a file
// that never held the winner's lease.
//
// Two Stores in one process is as close as a Go test gets to two processes: the flock is
// per open handle and each Store opens its own, so the kernel arbitrates between them the
// same way. What it cannot reproduce is a lock the OS declines to make exclusive within a
// process, which is why this is a regression test for the drop and not a proof of the
// lock.
func TestStoreUpdateSurvivesSeparateStores(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	loc := tempLocation(t, t.TempDir())

	const rounds = 30
	var wg sync.WaitGroup
	errs := make(chan error, 2*rounds)
	for w := range 2 {
		s := NewStore(loc)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range rounds {
				row := lease(fmt.Sprintf("w%d-%d", w, i))
				_, err := s.Update(ctx, row.ID, func(cur *types.Job) { *cur = row })
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}

	got, err := NewStore(loc).List()
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

// TestStoreUpdateCreatesTheRowItMerges keeps declaring a lease and advancing one the same
// call: a merge onto an id nothing has written yet starts from a zero row carrying the id.
func TestStoreUpdateCreatesTheRowItMerges(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := tempStore(t, t.TempDir())
	stored, err := s.Update(ctx, "fresh", func(u *types.Job) { u.State = types.StateRunning })
	require.NoError(t, err)
	assert.NotZero(t, stored.Created)
	assert.Equal(t, stamped(types.Job{ID: "fresh", State: types.StateRunning}, stored), stored)

	_, err = s.Update(ctx, "", func(*types.Job) {})
	require.ErrorIs(t, err, ErrNoID, "a merge with no id has no row to address")
}

// TestStorePutRecordsReleasedPaths is the early-release rule the skill teaches, seen
// from the store: a worker that has finished editing a contested path shrinks its
// write_paths, and the row then has to say WHICH version of that path the waiting lease
// inherits. A release with no digest would leave the waiter guessing.
func TestStorePutRecordsReleasedPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "kept.go"), []byte("package kept\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "released.go"), []byte("package released\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg"), 0o755))

	s := tempStore(t, root)
	seed(t, s, types.Job{
		ID:         "u1",
		WritePaths: []string{"kept.go", "released.go", "pkg", "gone.go"},
		State:      types.StateRunning,
	})
	before, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, before[0].Releases, "declaring a lease releases nothing")

	stored := seed(t, s, types.Job{
		ID:         "u1",
		WritePaths: []string{"kept.go"},
		State:      types.StateRunning,
	})

	require.Len(t, stored.Releases, 3, "every path the put dropped is recorded, in the order it was owned")
	assert.Equal(t, types.JobRelease{
		Path:       "released.go",
		Digest:     "sha256:" + fileHash(t, filepath.Join(root, "released.go")),
		ReleasedAt: stored.Releases[0].ReleasedAt,
	}, stored.Releases[0], "the digest is the file's content at the moment it was released")
	assert.NotZero(t, stored.Releases[0].ReleasedAt)
	// A directory has no single content digest and a path with nothing on disk has no
	// content at all; both say so by name rather than by a hash-shaped value.
	assert.Equal(t, types.JobRelease{
		Path: "pkg", Digest: types.DigestDir, ReleasedAt: stored.Releases[1].ReleasedAt,
	}, stored.Releases[1])
	assert.Equal(t, types.JobRelease{
		Path: "gone.go", Digest: types.DigestAbsent, ReleasedAt: stored.Releases[2].ReleasedAt,
	}, stored.Releases[2])
	for _, r := range stored.Releases {
		assert.NotEqual(t, "kept.go", r.Path, "a path the lease still owns was not released")
	}
}

// Dropping one declaration claim releases that declaration, digested over the lines the
// footprint places in it now, so a later edit elsewhere in the file leaves it unchanged.
func TestStorePutReleasesADeclaration(t *testing.T) {
	t.Parallel()

	const src = "package run\n\nfunc A() {\n\ta()\n}\n\nfunc B() {\n\tb()\n}\n"
	root := testkit.NewRepository(t, map[string]string{".gitattributes": "*.go diff=golang\n", "run.go": src}).Root()
	s := tempStore(t, root)
	seed(t, s, types.Job{ID: "u1", WritePaths: []string{"run.go#A", "run.go#B", "run.go#Gone"}, State: types.StateRunning})

	stored := seed(t, s, types.Job{ID: "u1", WritePaths: []string{"run.go#B"}, State: types.StateRunning})

	require.Len(t, stored.Releases, 2)
	sum := sha256.Sum256([]byte("func A() {\n\ta()\n}\n\n"))
	assert.Equal(t, types.JobRelease{Path: "run.go#A", Digest: "sha256:" + hex.EncodeToString(sum[:]), ReleasedAt: stored.Releases[0].ReleasedAt}, stored.Releases[0],
		"the declaration's own lines, the blank line git places in it included")
	assert.Equal(t, types.DigestAbsent, stored.Releases[1].Digest, "a declaration no line is placed in any more is absent")
}

// A release is the store's to say, not the caller's: a put that does not name
// write_paths releases nothing, and one that owns a path again stops claiming to have
// released it.
func TestStoreReleasesFollowTheWriteSet(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("one\n"), 0o644))

	s := tempStore(t, root)
	seed(t, s, types.Job{ID: "u1", WritePaths: []string{"a.go"}})

	released, err := s.Update(ctx, "u1", func(u *types.Job) { u.WritePaths = nil })
	require.NoError(t, err)
	require.Len(t, released.Releases, 1)
	first := released.Releases[0].Digest

	advanced, err := s.Update(ctx, "u1", func(u *types.Job) { u.State = types.StateRunning })
	require.NoError(t, err)
	assert.Equal(t, released.Releases, advanced.Releases, "a state advance neither releases nor forgets anything")

	// Owned again, edited, released again: the row keeps ONE entry, carrying the version
	// the next agent actually inherits.
	_, err = s.Update(ctx, "u1", func(u *types.Job) { u.WritePaths = []string{"a.go"} })
	require.NoError(t, err)
	reowned, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, reowned[0].Releases, "a path the lease owns again is not a path it released")

	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("two\n"), 0o644))
	again, err := s.Update(ctx, "u1", func(u *types.Job) { u.WritePaths = nil })
	require.NoError(t, err)
	require.Len(t, again.Releases, 1)
	assert.NotEqual(t, first, again.Releases[0].Digest, "the second release digests the file as it stands now")
}

func fileHash(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// What the store OBSERVED is not a caller's to send. A whole-row write that carried these
// would erase a registration nobody withdrew, or claim one that never happened.
func TestStoreKeepsItsOwnRecordAcrossAWholeRowWrite(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := execRepository(t)
	head := repo.Head()
	s := tempStore(t, repo.Root())
	declared := lease("a")
	declared.Checkpoint = head
	seed(t, s, declared)
	_, err := s.Exec(ctx, "a")
	require.NoError(t, err)
	require.NoError(t, s.RecordUnattributedWrite(ctx, "a", "internal/a/store.go"))

	forged := declared
	forged.ReportedBase, forged.BaseVerdict, forged.Registered = "deadbeef", types.BaseDiverged, 1
	forged.Unattributed = []types.JobUnattributedWrite{{Path: "docs/forged.md"}}
	stored := seed(t, s, forged)

	assert.NotZero(t, stored.Registered)
	require.Len(t, stored.Unattributed, 1)
	assert.Equal(t, "internal/a/store.go", stored.Unattributed[0].Path)

	want := stamped(declared, stored)
	want.ReportedBase, want.BaseVerdict = head, types.BaseMatch
	want.Registered, want.CheckoutRoot, want.Unattributed = stored.Registered, stored.CheckoutRoot, stored.Unattributed
	assert.Equal(t, want, stored, "the forged registration is dropped and the one the store observed kept")
}

// A re-fork that only moves a live row's boundaries widens it in place: the holder's state
// and registration survive. One that changes anything else it declares, or no boundary at
// all, hands the job out again as declared.
func TestJobForkWidensALiveRowInPlace(t *testing.T) {
	t.Parallel()

	fork := func(criteria string, write, read []string) func(*types.Job) {
		return Declare(types.Declaration{
			ID: "a", Agent: agent.Instructed(criteria), Checkpoint: "abc123", WritePaths: write, ReadPaths: read, State: types.StateDeclared,
		}, 0)
	}
	for _, tc := range []struct {
		name     string
		from     types.JobState
		criteria string
		write    []string
		read     []string
		want     types.JobState
		widened  int
	}{
		{name: "a running row widened keeps running", from: types.StateRunning, criteria: "goal", write: []string{"a.go", "b.go"}, want: types.StateRunning, widened: 1},
		{name: "an exited row given read paths stays exited", from: types.StateExited, criteria: "goal", write: []string{"a.go"}, read: []string{"docs"}, want: types.StateExited},
		{name: "a re-fork moving no boundary hands the job out again", from: types.StateExited, criteria: "goal", write: []string{"a.go"}, want: types.StateDeclared},
		{name: "a rewritten criteria hands the job out again", from: types.StateRunning, criteria: "new goal", write: []string{"a.go", "b.go"}, want: types.StateDeclared, widened: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			s := tempStore(t, execRepository(t).Root())
			_, err := s.Update(ctx, "a", fork("goal", []string{"a.go"}, nil))
			require.NoError(t, err)
			taken, err := s.Exec(ctx, "a")
			require.NoError(t, err)
			_, err = s.Update(ctx, "a", func(u *types.Job) { u.State = tc.from })
			require.NoError(t, err)

			stored, err := s.Update(ctx, "a", fork(tc.criteria, tc.write, tc.read))
			require.NoError(t, err)
			assert.ElementsMatch(t, tc.write, stored.WritePaths)
			assert.ElementsMatch(t, tc.read, stored.ReadPaths)
			assert.Equal(t, stamped(types.Job{
				ID:           "a",
				Agent:        agent.Instructed(tc.criteria),
				Checkpoint:   "abc123",
				WritePaths:   stored.WritePaths, // order is not what this test pins
				ReadPaths:    stored.ReadPaths,
				State:        tc.want,
				ReportedBase: taken.ReportedBase,
				BaseVerdict:  taken.BaseVerdict,
				Registered:   taken.Registered,
				CheckoutRoot: taken.CheckoutRoot,
				Widenings:    tc.widened,
			}, stored), stored)
		})
	}
}

// TestAForkNamingNoStateIsStoredDeclared pins every door that declares a row: a record
// naming no state is stored as declared, exactly as a flag fork is, because the guard
// reads an empty state as ended and would deny the holder every write.
func TestAForkNamingNoStateIsStoredDeclared(t *testing.T) {
	t.Parallel()

	check := types.LeaseCheck{Target: "go-test", Project: "."}
	for _, tc := range []struct {
		name  string
		fork  func(t *testing.T, s *Store, checkpoint string) (types.Job, error)
		proof types.JobWriteProof
		// validated says the door validates the whole record, which stamps the row.
		validated bool
	}{
		{name: "job fork --stdin", fork: func(t *testing.T, s *Store, checkpoint string) (types.Job, error) {
			row, err := decodeDeclaration(strings.NewReader(fmt.Sprintf(
				`{"api_version":%q,"id":"a","agent":{"instructions": "goal", "worker": {"type": "resumed"}},"checkpoint":%q,"write_paths":["a.go"],"check":{"target":"go-test","project":"."}}`,
				types.JobAPIVersion, checkpoint)))
			require.NoError(t, err)
			return s.Update(t.Context(), row.ID, Declare(row, 0))
		}},
		{name: "magus\\workspace().putJob", proof: types.WriteProofAlone, validated: true, fork: func(t *testing.T, s *Store, checkpoint string) (types.Job, error) {
			put, err := DecodePut(map[string]any{
				"agent": map[string]any{"instructions": "goal", "worker": map[string]any{"type": "resumed"}}, "checkpoint": checkpoint, "write_paths": []any{"a.go"}, "check": "go-test .",
			})
			require.NoError(t, err)
			merge, err := PutMerge(put)
			require.NoError(t, err)
			return ForkMerge(t.Context(), s, "a", merge, config.Jobs{}, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repo := testkit.NewRepository(t, nil)
			head := repo.Head()
			stored, err := tc.fork(t, tempStore(t, repo.Root()), head)
			require.NoError(t, err)
			assert.Equal(t, tc.validated, stored.Validated != 0, "validated stamp")
			assert.Equal(t, types.Job{
				ID:           "a",
				Agent:        agent.Instructed("goal"),
				Checkpoint:   head,
				WritePaths:   []string{"a.go"},
				Check:        &check,
				Validation:   check.String(),
				State:        types.StateDeclared,
				WriteProof:   tc.proof,
				Generation:   1,
				Created:      stored.Created,
				Updated:      stored.Updated,
				Validated:    stored.Validated,
				RegisteredBy: stored.RegisteredBy,
				Authority:    head,
			}, stored)
		})
	}
}

// plantRaw writes body as the store's file, byte for byte.
func plantRaw(t *testing.T, s *Store, body string) string {
	t.Helper()
	path, err := s.Path()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body+"\n"), 0o644))
	return path
}

// storedLines reads the store's file back as JSON, one object per line, not through the
// struct, so a member the struct does not declare is visible.
func storedLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(string(raw), "\n"), "every line of %s ends in a newline", path)
	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSuffix(string(raw), "\n"), "\n") {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m), "line %q", line)
		out = append(out, m)
	}
	return out
}

// storedRow is row id's last line in the file, its record whole.
func storedRow(t *testing.T, path, id string) map[string]any {
	t.Helper()
	var row map[string]any
	for _, line := range storedLines(t, path) {
		if meta, ok := line["metadata"].(map[string]any); ok && meta["name"] == id {
			row = line
		}
	}
	require.NotNil(t, row, "%s holds no row %s", path, id)
	return row
}

// storedSpec is the spec of row id's last line.
func storedSpec(t *testing.T, path, id string) map[string]any {
	t.Helper()
	spec, _ := storedRow(t, path, id)["spec"].(map[string]any)
	return spec
}

// storedStatus is the status of row id's last line.
func storedStatus(t *testing.T, path, id string) map[string]any {
	t.Helper()
	status, _ := storedRow(t, path, id)["status"].(map[string]any)
	return status
}

// recordLine is a Job record line for id, True in condition for reason,
// with the members spec and status add.
func recordLine(id, condition, reason, spec, status string) string {
	conditions := ""
	if condition != "" {
		conditions = fmt.Sprintf(`"conditions":[{"type":%q,"status":"True","lastTransitionTime":"1970-01-01T00:00:01Z","reason":%q,"message":""}],`, condition, reason)
	}
	return fmt.Sprintf(`{"apiVersion":%q,"kind":"Job","metadata":{"name":%q},"spec":{%s},"status":{%s"created":1,"updated":1%s}}`,
		RecordAPIVersion, id, spec, conditions, status)
}

// headerLine is the JobList line at this build's migration.
func headerLine(extra string) string {
	return fmt.Sprintf(`{"apiVersion":%q,"kind":"JobList","migration":%d%s}`, RecordAPIVersion, len(jobMigrations), extra)
}

// A row a newer magus wrote is an ordinary row to this one: updating it or any other row
// keeps the members this binary does not declare.
func TestStoreKeepsANewerRowWhole(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	future := recordLine("future", "Running", "Running", `"futureSpec":{"x":1}`, `,"futureStatus":2`)
	future = strings.Replace(future, `"spec"`, `"futureTop":true,"spec"`, 1)
	path := plantRaw(t, s, fmt.Sprintf("%s\n%s\n%s", headerLine(""), future, recordLine("other", "Running", "Running", "", "")))
	want := map[string]any{"x": float64(1)}
	kept := func(msg string) {
		t.Helper()
		assert.Equal(t, want, storedSpec(t, path, "future")["futureSpec"], msg)
		assert.InDelta(t, 2, storedStatus(t, path, "future")["futureStatus"], 0, msg)
		assert.Equal(t, true, storedRow(t, path, "future")["futureTop"], msg)
	}

	_, err := s.Update(t.Context(), "other", func(u *types.Job) { u.Agent = agent.Instructed("moved") })
	require.NoError(t, err)
	kept("an update of another row keeps this row's unknown members")
	rows, err := s.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"futureTop", "spec.futureSpec", "status.futureStatus"}, rows[0].Unknown, "the row names them")

	_, err = s.Update(t.Context(), "future", func(u *types.Job) { u.State = types.StatePass })
	require.NoError(t, err)
	kept("an update of this row keeps its unknown members")
	conditions := storedStatus(t, path, "future")["conditions"].([]any)
	assert.Equal(t, "Complete", conditions[0].(map[string]any)["type"], "the field the update named was applied")

	// A whole-row replace cannot drop what the record carries.
	_, err = s.Update(t.Context(), "future", func(u *types.Job) { *u = types.Job{State: types.StateRunning} })
	require.NoError(t, err)
	kept("a whole-row replace keeps them")
}

// A row requiring a feature this magus lacks is listed and named, and only a write to it is
// refused: every other row stays writable, and the sweep leaves it alone.
func TestStoreDegradesARowItCannotActOn(t *testing.T) {
	t.Parallel()

	s, notices := sweepStore(t, 10_000, time.Hour)
	future := strings.Replace(recordLine("future", "Declared", "Declared", "", ""),
		`"metadata":{"name":"future"}`, `"metadata":{"name":"future","annotations":{"batch.magus/requires":"never-implemented"}}`, 1)
	other := strings.ReplaceAll(recordLine("other", "Running", "Running", "", ""), `"created":1,"updated":1`, `"created":9999,"updated":9999`)
	path := plantRaw(t, s, fmt.Sprintf("%s\n%s\n%s", headerLine(""), future, other))
	before := storedRow(t, path, "future")

	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 2, "the row is read, not hidden")
	assert.Empty(t, notices.String(), "the sweep does not end a row it may not write, stale as it is")
	assert.Equal(t, []types.JobReadOnly{{Job: "future", Lacks: []string{"never-implemented"}}},
		types.NewJobList(rows).ReadOnly, "every listing names it")

	_, err = s.Update(t.Context(), "future", func(u *types.Job) { u.State = types.StatePass })
	require.ErrorIs(t, err, types.StoreRecordsNewer)
	assert.ErrorContains(t, err, fmt.Sprintf(`future requires "never-implemented", which this magus (%s) lacks: update magus`,
		RecordAPIVersion))
	_, err = s.Delete(t.Context(), "future", true)
	assert.ErrorContains(t, err, `requires "never-implemented"`, "a delete is a write to the row")

	_, err = s.Update(t.Context(), "other", func(u *types.Job) { u.State = types.StatePass })
	require.NoError(t, err, "every other row stays writable")
	after := storedRow(t, path, "future")
	for k, v := range before {
		assert.Equal(t, v, after[k], "the read-only row keeps %s as it was", k)
	}
}

// A requirement on the FILE is the one refusal wider than a row.
func TestStoreRefusesAFileItCannotActOn(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	path := plantRaw(t, s, `{"api_version":"magus.job/v1alpha99","requires":["jobs-v2"]}`)

	_, err := s.List()
	require.ErrorIs(t, err, types.JobStoreWriterOlder)
	assert.ErrorContains(t, err, fmt.Sprintf(`%s requires "jobs-v2", which this magus (%s) lacks, update magus`,
		path, RecordAPIVersion))

	_, err = s.Update(t.Context(), "B", func(cur *types.Job) { *cur = lease("B") })
	require.ErrorIs(t, err, types.JobStoreWriterOlder, "a write refuses the same file")
}

// The file's own envelope survives a rewrite and a clear, like a row's.
func TestStoreKeepsTheFileEnvelope(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	s.clock = func() time.Time { return time.Unix(1_700_000_000, 0) }
	path := plantRaw(t, s, `{"api_version":"magus.job/v1alpha99","plan":"p1"}`)
	seed(t, s, lease("a"))
	_, err := s.Clear(t.Context())
	require.NoError(t, err)

	assert.Equal(t, []map[string]any{{
		"apiVersion": string(RecordAPIVersion), "kind": "JobList", "migration": float64(len(jobMigrations)),
		"plan": "p1", "retained": float64(1_700_000_000),
	}}, storedLines(t, path), "a clear leaves the header's members alone, and the store's own retention stamp rides in it")
}

// An update appends the row's next line and leaves every other line as it was; the last
// line of a row is the row, in the place its first line put it.
func TestStoreUpdateAppendsTheRowsNextLine(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	seed(t, s, lease("a"))
	seed(t, s, lease("b"))
	path, err := s.Path()
	require.NoError(t, err)
	before := storedLines(t, path)

	_, err = s.Update(t.Context(), "a", func(u *types.Job) { u.Agent = agent.Instructed("moved") })
	require.NoError(t, err)
	after := storedLines(t, path)
	require.Len(t, after, len(before)+1, "one line appended, nothing rewritten")
	assert.Equal(t, before, after[:len(before)])
	assert.Equal(t, "a", after[len(before)]["metadata"].(map[string]any)["name"])
	assert.Equal(t, map[string]any{"instructions": "moved", "worker": map[string]any{"type": "Resumed"}},
		after[len(before)]["spec"].(map[string]any)["agent"], "enum values are CamelCase on the record")

	rows, err := s.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, []string{rows[0].ID, rows[1].ID}, "a row keeps its first line's place")
	assert.Equal(t, "moved", rows[0].Instructions())
}

// The file is rewritten to one line per row once superseded lines make it file.CompactDue.
func TestStoreCompactsPastTheBound(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	seed(t, s, lease("a"))
	path, err := s.Path()
	require.NoError(t, err)
	bound := 2 + file.CompactSlack
	for i := range bound {
		_, err := s.Update(t.Context(), "a", func(u *types.Job) { u.Agent = agent.Instructed(fmt.Sprint(i)) })
		require.NoError(t, err)
		assert.False(t, file.CompactDue(len(storedLines(t, path)), 1))
	}
	lines := storedLines(t, path)
	assert.Less(t, len(lines), bound, "the bound was crossed and the file rewritten")
	assert.NotContains(t, lines[0], "id", "a rewrite puts the header first")

	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, fmt.Sprint(bound-1), rows[0].Instructions())
}

// A delete drops the row's every line by rewriting the file.
func TestStoreDeleteRewritesWithoutTheRow(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	seed(t, s, lease("a"))
	seed(t, s, lease("b"))
	_, err := s.Update(t.Context(), "a", func(u *types.Job) { u.Agent = agent.Instructed("moved") })
	require.NoError(t, err)
	_, err = s.Delete(t.Context(), "a", false)
	require.NoError(t, err)

	path, err := s.Path()
	require.NoError(t, err)
	lines := storedLines(t, path)
	require.Len(t, lines, 2, "the header and b")
	assert.Equal(t, "b", lines[1]["metadata"].(map[string]any)["name"])
}

// A reader catching an append mid-write waits for it; a line no writer finished stays an
// error to readers, and the next write drops it and says so.
func TestStoreHandlesALineNoWriterFinished(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	var notices strings.Builder
	s.notices = &notices
	seed(t, s, lease("a"))
	path, err := s.Path()
	require.NoError(t, err)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(`{"id":"b","sta`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, err = s.List()
	require.ErrorContains(t, err, "ends in a line no writer finished")

	seed(t, s, lease("c"))
	assert.Contains(t, notices.String(), "ended in a line no writer finished; the rewrite dropped it")
	rows, err := s.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "c"}, []string{rows[0].ID, rows[1].ID})
}

// A plan an older magus kept as one JSON document is refused, never read as empty, and the
// error preserves it while an older checkout may still own its jobs.
func TestStoreRefusesTheOneDocumentPlan(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	path, err := s.Path()
	require.NoError(t, err)
	old := filepath.Join(filepath.Dir(path), "jobs.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(old, []byte(`{"jobs":[{"id":"a"}]}`+"\n"), 0o644))

	_, err = s.List()
	require.ErrorIs(t, err, file.ErrLegacyStore)
	require.ErrorIs(t, err, types.LegacyStorePresent)
	require.ErrorContains(t, err, "preserve it")
	require.ErrorContains(t, err, fmt.Sprintf("set it aside with `mv %s %s.old`", old, old), "the refusal names the move made below")
	_, err = s.Update(t.Context(), "a", func(*types.Job) {})
	require.ErrorIs(t, err, file.ErrLegacyStore, "a write is refused too")
	raw, err := os.ReadFile(old)
	require.NoError(t, err)
	assert.Equal(t, "{\"jobs\":[{\"id\":\"a\"}]}\n", string(raw))
	assert.NoFileExists(t, path)

	require.NoError(t, os.Rename(old, old+".old"))
	seed(t, s, lease("a"))
}

func TestShellQuoteKeepsAPathOneWord(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "/state/jobs/jobs.json", shellQuote("/state/jobs/jobs.json"))
	assert.Equal(t, `'/my state/it'\''s/jobs.json'`, shellQuote("/my state/it's/jobs.json"))
}

func TestStoreClear(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := tempStore(t, t.TempDir())
	dropped, err := s.Clear(ctx)
	require.NoError(t, err, "clearing a ledger that was never written is not an error")
	assert.Zero(t, dropped)

	seed(t, s, lease("a"))
	seed(t, s, lease("b"))

	dropped, err = s.Clear(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, dropped, "the count comes from inside the lock, so no door recomputes it")
	got, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, got, "a fresh plan starts from an empty ledger")

	// One plan per repository: the rows are archived, not kept, so a put after a clear is
	// row one.
	seed(t, s, lease("c"))
	got, err = s.List()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "c", got[0].ID)
}

// A cleared plan is still legible afterwards. The count a door prints back tells a caller
// it wiped somebody else's rows and gives them no way to read them again.
func TestClearArchivesWhatItDropped(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	seed(t, s, lease("adj/store"))
	path, err := s.Path()
	require.NoError(t, err)

	dropped, err := s.Clear(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, dropped)

	archives, err := filepath.Glob(filepath.Join(filepath.Dir(path), "jobs-*.jsonl"))
	require.NoError(t, err)
	require.Len(t, archives, 1)
	lines := storedLines(t, archives[0])
	require.Len(t, lines, 2, "the header and the row, in the ledger's own shape")
	assert.Equal(t, "adj/store", lines[1]["metadata"].(map[string]any)["name"])
}

func TestStorePersistsAcrossStores(t *testing.T) {
	t.Parallel()

	loc := tempLocation(t, t.TempDir())
	first := NewStore(loc)
	seed(t, first, lease("a"))

	// The point of the store: a plan outlives the process that declared it.
	second := NewStore(loc)
	got, err := second.List()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, stamped(lease("a"), got[0]), got[0])
}

// The split this move closes: an orchestrator declares a plan in one worktree and a
// worker in another reads the same rows. Two CHECKOUTS, one repository, one job store.
func TestLedgerIsSharedAcrossWorktreesOfOneRepository(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	git := filepath.Join(repo, ".git")
	require.NoError(t, os.MkdirAll(filepath.Join(git, "worktrees", "worker"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(git, "config"),
		[]byte("[remote \"origin\"]\n\turl = git@example.com:acme/widget.git\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(git, "worktrees", "worker", "commondir"), []byte("../..\n"), 0o644))
	worker := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(worker, ".git"),
		[]byte(fmt.Sprintf("gitdir: %s\n", filepath.Join(git, "worktrees", "worker"))), 0o644))

	base := t.TempDir()
	orchestrator := NewStore(Location{StateBase: base, CacheDir: t.TempDir(), Root: repo})
	seed(t, orchestrator, lease("plan/unit"))

	// A separate cache dir, which is what a second worktree actually has.
	got, err := NewStore(Location{StateBase: base, CacheDir: t.TempDir(), Root: worker}).List()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "plan/unit", got[0].ID)
}

// A member neither this binary's struct nor this call introduced survives an update of the
// row that carries it.
func TestStoreUpdatePreservesAnUnknownMember(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	path := plantRaw(t, s, fmt.Sprintf(
		`{"id":"adj/store","api_version":%q,"state":"running","created":1,"updated":1,"woolgathering":7}`,
		types.JobAPIVersion))

	_, err := s.Update(t.Context(), "adj/store", func(u *types.Job) { u.State = types.StatePass })
	require.NoError(t, err)

	assert.Equal(t, float64(7), storedRow(t, path, "adj/store")["woolgathering"],
		"a member this binary does not know survives an update to a field it does know")

	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, types.StatePass, rows[0].State, "the field the update named was still applied")
}

func TestStoreListReturnsCopies(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	seed(t, s, lease("a"))

	got, err := s.List()
	require.NoError(t, err)
	got[0].WritePaths[0] = "mutated"
	got[0].Agent = agent.Instructed("mutated")

	again, err := s.List()
	require.NoError(t, err)
	assert.Equal(t, stamped(lease("a"), again[0]), again[0], "a caller mutating a returned row cannot reach the store")
}

func TestStoreReportsAnUnreadableFile(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	path, err := s.Path()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("{not json\n"), 0o644))

	_, err = s.List()
	assert.ErrorContains(t, err, path+":1: ", "a corrupt job store is reported, never silently read as empty")
	assert.ErrorIs(t, err, types.StoreLineUndecodable)
}

func TestRecordUnattributedWrite(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644))
	s := tempStore(t, root)
	ctx := t.Context()

	seed(t, s, types.Job{ID: "worker-1", WritePaths: []string{"a.go"}})

	require.NoError(t, s.RecordUnattributedWrite(ctx, "worker-1", "a.go"))

	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Len(t, rows[0].Unattributed, 1)
	got := rows[0].Unattributed[0]
	assert.Equal(t, "a.go", got.Path)
	assert.NotEmpty(t, got.Digest, "the digest is the whole signal: without it there is nothing to compare")
	assert.NotEqual(t, types.DigestAbsent, got.Digest)
	assert.NotZero(t, got.At)
}

// One row per path, newest wins. A person saves a file a dozen times while an agent works, and
// eleven superseded digests would bury the only one that describes the tree.
func TestRecordUnattributedWriteKeepsOneRowPerPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("first\n"), 0o644))
	s := tempStore(t, root)
	ctx := t.Context()

	seed(t, s, types.Job{ID: "worker-1", WritePaths: []string{"a.go"}})
	require.NoError(t, s.RecordUnattributedWrite(ctx, "worker-1", "a.go"))

	rows, err := s.List()
	require.NoError(t, err)
	first := rows[0].Unattributed[0].Digest

	require.NoError(t, os.WriteFile(path, []byte("second\n"), 0o644))
	require.NoError(t, s.RecordUnattributedWrite(ctx, "worker-1", "a.go"))

	rows, err = s.List()
	require.NoError(t, err)
	require.Len(t, rows[0].Unattributed, 1, "one row per path")
	assert.NotEqual(t, first, rows[0].Unattributed[0].Digest, "the newest write is the one that describes the tree")
}

// The guard calls this on every write it grades, so an unbounded list would make a long editing
// session against a declared plan grow the ledger without limit.
func TestRecordUnattributedWriteIsBounded(t *testing.T) {
	root := t.TempDir()
	s := tempStore(t, root)
	ctx := t.Context()

	const over = 5
	var owned []string
	for i := range MaxUnattributedWrites + over {
		owned = append(owned, fmt.Sprintf("f%d.go", i))
	}
	seed(t, s, types.Job{ID: "worker-1", WritePaths: owned})
	for _, p := range owned {
		require.NoError(t, s.RecordUnattributedWrite(ctx, "worker-1", p))
	}

	rows, err := s.List()
	require.NoError(t, err)
	kept := rows[0].Unattributed
	require.Len(t, kept, MaxUnattributedWrites)
	// Written f0..f(Max+over-1), so trimming to the last Max drops exactly the first `over`.
	assert.Equal(t, fmt.Sprintf("f%d.go", over), kept[0].Path, "the oldest are dropped")
	assert.Equal(t, fmt.Sprintf("f%d.go", MaxUnattributedWrites+over-1), kept[len(kept)-1].Path)
}

// The generation counts declared-spec changes only: a lifecycle move, a deadline and a
// caller's own count leave it where it was.
func TestStoreBumpsTheGenerationOnlyForASpecChange(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	row := lease("a")
	created := seed(t, s, row)
	wantCreated := created
	wantCreated.Generation = 1
	wantCreated.Requires = nil // a row carrying only its generation stays writable by an older magus
	assert.Equal(t, wantCreated, created)

	row.State = types.StateRunning
	row.Deadline = 1 << 40
	assert.Equal(t, 1, seed(t, s, row).Generation, "state and deadline are not the spec")

	require.NoError(t, s.RecordRework(t.Context(), "a"))
	row.Agent = agent.Instructed("a sharper goal")
	assert.Equal(t, 2, seed(t, s, row).Generation)

	row.Generation = 9
	forged := seed(t, s, row)
	wantForged := forged
	wantForged.Generation = 2 // a caller's generation is replaced by the stored one
	assert.Equal(t, wantForged, forged)

	row.MaxAttempts = 3
	capped := seed(t, s, row)
	wantCapped := capped
	// max_attempts is spec, and an older magus would ignore the cap.
	wantCapped.Generation = 3
	wantCapped.Requires = []string{"job-generations"}
	assert.Equal(t, wantCapped, capped)
}

func TestStoreKeepsTheGenerationWhenWritePathsOnlyWiden(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	row := lease("a")
	row.WritePaths = []string{"internal/job/plan.go"}
	assert.Equal(t, 1, seed(t, s, row).Generation)

	row.WritePaths = []string{"internal/job/plan.go", "internal/job/plan_test.go"}
	assert.Equal(t, 1, seed(t, s, row).Generation, "an added path asks nothing the evidence did not answer")

	row.WritePaths = []string{"internal/job"}
	assert.Equal(t, 1, seed(t, s, row).Generation, "a path replaced by one holding it widens")

	row.WritePaths = []string{"cmd/magus/job.go"}
	assert.Equal(t, 2, seed(t, s, row).Generation, "narrowing or moving the paths is a new spec")

	row.WritePaths = []string{"cmd/magus/job.go", "types/job.go"}
	row.Agent = agent.Instructed("a sharper goal")
	assert.Equal(t, 3, seed(t, s, row).Generation, "a widening beside another change still counts")
}

// endAttempt ends row a as failed, its holder having run a failing check, been sent more
// work once and left a risk.
func endAttempt(t *testing.T, s *Store) {
	t.Helper()
	require.NoError(t, s.RecordRework(t.Context(), "a"))
	_, err := s.Update(t.Context(), "a", func(u *types.Job) {
		u.State = types.StateFail
		u.Attempt = &types.JobAttempt{Ref: "out1", Failed: true}
	})
	require.NoError(t, err)
	fileTry(t, s, "a", &types.JobResult{UnresolvedRisks: []string{"the second worktree was not tried"}})
}

// A fork of an ended row is the job's next attempt, and the one that
// ended is folded for the next holder. The row's evidence and counters
// start over, so the next ending counts its own attempt alone.
func TestStoreForkOfAnEndedRowFoldsTheAttempt(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	row := lease("a")
	first := seed(t, s, row)
	endAttempt(t, s)

	again := seed(t, s, row)

	tries := triesIn(t, s)
	prior := tries.Prior("a")
	require.Len(t, prior, 1)
	got := prior[0]
	// The trail of this checkout is empty, so the fold read no denial.
	want := types.Attempt{Job: "a", N: 1, Status: types.AttemptStatus{
		Ended: got.Status.Ended, End: types.AttemptEndExit,
		Result: &types.JobResult{UnresolvedRisks: []string{"the second worktree was not tried"}},
		State:  types.StateFail, ObservedGeneration: 1, Checkpoint: first.Checkpoint, ReworkRounds: 1,
		FailingRefs: []string{"out1"}, DenialsKnown: true, Retry: types.RetryVerdict{Retryable: true},
	}}
	assert.Equal(t, want, got)
	assert.Equal(t, 2, tries.LiveN("a"))
	assert.Nil(t, tries.Result("a"), "the next try has filed nothing")

	// An unchanged spec keeps its generation, the next attempt counts its
	// own rework rounds, and the tries are not on the row.
	wantAgain := first
	wantAgain.Updated = again.Updated
	assert.Equal(t, wantAgain, again)

	for range 3 {
		endAttempt(t, s)
		seed(t, s, row)
	}
	tries = triesIn(t, s)
	assert.Equal(t, 5, tries.LiveN("a"))
	var ns []int
	for _, p := range tries.Prior("a") {
		ns = append(ns, p.N)
	}
	assert.Equal(t, []int{1, 2, 3, 4}, ns, "every ended try is kept")
}

// max_attempts is checked at a fork of an ended row and nowhere else, against the cap the
// fork's own record carries.
func TestStoreRefusesAForkPastMaxAttempts(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	row := lease("a")
	row.MaxAttempts = 2
	seed(t, s, row)
	row.Agent = agent.Instructed("reworded while live")
	seed(t, s, row)
	endAttempt(t, s)
	seed(t, s, row)
	endAttempt(t, s)

	_, err := s.Update(t.Context(), "a", func(u *types.Job) { *u = row })
	require.ErrorIs(t, err, ErrMaxAttempts)
	assert.ErrorContains(t, err, "a used 2 of its 2 attempt(s)")
	assert.Equal(t, types.RetryAfterPerson, types.ClassifyRetry(err).Change)
	rows, err := s.List()
	require.NoError(t, err)
	assert.Equal(t, types.StateFail, rows[0].State, "a refused fork writes nothing")
	assert.Equal(t, 2, triesIn(t, s).LiveN("a"))

	row.MaxAttempts = 3
	seed(t, s, row)
	assert.Equal(t, 3, triesIn(t, s).LiveN("a"), "a record raising the cap forks past the old one")
}

// The guard records a holder's denials in the trail of the checkout it worked in, so the
// fold reads that trail and not the forker's.
func TestStoreFoldsTheDenialsFromTheHoldersCheckout(t *testing.T) {
	t.Parallel()

	holder, holderTrail := t.TempDir(), t.TempDir()
	deny := func(base, rule string) {
		trail.AppendAgentCommand(t.Context(), base, trail.AgentCommand{
			Host: "claude-code", Session: "s1", Tool: "file.write", Path: "docs/x.md",
			Decision: "deny", Reason: "docs/x.md is no lease's", Rule: rule, Lease: "a",
		})
	}
	deny(holderTrail, "lease-write")
	ended := lease("a")
	ended.State, ended.CheckoutRoot, ended.Created, ended.Updated = types.StateFail, holder, 1, 2

	for _, tc := range []struct {
		name      string
		resolve   func(string) (string, error)
		known     bool
		rules     []string
		retryable bool
	}{
		{name: "the holder's checkout trail", resolve: func(root string) (string, error) {
			assert.Equal(t, holder, root)
			return holderTrail, nil
		}, known: true, rules: []string{"lease-write"}},
		{name: "no resolver for another checkout", known: false, retryable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loc := tempLocation(t, t.TempDir())
			loc.TrailBase = tc.resolve
			deny(loc.CacheDir, "raw-tool")
			s := NewStore(loc)
			plant(t, s, ended)

			seed(t, s, lease("a"))

			prior := triesIn(t, s).Prior("a")
			require.Len(t, prior, 1)
			var rules []string
			for _, d := range prior[0].Status.Denials {
				rules = append(rules, d.Rule)
			}
			assert.Equal(t, tc.rules, rules)
			assert.Equal(t, tc.known, prior[0].Status.DenialsKnown)
			assert.Equal(t, tc.retryable, prior[0].Status.Retry.Retryable)
		})
	}
}

// A lease that ended between the grading and the record is nothing to report to anybody, and
// inventing a row for it would put a plan in the ledger nobody declared.
func TestRecordUnattributedWriteDoesNotInventARow(t *testing.T) {
	s := tempStore(t, t.TempDir())

	require.NoError(t, s.RecordUnattributedWrite(t.Context(), "never-declared", "a.go"))

	rows, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, rows)
}

// realSizedRows is n rows shaped like the store that made `magus job fork`
// take minutes under load. Half exited uncollected and a third ended.
// Each carries a criteria paragraph, aged across a month. Every row
// was taken in checkout, so each exited one is a landing candidate.
func realSizedRows(n int, now int64, checkout string) []types.Job {
	criteria := strings.Repeat("Address the findings in the plan's section and say which were deferred. ", 6)
	rows := make([]types.Job, n)
	for i := range rows {
		age := int64(i*2357) % (30 * 24 * 3600)
		row := types.Job{
			ID:           fmt.Sprintf("job-%04d", i),
			Agent:        &agent.Spec{Instructions: criteria, Model: agent.Model{ID: "sonnet"}, Worker: agent.Worker{Type: agent.WorkerTypeResumed}},
			Checkpoint:   "08566ec817db6ff7932f0a4d62d8a9ed08f8ca18",
			Validation:   "magus run test . -- -run Store",
			Registered:   now - age,
			CheckoutRoot: checkout,
			Created:      now - age,
			Updated:      now - age,
		}
		for p := range 8 {
			row.WritePaths = append(row.WritePaths, fmt.Sprintf("internal/p%d/file%d.go", i, p))
		}
		switch r := i % 100; {
		case r < 50:
			row.State = types.StateExited
		case r < 87:
			row.State = types.StateNoReturn
		case r < 95:
			row.State = types.StatePass
		case r < 98:
			row.State = types.StateRunning
		default:
			row.State = types.StateDeclared
		}
		if row.State.Terminal() && i > 0 && rows[i-1].State.Terminal() {
			row.Parent = rows[i-1].ID
		}
		rows[i] = row
	}
	return rows
}

// BenchmarkStoreAtRealSize reads and writes a store the size of the one that stalled
// forks: 1,100 rows as written, and the same rows after a retention pass. probes/op is the
// landing probes one read starts once the probe interval has passed, each a run of VCS
// subprocesses in a real checkout.
func BenchmarkStoreAtRealSize(b *testing.B) {
	now := time.Now().Unix()
	for _, tc := range []struct {
		name   string
		retain time.Duration
		// due fills the file with superseded lines up to the bound file.CompactDue sets, the
		// most a read ever decodes.
		due bool
	}{
		{"as-written", 0, false},
		{"due", 0, true},
		{"retained", config.Defaults().Jobs.Retain, false},
	} {
		s := NewStore(Location{StateBase: b.TempDir(), CacheDir: b.TempDir(), Root: b.TempDir(), Actor: &Actor{}})
		s.retain = &tc.retain
		s.clock = func() time.Time { return time.Unix(now, 0) }
		stale := 2 * time.Hour
		s.staleAfter = &stale
		probes := 0
		s.landed = func(context.Context, types.Job) string { probes++; return "" }
		rows := realSizedRows(1100, now, b.TempDir())
		// A pass long overdue, so this write runs one, as the first write past the
		// interval does on a store that has grown this large.
		if err := s.update(b.Context(), func(f *jobsFile) error { f.Jobs, f.Retained = rows, 1; return nil }); err != nil {
			b.Fatal(err)
		}
		if tc.due {
			path, err := s.Path()
			if err != nil {
				b.Fatal(err)
			}
			var superseded [][]byte
			for len(rows)+1+len(superseded) < 2*len(rows)+file.CompactSlack {
				line, err := json.Marshal(rows[len(superseded)%len(rows)])
				if err != nil {
					b.Fatal(err)
				}
				superseded = append(superseded, line)
			}
			if err := file.AppendLines(path, superseded, 0o644); err != nil {
				b.Fatal(err)
			}
		}

		b.Run(tc.name+"/list", func(b *testing.B) {
			probes = 0
			var n int
			for b.Loop() {
				got, err := s.List()
				if err != nil {
					b.Fatal(err)
				}
				n = len(got)
			}
			b.ReportMetric(float64(n), "rows")
			b.ReportMetric(float64(probes)/float64(b.N), "probes/op")
		})
		b.Run(tc.name+"/update", func(b *testing.B) {
			for b.Loop() {
				if _, err := s.Update(b.Context(), "job-0098", func(u *types.Job) { u.Agent.Instructions += "." }); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// A store another commit stamped is this build's to write, however much newer that
// commit is: only a schema this magus cannot act on refuses (see
// TestStoreRefusesAFileItCannotActOn).
func TestStoreWritesAStoreANewerCommitStamped(t *testing.T) {
	s := tempStore(t, t.TempDir())
	plantRaw(t, s, fmt.Sprintf(`{"api_version":%q,"writer":{"commit":"8a9d","time":"2099-01-01T00:00:00Z"}}`, types.JobAPIVersion))

	_, err := s.Update(t.Context(), "B", func(cur *types.Job) { *cur = lease("B") })
	require.NoError(t, err)
	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "B", rows[0].ID)
}

// migratingStore is a Store over a planted file, with migrations and its notices kept.
func (s *jobSuite) migratingStore(body string, migrations ...migrate.Migration) (*Store, string, *bytes.Buffer) {
	st := tempStore(s.T(), s.T().TempDir())
	notices := &bytes.Buffer{}
	st.notices, st.migrations = notices, migrations
	return st, plantRaw(s.T(), st, body), notices
}

// renamed is a migration that renames one spec member of every row carrying it.
func renamed(from, to string) migrate.Migration {
	return migrate.Migration{Name: fmt.Sprintf("%s to %s", from, to), Up: func(e migrate.Entry) (migrate.Entry, error) {
		var spec map[string]json.RawMessage
		if err := json.Unmarshal(e["spec"], &spec); err != nil {
			return nil, err
		}
		if v, ok := spec[from]; ok {
			delete(spec, from)
			spec[to] = v
		}
		var err error
		e["spec"], err = json.Marshal(spec)
		return e, err
	}}
}

// unmigrated is a header behind every migration, and row a running
// with spec member name.
func unmigrated(name string) string {
	return fmt.Sprintf("{\"apiVersion\":%q,\"kind\":\"JobList\"}\n%s", RecordAPIVersion,
		recordLine("a", "Running", "Running", fmt.Sprintf(`%q:"ship it"`, name), ""))
}

func (s *jobSuite) TestTheJobStoreMigratesOneVersionOnce() {
	st, path, notices := s.migratingStore(unmigrated("goal"), renamed("goal", "parent"))
	notice := fmt.Sprintf("job: migrated job store %s from 0 to 1 (1 entries)\n", path)

	f, err := st.read()
	s.Require().NoError(err)
	s.Require().Len(f.Jobs, 1)
	s.Equal("ship it", f.Jobs[0].Parent, "the row reads under the name the migration gave it")
	s.InDelta(1, storedLines(s.T(), path)[0]["migration"], 0, "the header records the migration")
	s.Equal(notice, notices.String())

	_, err = st.read()
	s.Require().NoError(err)
	s.Equal(notice, notices.String(), "a second read migrates nothing")
}

func (s *jobSuite) TestTheJobStoreMigratesSeveralVersionsFromWhereItStands() {
	body := strings.Replace(unmigrated("aim"), `"kind":"JobList"`, `"kind":"JobList","migration":1`, 1)
	st, path, _ := s.migratingStore(body, renamed("goal", "aim"), renamed("aim", "intent"), renamed("intent", "parent"))

	_, err := st.Update(s.T().Context(), "a", func(u *types.Job) { u.Checkpoint = "abc123" })
	s.Require().NoError(err)
	spec := storedSpec(s.T(), path, "a")
	s.Equal("ship it", spec["parent"], "migrations 2 and 3 ran, in order, before the write")
	s.Equal("abc123", spec["checkpoint"])
	s.InDelta(3, storedLines(s.T(), path)[0]["migration"], 0)
}

func (s *jobSuite) TestTheJobStoreAtItsMigrationIsLeftAlone() {
	body := fmt.Sprintf("{\"apiVersion\":%q,\"kind\":\"JobList\",\"migration\":1}\n%s", RecordAPIVersion, recordLine("a", "Running", "Running", "", ""))
	st, path, notices := s.migratingStore(body, migrate.Migration{Name: "never", Up: func(migrate.Entry) (migrate.Entry, error) {
		return nil, errors.New("ran")
	}})

	_, err := st.read()
	s.Require().NoError(err)
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)
	s.Equal(body+"\n", string(raw))
	s.Empty(notices.String())
}

func (s *jobSuite) TestTheJobStoreRefusesAStorePastItsMigrations() {
	body := fmt.Sprintf("{\"apiVersion\":%q,\"kind\":\"JobList\",\"migration\":3}\n%s", RecordAPIVersion, recordLine("a", "Running", "Running", "", ""))
	st, path, _ := s.migratingStore(body, renamed("goal", "parent"))

	_, err := st.read()
	s.Require().ErrorIs(err, types.JobStoreWriterOlder)
	s.ErrorContains(err, fmt.Sprintf("job store %s is at migration 3, newer than this magus knows (1): update magus", path))
	_, err = st.Update(s.T().Context(), "b", func(cur *types.Job) { *cur = lease("b") })
	s.Require().ErrorIs(err, types.JobStoreWriterOlder, "a write refuses it too")
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)
	s.Equal(body+"\n", string(raw))
}

func (s *jobSuite) TestAnInterruptedJobStoreMigrationLeavesTheFileIntact() {
	body := unmigrated("goal")
	st, path, notices := s.migratingStore(body, renamed("goal", "parent"), migrate.Migration{Name: "fail", Up: func(migrate.Entry) (migrate.Entry, error) {
		return nil, errors.New("disk on fire")
	}})

	_, err := st.read()
	s.Require().ErrorContains(err, "migration 2 (fail): disk on fire")
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)
	s.Equal(body+"\n", string(raw), "the first migration's work never reached the file")
	s.Empty(notices.String())
}

func (s *jobSuite) TestAFreshJobStoreIsWrittenAtItsMigration() {
	st := tempStore(s.T(), s.T().TempDir())
	st.migrations = []migrate.Migration{renamed("goal", "parent"), renamed("aim", "parent")}
	seed(s.T(), st, lease("a"))

	path, err := st.Path()
	s.Require().NoError(err)
	s.InDelta(2, storedLines(s.T(), path)[0]["migration"], 0, "a new store needs no migration")
}

// checkoutStore is a store at root, behind its one migration, holding row a in
// state with the members extra adds.
func (s *jobSuite) checkoutStore(root, state string, extra map[string]any) (*Store, string, *bytes.Buffer) {
	st := tempStore(s.T(), root)
	r := recordJob(types.Job{ID: "a", State: types.JobState(state), Created: 1, Updated: 1}, nil)
	r.Spec.Unknown = map[string]json.RawMessage{"goal": json.RawMessage(`"ship it"`)}
	line, err := json.Marshal(r)
	s.Require().NoError(err)
	var row map[string]any
	s.Require().NoError(json.Unmarshal(line, &row))
	maps.Copy(row["status"].(map[string]any), extra)
	line, err = json.Marshal(row)
	s.Require().NoError(err)
	notices := &bytes.Buffer{}
	st.notices, st.migrations = notices, []migrate.Migration{renamed("goal", "parent")}
	return st, plantRaw(s.T(), st, fmt.Sprintf("{\"apiVersion\":%q,\"kind\":\"JobList\"}\n%s", RecordAPIVersion, line)), notices
}

// forkCheckout is the worktree member of a row whose fork made root.
func forkCheckout(root string) map[string]any {
	return map[string]any{"worktree": map[string]any{"root": root, "source": "/src", "base": "abc123", "created": 1, "made": true}}
}

func (s *jobSuite) TestAJobCheckoutNeverMigratesTheSharedStore() {
	root := s.T().TempDir()
	st, path, notices := s.checkoutStore(root, "running", forkCheckout(root))
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)

	_, err = st.read()
	s.Require().ErrorContains(err, fmt.Sprintf("job store %s is at migration 0, and this magus writes migration 1 from %s, the checkout of live job a", path, root))
	s.ErrorContains(err, "the root integrates this change and migrates the store once, from its own checkout")
	_, err = st.Update(s.T().Context(), "b", func(cur *types.Job) { *cur = lease("b") })
	s.Require().ErrorContains(err, "a job's checkout never migrates the store", "a write refuses it too")
	after, err := os.ReadFile(path)
	s.Require().NoError(err)
	s.Equal(string(raw), string(after), "nothing was written")
	s.Empty(notices.String())
}

func (s *jobSuite) TestADirectoryInsideAJobCheckoutNeverMigratesTheSharedStore() {
	root := s.T().TempDir()
	nested := filepath.Join(root, "sub")
	s.Require().NoError(os.Mkdir(nested, 0o755))
	st, _, _ := s.checkoutStore(nested, "declared", forkCheckout(root))

	_, err := st.read()
	s.Require().ErrorContains(err, "a job's checkout never migrates the store")
}

func (s *jobSuite) TestTheCheckoutOfAnEndedJobMigrates() {
	root := s.T().TempDir()
	st, path, _ := s.checkoutStore(root, "pass", forkCheckout(root))

	f, err := st.read()
	s.Require().NoError(err)
	s.Equal("ship it", f.Jobs[0].Parent)
	s.InDelta(1, storedLines(s.T(), path)[0]["migration"], 0)
}

func (s *jobSuite) TestTheRootMigratesWhileAJobHoldsAnotherCheckout() {
	st, path, _ := s.checkoutStore(s.T().TempDir(), "running", forkCheckout(s.T().TempDir()))

	_, err := st.read()
	s.Require().NoError(err)
	s.InDelta(1, storedLines(s.T(), path)[0]["migration"], 0)
}

// A job taken in the root's checkout without a fork's checkout leaves that
// checkout the root's, which migrates as before.
func (s *jobSuite) TestACheckoutAJobWasOnlyTakenInMigrates() {
	root := s.T().TempDir()
	st, path, _ := s.checkoutStore(root, "running", map[string]any{"checkoutRoot": root})

	_, err := st.read()
	s.Require().NoError(err)
	s.InDelta(1, storedLines(s.T(), path)[0]["migration"], 0)
}

// agentSectionSuite pins the migration that moves a row's flat model,
// criteria and fresh_reason under agent.
type agentSectionSuite struct {
	suite.Suite
}

func TestAgentSectionMigration(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(agentSectionSuite))
}

// migrated is the row line after the job store's own migrations ran over it.
func (s *agentSectionSuite) migrated(line string) types.Job {
	st := tempStore(s.T(), s.T().TempDir())
	st.notices, st.migrations = &bytes.Buffer{}, jobMigrations
	path := plantRaw(s.T(), st, fmt.Sprintf("%s\n%s", `{"api_version":"magus.job/v1alpha1"}`, line))
	f, err := st.read()
	s.Require().NoError(err)
	s.Require().Len(f.Jobs, 1)
	row := storedRow(s.T(), path, f.Jobs[0].ID)
	for _, key := range []string{"criteria", "model", "fresh_reason"} {
		s.NotContains(row, key, "the migration keeps no flat member")
	}
	return f.Jobs[0]
}

func (s *agentSectionSuite) TestCriteriaAndATierNameMoveUnderAgent() {
	row := s.migrated(`{"id":"a","state":"running","created":1,"updated":1,"criteria":"ship it","model":"principal",` +
		`"model_tier":{"name":"principal","rank":2,"skills":"short","source":"row"}}`)
	s.Equal(&agent.Spec{
		Instructions: "ship it",
		Model:        agent.Model{Tier: "design"},
		Worker:       agent.Worker{Type: agent.WorkerTypeResumed},
	}, row.Agent)
	s.Require().NotNil(row.ModelTier)
	s.Equal("design", row.ModelTier.Name, "the resolved tier stays on the row, respelled by the four-tier migration")
	s.Equal(agent.ModelTierSourceJob, row.ModelTier.Source, "the flat source row reads as job")
}

func (s *agentSectionSuite) TestAModelTheTierMappedBecomesAnID() {
	row := s.migrated(`{"id":"a","state":"running","created":1,"updated":1,"model":"opus",` +
		`"model_tier":{"name":"principal","rank":2,"skills":"short","source":"row","model":"opus"}}`)
	s.Equal(agent.Model{ID: "opus"}, row.Agent.Model)
}

func (s *agentSectionSuite) TestAModelWithNoTierRecordedBecomesAnID() {
	row := s.migrated(`{"id":"a","state":"running","created":1,"updated":1,"model":"principal"}`)
	s.Equal(agent.Model{ID: "principal"}, row.Agent.Model)
}

func (s *agentSectionSuite) TestFreshReasonStartsANewWorker() {
	row := s.migrated(`{"id":"a","state":"declared","created":1,"updated":1,"follows":"b","fresh_reason":"b read the old limiter"}`)
	s.Equal(&agent.Spec{Worker: agent.Worker{Type: agent.WorkerTypeNew, Reason: "b read the old limiter"}}, row.Agent)
}

func (s *agentSectionSuite) TestAServerRowTakesNoSection() {
	row := s.migrated(`{"id":"rotate","holder":"server","state":"pass","created":1,"updated":1,"criteria":"rotate the trail"}`)
	s.Nil(row.Agent)
}

func (s *agentSectionSuite) TestARowWithNoAgentMemberTakesNoSection() {
	s.Nil(s.migrated(`{"id":"a","state":"running","created":1,"updated":1}`).Agent)
}

// workerResumedSuite pins the migration that respells a stored worker type
// "resume" as "resumed".
type workerResumedSuite struct {
	suite.Suite
}

func TestWorkerResumedMigration(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(workerResumedSuite))
}

// migrated is the row after the job store's migrations ran over a file
// already through migration 1.
func (s *workerResumedSuite) migrated(line string) (types.Job, map[string]any) {
	st := tempStore(s.T(), s.T().TempDir())
	st.notices, st.migrations = &bytes.Buffer{}, jobMigrations
	path := plantRaw(s.T(), st, fmt.Sprintf("%s\n%s", `{"api_version":"magus.job/v1alpha1","migration":1}`, line))
	f, err := st.read()
	s.Require().NoError(err)
	s.Require().Len(f.Jobs, 1)
	return f.Jobs[0], storedSpec(s.T(), path, f.Jobs[0].ID)
}

func (s *workerResumedSuite) TestAResumeWorkerBecomesResumed() {
	row, stored := s.migrated(`{"id":"a","state":"running","created":1,"updated":1,` +
		`"agent":{"instructions":"ship it","worker":{"type":"resume"}}}`)
	s.Equal(&agent.Spec{Instructions: "ship it", Worker: agent.Worker{Type: agent.WorkerTypeResumed}}, row.Agent)
	s.Equal(map[string]any{"type": "Resumed"}, stored["agent"].(map[string]any)["worker"], "the file holds the new spelling, CamelCase")
}

func (s *workerResumedSuite) TestANewWorkerKeepsItsReason() {
	row, _ := s.migrated(`{"id":"a","state":"declared","created":1,"updated":1,` +
		`"agent":{"worker":{"type":"new","reason":"b read the old limiter"}}}`)
	s.Equal(agent.Worker{Type: agent.WorkerTypeNew, Reason: "b read the old limiter"}, row.Agent.Worker)
}

func (s *workerResumedSuite) TestARowWithNoAgentIsUntouched() {
	row, stored := s.migrated(`{"id":"a","state":"running","created":1,"updated":1}`)
	s.Nil(row.Agent)
	s.NotContains(stored, "agent")
}

type StaleStampSuite struct {
	suite.Suite
	store *Store
}

func TestStaleStamp(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(StaleStampSuite))
}

func (s *StaleStampSuite) SetupTest() {
	s.store = tempStore(s.T(), s.T().TempDir())
}

func (s *StaleStampSuite) TestRefusesAnIntegerRow() {
	plantRaw(s.T(), s.store, `{"id":"a","schema_version":13,"state":"running","created":1,"updated":1}`)
	_, err := s.store.List()
	s.ErrorContains(err, "record carries no apiVersion")
	s.ErrorContains(err, "move jobs.jsonl aside, or delete it, and fork the jobs again")
}

func (s *StaleStampSuite) TestRefusesAMalformedVersion() {
	plantRaw(s.T(), s.store, `{"id":"a","api_version":"13","state":"running","created":1,"updated":1}`)
	_, err := s.store.List()
	s.ErrorContains(err, "move jobs.jsonl aside, or delete it")
}

// authoritySuite pins ADR 0045's Authority: computed by the store at a fork
// from who forked, and moved later only by the root.
type authoritySuite struct {
	suite.Suite
	repo *testkit.Repo
	loc  Location
}

func TestAuthority(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(authoritySuite))
}

func (s *authoritySuite) SetupTest() {
	s.repo = execRepository(s.T())
	s.loc = tempLocation(s.T(), s.repo.Root())
}

func (s *authoritySuite) fork(st *Store, row types.Job) (types.Job, error) {
	return st.Update(s.T().Context(), row.ID, func(cur *types.Job) { *cur = row })
}

func (s *authoritySuite) TestARootForkRecordsTheRootCheckoutsCommittedRevision() {
	stored, err := s.fork(NewStore(s.loc), types.Job{ID: "a", WritePaths: []string{"a.go"}})
	s.Require().NoError(err)
	s.Equal(s.repo.Head(), stored.Authority)
}

func (s *authoritySuite) TestNeitherADeclaredCheckpointNorASentAuthoritySetsIt() {
	head := s.repo.Head()
	s.repo.Commit("later", map[string]string{"b.txt": "b\n"})
	stored, err := s.fork(NewStore(s.loc), types.Job{ID: "a", Checkpoint: head, Authority: "forged"})
	s.Require().NoError(err)
	s.Equal(s.repo.Head(), stored.Authority)
}

func (s *authoritySuite) TestAWorkersChildTakesItsParentsAuthority() {
	parent, err := s.fork(NewStore(s.loc), types.Job{ID: "p", WritePaths: []string{"a.go"}, State: types.StateRunning})
	s.Require().NoError(err)
	worktree := s.repo.AddWorktree("p")
	worktree.Commit("the worker's own", map[string]string{"a.go": "package a\n"})
	worker := s.loc
	worker.Root, worker.Actor = worktree.Root(), &Actor{Lease: "p"}

	child, err := s.fork(NewStore(worker), types.Job{ID: "p/c", Parent: "p", WritePaths: []string{"a.go"}})
	s.Require().NoError(err)
	s.Equal(parent.Authority, child.Authority, "the child's checkout HEAD is the worker's commit, never its authority")
}

func (s *authoritySuite) TestALaterWriteCarriesTheAuthorityForward() {
	first, err := s.fork(NewStore(s.loc), types.Job{ID: "a", WritePaths: []string{"a.go"}})
	s.Require().NoError(err)
	s.repo.Commit("later", map[string]string{"b.txt": "b\n"})
	again, err := s.fork(NewStore(s.loc), types.Job{ID: "a", WritePaths: []string{"a.go", "b.go"}})
	s.Require().NoError(err)
	s.Equal(first.Authority, again.Authority)
}

func (s *authoritySuite) TestAForkOfTheNextAttemptRecordsTheRootsHeadThen() {
	_, err := s.fork(NewStore(s.loc), types.Job{ID: "a", WritePaths: []string{"a.go"}, State: types.StateFail})
	s.Require().NoError(err)
	later := s.repo.Commit("later", map[string]string{"b.txt": "b\n"})
	again, err := s.fork(NewStore(s.loc), types.Job{ID: "a", WritePaths: []string{"a.go"}, State: types.StateDeclared})
	s.Require().NoError(err)
	s.Equal(later, again.Authority)
}

func (s *authoritySuite) TestAForkWhereNoVersionControlAnswersRecordsNone() {
	stored, err := s.fork(tempStore(s.T(), s.T().TempDir()), types.Job{ID: "a", WritePaths: []string{"a.go"}})
	s.Require().NoError(err)
	s.Empty(stored.Authority)
}

func (s *authoritySuite) TestTheRootAdvancesARowsAuthority() {
	_, err := s.fork(NewStore(s.loc), types.Job{ID: "a", WritePaths: []string{"a.go"}})
	s.Require().NoError(err)
	later := s.repo.Commit("merged", map[string]string{"b.txt": "b\n"})
	advanced, err := NewStore(s.loc).AdvanceAuthority(s.T().Context(), "a")
	s.Require().NoError(err)
	s.Equal(later, advanced.Authority)
}

func (s *authoritySuite) TestALeasedCallerCannotAdvanceItsOwnAuthority() {
	first, err := s.fork(NewStore(s.loc), types.Job{ID: "a", WritePaths: []string{"a.go"}, State: types.StateRunning})
	s.Require().NoError(err)
	s.repo.Commit("the worker's own", map[string]string{"b.txt": "b\n"})
	_, err = boundStore(s.loc, "a").AdvanceAuthority(s.T().Context(), "a")
	var refused *RefusedError
	s.Require().ErrorAs(err, &refused)
	s.Contains(err.Error(), "only the root advances a row's authority")
	rows, err := NewStore(s.loc).List()
	s.Require().NoError(err)
	s.Equal(first.Authority, rows[0].Authority)
}

func (s *authoritySuite) TestAdvancingAnUndeclaredRowFails() {
	_, err := NewStore(s.loc).AdvanceAuthority(s.T().Context(), "nobody")
	s.ErrorContains(err, "nobody is not declared")
}
