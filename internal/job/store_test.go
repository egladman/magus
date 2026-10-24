package job

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		Criteria:   "goal for " + id,
		Checkpoint: "abc123",
		WritePaths: []string{"internal/" + id},
		DenyPaths:  []string{"MAGUS.md"},
		Model:      "standard",
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
	want.Schema, want.RegisteredBy, want.Created, want.Updated = got.Schema, got.RegisteredBy, got.Created, got.Updated
	want.Generation, want.Attempts = got.Generation, got.Attempts
	return want
}

func TestStoreRoundTrip(t *testing.T) {
	t.Parallel()

	stamped := func(row types.Job, generation int) types.Job {
		row.Generation, row.Attempts = generation, 1
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
				{ID: "a", Criteria: "revised", State: types.StatePass},
			},
			want: []types.Job{
				// The replacement carries no write paths, so the row's paths were
				// released by it; see TestStorePutRecordsReleasedPaths.
				{
					ID: "a", Criteria: "revised", State: types.StatePass, Generation: 2, Attempts: 1,
					Releases: []types.JobRelease{{Path: "internal/a", Digest: types.DigestAbsent}},
				},
				stamped(lease("b"), 1),
			},
		},
		{
			name: "a read-only row carries no paths by design",
			puts: []types.Job{{ID: "scout", Criteria: "inventory", ReadOnly: true, State: types.StateRunning}},
			want: []types.Job{{ID: "scout", Criteria: "inventory", ReadOnly: true, State: types.StateRunning, Generation: 1, Attempts: 1}},
		},
		{
			name: "no_return is stored as its own terminal state",
			puts: []types.Job{{ID: "a", State: types.StateNoReturn}},
			want: []types.Job{{ID: "a", State: types.StateNoReturn, Generation: 1, Attempts: 1}},
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
				// Timestamps and the schema stamp are the store's, not the fixture's;
				// assert them separately and compare the declared facts here.
				assert.Equal(t, types.JobSchemaVersion, got[i].Version, "the store stamps the row shape")
				assert.Equal(t, trail.LocalOrigin(t.Context()), got[i].RegisteredBy, "the store records the OS account that created the row")
				w.Created, w.Updated, w.Version, w.RegisteredBy = got[i].Created, got[i].Updated, got[i].Version, got[i].RegisteredBy
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
	s := tempStore(t, t.TempDir())
	row := types.Job{Criteria: "no id"}
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
	assert.Equal(t, "goal for a", got[0].Criteria, "neither merge erased the row it did not name")
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
	root := testkit.NewRepo(t, map[string]string{".gitattributes": "*.go diff=golang\n", "run.go": src}).Root()
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
	repo := execRepo(t)
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
			ID: "a", Criteria: criteria, Checkpoint: "abc123", WritePaths: write, ReadPaths: read, State: types.StateDeclared,
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
			s := tempStore(t, execRepo(t).Root())
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
				Criteria:     tc.criteria,
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
				`{"schema_version":%d,"id":"a","criteria":"goal","checkpoint":%q,"write_paths":["a.go"],"check":{"target":"go-test","project":"."}}`,
				types.JobSchemaVersion, checkpoint)))
			require.NoError(t, err)
			return s.Update(t.Context(), row.ID, Declare(row, 0))
		}},
		{name: "magus\\job.put", proof: types.WriteProofAlone, validated: true, fork: func(t *testing.T, s *Store, checkpoint string) (types.Job, error) {
			put, err := DecodePut(map[string]any{
				"criteria": "goal", "checkpoint": checkpoint, "write_paths": []any{"a.go"}, "check": "go-test .",
			})
			require.NoError(t, err)
			merge, err := PutMerge(put)
			require.NoError(t, err)
			return ForkMerge(t.Context(), s, "a", merge, config.Jobs{}, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repo := testkit.NewRepo(t, nil)
			head := repo.Head()
			stored, err := tc.fork(t, tempStore(t, repo.Root()), head)
			require.NoError(t, err)
			assert.Equal(t, tc.validated, stored.Validated != 0, "validated stamp")
			assert.Equal(t, types.Job{
				Schema:       types.Schema{Version: types.JobSchemaVersion},
				ID:           "a",
				Criteria:     "goal",
				Checkpoint:   head,
				WritePaths:   []string{"a.go"},
				Check:        &check,
				Validation:   check.String(),
				State:        types.StateDeclared,
				WriteProof:   tc.proof,
				Generation:   1,
				Attempts:     1,
				Created:      stored.Created,
				Updated:      stored.Updated,
				Validated:    stored.Validated,
				RegisteredBy: stored.RegisteredBy,
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

// storedRow is row id's last line in the file.
func storedRow(t *testing.T, path, id string) map[string]any {
	t.Helper()
	var row map[string]any
	for _, line := range storedLines(t, path) {
		if line["id"] == id {
			row = line
		}
	}
	require.NotNil(t, row, "%s holds no row %s", path, id)
	return row
}

// A row a newer magus wrote is an ordinary row to this one: updating it or any other row
// keeps the members this binary does not declare, and never lowers the newer stamp.
func TestStoreKeepsANewerRowWhole(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	path := plantRaw(t, s, fmt.Sprintf(
		`{"id":"future","schema_version":99,"requires":[],"state":"running","created":1,"updated":1,"future_field":{"x":1}}`+"\n"+
			`{"id":"other","schema_version":%d,"state":"running","created":1,"updated":1}`, types.JobSchemaVersion))
	want := map[string]any{"x": float64(1)}

	_, err := s.Update(t.Context(), "other", func(u *types.Job) { u.Criteria = "moved" })
	require.NoError(t, err)
	future := storedRow(t, path, "future")
	assert.Equal(t, want, future["future_field"], "an update of another row keeps this row's unknown member")
	assert.Equal(t, float64(99), future["schema_version"], "and its stamp")

	_, err = s.Update(t.Context(), "future", func(u *types.Job) { u.State = types.StatePass })
	require.NoError(t, err)
	future = storedRow(t, path, "future")
	assert.Equal(t, want, future["future_field"], "an update of this row keeps its unknown member")
	assert.Equal(t, float64(99), future["schema_version"], "a rewrite never lowers the stamp")
	assert.Equal(t, "pass", future["state"], "the field the update named was applied")
	assert.Equal(t, float64(types.JobSchemaVersion), storedRow(t, path, "other")["schema_version"])

	// A whole-row replace is still the store's to stamp: it cannot drop the envelope.
	_, err = s.Update(t.Context(), "future", func(u *types.Job) { *u = types.Job{State: types.StateRunning} })
	require.NoError(t, err)
	future = storedRow(t, path, "future")
	assert.Equal(t, want, future["future_field"])
	assert.Equal(t, float64(99), future["schema_version"])
}

// A row requiring a feature this magus lacks is listed and named, and only a write to it is
// refused: every other row stays writable, and the sweep leaves it alone.
func TestStoreDegradesARowItCannotActOn(t *testing.T) {
	t.Parallel()

	s, notices := sweepStore(t, 10_000, time.Hour)
	path := plantRaw(t, s, fmt.Sprintf(
		`{"id":"future","schema_version":99,"requires":["never-implemented"],"state":"declared","created":1,"updated":1,"future_field":7}`+"\n"+
			`{"id":"other","schema_version":%d,"state":"running","created":9999,"updated":9999}`, types.JobSchemaVersion))
	before := storedRow(t, path, "future")

	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 2, "the row is read, not hidden")
	assert.Empty(t, notices.String(), "the sweep does not end a row it may not write, stale as it is")
	assert.Equal(t, []types.JobReadOnly{{Job: "future", Lacks: []string{"never-implemented"}}},
		types.NewJobList(rows).ReadOnly, "every listing names it")

	_, err = s.Update(t.Context(), "future", func(u *types.Job) { u.State = types.StatePass })
	require.Error(t, err)
	assert.Equal(t, fmt.Sprintf(`job: future requires "never-implemented", which this magus (schema %d) lacks,`+
		` so it will not write that row: update magus, or run the command with the magus that wrote it`, types.JobSchemaVersion), err.Error())
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
	path := plantRaw(t, s, `{"schema_version":99,"requires":["jobs-v2"]}`)

	_, err := s.List()
	assert.Equal(t, fmt.Sprintf(`job: %s requires "jobs-v2", which this magus (schema %d) lacks, update magus`,
		path, types.JobSchemaVersion), err.Error())
}

// The file's own envelope survives a rewrite and a clear, like a row's.
func TestStoreKeepsTheFileEnvelope(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	s.clock = func() time.Time { return time.Unix(1_700_000_000, 0) }
	path := plantRaw(t, s, `{"schema_version":99,"plan":"p1"}`)
	seed(t, s, lease("a"))
	_, err := s.Clear(t.Context())
	require.NoError(t, err)

	assert.Equal(t, []map[string]any{{"schema_version": float64(99), "plan": "p1", "retained": float64(1_700_000_000)}},
		storedLines(t, path), "a clear leaves the header alone, and the store's own retention stamp rides in it")
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

	_, err = s.Update(t.Context(), "a", func(u *types.Job) { u.Criteria = "moved" })
	require.NoError(t, err)
	after := storedLines(t, path)
	require.Len(t, after, len(before)+1, "one line appended, nothing rewritten")
	assert.Equal(t, before, after[:len(before)])
	assert.Equal(t, "a", after[len(before)]["id"])
	assert.Equal(t, "moved", after[len(before)]["criteria"])

	rows, err := s.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, []string{rows[0].ID, rows[1].ID}, "a row keeps its first line's place")
	assert.Equal(t, "moved", rows[0].Criteria)
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
		_, err := s.Update(t.Context(), "a", func(u *types.Job) { u.Criteria = fmt.Sprint(i) })
		require.NoError(t, err)
		assert.False(t, file.CompactDue(len(storedLines(t, path)), 1))
	}
	lines := storedLines(t, path)
	assert.Less(t, len(lines), bound, "the bound was crossed and the file rewritten")
	assert.NotContains(t, lines[0], "id", "a rewrite puts the header first")

	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, fmt.Sprint(bound-1), rows[0].Criteria)
}

// A delete drops the row's every line by rewriting the file.
func TestStoreDeleteRewritesWithoutTheRow(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	seed(t, s, lease("a"))
	seed(t, s, lease("b"))
	_, err := s.Update(t.Context(), "a", func(u *types.Job) { u.Criteria = "moved" })
	require.NoError(t, err)
	_, err = s.Delete(t.Context(), "a", false)
	require.NoError(t, err)

	path, err := s.Path()
	require.NoError(t, err)
	lines := storedLines(t, path)
	require.Len(t, lines, 2, "the header and b")
	assert.Equal(t, "b", lines[1]["id"])
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
	assert.Equal(t, "adj/store", lines[1]["id"])
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
		`{"id":"adj/store","schema_version":%d,"state":"running","created":1,"updated":1,"woolgathering":7}`,
		types.JobSchemaVersion))

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
	got[0].Criteria = "mutated"

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
	wantCreated.Generation, wantCreated.Attempts = 1, 1
	wantCreated.Requires = nil // a row carrying only its generation stays writable by an older magus
	assert.Equal(t, wantCreated, created)

	row.State = types.StateRunning
	row.Deadline = 1 << 40
	assert.Equal(t, 1, seed(t, s, row).Generation, "state and deadline are not the spec")

	require.NoError(t, s.RecordRework(t.Context(), "a"))
	row.Criteria = "a sharper goal"
	assert.Equal(t, 2, seed(t, s, row).Generation)

	row.Generation, row.Attempts = 9, 9
	forged := seed(t, s, row)
	wantForged := forged
	wantForged.Generation, wantForged.Attempts = 2, 1 // a caller's generation is replaced by the stored one
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
	row.Criteria = "a sharper goal"
	assert.Equal(t, 3, seed(t, s, row).Generation, "a widening beside another change still counts")
}

