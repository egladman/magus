package job

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tmpLoc places a ledger entirely under temp directories, so no test resolves the real
// user state directory. Two stores built from ONE returned value share a leases file;
// two calls never do, whatever roots they are given.
//
// The actor is pinned UNBOUND rather than resolved, so that a developer running the suite
// from a worktree bound to a lease, or under a BAGGAGE that names one, grades these
// fixtures the same way CI does. The rules that turn on a bound actor pass their own.
func tmpLoc(t *testing.T, root string) Location {
	t.Helper()
	return Location{StateBase: t.TempDir(), CacheDir: t.TempDir(), Root: root, Actor: &Actor{}}
}

// boundStore is a Store acting as the worker leased to id, sharing loc's ledger file.
func boundStore(loc Location, id string) *Store {
	loc.Actor = &Actor{Lease: id}
	return NewStore(loc)
}

func tmpStore(t *testing.T, root string) *Store {
	t.Helper()
	return NewStore(tmpLoc(t, root))
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

// stamped copies the fields the store writes itself from got onto want, so a whole-row
// comparison covers every other field.
func stamped(want, got types.Job) types.Job {
	want.Schema, want.RegisteredBy, want.Created, want.Updated = got.Schema, got.RegisteredBy, got.Created, got.Updated
	return want
}

func TestStoreRoundTrip(t *testing.T) {
	t.Parallel()

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
			want: []types.Job{lease("a"), lease("b"), lease("c")},
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
					ID: "a", Criteria: "revised", State: types.StatePass,
					Releases: []types.JobRelease{{Path: "internal/a", Digest: types.DigestAbsent}},
				},
				lease("b"),
			},
		},
		{
			name: "a read-only row carries no paths by design",
			puts: []types.Job{{ID: "scout", Criteria: "inventory", ReadOnly: true, State: types.StateRunning}},
			want: []types.Job{{ID: "scout", Criteria: "inventory", ReadOnly: true, State: types.StateRunning}},
		},
		{
			name: "no_return is stored as its own terminal state",
			puts: []types.Job{{ID: "a", State: types.StateNoReturn}},
			want: []types.Job{{ID: "a", State: types.StateNoReturn}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := tmpStore(t, t.TempDir())
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
	s := tmpStore(t, t.TempDir())
	row := types.Job{Criteria: "no id"}
	_, err := s.Update(ctx, row.ID, func(cur *types.Job) { *cur = row })
	require.ErrorIs(t, err, ErrNoID, "a row with no id could never be updated or referred to again")

	got, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, got, "a rejected put writes nothing")
}

func TestStorePutPreservesCreatedOnUpdate(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, t.TempDir())
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
	s := tmpStore(t, t.TempDir())
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
	loc := tmpLoc(t, t.TempDir())

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
	s := tmpStore(t, t.TempDir())
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

	s := tmpStore(t, root)
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
		Digest:     "sha256:" + hashOf(t, filepath.Join(root, "released.go")),
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
	root := gitRepo(t, map[string]string{".gitattributes": "*.go diff=golang\n", "run.go": src})
	s := tmpStore(t, root)
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

	s := tmpStore(t, root)
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

