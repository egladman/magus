package job

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// observeClaim is an observer whose diff shows exactly what rep claims.
func observeClaim(rep types.JobResult) Observer {
	return func(context.Context, types.Job) (Observed, error) { return claimed(rep), nil }
}

// TestExitFilesEvidenceForAWaitInAnotherCheckout keeps the lifecycle at the Store
// boundary. The resolver is the worker checkout's output cache; the waiter deliberately
// has no resolver, proving Exit copied the portable attempt onto the repository-wide row.
func TestExitFilesEvidenceForAWaitInAnotherCheckout(t *testing.T) {
	t.Parallel()

	loc := declared(t, acceptRow())
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
	assert.Equal(t, attempt, *exited.Attempt)

	status, err := Wait(t.Context(), NewStore(loc), result.Job, nil, nil, observeClaim(result))
	require.NoError(t, err)
	// The primary check is LISTED, like any other gate: it is one, and reporting it only
	// when other gates existed is what let `describe job` say a check-only job had none.
	assert.Equal(t, types.JobStatus{
		Job:      result.Job,
		Verified: true,
		Risks:    []string{},
		Command:  result.Validation.Command,
		Gates:    []types.GateStatus{{ID: types.PrimaryGoalID, OutputRef: result.Validation.OutputRef, Verified: true}},
	}, status)

	rows, err := NewStore(loc).List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, types.StatePass, rows[0].State)
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

	status, err := Wait(t.Context(), NewStore(loc), row.ID, nil, nil, observeClaim(result))
	require.NoError(t, err)
	assert.True(t, status.Verified, status.Violations)
	require.Len(t, status.Gates, 2)
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
	}, nil)
	require.NoError(t, err)
	assert.False(t, status.Verified)
	assert.Contains(t, status.Violations, "job harness/ledger-accept is not read-only and the result claims no changed paths at all")

	rows, err := NewStore(loc).List()
	require.NoError(t, err)
	assert.Equal(t, types.StateRunning, rows[0].State, "a rejected result is evidence to repair, not a terminal verdict")
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
	}, observeClaim(result))
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
	s := tmpStore(t, t.TempDir())
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
			name:       "a declared job waiting on a live dependency is queued, not stale",
			staleAfter: 2 * time.Hour,
			rows: []types.Job{
				{ID: "first", State: types.StateRunning, Registered: fresh, Updated: fresh},
				{ID: "handed-back", State: types.StateExited, Registered: fresh, Updated: old},
				{ID: "queued", State: types.StateDeclared, DependsOn: []string{"first"}, Updated: old},
				{ID: "queued-on-exited", State: types.StateDeclared, DependsOn: []string{"handed-back"}, Updated: old},
			},
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
	checkout := t.TempDir()
	s := tmpStore(t, checkout)
	var notices strings.Builder
	s.notices = &notices
	seed(t, s, types.Job{ID: "w", State: types.StateDeclared, Checkpoint: "abc"})

	taken, err := s.Exec(t.Context(), "w", "abc")
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
		ReportedBase: "abc",
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
	s := tmpStore(t, root)
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
			s := tmpStore(t, root)
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
	s := tmpStore(t, root)
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

