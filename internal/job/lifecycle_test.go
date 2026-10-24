package job

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A probe history appends what changed, and is rewritten to the entries still kept when one
// is dropped, so a row no longer probed leaves the file.
func TestLandingProbesAppendAndRewriteOnADrop(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	path, err := s.Path()
	require.NoError(t, err)
	probes := filepath.Join(filepath.Dir(path), landingProbesFile)

	s.writeLandingProbes(s.readLandingProbes(), map[string]landingProbe{"a": {At: 1, Updated: 1}, "b": {At: 1, Updated: 1}})
	s.writeLandingProbes(s.readLandingProbes(), map[string]landingProbe{"a": {At: 2, Updated: 1}, "b": {At: 1, Updated: 1}})
	got := s.readLandingProbes()
	assert.Equal(t, probeLog{probes: map[string]landingProbe{"a": {At: 2, Updated: 1}, "b": {At: 1, Updated: 1}}, lines: 3}, got,
		"the changed entry was appended")

	s.writeLandingProbes(got, map[string]landingProbe{"b": {At: 1, Updated: 1}})
	raw, err := os.ReadFile(probes)
	require.NoError(t, err)
	assert.Equal(t, `{"id":"b","at":1,"updated":1}`+"\n", string(raw))
}

// TestEndedReasonSaysWhichEnd: a read-only scout its holder finished, and a row the root
// ended for an isolated worker that filed nothing, both read as abandoned no_return with no
// reason (2026-10-14). Every no_return a write records now says which end it was.
func TestEndedReasonSaysWhichEnd(t *testing.T) {
	t.Parallel()
	check := &types.LeaseCheck{Target: "test", Project: "."}
	filed := &types.JobResult{}
	for _, tt := range []struct {
		name     string
		row      types.Job
		byHolder bool
		want     string
	}{
		{"a read-only scout that filed nothing", types.Job{ID: "s", ReadOnly: true, Registered: 1}, true, "ended ungraded"},
		{"a result nobody verified", types.Job{ID: "w", Check: check, Registered: 1, Result: filed}, false, "ended unverified"},
		{"a row nobody took", types.Job{ID: "w", Check: check}, false, "before any holder ran"},
		{"its holder giving it up", types.Job{ID: "w", Check: check, Registered: 1}, true, "abandoned by its holder"},
		{"the root ending an isolated worker's row", types.Job{ID: "w", Check: check, Registered: 1}, false, "ended by someone other than its holder"},
	} {
		assert.Contains(t, endedReason(tt.row, tt.byHolder), tt.want, tt.name)
	}
}

// A read-only scout declaring no check or goal used to end no_return "returned ungraded"
// even when its holder filed a result. Nothing but that result can grade it, so filing one
// records pass; a scout with a script check still waits to be graded.
func TestExitPassesAReadOnlyRowNothingElseGrades(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	seed(t, s, types.Job{ID: "notes", ReadOnly: true, State: types.StateRunning, Registered: 1})
	seed(t, s, types.Job{ID: "probe", ReadOnly: true, State: types.StateRunning, Registered: 1, Check: &types.LeaseCheck{Script: "probe.buzz"}})
	none := func(context.Context, string) (types.JobAttempt, error) { return types.JobAttempt{}, nil }

	notes, err := Exit(t.Context(), s, "notes", &types.JobResult{}, none)
	require.NoError(t, err)
	notes.Schema, notes.Generation, notes.Attempts, notes.RegisteredBy = types.Schema{}, 0, 0, types.Origin{}
	notes.Created, notes.Updated, notes.Result, notes.GateAttempts = 0, 0, nil, nil
	assert.Equal(t, types.Job{ID: "notes", ReadOnly: true, State: types.StatePass, Registered: 1, EndReason: returnedReason}, notes,
		"the store's own stamps aside, a read-only row passes and changes nothing else")

	probe, err := Exit(t.Context(), s, "probe", &types.JobResult{}, none)
	require.NoError(t, err)
	assert.Equal(t, types.StateExited, probe.State, "a check grades it at wait")
	assert.Empty(t, probe.EndReason)
}

// observeClaim is an observer whose diff shows exactly what rep claims.
func observeClaim(rep types.JobResult) Observer {
	return func(context.Context, types.Job) (Observed, error) { return claimed(rep), nil }
}

// TestExitFilesEvidenceForAWaitInAnotherCheckout keeps the lifecycle at the Store
// boundary. The resolver is the worker checkout's output cache; the waiter deliberately
// has no resolver, proving Exit copied the portable attempt onto the repository-wide row.
func TestExitFilesEvidenceForAWaitInAnotherCheckout(t *testing.T) {
	t.Parallel()

	loc := declared(t, coveredRow())
	result := passingResult()
	attempt := passingRun
	attempt.Ref = result.Validation.OutputRef
	attempt.TimestampMs = 9_999_999_999_999
	resolver := func(_ context.Context, ref string) (types.JobAttempt, error) {
		assert.Equal(t, result.Validation.OutputRef, ref)
		return attempt, nil
	}

	exited, err := Exit(t.Context(), boundStore(loc, result.Job), result.Job, &result, resolver)
	require.NoError(t, err)
	assert.Equal(t, types.StateExited, exited.State)
	require.NotNil(t, exited.Result)
	require.NotNil(t, exited.Attempt)
	attempt.ObservedGeneration = 1
	assert.Equal(t, attempt, *exited.Attempt, "the run is stamped with the generation it was filed at")
	assert.Equal(t, 1, exited.Result.ObservedGeneration)

	status, err := Wait(t.Context(), NewStore(loc), result.Job, nil, nil, observeClaim(result), nil)
	require.NoError(t, err)
	// The primary check is LISTED, like any other gate: it is one, and reporting it only
	// when other gates existed is what let `describe job` say a check-only job had none.
	assert.True(t, status.Clean(), "a verified job carries no risk")
	status.UnresolvedRisks = nil
	assert.Equal(t, types.JobStatus{
		Job:                result.Job,
		Verified:           true,
		Command:            result.Validation.Command,
		Gates:              []types.GateStatus{{ID: types.PrimaryGoalID, OutputRef: result.Validation.OutputRef, Verified: true}},
		ObservedGeneration: 1,
	}, status)

	rows, err := NewStore(loc).List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, types.StatePass, rows[0].State)
}

func TestWaitIgnoresASoftPathNobodyWrote(t *testing.T) {
	t.Parallel()

	row := coveredRow()
	row.WritePaths = append(row.WritePaths, "docs/unwritten_test.go")
	row.SoftPaths = []string{"docs/unwritten_test.go"}
	loc := declared(t, row)
	result := passingResult()
	attempt := passingRun
	attempt.Ref = result.Validation.OutputRef
	attempt.TimestampMs = 9_999_999_999_999
	resolver := func(context.Context, string) (types.JobAttempt, error) { return attempt, nil }

	_, err := Exit(t.Context(), boundStore(loc, result.Job), result.Job, &result, resolver)
	require.NoError(t, err)
	status, err := Wait(t.Context(), NewStore(loc), result.Job, nil, nil, observeClaim(result), nil)
	require.NoError(t, err)
	assert.True(t, status.Verified)
	assert.Empty(t, status.Violations, "an unwritten soft path is no violation")

	rows, err := NewStore(loc).List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Empty(t, rows[0].Releases, "and the exit gave up no path it held")
	assert.Empty(t, rows[0].SoftPaths)
}

func TestExitFilesEveryGoalForAWaitInAnotherCheckout(t *testing.T) {
	t.Parallel()

	row := goalRow()
	loc := declared(t, row)
	result := goalResult()
	attempts := map[string]types.JobAttempt{}
	for _, snapshot := range goalAttempts() {
		snapshot.Attempt.TimestampMs = 9_999_999_999_999
		attempts[snapshot.Attempt.Ref] = snapshot.Attempt
	}
	resolver := func(_ context.Context, ref string) (types.JobAttempt, error) { return attempts[ref], nil }

	exited, err := Exit(t.Context(), boundStore(loc, row.ID), row.ID, &result, resolver)
	require.NoError(t, err)
	require.Len(t, exited.GateAttempts, 2)
	assert.Nil(t, exited.Attempt, "a gate-only job has no synthetic primary attempt")

	status, err := Wait(t.Context(), NewStore(loc), row.ID, nil, nil, observeClaim(result), nil)
	require.NoError(t, err)
	assert.True(t, status.Verified, status.Violations)
	require.Len(t, status.Gates, 2)
}

// coveredRow is acceptRow holding only what passingResult changes, so its exit narrows
// nothing and the spec keeps its generation.
func coveredRow() types.Job {
	row := acceptRow()
	row.WritePaths = []string{"internal/ledger", "cmd/magus/ledger.go"}
	return row
}

// An exited job awaiting its wait holds only what its result changed, so a fork over a
// file it never touched is not refused on its account; what it dropped is a release, and
// the wait still grades the result clean. An abandonment keeps every path.
func TestExitNarrowsToWhatTheResultChanged(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	loc := declared(t, row)
	result := passingResult()
	resolver := func(context.Context, string) (types.JobAttempt, error) {
		attempt := passingRun
		attempt.Ref, attempt.TimestampMs = result.Validation.OutputRef, 9_999_999_999_999
		return attempt, nil
	}

	exited, err := Exit(t.Context(), boundStore(loc, row.ID), row.ID, &result, resolver)
	require.NoError(t, err)
	require.Len(t, exited.Releases, 1)
	assert.False(t, exited.Releases[0].Revoked, "the holder gave it up")
	assert.Equal(t, 2, exited.Result.ObservedGeneration, "the evidence proved the wider spec, so it grades the narrowed one")
	want := row
	want.Version = types.JobSchemaVersion
	want.WritePaths = []string{"internal/ledger", "cmd/magus/ledger.go"} // a directory covers what is under it
	want.Releases = []types.JobRelease{{Path: "docs/reference/*.md", Digest: "absent", ReleasedAt: exited.Releases[0].ReleasedAt}}
	want.Generation = 2 // narrowing is a spec change
	want.Attempts, want.State = 1, types.StateExited
	want.RegisteredBy, want.Created, want.Updated = exited.RegisteredBy, exited.Created, exited.Updated
	want.Result, want.Attempt, want.GateAttempts = exited.Result, exited.Attempt, exited.GateAttempts
	assert.Equal(t, want, exited)

	status, err := Wait(t.Context(), NewStore(loc), row.ID, nil, nil, observeClaim(result), nil)
	require.NoError(t, err)
	assert.True(t, status.Verified, status.Violations)

	t.Run("a claim covers its file", func(t *testing.T) {
		t.Parallel()
		claims := acceptRow()
		claims.WritePaths = []string{"run.go#A", "run.go#B", "other.go#C"}
		loc := declared(t, claims)
		changed := passingResult()
		changed.ChangedPaths = []string{"run.go"}
		exited, err := Exit(t.Context(), boundStore(loc, claims.ID), claims.ID, &changed, resolver)
		require.NoError(t, err)
		assert.Equal(t, []string{"run.go#A", "run.go#B"}, exited.WritePaths)
	})

	t.Run("a result covering none of the paths keeps them for the grade to name", func(t *testing.T) {
		t.Parallel()
		stray := acceptRow()
		loc := declared(t, stray)
		off := passingResult()
		off.ChangedPaths = []string{"elsewhere.go"}
		exited, err := Exit(t.Context(), boundStore(loc, stray.ID), stray.ID, &off, resolver)
		require.NoError(t, err)
		assert.Equal(t, stray.WritePaths, exited.WritePaths)
		assert.Empty(t, exited.Releases)
	})

	t.Run("an abandonment keeps every path", func(t *testing.T) {
		t.Parallel()
		kept := acceptRow()
		loc := declared(t, kept)
		stored, err := Exit(t.Context(), boundStore(loc, kept.ID), kept.ID, nil, nil)
		require.NoError(t, err)
		assert.Equal(t, kept.WritePaths, stored.WritePaths)
	})
}