func hashOf(t *testing.T, path string) string {
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
	s := tmpStore(t, t.TempDir())
	seed(t, s, lease("a"))
	_, err := s.Exec(ctx, "a", "abc123")
	require.NoError(t, err)
	require.NoError(t, s.RecordUnattributedWrite(ctx, "a", "internal/a/store.go"))

	forged := lease("a")
	forged.ReportedBase, forged.BaseVerdict, forged.Registered = "deadbeef", types.BaseDiverged, 1
	forged.Unattributed = []types.JobUnattributedWrite{{Path: "docs/forged.md"}}
	stored := seed(t, s, forged)

	assert.NotZero(t, stored.Registered)
	require.Len(t, stored.Unattributed, 1)
	assert.Equal(t, "internal/a/store.go", stored.Unattributed[0].Path)

	want := stamped(lease("a"), stored)
	want.ReportedBase, want.BaseVerdict = "abc123", types.BaseMatch
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
	}{
		{name: "a running row widened keeps running", from: types.StateRunning, criteria: "goal", write: []string{"a.go", "b.go"}, want: types.StateRunning},
		{name: "an exited row given read paths stays exited", from: types.StateExited, criteria: "goal", write: []string{"a.go"}, read: []string{"docs"}, want: types.StateExited},
		{name: "a re-fork moving no boundary hands the job out again", from: types.StateExited, criteria: "goal", write: []string{"a.go"}, want: types.StateDeclared},
		{name: "a rewritten criteria hands the job out again", from: types.StateRunning, criteria: "new goal", write: []string{"a.go", "b.go"}, want: types.StateDeclared},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			s := tmpStore(t, t.TempDir())
			_, err := s.Update(ctx, "a", fork("goal", []string{"a.go"}, nil))
			require.NoError(t, err)
			taken, err := s.Exec(ctx, "a", "abc123")
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
		fork  func(t *testing.T, s *Store) (types.Job, error)
		proof types.JobWriteProof
	}{
		{name: "job fork --stdin", fork: func(t *testing.T, s *Store) (types.Job, error) {
			row, err := DecodeDeclaration(strings.NewReader(fmt.Sprintf(
				`{"schema_version":%d,"id":"a","criteria":"goal","checkpoint":"abc123","write_paths":["a.go"],"check":{"target":"go-test","project":"."}}`,
				types.JobSchemaVersion)))
			require.NoError(t, err)
			return s.Update(t.Context(), row.ID, Declare(row, 0))
		}},
		{name: "magus\\job.put", proof: types.WriteProofAlone, fork: func(t *testing.T, s *Store) (types.Job, error) {
			merge, err := ParseMerge(map[string]any{
				"criteria": "goal", "checkpoint": "abc123", "write_paths": []any{"a.go"}, "check": "go-test .",
			})
			require.NoError(t, err)
			return ForkMerge(t.Context(), s, "a", merge, config.Jobs{}, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stored, err := tc.fork(t, tmpStore(t, t.TempDir()))
			require.NoError(t, err)
			assert.Equal(t, types.Job{
				Schema:       types.Schema{Version: types.JobSchemaVersion},
				ID:           "a",
				Criteria:     "goal",
				Checkpoint:   "abc123",
				WritePaths:   []string{"a.go"},
				Check:        &check,
				Validation:   check.String(),
				State:        types.StateDeclared,
				WriteProof:   tc.proof,
				Created:      stored.Created,
				Updated:      stored.Updated,
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

// storedRow reads row id back from the file as JSON, not through the struct, so a member
// the struct does not declare is visible.
func storedRow(t *testing.T, path, id string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var f struct {
		Jobs []map[string]any `json:"jobs"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	for _, row := range f.Jobs {
		if row["id"] == id {
			return row
		}
	}
	require.Failf(t, "no row", "%s holds no row %s", path, id)
	return nil
}

// A row a newer magus wrote is an ordinary row to this one: updating it or any other row
// keeps the members this binary does not declare, and never lowers the newer stamp.
func TestStoreKeepsANewerRowWhole(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, t.TempDir())
	path := plantRaw(t, s, fmt.Sprintf(`{"jobs":[`+
		`{"id":"future","schema_version":99,"requires":[],"state":"running","created":1,"updated":1,"future_field":{"x":1}},`+
		`{"id":"other","schema_version":%d,"state":"running","created":1,"updated":1}]}`, types.JobSchemaVersion))
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
	path := plantRaw(t, s, fmt.Sprintf(`{"jobs":[`+
		`{"id":"future","schema_version":99,"requires":["never-implemented"],"state":"declared","created":1,"updated":1,"future_field":7},`+
		`{"id":"other","schema_version":%d,"state":"running","created":9999,"updated":9999}]}`, types.JobSchemaVersion))
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
		` so it will not write that row. Update magus, or run the command with the magus that wrote it`, types.JobSchemaVersion), err.Error())
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

	s := tmpStore(t, t.TempDir())
	path := plantRaw(t, s, `{"schema_version":99,"requires":["jobs-v2"],"jobs":[]}`)

	_, err := s.List()
	assert.Equal(t, fmt.Sprintf(`job: %s requires "jobs-v2", which this magus (schema %d) lacks; update magus`,
		path, types.JobSchemaVersion), err.Error())
}

// The file's own envelope survives a rewrite and a clear, like a row's.
func TestStoreKeepsTheFileEnvelope(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, t.TempDir())
	path := plantRaw(t, s, `{"schema_version":99,"plan":"p1","jobs":[]}`)
	seed(t, s, lease("a"))
	_, err := s.Clear(t.Context())
	require.NoError(t, err)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var f map[string]any
	require.NoError(t, json.Unmarshal(raw, &f))
	assert.Equal(t, map[string]any{"schema_version": float64(99), "plan": "p1", "jobs": nil}, f)
}

// compat: see the legacy fields on Row. A plan written before the rename keeps its
// boundaries; without the fold every one of them would read empty and the next put would store
// that as the truth.
func TestStoreReadsAPlanWrittenBeforeTheRename(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, t.TempDir())
	path, err := s.Path()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{"jobs":[{"id":"adj/store","schema_version":1,`+
		`"owned_paths":["internal/ledger"],"forbidden_paths":["MAGUS.md"],"focus":["internal/hint"],`+
		`"tier":"principal"}]}`+"\n"), 0o644))

	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, types.Job{
		Schema:     rows[0].Schema, // the envelope is the store's
		ID:         "adj/store",
		WritePaths: []string{"internal/ledger"},
		DenyPaths:  []string{"MAGUS.md"},
		ReadPaths:  []string{"internal/hint"},
		Model:      "principal",
	}, rows[0])
}

// A row an older magus wrote keeps its goals: read back under the new name and written
// out under both, so that magus, sharing the store, still grades them. Where the two
// spellings differ the older magus wrote last, and its spelling wins.
func TestStoreReadsGoalsStoredUnderTheirOldName(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, t.TempDir())
	path := plantRaw(t, s, `{"jobs":[{"id":"a","schema_version":11,"state":"running","created":1,"updated":1,`+
		`"completion_gates":[{"id":"gone","kind":"symbol","expect":"absent","symbols":["Legacy"]}]}]}`)

	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, []types.Goal{{ID: "gone", Kind: types.GoalKindSymbol, Expect: types.ExpectAbsent, Symbols: []string{"Legacy"}}}, rows[0].Goals)

	_, err = s.Update(t.Context(), "a", func(u *types.Job) { u.Criteria = "rewritten" })
	require.NoError(t, err)
	stored := storedRow(t, path, "a")
	want := []any{map[string]any{
		"id": "gone", "kind": "symbol", "expect": "absent", "symbols": []any{"Legacy"}, "check": map[string]any{"target": ""},
	}}
	assert.Equal(t, want, stored["goals"])
	assert.Equal(t, want, stored["completion_gates"], "an older magus sharing the store reads this spelling")

	plantRaw(t, s, `{"jobs":[{"id":"b","state":"running","created":1,"updated":1,`+
		`"completion_gates":[{"id":"x","kind":"paths","expect":"changed","paths":["a"]}],`+
		`"goals":[{"id":"y","kind":"paths","expect":"changed","paths":["b"]}]}]}`)
	rows, err = s.List()
	require.NoError(t, err)
	assert.Equal(t, []types.Goal{{ID: "x", Kind: types.GoalKindPaths, Expect: types.ExpectChanged, Paths: []string{"a"}}}, rows[0].Goals)
}

func TestStoreClear(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := tmpStore(t, t.TempDir())
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

	s := tmpStore(t, t.TempDir())
	seed(t, s, lease("adj/store"))
	path, err := s.Path()
	require.NoError(t, err)

	dropped, err := s.Clear(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, dropped)

	archives, err := filepath.Glob(filepath.Join(filepath.Dir(path), "jobs-*.json"))
	require.NoError(t, err)
	require.Len(t, archives, 1)
	raw, err := os.ReadFile(archives[0])
	require.NoError(t, err)
	assert.Contains(t, string(raw), "adj/store")
}

func TestStorePersistsAcrossStores(t *testing.T) {
	t.Parallel()

	loc := tmpLoc(t, t.TempDir())
	first := NewStore(loc)
	seed(t, first, lease("a"))

	// The point of the store: a plan outlives the process that declared it.
	second := NewStore(loc)
	got, err := second.List()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, stamped(lease("a"), got[0]), got[0])

	assert.NoFileExists(t, filepath.Join(loc.CacheDir, "ledger", "leases.json"),
		"the cache directory is no longer the ledger's home")
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
		[]byte("gitdir: "+filepath.Join(git, "worktrees", "worker")+"\n"), 0o644))

	base := t.TempDir()
	orchestrator := NewStore(Location{StateBase: base, CacheDir: t.TempDir(), Root: repo})
	seed(t, orchestrator, lease("plan/unit"))

	// A separate cache dir, which is what a second worktree actually has.
	got, err := NewStore(Location{StateBase: base, CacheDir: t.TempDir(), Root: worker}).List()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "plan/unit", got[0].ID)
}