// endAttempt ends row a as failed, its holder having run a failing check, been sent more
// work once and left a risk.
func endAttempt(t *testing.T, s *Store) {
	t.Helper()
	require.NoError(t, s.RecordRework(t.Context(), "a"))
	_, err := s.Update(t.Context(), "a", func(u *types.Job) {
		u.State = types.StateFail
		u.Result = &types.JobResult{UnresolvedRisks: []string{"the second worktree was not tried"}}
		u.Attempt = &types.JobAttempt{Ref: "out1", Failed: true}
	})
	require.NoError(t, err)
}

// A fork of an ended row is the job's next attempt: the one that ended is folded into Prior
// for the next holder, and the evidence and counters start over so the next ending counts
// its own attempt alone. Three ended attempts are kept, newest last.
func TestStoreForkOfAnEndedRowFoldsTheAttempt(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	row := lease("a")
	seed(t, s, row)
	endAttempt(t, s)

	again := seed(t, s, row)

	require.Len(t, again.Prior, 1)
	prior := again.Prior[0]
	wantPrior := prior
	wantPrior.N, wantPrior.State, wantPrior.ReworkRounds = 1, types.StateFail, 1
	wantPrior.UnresolvedRisks, wantPrior.FailingRefs = []string{"the second worktree was not tried"}, []string{"out1"}
	assert.Equal(t, wantPrior, prior)
	assert.True(t, prior.DenialsKnown, "this checkout's trail is empty, so it holds no denial")
	assert.True(t, prior.Retry.Retryable)

	// A re-fork with an unchanged spec leaves the generation alone, the next attempt counts
	// its own rework rounds, and an older magus would re-fork without folding.
	wantAgain := again
	wantAgain.State, wantAgain.Attempts, wantAgain.Generation = types.StateDeclared, 2, 1
	wantAgain.Result, wantAgain.Attempt, wantAgain.ReworkRounds = nil, nil, 0
	wantAgain.Requires = []string{"job-generations"}
	assert.Equal(t, wantAgain, again)

	for range 3 {
		endAttempt(t, s)
		again = seed(t, s, row)
	}
	assert.Equal(t, 5, again.Attempts)
	require.Len(t, again.Prior, 3)
	assert.Equal(t, []int{2, 3, 4}, []int{again.Prior[0].N, again.Prior[1].N, again.Prior[2].N})
}