func TestExitWithoutAResultRecordsNoReturn(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	loc := declared(t, row)
	stored, err := Exit(t.Context(), boundStore(loc, row.ID), row.ID, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, types.StateNoReturn, stored.State)
	assert.Nil(t, stored.Result)
	assert.Nil(t, stored.Attempt)
}

func TestWaitKeepsARejectedJobOpen(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	loc := declared(t, row)
	result := passingResult()
	result.ChangedPaths = nil // a writing job cannot be accepted on an empty claimed change set

	status, err := Wait(t.Context(), NewStore(loc), row.ID, &result, func(_ context.Context, _ string) (types.JobAttempt, error) {
		return passingRun, nil
	}, nil, nil)
	require.NoError(t, err)
	assert.False(t, status.Verified)
	assert.Contains(t, status.Violations, "job harness/ledger-accept is not read-only and the result claims no changed paths at all")

	rows, err := NewStore(loc).List()
	require.NoError(t, err)
	assert.Equal(t, types.StateRunning, rows[0].State, "a rejected result is evidence to repair, not a terminal verdict")
}

// A rejection is recorded for the next attempt to read, in the verdict's write and with the
// row's heartbeat untouched; a pass clears it.
func TestWaitRecordsARejectionAndAPassClearsIt(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	loc := declared(t, row)
	before, err := NewStore(loc).List()
	require.NoError(t, err)
	run := passingRun
	run.Ref, run.TimestampMs = passingResult().Validation.OutputRef, 9_999_999_999_999
	resolve := func(context.Context, string) (types.JobAttempt, error) { return run, nil }
	empty := passingResult()
	empty.ChangedPaths = nil

	_, err = Wait(t.Context(), NewStore(loc), row.ID, &empty, resolve, nil, nil)
	require.NoError(t, err)

	rows, err := NewStore(loc).List()
	require.NoError(t, err)
	require.NotNil(t, rows[0].Rejection)
	assert.Contains(t, rows[0].Rejection.Violations, "job harness/ledger-accept is not read-only and the result claims no changed paths at all")
	assert.Positive(t, rows[0].Rejection.At)
	assert.Equal(t, 1, rows[0].Rejection.ObservedGeneration)
	assert.Equal(t, before[0].Updated, rows[0].Updated, "the waiter is not the holder, so the heartbeat stays")

	result := passingResult()
	status, err := Wait(t.Context(), NewStore(loc), row.ID, &result, resolve, observeClaim(result), nil)
	require.NoError(t, err)
	require.True(t, status.Verified, status.Violations)
	rows, err = NewStore(loc).List()
	require.NoError(t, err)
	assert.Nil(t, rows[0].Rejection)
}

// Evidence filed before the spec changed never passes a wait: the wait says which generation
// it came from and what to run again, and describe labels it prior. The run behind the check
// belongs to the spec that asked for it, so it no longer grades.
func TestWaitNeverPassesOnEvidenceOfAnEarlierSpec(t *testing.T) {
	t.Parallel()

	loc := declared(t, coveredRow())
	result := passingResult()
	attempt := passingRun
	attempt.Ref, attempt.TimestampMs = result.Validation.OutputRef, 9_999_999_999_999
	resolver := func(context.Context, string) (types.JobAttempt, error) { return attempt, nil }
	_, err := Exit(t.Context(), boundStore(loc, result.Job), result.Job, &result, resolver)
	require.NoError(t, err)
	respecced, err := NewStore(loc).Update(t.Context(), result.Job, func(u *types.Job) { u.Criteria = "also cover the console" })
	require.NoError(t, err)
	require.Equal(t, 2, respecced.Generation)

	described, err := GradeGates(t.Context(), NewStore(loc), result.Job, observeClaim(result))
	require.NoError(t, err)
	wantDescribed := described
	wantDescribed.PriorGeneration, wantDescribed.ObservedGeneration = 1, 2
	wantDescribed.Verified = false // the run of generation 1 no longer grades the check
	assert.Equal(t, wantDescribed, described)

	status, err := Wait(t.Context(), NewStore(loc), result.Job, nil, nil, observeClaim(result), nil)
	require.NoError(t, err)
	assert.False(t, status.Verified)
	assert.Equal(t, 1, status.PriorGeneration)
	i := slices.IndexFunc(status.Violations, func(v string) bool {
		return strings.HasPrefix(v, "the evidence is from generation 1 and the spec is now generation 2: run ")
	})
	require.GreaterOrEqual(t, i, 0, status.Violations)
	assert.Contains(t, status.Violations[i], "go-test", "the violation names the check to run again")
	rows, err := NewStore(loc).List()
	require.NoError(t, err)
	assert.Equal(t, types.StateRunning, rows[0].State, "the rejection sends it back to rework")
	require.NotNil(t, rows[0].Rejection)
	assert.Equal(t, 2, rows[0].Rejection.ObservedGeneration)
}

// A rejecting wait sends an exited row back to rework, holding its write paths again under
// the rule a claim meets; a path another job took meanwhile refuses the re-claim, naming
// that job, and the row stays exited holding nothing.
func TestWaitReclaimsTheWritePathsForRework(t *testing.T) {
	t.Parallel()

	result := passingResult()
	attempt := passingRun
	attempt.Ref, attempt.TimestampMs = result.Validation.OutputRef, 9_999_999_999_999
	resolve := func(context.Context, string) (types.JobAttempt, error) { return attempt, nil }
	empty := result
	empty.ChangedPaths = nil
	exited := func(t *testing.T, others ...types.Job) Location {
		t.Helper()
		loc := declared(t, append([]types.Job{acceptRow()}, others...)...)
		_, err := Exit(t.Context(), boundStore(loc, result.Job), result.Job, &result, resolve)
		require.NoError(t, err)
		return loc
	}
	state := func(t *testing.T, loc Location) types.JobState {
		t.Helper()
		rows, err := NewStore(loc).List()
		require.NoError(t, err)
		return rows[slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == result.Job })].State
	}

	loc := exited(t)
	status, err := Wait(t.Context(), NewStore(loc), result.Job, &empty, resolve, nil, nil)
	require.NoError(t, err)
	require.False(t, status.Verified)
	assert.Equal(t, types.StateRunning, state(t, loc))
	assert.Contains(t, strings.Join(status.Notes, "\n"), "holds its write paths again")

	taker := types.Job{ID: "taker", Criteria: "c", WritePaths: []string{"cmd/magus/ledger.go"}, State: types.StateRunning}
	loc = exited(t, taker)
	status, err = Wait(t.Context(), NewStore(loc), result.Job, &empty, resolve, nil, nil)
	require.NoError(t, err)
	require.False(t, status.Verified)
	assert.Equal(t, types.StateExited, state(t, loc))
	assert.Contains(t, strings.Join(status.Notes, "\n"), "cmd/magus/ledger.go is held by live job taker")
}

// lineageRow is acceptRow renamed into a tree under parent.
func lineageRow(id, parent string) types.Job {
	row := acceptRow()
	row.ID, row.Parent = id, parent
	return row
}

// lineage is a worker's tree: its root, the worker, a sibling, a row outside the tree,
// and below the worker whatever the case adds.
func lineage(below ...types.Job) []types.Job {
	return append([]types.Job{
		lineageRow("root", ""),
		lineageRow("root/worker", "root"),
		lineageRow("root/sibling", "root"),
		lineageRow("other", ""),
	}, below...)
}

// waitAs waits on target as the worker leased to holder, with a result that verifies.
func waitAs(t *testing.T, loc Location, holder, target string) (types.JobStatus, error) {
	t.Helper()
	result := passingResult()
	result.Job = target
	attempt := passingRun
	attempt.Ref = result.Validation.OutputRef
	attempt.TimestampMs = 9_999_999_999_999
	return Wait(t.Context(), boundStore(loc, holder), target, &result, func(context.Context, string) (types.JobAttempt, error) {
		return attempt, nil
	}, observeClaim(result), nil)
}

// jobStates maps each row in loc's store to its state.
func jobStates(t *testing.T, loc Location) map[string]types.JobState {
	t.Helper()
	rows, err := NewStore(loc).List()
	require.NoError(t, err)
	out := map[string]types.JobState{}
	for _, row := range rows {
		out[row.ID] = row.State
	}
	return out
}

func TestWaitLetsAHolderVerifyItsDescendants(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		rows   []types.Job
		target string
	}{
		"child": {
			rows:   lineage(lineageRow("root/worker/child", "root/worker")),
			target: "root/worker/child",
		},
		"grandchild": {
			rows: lineage(
				lineageRow("root/worker/child", "root/worker"),
				lineageRow("root/worker/child/leaf", "root/worker/child"),
			),
			target: "root/worker/child/leaf",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			loc := declared(t, tt.rows...)
			status, err := waitAs(t, loc, "root/worker", tt.target)
			require.NoError(t, err)
			assert.True(t, status.Verified, status.Violations)

			want := map[string]types.JobState{}
			for _, row := range tt.rows {
				want[row.ID] = types.StateRunning
			}
			want[tt.target] = types.StatePass
			assert.Equal(t, want, jobStates(t, loc))
		})
	}
}

func TestWaitRefusesAHolderOutsideItsDescendants(t *testing.T) {
	t.Parallel()

	for name, target := range map[string]string{
		"its own row": "root/worker",
		"a sibling":   "root/sibling",
		"an ancestor": "root",
		"unrelated":   "other",
		"undeclared":  "root/worker/ghost",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rows := lineage(lineageRow("root/worker/child", "root/worker"))
			loc := declared(t, rows...)
			_, err := waitAs(t, loc, "root/worker", target)
			require.EqualError(t, err, "job: this checkout holds the lease on root/worker and a holder does not verify its own work")

			want := map[string]types.JobState{}
			for _, row := range rows {
				want[row.ID] = types.StateRunning
			}
			assert.Equal(t, want, jobStates(t, loc), "a refused wait writes nothing")
		})
	}
}

// plant writes rows exactly as given, timestamps included, which no write door allows.
func plant(t *testing.T, s *Store, rows ...types.Job) {
	t.Helper()
	require.NoError(t, s.update(t.Context(), func(f *jobsFile) error {
		*f = jobsFile{Jobs: rows}
		return nil
	}))
}

