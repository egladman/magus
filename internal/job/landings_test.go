package job

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
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
	repo := testkit.NewRepository(t, map[string]string{"a.go": "package a\n", "b.go": "package b\n", "c.go": "package c\n", "d.go": "package d\n"})
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
		assert.True(t, strings.HasSuffix(got[id].EndReason, fmt.Sprintf(" in %s read the same there", dir)), got[id].EndReason)
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
	repo := testkit.NewRepository(t, map[string]string{"a.go": "package a\n", "b.go": "package b\n"})
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

// A worker whose branch a live integration job merged ends as landed before the base has
// the work: the integration row's checkout reads the worker's paths as the worker does. A
// worker no live integration row names stays exited.
func TestAWorkerMergedIntoALiveIntegrationCheckoutEnds(t *testing.T) {
	repo := testkit.NewRepository(t, map[string]string{"a.go": "package a\n", "b.go": "package b\n"})
	seedRev := repo.Head()
	wa := repo.AddWorktree("job-a")
	wa.Write("a.go", "package a // merged\n")
	wa.Git("commit", "-q", "-am", "job-a")
	wb := repo.AddWorktree("job-b")
	wb.Write("b.go", "package b // unmerged\n")
	wb.Git("commit", "-q", "-am", "job-b")

	integration := repo.AddWorktree("integration")
	integration.Git("merge", "-q", "--no-ff", "-m", "merge job-a", "job-a")
	repo.Git("update-ref", "refs/remotes/origin/main", seedRev)

	s := tempStore(t, repo.Root())
	var notices strings.Builder
	s.notices = &notices
	exited := func(id string, wt *testkit.Repo, file string) types.Job {
		return types.Job{ID: id, State: types.StateExited, Checkpoint: seedRev, CheckoutRoot: wt.Root(),
			WritePaths: []string{file}, Result: &types.JobResult{ChangedPaths: []string{file}}, Updated: 1}
	}
	plant(t, s, exited("job-a", wa, "a.go"), exited("job-b", wb, "b.go"),
		types.Job{ID: "integration", State: types.StateRunning, Checkpoint: seedRev, CheckoutRoot: integration.Root(),
			Integrates: []string{"job-a", "job-b"}, Updated: 1})

	got := states(t, s)
	ended := exited("job-a", wa, "a.go")
	ended.Schema = got["job-a"].Schema // the envelope is the store's, stamped on read
	ended.State, ended.Updated = types.StateNoReturn, got["job-a"].Updated
	ended.EndReason = fmt.Sprintf("its work landed in integration job integration at %s: the 1 changed path(s) at %s in %s read the same there",
		integration.Head()[:7], wa.Head()[:7], wa.Root())
	assert.Equal(t, ended, got["job-a"])
	assert.Equal(t, types.StateExited, got["job-b"].State, "named by the integration row, but its checkout lacks the work")
}