// The old home was <cacheDir>/job. A ledger written there before the move is carried
// forward once, so a plan declared by an older magus is not silently emptied.
func TestLedgerAdoptsTheLegacyCacheDirLocation(t *testing.T) {
	t.Parallel()

	loc := tmpLoc(t, t.TempDir())
	legacy := filepath.Join(loc.CacheDir, "ledger")
	require.NoError(t, os.MkdirAll(legacy, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "jobs.json"),
		[]byte(`{"jobs":[{"id":"carried","created":1,"updated":1}]}`), 0o644))

	got, err := NewStore(loc).List()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "carried", got[0].ID)
	assert.NoDirExists(t, legacy, "adopted, not copied: a second reader cannot find an older answer")

	// Once. A ledger planted at the old home after the move stays where it is, because
	// the store that owns the rows now already exists.
	require.NoError(t, os.MkdirAll(legacy, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "jobs.json"),
		[]byte(`{"jobs":[{"id":"stale","created":1,"updated":1}]}`), 0o644))
	seed(t, NewStore(loc), lease("live"))
	again, err := NewStore(loc).List()
	require.NoError(t, err)
	ids := make([]string, len(again))
	for i, u := range again {
		ids[i] = u.ID
	}
	assert.Equal(t, []string{"carried", "live"}, ids)
	assert.DirExists(t, legacy)
}