// sweepStore is a temp store whose sweep runs at now with a fixed jobs.stale_after, its
// stderr lines captured.
func sweepStore(t *testing.T, now int64, staleAfter time.Duration) (*Store, *strings.Builder) {
	t.Helper()
	s := tempStore(t, t.TempDir())
	var notices strings.Builder
	s.clock = func() time.Time { return time.Unix(now, 0) }
	s.staleAfter = &staleAfter
	s.notices = &notices
	return s, &notices
}

func states(t *testing.T, s *Store) map[string]types.Job {
	t.Helper()
	rows, err := s.List()
	require.NoError(t, err)
	out := map[string]types.Job{}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out
}

func TestListEndsProvablyDeadJobs(t *testing.T) {
	const now = int64(100_000)
	fresh := now - 60
	old := now - int64((3 * time.Hour).Seconds())
	gone := filepath.Join(t.TempDir(), "removed-worktree")
	here := t.TempDir()
	notDir := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(notDir, nil, 0o644))

	tests := []struct {
		name       string
		staleAfter time.Duration
		rows       []types.Job
		ended      map[string]string
	}{
		{
			name: "a child of an ended parent ends with it",
			rows: []types.Job{
				{ID: "root", State: types.StatePass, Updated: fresh},
				{ID: "root/a", Parent: "root", State: types.StateRunning, Registered: fresh, Updated: fresh},
			},
			ended: map[string]string{"root/a": "parent root ended as pass, and its tree ends with it"},
		},
		{
			name: "the whole tree under an ended root ends, grandchildren included",
			rows: []types.Job{
				{ID: "root", State: types.StateNoReturn, Updated: fresh},
				{ID: "root/a", Parent: "root", State: types.StateRunning, Updated: fresh},
				{ID: "root/a/b", Parent: "root/a", State: types.StateExited, Updated: fresh},
			},
			ended: map[string]string{
				"root/a":   "parent root ended as no_return, and its tree ends with it",
				"root/a/b": "ancestor root ended as no_return, and its tree ends with it",
			},
		},
		{
			name: "a job taken in a checkout that no longer exists ends, and its children with it",
			rows: []types.Job{
				{ID: "w", State: types.StateDeclared, Registered: fresh, CheckoutRoot: gone, Updated: fresh},
				{ID: "w/c", Parent: "w", State: types.StateDeclared, Updated: fresh},
			},
			ended: map[string]string{
				"w":   "taken in " + gone + ", which no longer exists",
				"w/c": "parent w ended as no_return, and its tree ends with it",
			},
		},
		{
			name: "a checkout that is still there, one that cannot be read, and an exited holder all stay live",
			rows: []types.Job{
				{ID: "present", State: types.StateRunning, Registered: fresh, CheckoutRoot: here, Updated: old},
				{ID: "undecidable", State: types.StateRunning, Registered: fresh, CheckoutRoot: filepath.Join(notDir, "sub"), Updated: old},
				{ID: "returned", State: types.StateExited, Registered: fresh, CheckoutRoot: gone, Updated: old},
			},
		},
		{
			name:       "a declared job nobody took ends once it is older than stale_after",
			staleAfter: 2 * time.Hour,
			rows: []types.Job{
				{ID: "untaken", State: types.StateDeclared, Updated: old},
				{ID: "recent", State: types.StateDeclared, Updated: fresh},
				{ID: "taken", State: types.StateDeclared, Registered: old, Updated: old},
				{ID: "running", State: types.StateRunning, Updated: old},
				{ID: "maintenance", Holder: types.HolderServer, State: types.StateDeclared, Updated: old},
			},
			ended: map[string]string{"untaken": "declared and never taken, untouched for 3h0m0s (jobs.stale_after is 2h0m0s)"},
		},
		{
			name:       "a declared job waiting on a running dependency is queued, and an exited one starts its clock",
			staleAfter: 2 * time.Hour,
			rows: []types.Job{
				{ID: "first", State: types.StateRunning, Registered: fresh, Updated: fresh},
				{ID: "handed-back", State: types.StateExited, Registered: fresh, Updated: old},
				{ID: "just-handed-back", State: types.StateExited, Registered: fresh, Updated: fresh},
				{ID: "queued", State: types.StateDeclared, DependsOn: []string{"first"}, Updated: old},
				{ID: "queued-on-exited", State: types.StateDeclared, DependsOn: []string{"handed-back"}, Updated: old},
				{ID: "queued-on-just-exited", State: types.StateDeclared, DependsOn: []string{"just-handed-back"}, Updated: old},
			},
			ended: map[string]string{"queued-on-exited": "declared and never taken, untouched for 3h0m0s (jobs.stale_after is 2h0m0s)"},
		},
		{
			name:       "a waiter's clock starts when its last dependency ended",
			staleAfter: 2 * time.Hour,
			rows: []types.Job{
				{ID: "just-done", State: types.StatePass, Updated: fresh},
				{ID: "long-done", State: types.StatePass, Updated: old},
				{ID: "unblocked", State: types.StateDeclared, DependsOn: []string{"just-done", "long-done"}, Updated: old},
				{ID: "forgotten", State: types.StateDeclared, DependsOn: []string{"long-done"}, Updated: old - 60},
			},
			ended: map[string]string{"forgotten": "declared and never taken, untouched for 3h0m0s (jobs.stale_after is 2h0m0s)"},
		},
		{
			name:       "a checkout_root with no registration is not a holder",
			staleAfter: 2 * time.Hour,
			rows: []types.Job{
				{ID: "forked", State: types.StateDeclared, CheckoutRoot: here, Updated: old},
			},
			ended: map[string]string{"forked": "declared and never taken, untouched for 3h0m0s (jobs.stale_after is 2h0m0s)"},
		},
		{
			name: "a taken job past its deadline ends, and so does its tree; an untaken or returned one stays",
			rows: []types.Job{
				{ID: "late", State: types.StateRunning, Registered: fresh, Deadline: now - 600, Updated: fresh},
				{ID: "late/c", Parent: "late", State: types.StateRunning, Registered: fresh, Updated: fresh},
				{ID: "untaken", State: types.StateDeclared, Deadline: now - 600, Updated: fresh},
				{ID: "returned", State: types.StateExited, Registered: fresh, Deadline: now - 600, Updated: fresh},
				{ID: "in-time", State: types.StateRunning, Registered: fresh, Deadline: now + 600, Updated: fresh},
			},
			ended: map[string]string{
				"late":   "overdue: its deadline passed 10m0s ago",
				"late/c": "parent late ended as no_return, and its tree ends with it",
			},
		},
		{
			name: "a zero stale_after ends nothing for age",
			rows: []types.Job{{ID: "untaken", State: types.StateDeclared, Updated: 1}},
		},
		{
			name:       "an untaken root outlives its working children and ends after the last untaken one",
			staleAfter: 2 * time.Hour,
			rows: []types.Job{
				{ID: "busy", State: types.StateDeclared, Updated: old},
				{ID: "busy/w", Parent: "busy", State: types.StateRunning, Registered: fresh, Updated: fresh},
				{ID: "idle", State: types.StateDeclared, Updated: old},
				{ID: "idle/w", Parent: "idle", State: types.StateDeclared, Updated: old},
			},
			ended: map[string]string{
				"idle/w": "declared and never taken, untouched for 3h0m0s (jobs.stale_after is 2h0m0s)",
				"idle":   "declared and never taken, untouched for 3h0m0s (jobs.stale_after is 2h0m0s)",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, notices := sweepStore(t, now, tt.staleAfter)
			plant(t, s, tt.rows...)

			got := states(t, s)
			var lines []string
			for _, want := range tt.rows {
				row := got[want.ID]
				reason, ended := tt.ended[want.ID]
				wantRow := want
				wantRow.Schema = row.Schema // the envelope is the store's, stamped on read
				if !ended {
					assert.Equal(t, wantRow, row, "%s stays as it was", want.ID)
					continue
				}
				wantRow.State, wantRow.EndReason, wantRow.Updated = types.StateNoReturn, reason, now
				assert.Equal(t, wantRow, row, "%s ends as no_return with its reason, and ending a row is its own transition", want.ID)
				lines = append(lines, "ended "+want.ID+": "+reason)
			}
			gotLines := strings.Split(strings.TrimSuffix(notices.String(), "\n"), "\n")
			if len(lines) == 0 {
				gotLines = nil
			}
			assert.ElementsMatch(t, lines, gotLines, "one stderr line per ended job")
		})
	}
}

// A sweep with nothing left to end writes nothing and prints nothing, so every read after
// the first is the lock-free read it always was.
func TestListSweepIsIdempotent(t *testing.T) {
	s, notices := sweepStore(t, 100_000, time.Hour)
	plant(t, s,
		types.Job{ID: "root", State: types.StateFail, Updated: 99_000},
		types.Job{ID: "root/a", Parent: "root", State: types.StateDeclared, Updated: 99_000},
	)
	first := states(t, s)
	require.Equal(t, types.StateNoReturn, first["root/a"].State)
	path, err := s.Path()
	require.NoError(t, err)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	notices.Reset()

	assert.Equal(t, first, states(t, s))
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
	assert.Empty(t, notices.String())
}

// Exec records where the job was taken, so removing that worktree ends the job.
func TestExecRecordsTheCheckoutAndItsRemovalEndsTheJob(t *testing.T) {
	checkout := execRepo(t).Root()
	s := tempStore(t, checkout)
	var notices strings.Builder
	s.notices = &notices
	seed(t, s, types.Job{ID: "w", State: types.StateDeclared, Checkpoint: "abc"})

	taken, err := s.Exec(t.Context(), "w")
	require.NoError(t, err)
	abs, err := filepath.Abs(checkout)
	require.NoError(t, err)
	assert.Equal(t, abs, taken.CheckoutRoot)

	seed(t, s, types.Job{ID: "w", State: types.StateDeclared, Checkpoint: "abc", Criteria: "rewritten"})
	require.NoError(t, os.RemoveAll(checkout))

	rows := states(t, s)
	assert.Equal(t, stamped(types.Job{
		ID:           "w",
		Criteria:     "rewritten",
		Checkpoint:   "abc",
		State:        types.StateNoReturn,
		ReportedBase: taken.ReportedBase,
		BaseVerdict:  taken.BaseVerdict,
		Registered:   taken.Registered,
		CheckoutRoot: abs,
		EndReason:    "taken in " + abs + ", which no longer exists",
	}, rows["w"]), rows["w"], "a declaration carries the store's record forward")
	assert.Equal(t, "ended w: taken in "+abs+", which no longer exists\n", notices.String())
}