// max_attempts is checked at a fork of an ended row and nowhere else, against the cap the
// fork's own record carries.
func TestStoreRefusesAForkPastMaxAttempts(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	row := lease("a")
	row.MaxAttempts = 2
	seed(t, s, row)
	row.Criteria = "reworded while live"
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
	assert.Equal(t, 2, rows[0].Attempts)

	row.MaxAttempts = 3
	raised := seed(t, s, row)
	assert.Equal(t, 3, raised.Attempts, "a record raising the cap forks past the old one")
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

			again := seed(t, s, lease("a"))

			require.Len(t, again.Prior, 1)
			var rules []string
			for _, d := range again.Prior[0].Denials {
				rules = append(rules, d.Rule)
			}
			assert.Equal(t, tc.rules, rules)
			assert.Equal(t, tc.known, again.Prior[0].DenialsKnown)
			assert.Equal(t, tc.retryable, again.Prior[0].Retry.Retryable)
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

// realSizedRows is n rows shaped like the store that made `magus job fork` take minutes
// under load: half exited with a result nobody collected, a third ended, the rest still
// held, each carrying a criteria paragraph and a result, aged across a month. Every row
// was taken in checkout, so each exited one is a landing candidate.
func realSizedRows(n int, now int64, checkout string) []types.Job {
	criteria := strings.Repeat("Address the findings in the plan's section and say which were deferred. ", 6)
	rows := make([]types.Job, n)
	for i := range rows {
		age := int64(i*2357) % (30 * 24 * 3600)
		row := types.Job{
			ID:           fmt.Sprintf("job-%04d", i),
			Criteria:     criteria,
			Checkpoint:   "08566ec817db6ff7932f0a4d62d8a9ed08f8ca18",
			Model:        "sonnet",
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
			row.Result = &types.JobResult{Job: row.ID, ChangedPaths: row.WritePaths, UnresolvedRisks: []string{criteria}}
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
				if _, err := s.Update(b.Context(), "job-0098", func(u *types.Job) { u.Criteria += "." }); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestStoreRefusesAWriterOlderThanTheStamp(t *testing.T) {
	newer := types.MagusBuild{Version: "v0.5.0", Commit: "8a9d", Date: "2026-10-22T18:40:11Z"}
	older := types.MagusBuild{Version: "v0.4.9", Commit: "1f3c", Date: "2026-10-20T09:12:00-04:00"}
	loc := tempLocation(t, t.TempDir())
	loc.Writer = newer
	_, err := NewStore(loc).Update(t.Context(), "A", func(cur *types.Job) { *cur = lease("A") })
	require.NoError(t, err)

	loc.Writer = older
	old := NewStore(loc)
	_, err = old.Update(t.Context(), "B", func(cur *types.Job) { *cur = lease("B") })
	require.ErrorIs(t, err, types.JobStoreWriterOlder)
	assert.ErrorContains(t, err, "this magus, 1f3c (committed 2026-10-20T13:12:00Z), is older than 8a9d (committed 2026-10-22T18:40:11Z)")
	rows, err := old.List()
	require.NoError(t, err, "a read by an older build still works")
	assert.Len(t, rows, 1)

	loc.Writer = types.MagusBuild{Version: "v0.4.9-dirty", Commit: "8a9d", Date: "2026-10-22T18:40:11Z"}
	_, err = NewStore(loc).Update(t.Context(), "B", func(cur *types.Job) { *cur = lease("B") })
	require.ErrorIs(t, err, types.JobStoreWriterOlder)
	assert.ErrorContains(t, err, "this magus has no commit stamp")
}

// The fork door writes through InWorkspace and the daemon's job tool through As; each
// builds its view field by field, and one that carried the stamp resolved at NewStore lost
// it, refusing every write under the build that last wrote the file.
func TestStoreViewsWriteAsTheRunningBuild(t *testing.T) {
	stamped := types.MagusBuild{Commit: "5ad8", Date: "2026-10-23T23:09:10-04:00"}
	was := runningBuild
	runningBuild = func() types.MagusBuild { return stamped }
	t.Cleanup(func() { runningBuild = was })

	loc := tempLocation(t, t.TempDir())
	s := NewStore(loc)
	_, err := s.Update(t.Context(), "A", func(cur *types.Job) { *cur = lease("A") })
	require.NoError(t, err)

	for name, view := range map[string]*Store{"InWorkspace": s.InWorkspace(Workspace{}), "As": s.As(Actor{})} {
		_, err := view.Update(t.Context(), name, func(cur *types.Job) { *cur = lease(name) })
		require.NoError(t, err, name)
	}
	f, err := s.read()
	require.NoError(t, err)
	require.NotNil(t, f.Writer)
	assert.Equal(t, "5ad8", f.Writer.Commit)
	assert.Len(t, f.Jobs, 3)
}

func TestStoreHandsTheStampToANewerWriter(t *testing.T) {
	loc := tempLocation(t, t.TempDir())
	for _, b := range []types.MagusBuild{
		{Commit: "1f3c", Date: "2026-10-20T09:12:00Z"},
		{Commit: "8a9d", Date: "2026-10-22T18:40:11Z"},
		{Commit: "8a9d", Date: "2026-10-22T18:40:11Z"},
	} {
		loc.Writer = b
		_, err := NewStore(loc).Update(t.Context(), b.Commit, func(cur *types.Job) { *cur = lease(b.Commit) })
		require.NoError(t, err)
	}
	f, err := NewStore(loc).read()
	require.NoError(t, err)
	require.NotNil(t, f.Writer)
	assert.Equal(t, "8a9d", f.Writer.Commit)
}