// The plan the previous vintage kept as <state>/magus/ledger/<repo>/leases.json is carried
// into the job store once, with every member of every row intact, and the old file is LEFT
// for the binaries still reading it.
func TestStoreCarriesAPreRenamePlanForward(t *testing.T) {
	t.Parallel()

	loc := tmpLoc(t, t.TempDir())
	path, err := NewStore(loc).Path()
	require.NoError(t, err)
	legacy := filepath.Join(loc.StateBase, "magus", "ledger", filepath.Base(filepath.Dir(path)), "leases.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(legacy), 0o755))
	require.NoError(t, os.WriteFile(legacy, []byte(`{"leases":[{"id":"wave3/move","schema_version":1,`+
		`"write_paths":["internal/guard"],"state":"running","created":1,"updated":1,"woolgathering":7}]}`+"\n"), 0o644))

	got, err := NewStore(loc).List()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, types.Job{
		Schema:     got[0].Schema, // the envelope carries the members this magus does not know
		ID:         "wave3/move",
		WritePaths: []string{"internal/guard"},
		State:      types.StateRunning,
		Created:    1,
		Updated:    1,
	}, got[0])
	assert.FileExists(t, legacy, "copied, not moved: an older binary elsewhere is still reading it")

	carried, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(carried), `"woolgathering": 7`, "a member this magus does not know survives the carry")

	// Once. Rows written to the old file afterwards do not reach a store that has its own.
	require.NoError(t, os.WriteFile(legacy, []byte(`{"leases":[{"id":"later","created":1,"updated":1}]}`+"\n"), 0o644))
	again, err := NewStore(loc).List()
	require.NoError(t, err)
	require.Len(t, again, 1)
	assert.Equal(t, "wave3/move", again[0].ID)
}

// TestStoreUpdatePreservesAnUnknownMember is the UPDATE path: mutate rewrites the WHOLE
// file on every put, so a member neither this binary's struct nor this call introduced was
// dropped from every row in the file, not just the one being touched. This planted the
// member directly at the store's own path, unlike TestStoreCarriesAPreRenamePlanForward,
// which covers the one-time carry from the pre-rename file.
func TestStoreUpdatePreservesAnUnknownMember(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, t.TempDir())
	path, err := s.Path()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf(
		`{"jobs":[{"id":"adj/store","schema_version":%d,"state":"running","created":1,"updated":1,"woolgathering":7}]}`,
		types.JobSchemaVersion)+"\n"), 0o644))

	_, err = s.Update(t.Context(), "adj/store", func(u *types.Job) { u.State = types.StatePass })
	require.NoError(t, err)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"woolgathering": 7`,
		"a member this binary does not know survives an update to a field it does know")

	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, types.StatePass, rows[0].State, "the field the update named was still applied")
}

// A pre-rename spelling folded onto its current field by foldStoredNames must not
// ride back out under the old name too: the row's unknown members would otherwise carry it
// forward forever, and the NEXT read would find the field declared under both spellings,
// which foldStoredNames refuses outright.
func TestStoreUpdateDropsAFoldedLegacySpelling(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, t.TempDir())
	path, err := s.Path()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf(
		`{"jobs":[{"id":"adj/store","schema_version":%d,"owned_paths":["internal/ledger"],"created":1,"updated":1}]}`,
		types.JobSchemaVersion)+"\n"), 0o644))

	_, err = s.Update(t.Context(), "adj/store", func(u *types.Job) { u.State = types.StateRunning })
	require.NoError(t, err)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "owned_paths", "the old spelling does not ride forward once it has been folded")
	assert.Contains(t, string(raw), `"write_paths"`, "the current spelling carries the folded value")

	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, []string{"internal/ledger"}, rows[0].WritePaths, "a second read does not trip the both-spellings refusal")
}

func TestStoreListReturnsCopies(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, t.TempDir())
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

	s := tmpStore(t, t.TempDir())
	path, err := s.Path()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))

	_, err = s.List()
	assert.Error(t, err, "a corrupt job store is reported, never silently read as empty")
}

func TestRecordUnattributedWrite(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644))
	s := tmpStore(t, root)
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
	s := tmpStore(t, root)
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
	s := tmpStore(t, root)
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

// A lease that ended between the grading and the record is nothing to report to anybody, and
// inventing a row for it would put a plan in the ledger nobody declared.
func TestRecordUnattributedWriteDoesNotInventARow(t *testing.T) {
	s := tmpStore(t, t.TempDir())

	require.NoError(t, s.RecordUnattributedWrite(t.Context(), "never-declared", "a.go"))

	rows, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, rows)
}