// The guard appends to unattributed every time somebody else writes inside a job's paths.
// That is not the job doing anything, and moving updated for it made dead rows look alive.
func TestUnattributedWritesLeaveUpdatedAlone(t *testing.T) {
	root := t.TempDir()
	s := tempStore(t, root)
	plant(t, s, types.Job{ID: "w", State: types.StateRunning, WritePaths: []string{"a.go"}, Created: 50, Updated: 50})

	require.NoError(t, s.RecordUnattributedWrite(t.Context(), "w", "a.go"))

	row := states(t, s)["w"]
	require.Len(t, row.Unattributed, 1)
	assert.Equal(t, int64(50), row.Updated)
}

// The sweep reads jobs.stale_after from the workspace's own magus.yaml, where a written
// zero means never and an absent key is the 2h default.
func TestSweepReadsStaleAfterFromTheWorkspace(t *testing.T) {
	old := time.Now().Add(-3 * time.Hour).Unix()
	for _, tc := range []struct {
		name, yaml string
		ended      bool
	}{
		{name: "the default ends a row three hours old", ended: true},
		{name: "a written zero is never", yaml: "jobs:\n  stale_after: 0s\n"},
		{name: "a longer window keeps it", yaml: "jobs:\n  stale_after: 4h\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.yaml != "" {
				require.NoError(t, os.WriteFile(filepath.Join(root, config.Filename), []byte(tc.yaml), 0o644))
			}
			s := tempStore(t, root)
			s.notices = io.Discard
			plant(t, s, types.Job{ID: "untaken", State: types.StateDeclared, Updated: old})

			want := types.StateDeclared
			if tc.ended {
				want = types.StateNoReturn
			}
			assert.Equal(t, want, states(t, s)["untaken"].State)
		})
	}
}

// A magus.yaml the sweep cannot read is an error on the read, never a silent default.
func TestSweepRefusesAnUnreadableWorkspaceConfig(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, config.Filename), []byte("jobs:\n  stale_aftr: 1h\n"), 0o644))
	s := tempStore(t, root)
	plant(t, s, types.Job{ID: "untaken", State: types.StateDeclared, Updated: 1})

	_, err := s.List()
	assert.ErrorContains(t, err, "jobs.stale_after")
}

// An exited row the landing probe proves is on the base ends as no_return with the
// probe's reason; one with a live child, one another write moved since the probe, and one
// the probe does not prove all stay.
func TestListEndsExitedJobsWhoseWorkLanded(t *testing.T) {
	s, notices := sweepStore(t, 100_000, 0)
	result := &types.JobResult{ChangedPaths: []string{"a.go"}}
	exited := func(id string) types.Job {
		return types.Job{ID: id, State: types.StateExited, Result: result, Checkpoint: "c0", CheckoutRoot: "/w/" + id, Updated: 90_000}
	}
	busy := exited("busy")
	plant(t, s,
		exited("landed"),
		busy,
		types.Job{ID: "busy/child", Parent: "busy", State: types.StateRunning, Updated: 90_000},
		exited("pending"),
		types.Job{ID: "reader", State: types.StateExited, ReadOnly: true, Updated: 90_000},
	)
	var probed []string
	s.landed = func(_ context.Context, row types.Job) string {
		probed = append(probed, row.ID)
		if row.ID == "pending" {
			return ""
		}
		return "its work landed on origin/main at base1"
	}

	got := states(t, s)
	assert.Equal(t, []string{"landed", "busy", "pending"}, probed, "only exited rows with changed paths in a checkout are probed")
	landed := exited("landed")
	landed.Schema = got["landed"].Schema // the envelope is the store's, stamped on read
	landed.State, landed.EndReason, landed.Updated = types.StateNoReturn, "its work landed on origin/main at base1", 100_000
	assert.Equal(t, landed, got["landed"])
	for _, id := range []string{"busy", "pending", "reader"} {
		assert.Equal(t, types.StateExited, got[id].State, id)
	}
	assert.Equal(t, "ended landed: its work landed on origin/main at base1\n", notices.String())
}

// One read probes at most landingBudget rows, least recently probed first, and records
// each before probing it: a guard hook killed mid-probe has still taken its turn, so the
// next round moves on instead of starting the same probes over. Reads inside one
// landingRound share that round and probe nothing more.
func TestALandingSweepIsBoundedAndRecordsItsProbesFirst(t *testing.T) {
	s, _ := sweepStore(t, 100_000, 0)
	var rows []types.Job
	for i := range landingBudget + 2 {
		rows = append(rows, types.Job{ID: fmt.Sprintf("exited-%d", i), State: types.StateExited, Checkpoint: "c0",
			CheckoutRoot: "/w", Result: &types.JobResult{ChangedPaths: []string{"a.go"}}, Updated: 90_000})
	}
	plant(t, s, rows...)
	var probed []string
	s.landed = func(_ context.Context, row types.Job) string {
		assert.Contains(t, s.readLandingProbes().probes, row.ID, "recorded before the probe runs")
		probed = append(probed, row.ID)
		return ""
	}

	states(t, s)
	assert.Equal(t, []string{"exited-0", "exited-1", "exited-2", "exited-3"}, probed)
	probed = nil
	states(t, s)
	assert.Empty(t, probed, "a read inside the round probes nothing")
	s.clock = func() time.Time { return time.Unix(100_000, 0).Add(landingRound) }
	states(t, s)
	assert.Equal(t, []string{"exited-4", "exited-5"}, probed, "the next round takes the rows the budget left")
	probed = nil
	s.clock = func() time.Time { return time.Unix(100_000, 0).Add(2 * landingRound) }
	states(t, s)
	assert.Empty(t, probed, "every row was probed within the interval")
}

// Two jobs whose branches were squashed into ONE base commit both end, judged by content
// alone: no ancestry joins either branch to the squash. A job whose commit is not on the
// base, and one whose change was never committed, both stay exited.
func TestLandedJobsEndWhenSquashedIntoOneBaseCommit(t *testing.T) {
	repo := testkit.NewRepo(t, map[string]string{"a.go": "package a\n", "b.go": "package b\n", "c.go": "package c\n", "d.go": "package d\n"})
	root := repo.Root()
	seedRev := repo.Head()
	worktree := func(name, file, body string, commit bool) *testkit.Repo {
		wt := repo.AddWorktree(name)
		wt.Write(file, body)
		if commit {
			wt.Git("commit", "-q", "-am", name)
		}
		return wt
	}
	wa := worktree("job-a", "a.go", "package a // landed\n", true)
	wb := worktree("job-b", "b.go", "package b // landed\n", true)
	wc := worktree("job-c", "c.go", "package c // not merged\n", true)
	wd := worktree("job-d", "d.go", "package d // uncommitted\n", false)

	repo.Commit("squash of job-a and job-b", map[string]string{"a.go": "package a // landed\n", "b.go": "package b // landed\n"})
	squash := repo.Head()
	repo.Git("update-ref", "refs/remotes/origin/main", squash)

	s := tempStore(t, root)
	var notices strings.Builder
	s.notices = &notices
	exited := func(id string, wt *testkit.Repo, file string) types.Job {
		return types.Job{ID: id, State: types.StateExited, Checkpoint: seedRev, CheckoutRoot: wt.Root(),
			WritePaths: []string{file}, Result: &types.JobResult{ChangedPaths: []string{file}}, Updated: 1}
	}
	plant(t, s, exited("job-a", wa, "a.go"), exited("job-b", wb, "b.go"), exited("job-c", wc, "c.go"), exited("job-d", wd, "d.go"))

	got := states(t, s)
	for id, wt := range map[string]*testkit.Repo{"job-a": wa, "job-b": wb} {
		dir := wt.Root()
		head := wt.Head()
		assert.Equal(t, types.StateNoReturn, got[id].State, id)
		var base, short string
		_, err := fmt.Sscanf(got[id].EndReason, "its work landed on origin/main at %s the 1 changed path(s) at %s", &base, &short)
		require.NoError(t, err, got[id].EndReason)
		assert.True(t, strings.HasPrefix(squash, strings.TrimSuffix(base, ":")), "names the squash commit: %s", got[id].EndReason)
		assert.True(t, strings.HasPrefix(head, short), "names the job's own HEAD: %s", got[id].EndReason)
		assert.True(t, strings.HasSuffix(got[id].EndReason, " in "+dir+" read the same there"), got[id].EndReason)
	}
	assert.Equal(t, types.StateExited, got["job-c"].State, "a commit the base never took")
	assert.Equal(t, types.StateExited, got["job-d"].State, "HEAD reads as the checkpoint, so it says nothing of the change")

	jobs, err := s.Path()
	require.NoError(t, err)
	probes, err := os.ReadFile(filepath.Join(filepath.Dir(jobs), landingProbesFile))
	require.NoError(t, err)
	assert.Contains(t, string(probes), `"job-c"`, "an unproven row waits out the interval before its next probe")
}

// A result naming only the part of the work the base took does not end the job: the
// landing is judged on the checkout's own diff, and the rest of it is still unlanded.
func TestALandedSubsetOfTheWorkLeavesTheJobExited(t *testing.T) {
	repo := testkit.NewRepo(t, map[string]string{"a.go": "package a\n", "b.go": "package b\n"})
	seedRev := repo.Head()
	wt := repo.AddWorktree("job")
	wt.Write("a.go", "package a // landed\n")
	wt.Write("b.go", "package b // not merged\n")
	wt.Git("commit", "-q", "-am", "job")

	repo.Commit("take a.go alone", map[string]string{"a.go": "package a // landed\n"})
	repo.Git("update-ref", "refs/remotes/origin/main", repo.Head())

	s := tempStore(t, repo.Root())
	s.notices = io.Discard
	plant(t, s, types.Job{ID: "job", State: types.StateExited, Checkpoint: seedRev, CheckoutRoot: wt.Root(),
		WritePaths: []string{"a.go", "b.go"}, Result: &types.JobResult{ChangedPaths: []string{"a.go"}}, Updated: 1})

	assert.Equal(t, types.StateExited, states(t, s)["job"].State)
}

// Exit resolves a ref the caller's checkout never recorded in the checkout the job was
// taken in, so an orchestrator can file its worker's result; with no Outputs it refuses.
func TestExitResolvesARefFromTheJobsCheckout(t *testing.T) {
	t.Parallel()

	worker := t.TempDir()
	row := acceptRow()
	row.CheckoutRoot, row.Registered = worker, 1
	result := passingResult()
	here := func(context.Context, string) (types.JobAttempt, error) { return types.JobAttempt{}, nil }

	loc := declared(t)
	plant(t, NewStore(loc), row)
	_, err := Exit(t.Context(), NewStore(loc), row.ID, &result, here)
	require.EqualError(t, err, `job: the result's output ref "a1b2c3d4" names no run this checkout recorded`)

	var asked []string
	loc.Outputs = func(root string) AttemptResolver {
		return func(_ context.Context, ref string) (types.JobAttempt, error) {
			asked = append(asked, root)
			if root != worker {
				return types.JobAttempt{}, nil
			}
			found := passingRun
			found.Ref = ref
			return found, nil
		}
	}
	stored, err := Exit(t.Context(), NewStore(loc), row.ID, &result, here)
	require.NoError(t, err)
	assert.Equal(t, []string{worker}, asked)
	assert.Equal(t, types.StateExited, stored.State)
	require.NotNil(t, stored.Attempt)
	assert.Equal(t, "a1b2c3d4", stored.Attempt.Ref)
}