// git runs one git command in dir with the box's own config shut out, and returns its
// trimmed output.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// Two jobs whose branches were squashed into ONE base commit both end, judged by content
// alone: no ancestry joins either branch to the squash. A job whose commit is not on the
// base, and one whose change was never committed, both stay exited.
func TestLandedJobsEndWhenSquashedIntoOneBaseCommit(t *testing.T) {
	root := gitRepo(t, map[string]string{"a.go": "package a\n", "b.go": "package b\n", "c.go": "package c\n", "d.go": "package d\n"})
	seedRev := commitRepo(t, root)
	worktree := func(name, file, body string, commit bool) string {
		dir := filepath.Join(t.TempDir(), name)
		git(t, root, "worktree", "add", "-q", "-b", name, dir, seedRev)
		require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644))
		if commit {
			git(t, dir, "commit", "-q", "-am", name)
		}
		return dir
	}
	wa := worktree("job-a", "a.go", "package a // landed\n", true)
	wb := worktree("job-b", "b.go", "package b // landed\n", true)
	wc := worktree("job-c", "c.go", "package c // not merged\n", true)
	wd := worktree("job-d", "d.go", "package d // uncommitted\n", false)

	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("package a // landed\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.go"), []byte("package b // landed\n"), 0o644))
	git(t, root, "commit", "-q", "-am", "squash of job-a and job-b")
	squash := git(t, root, "rev-parse", "HEAD")
	git(t, root, "update-ref", "refs/remotes/origin/main", squash)

	s := tmpStore(t, root)
	var notices strings.Builder
	s.notices = &notices
	exited := func(id, dir, file string) types.Job {
		return types.Job{ID: id, State: types.StateExited, Checkpoint: seedRev, CheckoutRoot: dir,
			WritePaths: []string{file}, Result: &types.JobResult{ChangedPaths: []string{file}}, Updated: 1}
	}
	plant(t, s, exited("job-a", wa, "a.go"), exited("job-b", wb, "b.go"), exited("job-c", wc, "c.go"), exited("job-d", wd, "d.go"))

	got := states(t, s)
	for id, dir := range map[string]string{"job-a": wa, "job-b": wb} {
		head := git(t, dir, "rev-parse", "HEAD")
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
	id, label, file string
	refs            int
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

// TestWaitGradesEveryGoal is the audit of every kind and expectation a goal can declare,
// end to end: a real diff since the checkpoint, a real tree, and a graph that resolves
// fuzzily. Each pair passes job wait when it holds and fails it when it does not.
//
// The work sits in the worker's own checkout while wait runs in a clean one, which is
// how an orchestrator waits; grading the waiter's tree failed every writing job.
func TestWaitGradesEveryGoal(t *testing.T) {
	t.Parallel()

	repo := gitRepo(t, map[string]string{
		"api.go": "package a\n\nfunc Api() {}\n", "keep.go": "package a\n\nfunc Keep() {}\n",
		"legacy.go": "package a\n\nfunc Legacy() {}\n",
	})
	checkpoint := commitRepo(t, repo)
	worker := filepath.Join(t.TempDir(), "worker")
	git(t, repo, "worktree", "add", "-q", "-b", "worker", worker, checkpoint)
	require.NoError(t, os.WriteFile(filepath.Join(worker, "api.go"), []byte("package a\n\nfunc Api() { Keep() }\n"), 0o644))
	require.NoError(t, os.Remove(filepath.Join(worker, "legacy.go")))
	require.NoError(t, os.WriteFile(filepath.Join(worker, "new.go"), []byte("package a\n"), 0o644))
	changed := []string{"api.go", "legacy.go", "new.go"}

	// Legacy is gone and LegacyAdapter survives, which a fuzzy lookup of Legacy lands on.
	graph := goalGraph{
		{id: "symbol:go a/Api().", label: "Api", file: "api.go"},
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
			loc := tmpLoc(t, t.TempDir())
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
				CheckpointObserver(repo, read))
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
		{ID: "late-idle", Reason: overdue},
		{ID: "done", Reason: "exited 1m0s ago and nobody collected its result with `magus job wait`"},
		{ID: "done/c", Reason: "parent done ended as no_return, and its tree ends with it"},
	}
	tests := []struct {
		name string
		all  bool
		want []Ending
	}{
		{name: "the default ends what is provably not worked", want: defaults},
		{
			name: "all also ends a taken job nobody touched, and the root above it",
			all:  true,
			want: append(slices.Clone(defaults[:3]),
				Ending{ID: "idle", Reason: taken},
				Ending{ID: "old-plan", Reason: "declared and never taken, untouched for 3h0m0s (jobs.stale_after is 2h0m0s)"},
				Ending{ID: "old-plan/w", Reason: taken},
				defaults[3], defaults[4],
			),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, notices := sweepStore(t, now, 2*time.Hour)
			s.landed = func(context.Context, types.Job) string { return "" }
			rows := pruneRows(now)
			plant(t, s, rows...)

			got, err := s.Prune(t.Context(), PruneOptions{All: tt.all})
			require.NoError(t, err)
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
	assert.Len(t, got, 5, "a dry run lists what a prune would end")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}

func TestJobPruneRefusesABoundWorker(t *testing.T) {
	loc := declared(t, types.Job{ID: "w", State: types.StateExited})
	_, err := boundStore(loc, "w").Prune(t.Context(), PruneOptions{})
	var refused *RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, types.StateExited, jobStates(t, loc)["w"])
}