// goalSymbol is one symbol the goalGraph holds.
type goalSymbol struct {
	id, label, file, signature string
	refs                       int
}

// goalGraph resolves the way the knowledge graph does: Refs takes the first symbol whose
// id is ref or whose label CONTAINS it, so a bare name can land on a longer one.
type goalGraph []goalSymbol

func (g goalGraph) Refs(ref string) (types.KnowledgeRefsOutput, bool) {
	for _, s := range g {
		if s.id == ref || strings.Contains(s.label, ref) {
			return types.KnowledgeRefsOutput{Symbol: s.id, Label: s.label, RefCount: s.refs,
				Defs: []types.KnowledgeRefSite{{File: s.file}}}, true
		}
	}
	return types.KnowledgeRefsOutput{}, false
}

func (g goalGraph) Resolve(input string, _ int) []types.KnowledgeMatch {
	var out []types.KnowledgeMatch
	for _, s := range g {
		if strings.Contains(s.label, input) {
			out = append(out, types.KnowledgeMatch{ID: s.id, Kind: types.KindSymbol, Label: s.label})
		}
	}
	return out
}

// SignatureMatches compares text: the shape comparison is the knowledge graph's, tested there.
func (g goalGraph) SignatureMatches(id, declared string) (string, bool, bool) {
	for _, s := range g {
		if s.id == id && s.signature != "" {
			return s.signature, s.signature == declared, true
		}
	}
	return "", false, false
}

// TestWaitGradesEveryGoal is the audit of every kind and expectation a goal can declare,
// end to end: a real diff since the checkpoint, a real tree, and a graph that resolves
// fuzzily. Each pair passes job wait when it holds and fails it when it does not.
//
// The work sits in the worker's own checkout while wait runs in a clean one, which is
// how an orchestrator waits; grading the waiter's tree failed every writing job.
func TestWaitGradesEveryGoal(t *testing.T) {
	t.Parallel()

	repo := testkit.NewRepo(t, map[string]string{
		"api.go": "package a\n\nfunc Api() {}\n", "keep.go": "package a\n\nfunc Keep() {}\n",
		"legacy.go": "package a\n\nfunc Legacy() {}\n",
	})
	checkpoint := repo.Head()
	workerTree := repo.AddWorktree("worker")
	worker := workerTree.Root()
	workerTree.Write("api.go", "package a\n\nfunc Api() { Keep() }\n")
	require.NoError(t, os.Remove(filepath.Join(worker, "legacy.go")))
	workerTree.Write("new.go", "package a\n")
	changed := []string{"api.go", "legacy.go", "new.go"}

	// Legacy is gone and LegacyAdapter survives, which a fuzzy lookup of Legacy lands on.
	graph := goalGraph{
		{id: "symbol:go a/Api().", label: "Api", file: "api.go", signature: "func Api()"},
		{id: "symbol:go a/Keep().", label: "Keep", file: "keep.go", refs: 2},
		{id: "symbol:go a/Lonely().", label: "Lonely", file: "keep.go"},
		{id: "symbol:go a/LegacyAdapter().", label: "LegacyAdapter", file: "keep.go"},
	}
	paths := func(expect types.GoalExpect, p string) types.Goal {
		return types.Goal{ID: "goal", Kind: types.GoalKindPaths, Expect: expect, Paths: []string{p}}
	}
	symbol := func(expect types.GoalExpect, name string) types.Goal {
		return types.Goal{ID: "goal", Kind: types.GoalKindSymbol, Expect: expect, Symbols: []string{name}}
	}
	signature := func(name, declared string) types.Goal {
		return types.Goal{ID: "goal", Kind: types.GoalKindSymbol, Expect: types.ExpectSignature, Symbols: []string{name}, Signature: declared}
	}
	check := types.Goal{ID: "goal", Check: types.LeaseCheck{Target: "go-test", Project: "."}}
	run := types.JobAttempt{Found: true, Project: ".", Target: "go-test", Spell: "go", TimestampMs: 9_999_999_999_999}
	failed := run
	failed.Failed = true

	for _, tc := range []struct {
		name       string
		goal       types.Goal
		checkpoint string
		checkout   string
		blind      bool
		run        types.JobAttempt
		holds      bool
		why        string
	}{
		{name: "paths changed holds when the diff touches it", goal: paths("", "api.go"), holds: true},
		{name: "paths changed fails when it is untouched", goal: paths("", "keep.go"), why: `nothing matching "keep.go" changed`},
		{name: "paths changed fails when the diff is unreadable", goal: paths("", "api.go"), checkpoint: "0123456789abcdef0123456789abcdef01234567",
			why: "could not read what this job changed"},
		{name: "paths changed fails when the job's checkout is gone and the waiter's is clean", goal: paths("", "api.go"),
			checkout: filepath.Join(t.TempDir(), "removed"), why: `nothing matching "api.go" changed`},
		{name: "paths present holds when it is there", goal: paths(types.ExpectPresent, "new.go"), holds: true},
		{name: "paths present fails when it is gone", goal: paths(types.ExpectPresent, "legacy.go"), why: `nothing matching "legacy.go" is in the tree`},
		{name: "paths absent holds when it is gone", goal: paths(types.ExpectAbsent, "legacy.go"), holds: true},
		{name: "paths absent fails while it is there", goal: paths(types.ExpectAbsent, "keep.go"), why: `"keep.go" is still in the tree`},
		{name: "symbol changed holds when its definition changed", goal: symbol("", "Api"), holds: true},
		{name: "symbol changed fails when its definition is untouched", goal: symbol("", "Keep"), why: "none of that changed"},
		{name: "symbol changed fails when the graph is unreadable", goal: symbol("", "Api"), blind: true,
			why: "could not read the symbol graph"},
		{name: "symbol present holds while it resolves", goal: symbol(types.ExpectPresent, "Keep"), holds: true},
		{name: "symbol present fails on a longer name that survives", goal: symbol(types.ExpectPresent, "Legacy"),
			why: `"Legacy" is defined nowhere`},
		{name: "symbol absent holds once it resolves nowhere", goal: symbol(types.ExpectAbsent, "Legacy"), holds: true},
		{name: "symbol absent fails while it still resolves", goal: symbol(types.ExpectAbsent, "Keep"), why: `"Keep" is still defined in keep.go`},
		{name: "symbol unreferenced holds when nothing references it", goal: symbol(types.ExpectUnreferenced, "Lonely"), holds: true},
		{name: "symbol unreferenced fails while a reference remains", goal: symbol(types.ExpectUnreferenced, "Keep"),
			why: `2 place(s) still reference "Keep"`},
		{name: "symbol signature holds when the indexed declaration matches", goal: signature("Api", "func Api()"), holds: true},
		{name: "symbol signature fails when the declaration differs", goal: signature("Api", "func Api(n int)"),
			why: "\"Api\" is declared `func Api()`, and this goal expects `func Api(n int)`"},
		{name: "symbol signature fails when the index holds none", goal: signature("Keep", "func Keep()"),
			why: `the symbol index holds no signature for "Keep"`},
		{name: "symbol signature fails on a symbol defined nowhere", goal: signature("Legacy", "func Legacy()"),
			why: `"Legacy" is defined nowhere the graph can see`},
		{name: "symbol signature fails when the graph is unreadable", goal: signature("Api", "func Api()"), blind: true,
			why: "could not read the symbol graph"},
		{name: "symbol unreferenced fails once the definition is gone", goal: symbol(types.ExpectUnreferenced, "Legacy"),
			why: "cannot count what still names it"},
		{name: "check passed holds on a passing run", goal: check, run: run, holds: true},
		{name: "check passed fails on a failed run", goal: check, run: failed, why: "did not pass"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			row := types.Job{
				ID: "goal", Criteria: "the goal holds", WritePaths: []string{"."}, State: types.StateRunning, Created: 1,
				Checkpoint: cmp.Or(tc.checkpoint, checkpoint), CheckoutRoot: cmp.Or(tc.checkout, worker),
				Goals: []types.Goal{tc.goal.Resolve()},
			}
			loc := tempLocation(t, t.TempDir())
			plant(t, NewStore(loc), row)
			result := types.JobResult{Schema: types.Schema{Version: types.JobResultSchemaVersion}, Job: "goal", ChangedPaths: changed}
			if tc.run.Found {
				result.GateEvidence = []types.GateEvidence{{GateID: "goal", OutputRef: "ref"}}
			}
			read := GraphSymbols(graph)
			if tc.blind {
				read = nil
			}

			status, err := Wait(t.Context(), NewStore(loc), "goal", &result,
				func(context.Context, string) (types.JobAttempt, error) { return tc.run, nil },
				CheckpointObserver(repo.Root(), read), nil)
			require.NoError(t, err)
			require.Len(t, status.Gates, 1)
			rows, err := NewStore(loc).List()
			require.NoError(t, err)
			if tc.holds {
				assert.True(t, status.Verified, "violations: %v", status.Violations)
				assert.Equal(t, types.StatePass, rows[0].State)
				return
			}
			assert.False(t, status.Gates[0].Verified)
			assert.Contains(t, strings.Join(status.Gates[0].Violations, "\n"), tc.why)
			// Not StateRunning: the sweep ends a job whose checkout is gone before wait grades it.
			assert.NotEqual(t, types.StatePass, rows[0].State, "an unmet goal never passes the job")
		})
	}
}

// pruneRows is a plan with one row for each reason prune ends a job and each it keeps one.
func pruneRows(now int64) []types.Job {
	fresh, old, deadline := now-60, now-int64((3*time.Hour).Seconds()), now-600
	return []types.Job{
		{ID: "exited", State: types.StateExited, Registered: old, Updated: old},
		{ID: "late", State: types.StateDeclared, Deadline: deadline, Updated: fresh},
		{ID: "late-held", State: types.StateRunning, Registered: fresh, Deadline: deadline, Updated: fresh},
		{ID: "late-idle", State: types.StateRunning, Registered: old, Deadline: deadline, Updated: old},
		{ID: "idle", State: types.StateRunning, Registered: old, Updated: old},
		{ID: "busy", State: types.StateRunning, Registered: fresh, Updated: fresh},
		{ID: "plan", State: types.StateDeclared, Updated: old},
		{ID: "plan/w", Parent: "plan", State: types.StateRunning, Registered: fresh, Updated: fresh},
		{ID: "old-plan", State: types.StateDeclared, Updated: old},
		{ID: "old-plan/w", Parent: "old-plan", State: types.StateRunning, Registered: old, Updated: old},
		{ID: "done", State: types.StateExited, Updated: fresh},
		{ID: "done/c", Parent: "done", State: types.StateDeclared, Updated: fresh},
		{ID: "fresh", State: types.StateDeclared, Updated: fresh},
		{ID: "maint", Holder: types.HolderServer, State: types.StateExited, Updated: old},
		{ID: "graded", State: types.StatePass, Updated: old},
	}
}

func TestJobPruneEndsOnlyRowsNobodyIsWorking(t *testing.T) {
	const now = int64(100_000)
	const exited = "exited 3h0m0s ago and nobody collected its result with `magus job wait`"
	const overdue = "overdue: its deadline passed 10m0s ago"
	const taken = "taken, then untouched for 3h0m0s (jobs.stale_after is 2h0m0s)"
	defaults := []Ending{
		{ID: "exited", Reason: exited},
		{ID: "late", Reason: overdue},
		{ID: "late-held", Reason: overdue},
		{ID: "late-idle", Reason: overdue},
		{ID: "done", Reason: "exited 1m0s ago and nobody collected its result with `magus job wait`"},
		{ID: "done/c", Reason: "parent done ended as no_return, and its tree ends with it"},
	}
	tests := []struct {
		name     string
		all      bool
		restated []string
		refused  string
		want     []Ending
	}{
		{
			name:    "the default names a job that would end only with its ancestor, and ends nothing",
			refused: "prune would end 1 live job(s) only because an ancestor it ends does: done/c (declared, session holder, never taken): restate each to end it too: `magus job prune done/c`",
		},
		{name: "restating that job ends it with what is provably not worked", restated: []string{"done/c"}, want: defaults},
		{
			name:     "all also ends a taken job nobody touched, and the root above it",
			all:      true,
			restated: []string{"done/c"},
			want: append(slices.Clone(defaults[:4]),
				Ending{ID: "idle", Reason: taken},
				Ending{ID: "old-plan", Reason: "declared and never taken, untouched for 3h0m0s (jobs.stale_after is 2h0m0s)"},
				Ending{ID: "old-plan/w", Reason: taken},
				defaults[4], defaults[5],
			),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, notices := sweepStore(t, now, 2*time.Hour)
			s.landed = func(context.Context, types.Job) string { return "" }
			rows := pruneRows(now)
			plant(t, s, rows...)

			got, err := s.Prune(t.Context(), PruneOptions{All: tt.all}, tt.restated...)
			if tt.refused != "" {
				require.ErrorContains(t, err, tt.refused)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.want, got)
			assert.Empty(t, notices.String(), "prune reports its endings to its caller, not as sweep notices")

			f, err := s.read()
			require.NoError(t, err)
			for i, row := range f.Jobs {
				at := slices.IndexFunc(tt.want, func(e Ending) bool { return e.ID == row.ID })
				if at < 0 {
					assert.Equal(t, rows[i].State, row.State, "%s is left as it was", row.ID)
					assert.Equal(t, rows[i].Updated, row.Updated, row.ID)
					assert.Empty(t, row.EndReason, row.ID)
					continue
				}
				assert.Equal(t, types.StateNoReturn, row.State, row.ID)
				assert.Equal(t, tt.want[at].Reason, row.EndReason, row.ID)
				assert.Equal(t, now, row.Updated, row.ID)
			}
		})
	}
}

// A paused row outlives the read sweep and `prune --all` while its pause holds; past its
// bound both end it as they would any row.
func TestPausedJobOutlivesTheSweepAndPruneUntilItsPauseLapses(t *testing.T) {
	const now = int64(100_000)
	old := now - int64((3 * time.Hour).Seconds())
	s, _ := sweepStore(t, now, 2*time.Hour)
	s.landed = func(context.Context, types.Job) string { return "" }
	plant(t, s,
		types.Job{ID: "held", State: types.StateDeclared, Updated: old, Paused: old, PauseReason: "busy", PausedUntil: now + 60},
		types.Job{ID: "lapsed", State: types.StateDeclared, Updated: old, Paused: old, PauseReason: "busy", PausedUntil: now - 60},
		types.Job{ID: "taken", State: types.StateRunning, Registered: old, Updated: old, Paused: old, PauseReason: "busy"},
		types.Job{ID: "taken-lapsed", State: types.StateRunning, Registered: old, Updated: old, Paused: old, PauseReason: "busy", PausedUntil: now},
	)

	rows := states(t, s)
	assert.Equal(t, types.StateDeclared, rows["held"].State, "the sweep leaves a paused row alone")
	assert.Equal(t, types.StateNoReturn, rows["lapsed"].State)
	assert.Equal(t, "declared and never taken, untouched for 3h0m0s (jobs.stale_after is 2h0m0s)", rows["lapsed"].EndReason)

	got, err := s.Prune(t.Context(), PruneOptions{All: true})
	require.NoError(t, err)
	assert.Equal(t, []Ending{{ID: "taken-lapsed", Reason: "taken, then untouched for 3h0m0s (jobs.stale_after is 2h0m0s)"}}, got,
		"--all skips a paused row until its pause lapses")
}

func TestJobPruneKeepsAJobQueuedOnALiveDependency(t *testing.T) {
	const now = int64(100_000)
	old := now - int64((3 * time.Hour).Seconds())
	s, _ := sweepStore(t, now, 2*time.Hour)
	s.landed = func(context.Context, types.Job) string { return "" }
	plant(t, s,
		types.Job{ID: "first", State: types.StateRunning, Registered: now - 60, Updated: now - 60},
		types.Job{ID: "queued", State: types.StateDeclared, DependsOn: []string{"first"}, Updated: old},
	)

	got, err := s.Prune(t.Context(), PruneOptions{All: true})
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestJobPruneDryRunEndsNothing(t *testing.T) {
	const now = int64(100_000)
	s, _ := sweepStore(t, now, 2*time.Hour)
	s.landed = func(context.Context, types.Job) string { return "" }
	plant(t, s, pruneRows(now)...)
	path, err := s.Path()
	require.NoError(t, err)
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	got, err := s.Prune(t.Context(), PruneOptions{DryRun: true})
	require.NoError(t, err)
	assert.Len(t, got, 6, "a dry run lists what a prune would end")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}

// A dry-run store's read reports the rows its sweep would end, and writes nothing.
func TestDryRunListEndsRowsOnlyInWhatItReturns(t *testing.T) {
	const now = int64(100_000)
	s, notices := sweepStore(t, now, 2*time.Hour)
	s.landed = func(context.Context, types.Job) string { return "" }
	plant(t, s, types.Job{ID: "late", State: types.StateRunning, Registered: now - 60, Deadline: now - 600, Updated: now - 60})
	path, err := s.Path()
	require.NoError(t, err)
	dir := func() map[string]string {
		entries, err := os.ReadDir(filepath.Dir(path))
		require.NoError(t, err)
		out := map[string]string{}
		for _, e := range entries {
			body, err := os.ReadFile(filepath.Join(filepath.Dir(path), e.Name()))
			require.NoError(t, err)
			out[e.Name()] = string(body)
		}
		return out
	}
	before := dir()

	s.dryRun = true
	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, types.StateNoReturn, rows[0].State)
	assert.Contains(t, rows[0].EndReason, overdueReasonPrefix)
	assert.Equal(t, before, dir())
	assert.Empty(t, notices.String())
}

func TestJobPruneRefusesABoundWorker(t *testing.T) {
	loc := declared(t, types.Job{ID: "w", State: types.StateExited})
	_, err := boundStore(loc, "w").Prune(t.Context(), PruneOptions{})
	var refused *RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, types.StateExited, jobStates(t, loc)["w"])
}

// waiterEvidenceJob is a job its holder exited with the runs in holderRuns, in a checkout
// whose changed files the waiter's checkout (loc.Root) holds the same copy of.
type waiterEvidenceJob struct {
	loc            Location
	holder, waiter string
	row            types.Job
	result         types.JobResult
}

var (
	failedRun   = types.JobAttempt{Found: true, Ref: "holder", Project: ".", Target: "go-test", Spell: "go", Failed: true, TimestampMs: 9_999_999_999_999}
	passedHere  = types.JobAttempt{Found: true, Ref: "here", Project: ".", Target: "go-test", Spell: "go", TimestampMs: 9_999_999_999_999}
	otherChecks = types.JobAttempt{Found: true, Ref: "here", Project: ".", Target: "go-vet", Spell: "go", TimestampMs: 9_999_999_999_999}
)

func exitedWithRuns(t *testing.T, row types.Job, result types.JobResult, holderRuns map[string]types.JobAttempt) waiterEvidenceJob {
	t.Helper()
	return exitedWithRunsIn(t, t.TempDir(), row, result, holderRuns)
}

// exitedWithRunsIn is exitedWithRuns with waiter as the waiter's checkout.
func exitedWithRunsIn(t *testing.T, waiter string, row types.Job, result types.JobResult, holderRuns map[string]types.JobAttempt) waiterEvidenceJob {
	t.Helper()
	holder := t.TempDir()
	for _, changed := range result.ChangedPaths {
		for _, dir := range []string{holder, waiter} {
			p := filepath.Join(dir, filepath.FromSlash(changed))
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			require.NoError(t, os.WriteFile(p, []byte("package a\n"), 0o644))
		}
	}
	row.CheckoutRoot = holder
	loc := tempLocation(t, waiter)
	seed(t, NewStore(loc), row)
	_, err := Exit(t.Context(), boundStore(loc, row.ID), row.ID, &result, func(_ context.Context, ref string) (types.JobAttempt, error) {
		return holderRuns[ref], nil
	})
	require.NoError(t, err)
	return waiterEvidenceJob{loc: loc, holder: holder, waiter: waiter, row: row, result: result}
}

// waitWithEvidence waits as the waiter, resolving refs in the waiter's own output store,
// against a diff that shows exactly what the holder claimed.
func (j waiterEvidenceJob) waitWithEvidence(t *testing.T, here map[string]types.JobAttempt, refs map[string]string) (types.JobStatus, error) {
	t.Helper()
	return j.waitObserving(t, here, refs, observeClaim(j.result))
}

func (j waiterEvidenceJob) waitObserving(t *testing.T, here map[string]types.JobAttempt, refs map[string]string, observe Observer) (types.JobStatus, error) {
	t.Helper()
	return Wait(t.Context(), NewStore(j.loc), j.row.ID, nil, func(_ context.Context, ref string) (types.JobAttempt, error) {
		return here[ref], nil
	}, observe, refs)
}

func (j waiterEvidenceJob) stored(t *testing.T) types.Job {
	t.Helper()
	rows, err := NewStore(j.loc).List()
	require.NoError(t, err)
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == j.row.ID })
	require.GreaterOrEqual(t, i, 0)
	return rows[i]
}

// requireUnmoved asserts the wait recorded nothing: the job stays exited, with no waiter
// evidence beside it.
func (j waiterEvidenceJob) requireUnmoved(t *testing.T) {
	t.Helper()
	j.requireState(t, types.StateExited)
}

// requireReworked asserts the wait rejected the job back to rework, with no waiter evidence
// beside it.
func (j waiterEvidenceJob) requireReworked(t *testing.T) {
	t.Helper()
	j.requireState(t, types.StateRunning)
}

func (j waiterEvidenceJob) requireState(t *testing.T, state types.JobState) {
	t.Helper()
	row := j.stored(t)
	assert.Equal(t, state, row.State)
	assert.Nil(t, row.WaiterEvidence)
}

func failedPrimary(t *testing.T) waiterEvidenceJob {
	t.Helper()
	return exitedWithRuns(t, acceptRow(), passingResult(), map[string]types.JobAttempt{"a1b2c3d4": failedRun})
}

func TestWaitTakesWaiterEvidenceForAGoalTheHolderFailed(t *testing.T) {
	t.Parallel()

	j := failedPrimary(t)
	status, err := j.waitWithEvidence(t, map[string]types.JobAttempt{"here": passedHere}, map[string]string{types.PrimaryGoalID: "here"})
	require.NoError(t, err)
	require.True(t, status.Verified, status.Violations)

	row := j.stored(t)
	assert.Equal(t, types.StatePass, row.State)
	require.NotNil(t, row.Attempt)
	assert.True(t, row.Attempt.Failed, "the holder's own attempt stays beside the waiter's")
	require.NotNil(t, row.Result)
	assert.Equal(t, "a1b2c3d4", row.Result.Validation.OutputRef)
	require.NotNil(t, row.WaiterEvidence)
	assert.Equal(t, j.waiter, row.WaiterEvidence.Checkout)
	assert.NotZero(t, row.WaiterEvidence.At)
	assert.True(t, row.WaiterEvidence.Verified)
	assert.Equal(t, []types.GateStatus{{ID: types.PrimaryGoalID, Verified: true, OutputRef: "here"}}, row.WaiterEvidence.Gates)
}

func TestWaitTakesWaiterEvidenceForOneNamedGoalOnly(t *testing.T) {
	t.Parallel()

	attempts := map[string]types.JobAttempt{}
	for _, snapshot := range goalAttempts() {
		attempts[snapshot.Attempt.Ref] = snapshot.Attempt
	}
	attempts["unit-ref"] = failedRun
	docs := attempts["docs-ref"]
	docs.TimestampMs = 9_999_999_999_999
	attempts["docs-ref"] = docs
	j := exitedWithRuns(t, goalRow(), goalResult(), attempts)

	status, err := j.waitWithEvidence(t, map[string]types.JobAttempt{"here": passedHere}, map[string]string{"unit": "here"})
	require.NoError(t, err)
	require.True(t, status.Verified, status.Violations)

	row := j.stored(t)
	require.NotNil(t, row.WaiterEvidence)
	assert.Equal(t, []types.GateStatus{{ID: "unit", Verified: true, OutputRef: "here"}}, row.WaiterEvidence.Gates, "docs passed on the holder's evidence and took none")
	assert.Equal(t, "unit-ref", row.Result.GateEvidence[0].OutputRef)
	assert.True(t, row.GateAttempts[0].Attempt.Failed)
}

func TestWaitEvidenceResolvesOnlyInTheWaitersCheckout(t *testing.T) {
	t.Parallel()

	j := failedPrimary(t)
	j.loc.Outputs = func(root string) AttemptResolver {
		return func(context.Context, string) (types.JobAttempt, error) { return passedHere, nil }
	}
	_, err := j.waitWithEvidence(t, nil, map[string]string{types.PrimaryGoalID: "here"})
	require.ErrorContains(t, err, `output ref "here" names no run this checkout recorded`)
	j.requireUnmoved(t)
}

func TestWaitEvidenceStillHasToVerify(t *testing.T) {
	t.Parallel()

	failedHere := passedHere
	failedHere.Failed = true
	for name, run := range map[string]types.JobAttempt{
		"a failed run":           failedHere,
		"a run of another check": otherChecks,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			j := failedPrimary(t)
			status, err := j.waitWithEvidence(t, map[string]types.JobAttempt{"here": run}, map[string]string{types.PrimaryGoalID: "here"})
			require.NoError(t, err)
			assert.False(t, status.Verified)
			j.requireReworked(t)
		})
	}
}

func TestWaitEvidenceRefusesAGoalItCannotTake(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		job  func(t *testing.T) waiterEvidenceJob
		refs map[string]string
		want string
	}{
		"a goal the job does not hold": {
			job: failedPrimary, refs: map[string]string{"nope": "here"}, want: `holds no goal "nope"`,
		},
		"a goal that is not a check": {
			job: func(t *testing.T) waiterEvidenceJob {
				row := acceptRow()
				row.Goals = []types.Goal{{ID: "api", Kind: types.GoalKindPaths, Paths: []string{"internal/ledger"}}}
				return exitedWithRuns(t, row, passingResult(), map[string]types.JobAttempt{"a1b2c3d4": failedRun})
			},
			refs: map[string]string{"api": "here"}, want: `goal "api" is a paths goal`,
		},
		"a goal the holder's evidence already passes": {
			job: func(t *testing.T) waiterEvidenceJob {
				ok := passingRun
				ok.TimestampMs = 9_999_999_999_999
				return exitedWithRuns(t, acceptRow(), passingResult(), map[string]types.JobAttempt{"a1b2c3d4": ok})
			},
			refs: map[string]string{types.PrimaryGoalID: "here"}, want: "already passes on the holder's own evidence",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			j := tc.job(t)
			_, err := j.waitWithEvidence(t, map[string]types.JobAttempt{"here": passedHere}, tc.refs)
			require.ErrorContains(t, err, tc.want)
			j.requireUnmoved(t)
		})
	}
}

func TestWaitEvidenceRefusesChangedPathsThatDiffer(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		alter func(t *testing.T, j waiterEvidenceJob)
		want  string
	}{
		"a changed path that differs": {
			alter: func(t *testing.T, j waiterEvidenceJob) {
				require.NoError(t, os.WriteFile(filepath.Join(j.waiter, "cmd", "magus", "ledger.go"), []byte("package b\n"), 0o644))
			},
			want: "1 differ between",
		},
		"a changed path missing from the waiter's checkout": {
			alter: func(t *testing.T, j waiterEvidenceJob) {
				require.NoError(t, os.Remove(filepath.Join(j.waiter, "cmd", "magus", "ledger.go")))
			},
			want: "cmd/magus/ledger.go",
		},
		"a holder checkout that is gone": {
			alter: func(t *testing.T, j waiterEvidenceJob) { require.NoError(t, os.RemoveAll(j.holder)) },
			want:  "is not there to compare against",
		},
		"a symbolic link to a different target": {
			alter: func(t *testing.T, j waiterEvidenceJob) {
				for dir, target := range map[string]string{j.holder: "a", j.waiter: "b"} {
					p := filepath.Join(dir, "cmd", "magus", "ledger.go")
					require.NoError(t, os.Remove(p))
					require.NoError(t, os.Symlink(target, p))
				}
			},
			want: "1 differ between",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			j := failedPrimary(t)
			tc.alter(t, j)
			_, err := j.waitWithEvidence(t, map[string]types.JobAttempt{"here": passedHere}, map[string]string{types.PrimaryGoalID: "here"})
			require.ErrorContains(t, err, tc.want)
			j.requireUnmoved(t)
		})
	}
}

// The paths compared are the ones the job's diff shows, not only the ones its holder
// listed: a file the holder left out of its result still has to match.
func TestWaitEvidenceComparesTheObservedDiff(t *testing.T) {
	t.Parallel()

	j := failedPrimary(t)
	for dir, body := range map[string]string{j.holder: "package a\n", j.waiter: "package b\n"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "unlisted.go"), []byte(body), 0o644))
	}
	observed := claimed(j.result)
	observed.Changed = append(slices.Clone(observed.Changed), "unlisted.go")

	_, err := j.waitObserving(t, map[string]types.JobAttempt{"here": passedHere}, map[string]string{types.PrimaryGoalID: "here"},
		func(context.Context, types.Job) (Observed, error) { return observed, nil })

	require.ErrorContains(t, err, "unlisted.go")
	j.requireUnmoved(t)

	_, err = j.waitObserving(t, map[string]types.JobAttempt{"here": passedHere}, map[string]string{types.PrimaryGoalID: "here"},
		func(context.Context, types.Job) (Observed, error) { return Observed{}, nil })
	require.ErrorContains(t, err, "the job's diff could not be read", "an unread diff names nothing to compare")
	j.requireUnmoved(t)
}

// A run recorded before the job's files reached the waiter's checkout graded other files,
// even when the bytes match now.
func TestWaitEvidenceRefusesAFileChangedAfterTheRun(t *testing.T) {
	t.Parallel()

	j := failedPrimary(t)
	before := passedHere
	before.TimestampMs = time.Now().Add(-time.Hour).UnixMilli()

	_, err := j.waitWithEvidence(t, map[string]types.JobAttempt{"here": before}, map[string]string{types.PrimaryGoalID: "here"})

	require.ErrorContains(t, err, "did not change after it")
	assert.Contains(t, err.Error(), "cmd/magus/ledger.go")
	j.requireUnmoved(t)
}

// The holder's own passing run grades the files it read; a changed path written after it
// means the pass is for other files, and the job stays open.
func TestWaitRefusesAHolderRunTheJobWroteAfter(t *testing.T) {
	t.Parallel()

	// Dated within the range a file time holds, which passedHere's is not.
	run := passedHere
	run.TimestampMs = time.Now().Add(time.Minute).UnixMilli()
	j := exitedWithRuns(t, acceptRow(), passingResult(), map[string]types.JobAttempt{"a1b2c3d4": run})
	graded, err := GradeGates(t.Context(), NewStore(j.loc), j.row.ID, observeClaim(j.result))
	require.NoError(t, err)
	require.True(t, graded.Verified, "the control: a run after every write passes")

	later := time.UnixMilli(run.TimestampMs).Add(time.Hour)
	changed := j.result.ChangedPaths[0]
	require.NoError(t, os.Chtimes(filepath.Join(j.holder, filepath.FromSlash(changed)), later, later))

	status, err := Wait(t.Context(), NewStore(j.loc), j.row.ID, nil, nil, observeClaim(j.result), nil)
	require.NoError(t, err)
	assert.False(t, status.Verified)
	require.Len(t, status.Gates, 1)
	require.Len(t, status.Gates[0].Violations, 1, status.Gates[0].Violations)
	assert.Contains(t, status.Gates[0].Violations[0], "predates the job's last write")
	assert.Contains(t, status.Gates[0].Violations[0], changed)
	j.requireReworked(t)
}

func TestWaitEvidenceCountsARunAtTheCurrentKey(t *testing.T) {
	t.Parallel()

	run := passedHere
	run.TimestampMs = time.Now().Add(time.Minute).UnixMilli()
	j := exitedWithRuns(t, acceptRow(), passingResult(), map[string]types.JobAttempt{"a1b2c3d4": run})
	later := time.UnixMilli(run.TimestampMs).Add(time.Hour)
	changed := j.result.ChangedPaths[0]
	require.NoError(t, os.Chtimes(filepath.Join(j.holder, filepath.FromSlash(changed)), later, later))

	var asked []string
	current := func(_ context.Context, tree string, run types.JobAttempt) bool {
		asked = append(asked, tree+" "+run.Ref)
		return true
	}
	claimed := observeClaim(j.result)
	observe := func(ctx context.Context, row types.Job) (Observed, error) {
		seen, err := claimed(ctx, row)
		seen.CurrentRun = current
		return seen, err
	}
	status, err := Wait(t.Context(), NewStore(j.loc), j.row.ID, nil, nil, observe, nil)

	require.NoError(t, err)
	assert.True(t, status.Verified, "a run whose key is the one its check mints now read the files as they stand: %v", status.Gates)
	assert.NotEmpty(t, asked)
}

func TestSameContentComparesLinksWithoutFollowingThem(t *testing.T) {
	t.Parallel()

	left, right := t.TempDir(), t.TempDir()
	for _, dir := range []string{left, right} {
		require.NoError(t, os.Symlink(".", filepath.Join(dir, "loop")))
	}
	assert.True(t, sameContent(t.Context(), left, right), "a link to its own directory is compared by target, never walked")

	require.NoError(t, os.Remove(filepath.Join(right, "loop")))
	require.NoError(t, os.Symlink("..", filepath.Join(right, "loop")))
	assert.False(t, sameContent(t.Context(), left, right))
}

// Two checkouts of one submodule commit hold different .git files, each naming its own
// checkout's metadata; the commit checked out is what they share.
func TestSameContentComparesASubmoduleByItsHead(t *testing.T) {
	t.Parallel()

	origin := testkit.NewRepo(t, map[string]string{"x.go": "package x\n"})
	first := origin.Head()
	second := origin.Commit("same tree, another commit", nil)
	checkout := func(rev string) string {
		dir := filepath.Join(t.TempDir(), "sub")
		origin.Git("worktree", "add", "-q", "--detach", dir, rev)
		return dir
	}
	left, right, moved := checkout(first), checkout(first), checkout(second)
	l, err := os.ReadFile(filepath.Join(left, ".git"))
	require.NoError(t, err)
	r, err := os.ReadFile(filepath.Join(right, ".git"))
	require.NoError(t, err)
	require.NotEqual(t, string(l), string(r), "the control: each checkout's .git names its own metadata")

	assert.True(t, sameContent(t.Context(), left, right), "the same HEAD and the same files")
	assert.False(t, sameContent(t.Context(), left, moved), "the same files at another HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(right, "x.go"), []byte("package y\n"), 0o644))
	assert.False(t, sameContent(t.Context(), left, right), "the same HEAD with other files")
}

// ageTree dates root and everything beneath it at.
func ageTree(t *testing.T, root string, at time.Time) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(p, at, at)
	}))
}

// A run that started before the job's files changed graded other files, however late it
// ended: its record dates its end. The waiter's tree is dated two hours back and the run
// started one hour back, so only what happens after it reads as later.
func TestWaitEvidenceDatesARunByItsStart(t *testing.T) {
	t.Parallel()

	spanning := passedHere
	spanning.TimestampMs = time.Now().Add(time.Hour).UnixMilli()
	spanning.DurationMs = (2 * time.Hour).Milliseconds()
	tests := map[string]struct {
		extra string
		alter func(t *testing.T, j waiterEvidenceJob)
		want  string
	}{
		"nothing changed since it started": {},
		"a file written while it ran": {
			alter: func(t *testing.T, j waiterEvidenceJob) {
				for _, dir := range []string{j.holder, j.waiter} {
					require.NoError(t, os.WriteFile(filepath.Join(dir, "cmd", "magus", "ledger.go"), []byte("package b\n"), 0o644))
				}
			},
			want: "cmd/magus/ledger.go",
		},
		"a top-level file deleted after it started": {
			extra: "tools.go",
			alter: func(t *testing.T, j waiterEvidenceJob) {
				require.NoError(t, os.Remove(filepath.Join(j.waiter, "tools.go")))
			},
			want: "tools.go",
		},
		"a top-level directory deleted after it started": {
			extra: "legacy/old.go",
			alter: func(t *testing.T, j waiterEvidenceJob) {
				require.NoError(t, os.RemoveAll(filepath.Join(j.waiter, "legacy")))
			},
			want: "legacy/old.go",
		},
		"a file renamed into place with its old time": {
			alter: func(t *testing.T, j waiterEvidenceJob) {
				staged := filepath.Join(j.waiter, "staged.go")
				for _, p := range []string{staged, filepath.Join(j.holder, "cmd", "magus", "ledger.go")} {
					require.NoError(t, os.WriteFile(p, []byte("package b\n"), 0o644))
				}
				old := time.Now().Add(-2 * time.Hour)
				require.NoError(t, os.Chtimes(staged, old, old))
				require.NoError(t, os.Rename(staged, filepath.Join(j.waiter, "cmd", "magus", "ledger.go")))
				require.NoError(t, os.Chtimes(j.waiter, old, old))
			},
			want: "cmd/magus/ledger.go",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			j := failedPrimary(t)
			observed := claimed(j.result)
			if tc.extra != "" {
				p := filepath.Join(j.waiter, filepath.FromSlash(tc.extra))
				require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
				require.NoError(t, os.WriteFile(p, []byte("package a\n"), 0o644))
				observed.Changed = append(slices.Clone(observed.Changed), tc.extra)
			}
			ageTree(t, j.waiter, time.Now().Add(-2*time.Hour))
			if tc.alter != nil {
				tc.alter(t, j)
			}

			status, err := j.waitObserving(t, map[string]types.JobAttempt{"here": spanning}, map[string]string{types.PrimaryGoalID: "here"},
				func(context.Context, types.Job) (Observed, error) { return observed, nil })

			if tc.want == "" {
				require.NoError(t, err)
				assert.True(t, status.Verified, status.Violations)
				return
			}
			require.ErrorContains(t, err, "did not change after it started")
			assert.Contains(t, err.Error(), tc.want)
			j.requireUnmoved(t)
		})
	}
}

// A run of a clean tree read its recorded revision, so a path has to read now as it does
// there, whenever the run ended; file times are not consulted.
func TestWaitEvidenceHoldsACleanRunToItsRevision(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		alter func(t *testing.T, j waiterEvidenceJob)
		want  string
	}{
		"nothing changed since the revision": {},
		"a file changed after the revision": {
			alter: func(t *testing.T, j waiterEvidenceJob) {
				for _, dir := range []string{j.holder, j.waiter} {
					require.NoError(t, os.WriteFile(filepath.Join(dir, "cmd", "magus", "ledger.go"), []byte("package b\n"), 0o644))
				}
			},
			want: "cmd/magus/ledger.go",
		},
		"a top-level file deleted after the revision": {
			alter: func(t *testing.T, j waiterEvidenceJob) {
				for _, dir := range []string{j.holder, j.waiter} {
					require.NoError(t, os.Remove(filepath.Join(dir, "tools.go")))
				}
			},
			want: "tools.go",
		},
		"a file the revision lacks": {
			alter: func(t *testing.T, j waiterEvidenceJob) {
				for _, dir := range []string{j.holder, j.waiter} {
					require.NoError(t, os.WriteFile(filepath.Join(dir, "new.go"), []byte("package a\n"), 0o644))
				}
			},
			want: "new.go",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			repo := testkit.NewRepo(t, map[string]string{"tools.go": "package a\n"})
			j := exitedWithRunsIn(t, repo.Root(), acceptRow(), passingResult(), map[string]types.JobAttempt{"a1b2c3d4": failedRun})
			require.NoError(t, os.WriteFile(filepath.Join(j.holder, "tools.go"), []byte("package a\n"), 0o644))
			clean := passedHere
			clean.Revision = repo.Commit("the work", nil)
			observed := claimed(j.result)
			observed.Changed = append(slices.Clone(observed.Changed), "tools.go", "new.go")
			if tc.alter != nil {
				tc.alter(t, j)
			}

			status, err := j.waitObserving(t, map[string]types.JobAttempt{"here": clean}, map[string]string{types.PrimaryGoalID: "here"},
				func(context.Context, types.Job) (Observed, error) { return observed, nil })

			if tc.want == "" {
				require.NoError(t, err)
				assert.True(t, status.Verified, status.Violations)
				return
			}
			require.ErrorContains(t, err, "the clean revision run here read")
			assert.Contains(t, err.Error(), tc.want)
			j.requireUnmoved(t)
		})
	}
}

// A ref may name a step, which its next run answers for; the job records the execution
// the ref resolved to.
func TestWaitEvidenceRecordsTheAttemptTheRefResolvedTo(t *testing.T) {
	t.Parallel()

	j := failedPrimary(t)
	run := passedHere
	run.AttemptID = "1a2b3c4d5e6f"
	status, err := j.waitWithEvidence(t, map[string]types.JobAttempt{"here": run}, map[string]string{types.PrimaryGoalID: "here"})
	require.NoError(t, err)
	require.True(t, status.Verified, status.Violations)

	row := j.stored(t)
	require.NotNil(t, row.WaiterEvidence)
	assert.Equal(t, []types.GateStatus{{ID: types.PrimaryGoalID, Verified: true, OutputRef: "1a2b3c4d5e6f"}}, row.WaiterEvidence.Gates)
}

func TestWaitEvidenceNeedsTheFiledResult(t *testing.T) {
	t.Parallel()

	j := failedPrimary(t)
	supplied := j.result
	_, err := Wait(t.Context(), NewStore(j.loc), j.row.ID, &supplied, func(context.Context, string) (types.JobAttempt, error) {
		return passedHere, nil
	}, observeClaim(supplied), map[string]string{types.PrimaryGoalID: "here"})
	require.ErrorContains(t, err, "starts from the result filed with the job")
}

func TestWaitEvidenceFollowsWhoMayVerify(t *testing.T) {
	t.Parallel()

	j := failedPrimary(t)
	_, err := Wait(t.Context(), boundStore(j.loc, j.row.ID), j.row.ID, nil, func(context.Context, string) (types.JobAttempt, error) {
		return passedHere, nil
	}, observeClaim(j.result), map[string]string{types.PrimaryGoalID: "here"})
	require.ErrorContains(t, err, "a holder does not verify its own work")
}

func TestStaleRunsLeaveOutGeneratedPaths(t *testing.T) {
	t.Parallel()

	ws := Workspace{Projects: []*types.Project{{Path: ".", TargetOutputs: map[string][]types.OutputRef{
		"index-generate": {{Glob: "MAGUS.md"}},
		"gen":            {{Glob: "gen/*.go", Except: []string{"gen/*_test.go"}}},
	}}}}
	s := NewStore(tempLocation(t, t.TempDir())).InWorkspace(ws)
	assert.True(t, s.generated("MAGUS.md"))
	assert.True(t, s.generated("gen/a.go"))
	assert.True(t, s.generated(".gitattributes"), "a file magus maintains itself is regenerated beside the change too")
	assert.False(t, s.generated("gen/a_test.go"), "an output's exception is written by hand")
	assert.False(t, s.generated("internal/job/verify.go"))
	assert.False(t, tempStore(t, t.TempDir()).generated("MAGUS.md"), "a store opened without a workspace knows no declared output")
}
