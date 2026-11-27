package job

import (
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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
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

	require.NoError(t, s.writeLandingProbes(s.readLandingProbes(), map[string]landingProbe{"a": {At: 1, Updated: 1}, "b": {At: 1, Updated: 1}}))
	require.NoError(t, s.writeLandingProbes(s.readLandingProbes(), map[string]landingProbe{"a": {At: 2, Updated: 1}, "b": {At: 1, Updated: 1}}))
	got := s.readLandingProbes()
	assert.Equal(t, probeLog{probes: map[string]landingProbe{"a": {At: 2, Updated: 1}, "b": {At: 1, Updated: 1}}, lines: 3}, got,
		"the changed entry was appended")

	require.NoError(t, s.writeLandingProbes(got, map[string]landingProbe{"b": {At: 1, Updated: 1}}))
	raw, err := os.ReadFile(probes)
	require.NoError(t, err)
	assert.Equal(t, `{"id":"b","at":1,"updated":1}`+"\n", string(raw))
}

func TestWriteLandingProbesReturnsAFailedWrite(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	path, err := s.Path()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(filepath.Dir(path), landingProbesFile), 0o755))

	err = s.writeLandingProbes(probeLog{}, map[string]landingProbe{"a": {At: 1, Updated: 1}})
	assert.Error(t, err)
}

func TestWriteUnmergedFactsReturnsAFailedWrite(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "facts.jsonl")
	require.NoError(t, os.MkdirAll(path, 0o755))
	stored := storedUnmergedFacts{facts: map[string]unmergedFact{}}
	next := map[string]unmergedFact{"a": {ID: "a", Branch: "main"}}

	err := writeUnmergedFacts(path, stored, next, func(unmergedFact) bool { return true })
	assert.Error(t, err)
}

// An exited row the landing probe proves is on the base ends as no_return with the
// probe's reason; one with a live child, one another write moved since the probe, and one
// the probe does not prove all stay.
func TestListEndsExitedJobsWhoseWorkLanded(t *testing.T) {
	s, notices := sweepStore(t, 100_000, 0)
	exited := func(id string) job.Job {
		return job.Job{ID: id, State: job.StateExited, Checkpoint: "c0", CheckoutRoot: "/w/" + id, Updated: 90_000}
	}
	busy := exited("busy")
	plant(t, s,
		exited("landed"),
		busy,
		job.Job{ID: "busy/child", Parent: "busy", State: job.StateRunning, Updated: 90_000},
		exited("pending"),
		job.Job{ID: "reader", State: job.StateExited, ReadOnly: true, Updated: 90_000},
	)
	var probed []string
	s.landed = func(_ context.Context, row job.Job) string {
		probed = append(probed, row.ID)
		if row.ID == "pending" {
			return ""
		}
		return "its work landed on origin/main at base1"
	}

	got := states(t, s)
	assert.Equal(t, []string{"landed", "busy", "pending"}, probed, "only exited rows in a checkout are probed")
	landed := exited("landed")
	landed.State, landed.EndReason, landed.Updated, landed.Landed = job.StateNoReturn, "its work landed on origin/main at base1", 100_000, true
	assert.Equal(t, landed, got["landed"])
	assert.False(t, got["busy/child"].Landed, "another ending is no landing")
	for _, id := range []string{"busy", "pending", "reader"} {
		assert.Equal(t, job.StateExited, got[id].State, id)
	}
	assert.Equal(t, "ended landed: its work landed on origin/main at base1\n", notices.String())
}

// One read probes at most landingBudget rows, least recently probed first, and records
// each before probing it: a guard hook killed mid-probe has still taken its turn, so the
// next round moves on instead of starting the same probes over. Reads inside one
// landingRound share that round and probe nothing more.
func TestALandingSweepIsBoundedAndRecordsItsProbesFirst(t *testing.T) {
	s, _ := sweepStore(t, 100_000, 0)
	var rows []job.Job
	for i := range landingBudget + 2 {
		rows = append(rows, job.Job{ID: fmt.Sprintf("exited-%d", i), State: job.StateExited, Checkpoint: "c0",
			CheckoutRoot: "/w", Updated: 90_000})
	}
	plant(t, s, rows...)
	var probed []string
	s.landed = func(_ context.Context, row job.Job) string {
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
	exited := func(id string, wt *testkit.Repo, file string) job.Job {
		return job.Job{ID: id, State: job.StateExited, Checkpoint: seedRev, CheckoutRoot: wt.Root(),
			Paths: job.Paths{Write: []string{file}}, Updated: 1}
	}
	plant(t, s, exited("job-a", wa, "a.go"), exited("job-b", wb, "b.go"), exited("job-c", wc, "c.go"), exited("job-d", wd, "d.go"))

	got := states(t, s)
	for id, wt := range map[string]*testkit.Repo{"job-a": wa, "job-b": wb} {
		dir := wt.Root()
		head := wt.Head()
		assert.Equal(t, job.StateNoReturn, got[id].State, id)
		var base, short string
		_, err := fmt.Sscanf(got[id].EndReason, "its work landed on origin/main at %s the 1 changed path(s) at %s", &base, &short)
		require.NoError(t, err, got[id].EndReason)
		assert.True(t, strings.HasPrefix(squash, strings.TrimSuffix(base, ":")), "names the squash commit: %s", got[id].EndReason)
		assert.True(t, strings.HasPrefix(head, short), "names the job's own HEAD: %s", got[id].EndReason)
		assert.True(t, strings.HasSuffix(got[id].EndReason, fmt.Sprintf(" in %s read the same there", dir)), got[id].EndReason)
	}
	assert.Equal(t, job.StateExited, got["job-c"].State, "a commit the base never took")
	assert.Equal(t, job.StateExited, got["job-d"].State, "HEAD reads as the checkpoint, so it says nothing of the change")

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
	plant(t, s, job.Job{ID: "job", State: job.StateExited, Checkpoint: seedRev, CheckoutRoot: wt.Root(),
		Paths: job.Paths{Write: []string{"a.go", "b.go"}}, Updated: 1})
	fileTry(t, s, "job", &job.Result{ChangedPaths: []string{"a.go"}})

	assert.Equal(t, job.StateExited, states(t, s)["job"].State)
}

func landedWithNoResult(t *testing.T, dirty, differs bool, registered int64) job.Job {
	t.Helper()
	repo := testkit.NewRepository(t, map[string]string{"a.go": "package a\n"})
	seedRev := repo.Head()
	wt := repo.AddWorktree("job")
	wt.Write("a.go", "package a // landed\n")
	wt.Git("commit", "-q", "-am", "job")
	body := "package a // landed\n"
	if differs {
		body = "package a // changed again on the base\n"
	}
	repo.Commit("squash", map[string]string{"a.go": body})
	repo.Git("update-ref", "refs/remotes/origin/main", repo.Head())
	if dirty {
		wt.Write("a.go", "package a // still editing\n")
	}

	s := tempStore(t, repo.Root())
	s.notices = io.Discard
	plant(t, s, job.Job{ID: "job", State: job.StateRunning, Checkpoint: seedRev, CheckoutRoot: wt.Root(),
		Paths: job.Paths{Write: []string{"a.go"}}, Registered: registered, Updated: 1})
	return states(t, s)["job"]
}

// A running row with no filed result ends once its committed diff is on base.
func TestARunningRowWithNoResultEndsWhenItsCommittedWorkLanded(t *testing.T) {
	got := landedWithNoResult(t, false, false, 0)

	assert.Equal(t, job.StateNoReturn, got.State)
	assert.True(t, got.Landed)
	assert.Contains(t, got.EndReason, "its work landed on origin/main")
}

// main-green-proto was ended under its worker once part of its diff was on
// the base.
func TestARowAHolderTookIsNeverEndedByItsCommittedWorkLanding(t *testing.T) {
	got := landedWithNoResult(t, false, false, 1)

	assert.Equal(t, job.StateRunning, got.State)
	assert.False(t, got.Landed)
}

func TestARunningRowWithNoResultAndUncommittedEditsKeepsItsLease(t *testing.T) {
	got := landedWithNoResult(t, true, false, 0)

	assert.Equal(t, job.StateRunning, got.State)
	assert.False(t, got.Landed)
}

// The base squashed the work, then edited the same file elsewhere. The path
// reads otherwise than HEAD, yet merging the work there changes nothing.
func TestARowEndsWhenTheBaseEditedItsLandedFileSince(t *testing.T) {
	repo := testkit.NewRepository(t, map[string]string{"a.go": "package a\n\nfunc A() {}\n"})
	seedRev := repo.Head()
	wt := repo.AddWorktree("job")
	wt.Write("a.go", "package a // landed\n\nfunc A() {}\n")
	wt.Git("commit", "-q", "-am", "job")
	repo.Commit("squash", map[string]string{"a.go": "package a // landed\n\nfunc A() {}\n"})
	repo.Commit("later", map[string]string{"a.go": "package a // landed\n\nfunc A() { _ = 1 }\n"})
	repo.Git("update-ref", "refs/remotes/origin/main", repo.Head())

	s := tempStore(t, repo.Root())
	s.notices = io.Discard
	plant(t, s, job.Job{ID: "job", State: job.StateRunning, Checkpoint: seedRev, CheckoutRoot: wt.Root(),
		Paths: job.Paths{Write: []string{"a.go"}}, Updated: 1})
	got := states(t, s)["job"]

	assert.Equal(t, job.StateNoReturn, got.State)
	assert.True(t, got.Landed)
	assert.Contains(t, got.EndReason, "merge there changing nothing, though 1 read otherwise since the base edited them")
}

func TestARunningRowWithNoResultAndAPathDifferingAtTheBaseKeepsItsLease(t *testing.T) {
	got := landedWithNoResult(t, false, true, 0)

	assert.Equal(t, job.StateRunning, got.State)
	assert.False(t, got.Landed)
}

// A worker whose branch a live integration job merged ends as landed before the base has
// the work: the integration row's checkout reads the worker's paths as the worker does. A
// worker no live integration row names stays exited.
func TestAWorkerMergedIntoALiveIntegrationCheckoutEnds(t *testing.T) {
	workerMergedIntoIntegration(t, job.StateExited, false)
}

func TestARunningWorkerWithAFiledResultEndsWhenItsCleanWorkLanded(t *testing.T) {
	workerMergedIntoIntegration(t, job.StateRunning, false)
}

func TestARunningWorkerWithUncommittedEditsKeepsItsLease(t *testing.T) {
	workerMergedIntoIntegration(t, job.StateRunning, true)
}

func workerMergedIntoIntegration(t *testing.T, state job.State, dirty bool) {
	t.Helper()
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
	exited := func(id string, wt *testkit.Repo, file string) job.Job {
		return job.Job{ID: id, State: state, Checkpoint: seedRev, CheckoutRoot: wt.Root(),
			Paths: job.Paths{Write: []string{file}}, Updated: 1}
	}
	plant(t, s, exited("job-a", wa, "a.go"), exited("job-b", wb, "b.go"),
		job.Job{ID: "integration", State: job.StateRunning, Checkpoint: seedRev, CheckoutRoot: integration.Root(),
			Integrates: []string{"job-a", "job-b"}, Updated: 1})
	fileTry(t, s, "job-a", &job.Result{ChangedPaths: []string{"a.go"}})
	fileTry(t, s, "job-b", &job.Result{ChangedPaths: []string{"b.go"}})

	if dirty {
		wa.Write("a.go", "package a // still editing\n")
	}
	got := states(t, s)
	if dirty {
		assert.Equal(t, state, got["job-a"].State)
		assert.False(t, got["job-a"].Landed)
		return
	}
	ended := exited("job-a", wa, "a.go")
	ended.State, ended.Updated = job.StateNoReturn, got["job-a"].Updated
	ended.Landed = true
	ended.EndReason = fmt.Sprintf("its work landed in integration job integration at %s: the 1 changed path(s) at %s in %s read the same there",
		integration.Head()[:7], wa.Head()[:7], wa.Root())
	assert.Equal(t, ended, got["job-a"])
	assert.Equal(t, state, got["job-b"].State, "named by the integration row, but its checkout lacks the work")
}

type unmergedSuite struct {
	suite.Suite
	store *Store
	repo  *testkit.Repo
	main  string
	tries job.Attempts
}

func TestUnmerged(t *testing.T) { suite.Run(t, new(unmergedSuite)) }

func (s *unmergedSuite) SetupTest() {
	s.store, _, s.repo = worktreeStore(s.T())
	s.main = s.repo.Git("symbolic-ref", "--short", "HEAD")
	s.tries = job.Attempts{}
}

func (s *unmergedSuite) branch(name string) string {
	s.repo.Git("switch", "-q", "-c", name, s.main)
	s.repo.Commit(name, map[string]string{name + ".txt": name + "\n"})
	s.repo.Git("switch", "-q", s.main)
	return name
}

func (s *unmergedSuite) unmerged(rows ...job.Job) job.UnmergedReport {
	return Unmerged(s.T().Context(), s.repo.Root(), rows, s.tries)
}

// filed has job id's live try file result.
func (s *unmergedSuite) filed(id string, result *job.Result) {
	s.tries[id] = []job.Attempt{filedTry(id, result)}
}

func (s *unmergedSuite) TestAFiledRowWithCommitsOffTheBranchIsNamedWithItsHeadAndRemedy() {
	head := s.repo.Git("rev-parse", "--short=12", s.branch("w"))
	s.filed("w", &job.Result{ChangedPaths: []string{"w.txt"}})

	got := s.unmerged(job.Job{ID: "w", State: job.StateExited})

	s.Equal(s.main, got.Branch)
	s.Require().Len(got.Jobs, 1)
	s.Equal("w", got.Jobs[0].Job)
	s.True(strings.HasPrefix(head, got.Jobs[0].Head), got.Jobs[0].Head)
	s.Contains(got.Jobs[0].Why, "not on "+s.main)
	s.Contains(got.Jobs[0].Remedy, fmt.Sprintf("merge %s into %s", got.Jobs[0].Head, s.main))
	s.Contains(got.Jobs[0].Remedy, "left_unmerged")
}

func (s *unmergedSuite) TestAnAncestorOfTheBranchIsMerged() {
	s.branch("w")
	s.repo.Git("merge", "-q", "--no-ff", "-m", "merge", "w")

	s.filed("w", &job.Result{ChangedPaths: []string{"w.txt"}})
	got := s.unmerged(job.Job{ID: "w", State: job.StatePass})

	s.Empty(got.Jobs)
	s.Empty(got.Unknown)
}

func (s *unmergedSuite) TestABranchSquashedOnlyOnTheRemoteIsMerged() {
	s.repo.Git("remote", "add", "origin", s.repo.Root())
	stale := s.repo.Head()
	s.branch("w")
	s.repo.Commit("squash", map[string]string{"w.txt": "w\n"})
	s.repo.Git("update-ref", "refs/remotes/origin/main", s.repo.Head())
	s.repo.Git("reset", "-q", "--hard", stale)

	s.Empty(s.unmerged(job.Job{ID: "w", State: job.StatePass}).Jobs)
}

func (s *unmergedSuite) TestAnUnreadRemoteIsNamedBesideTheMiss() {
	s.repo.Git("remote", "add", "origin", s.repo.Root())
	s.branch("w")

	got := s.unmerged(job.Job{ID: "w", State: job.StatePass})

	s.Require().Len(got.Jobs, 1)
	s.Contains(got.Jobs[0].Why, "origin/main does not read here")
}

func (s *unmergedSuite) TestASquashedBranchIsMergedByContent() {
	s.branch("w")
	s.repo.Commit("squash", map[string]string{"w.txt": "w\n"})

	s.Empty(s.unmerged(job.Job{ID: "w", State: job.StatePass}).Jobs)
}

func (s *unmergedSuite) TestAReplayedBranchIsMergedAfterTheBranchWentOnToChangeItsFile() {
	s.repo.Commit("shared", map[string]string{"shared.txt": "1\n2\n3\n4\n5\n6\n7\n8\n9\n"})
	s.repo.Git("switch", "-q", "-c", "w", s.main)
	s.repo.Commit("edit top", map[string]string{"shared.txt": "one\n2\n3\n4\n5\n6\n7\n8\n9\n"})
	s.repo.Git("switch", "-q", s.main)
	s.repo.Git("cherry-pick", "w")
	s.repo.Commit("edit bottom", map[string]string{"shared.txt": "one\n2\n3\n4\n5\n6\n7\n8\nnine\n"})

	s.Empty(s.unmerged(job.Job{ID: "w", State: job.StatePass}).Jobs)
}

func (s *unmergedSuite) TestAReadRunsNoMergeDriverAndLeavesNoTempFile() {
	called := filepath.Join(s.T().TempDir(), "driver-called")
	s.repo.Git("config", "merge.magus.driver", fmt.Sprintf("touch %q; exit 9", called))
	s.repo.Commit("shared", map[string]string{
		".gitattributes": "shared.lock merge=magus\n",
		"shared.lock":    "1\n2\n3\n4\n5\n6\n7\n8\n9\n",
	})
	s.repo.Git("switch", "-q", "-c", "w", s.main)
	s.repo.Commit("edit top", map[string]string{"shared.lock": "one\n2\n3\n4\n5\n6\n7\n8\n9\n"})
	s.repo.Git("switch", "-q", s.main)
	s.repo.Commit("edit bottom", map[string]string{"shared.lock": "1\n2\n3\n4\n5\n6\n7\n8\nnine\n"})

	got := s.unmerged(job.Job{ID: "w", State: job.StatePass})

	s.Require().Len(got.Jobs, 1)
	s.NoFileExists(called)
	leftovers, err := filepath.Glob(filepath.Join(s.repo.Root(), ".merge_file_*"))
	s.Require().NoError(err)
	s.Empty(leftovers)
}

func (s *unmergedSuite) TestAReplayedCommitIsMergedAfterTheBranchRewroteItsLine() {
	s.repo.Git("switch", "-q", "-c", "w", s.main)
	s.repo.Commit("write the line", map[string]string{"line.txt": "first\n"})
	s.repo.Git("switch", "-q", s.main)
	s.repo.Git("cherry-pick", "w")
	s.repo.Commit("rewrite the line", map[string]string{"line.txt": "second\n"})

	s.Empty(s.unmerged(job.Job{ID: "w", State: job.StatePass}).Jobs)
}

func (s *unmergedSuite) TestACommitThatOnlySharesItsSubjectWithAnotherAuthorIsNotReplayed() {
	s.repo.Git("switch", "-q", "-c", "w", s.main)
	s.repo.Commit("write the line", map[string]string{"line.txt": "first\n"})
	s.repo.Git("switch", "-q", s.main)
	s.repo.Git("-c", "user.email=other@example.com", "commit", "-q", "--allow-empty", "-m", "write the line")

	s.Len(s.unmerged(job.Job{ID: "w", State: job.StatePass}).Jobs, 1)
}

func (s *unmergedSuite) TestAReadPastItsDeadlineNamesWhatItLeftAndTheNextCarriesOn() {
	s.branch("w")
	row := job.Job{ID: "w", State: job.StateExited}
	s.filed("w", &job.Result{ChangedPaths: []string{"w.txt"}})
	spent, cancel := context.WithCancel(s.T().Context())
	cancel()

	got := Unmerged(spent, s.repo.Root(), []job.Job{row}, s.tries)

	if got.Unknown == "" {
		s.Fail("a read that answered nothing must say so", "%+v", got)
	}
	s.Empty(got.Jobs, "nothing is claimed of a row it did not read")
	s.Len(s.unmerged(row).Jobs, 1)
}

func (s *unmergedSuite) TestAnAnswerForARowTheStoreLostIsDropped() {
	s.branch("gone")
	s.branch("w")
	gone := job.Job{ID: "gone", State: job.StatePass}
	kept := job.Job{ID: "w", State: job.StatePass}
	s.unmerged(gone, kept)
	path := unmergedFactsPath(s.repo.Root())
	s.Require().Contains(readUnmergedFacts(path).facts, unmergedKey("gone", s.main))

	s.unmerged(kept)

	facts := readUnmergedFacts(path).facts
	s.NotContains(facts, unmergedKey("gone", s.main))
	s.Contains(facts, unmergedKey("w", s.main))
}

func (s *unmergedSuite) TestAnAnswerForABranchThatIsGoneIsDropped() {
	s.branch("w")
	row := job.Job{ID: "w", State: job.StatePass}
	s.repo.Git("switch", "-q", "-c", "other", s.main)
	s.unmerged(row)
	path := unmergedFactsPath(s.repo.Root())
	s.Require().Contains(readUnmergedFacts(path).facts, unmergedKey("w", "other"))
	s.repo.Git("switch", "-q", s.main)
	s.repo.Git("branch", "-q", "-D", "other")

	s.unmerged(row)

	facts := readUnmergedFacts(path).facts
	s.NotContains(facts, unmergedKey("w", "other"))
	s.Contains(facts, unmergedKey("w", s.main))
}

// slowMissing answers RangeFiles, which opens every content read, after took, or
// gives up with its context.
type slowMissing struct {
	types.VCSDriver
	took time.Duration
}

func (r slowMissing) RangeFiles(ctx context.Context, dir, base, head string, paths []string) ([]string, error) {
	select {
	case <-time.After(r.took):
		return r.VCSDriver.RangeFiles(ctx, dir, base, head, paths)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *unmergedSuite) readWith(ctx context.Context, took time.Duration, work ...job.Job) *unmergedRead {
	driver, why := resolveDriver(ctx, s.repo.Root())
	s.Require().NotNil(driver, why)
	meta, err := driver.Metadata(ctx, s.repo.Root())
	s.Require().NoError(err)
	r := &unmergedRead{
		driver: slowMissing{driver, took},
		root:   s.repo.Root(), branch: s.main, tip: meta.ID, now: time.Now().Unix(),
	}
	r.run(ctx, work, work)
	return r
}

func (s *unmergedSuite) TestARowOverItsRowBudgetAnswersOnTheReadAfterItWasCut() {
	s.branch("w")
	row := job.Job{ID: "w", State: job.StateExited}
	took := unmergedRowBudget + 200*time.Millisecond

	s.Equal(1, s.readWith(s.T().Context(), took, row).unread, "the first read cuts it at the row budget")

	second := s.readWith(s.T().Context(), took, row)
	s.Empty(second.unknown(), "the next read lets it spend what is left")
	s.Equal(1, second.rows["w"].fact.Commits)
}

func (s *unmergedSuite) TestARowARepeatedReadCutsIsNamedAsCutBefore() {
	head := s.repo.Git("rev-parse", s.branch("w"))
	row := job.Job{ID: "w", State: job.StateExited}
	line, err := codec.Marshal(unmergedFact{ID: "w", Branch: s.main, Head: head, CutAt: 1})
	s.Require().NoError(err)
	path := unmergedFactsPath(s.repo.Root())
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(codec.AppendLines(path, [][]byte{line}, 0o644))
	spent, cancel := context.WithTimeout(s.T().Context(), time.Second)
	defer cancel()

	got := s.readWith(spent, time.Minute, row).unknown()

	s.Contains(got, "1 of those a read had already cut")
}

func (s *unmergedSuite) TestAStoredMergeIsReadInsteadOfAskingAgain() {
	head := s.repo.Git("rev-parse", s.branch("w"))
	line, err := codec.Marshal(unmergedFact{ID: "w", Branch: s.main, Head: head, Tip: s.repo.Git("rev-parse", "HEAD"), Merged: true, MergedAt: time.Now().Unix()})
	s.Require().NoError(err)
	path := unmergedFactsPath(s.repo.Root())
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(codec.AppendLines(path, [][]byte{line}, 0o644))

	s.Empty(s.unmerged(job.Job{ID: "w", State: job.StatePass}).Jobs)
}

func (s *unmergedSuite) TestAStoredMergeOfAnOlderHeadStandsForNothing() {
	head := s.repo.Git("rev-parse", s.branch("w"))
	s.Empty(s.unmerged(job.Job{ID: "other", State: job.StatePass}).Jobs)
	line, err := codec.Marshal(unmergedFact{ID: "w", Branch: s.main, Head: head + "0", Tip: s.repo.Git("rev-parse", "HEAD"), Merged: true})
	s.Require().NoError(err)
	path := unmergedFactsPath(s.repo.Root())
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(codec.AppendLines(path, [][]byte{line}, 0o644))

	s.Len(s.unmerged(job.Job{ID: "w", State: job.StatePass}).Jobs, 1)
}

func (s *unmergedSuite) TestARowEndedWithWorkOnItsBranchAndNoStatedReasonCounts() {
	s.branch("w")

	got := s.unmerged(job.Job{ID: "w", State: job.StateNoReturn, EndReason: "abandoned by its holder, which filed no result"})

	s.Require().Len(got.Jobs, 1)
	s.Equal("w", got.Jobs[0].Job)
}

func (s *unmergedSuite) TestAStatedReasonLeavesTheRowOut() {
	s.branch("w")

	got := s.unmerged(job.Job{ID: "w", State: job.StateNoReturn, LeftUnmerged: "the approach was dropped"})

	s.Empty(got.Jobs)
}

func (s *unmergedSuite) TestARowALandingProbeEndedIsLeftOut() {
	s.branch("w")

	got := s.unmerged(job.Job{ID: "w", State: job.StateNoReturn, Landed: true})

	s.Empty(got.Jobs)
}

func (s *unmergedSuite) TestAnEndReasonThatOnlyReadsLikeALandingIsStillChecked() {
	s.branch("w")

	got := s.unmerged(job.Job{ID: "w", State: job.StateNoReturn, EndReason: "its work landed on main at abc"})

	s.Len(got.Jobs, 1)
}

func (s *unmergedSuite) TestARunningRowIsStillWorking() {
	s.branch("w")

	s.Empty(s.unmerged(job.Job{ID: "w", State: job.StateRunning}).Jobs)
}

func (s *unmergedSuite) TestAFiledCheckoutHoldingUncommittedChangesCounts() {
	where := filepath.Join(s.T().TempDir(), "w")
	s.repo.Git("worktree", "add", "-q", where, "-b", "w")
	s.Require().NoError(os.WriteFile(filepath.Join(where, "w.txt"), []byte("dirty\n"), 0o644))
	row := job.Job{ID: "w", State: job.StateExited, Paths: job.Paths{Write: []string{"w.txt"}}, CheckoutRoot: where}
	s.filed("w", &job.Result{ChangedPaths: []string{"w.txt"}})

	got := s.unmerged(row)

	s.Require().Len(got.Jobs, 1)
	s.NotEmpty(got.Jobs[0].Head)
	s.Contains(got.Jobs[0].Why, "1 uncommitted path(s), first w.txt")
}

func (s *unmergedSuite) TestACheckoutOutsideTheRepositoryIsNamedByItsJob() {
	where := filepath.Join(s.T().TempDir(), "w")
	s.repo.Git("worktree", "add", "-q", where, "-b", "w")
	s.Require().NoError(os.WriteFile(filepath.Join(where, "w.txt"), []byte("dirty\n"), 0o644))
	row := job.Job{ID: "w", State: job.StateExited, Paths: job.Paths{Write: []string{"w.txt"}}, CheckoutRoot: where}
	s.filed("w", &job.Result{ChangedPaths: []string{"w.txt"}})

	got := s.unmerged(row)

	s.Require().Len(got.Jobs, 1)
	s.Equal("w holds 1 uncommitted path(s), first w.txt", got.Jobs[0].Why)
	s.Contains(got.Jobs[0].Remedy, "commit them in w,")
	s.NotContains(got.Jobs[0].Remedy, where)
}

func (s *unmergedSuite) TestAnUnreadableStatusNamesNoPath() {
	where := filepath.Join(s.T().TempDir(), "w")
	s.repo.Git("worktree", "add", "-q", where, "-b", "w")
	gitDir := strings.TrimSpace(s.repo.Git("-C", where, "rev-parse", "--absolute-git-dir"))
	s.Require().NoError(os.WriteFile(filepath.Join(gitDir, "index"), []byte("not an index"), 0o644))
	row := job.Job{ID: "w", State: job.StateExited, Paths: job.Paths{Write: []string{"w.txt"}}, CheckoutRoot: where}
	s.filed("w", &job.Result{ChangedPaths: []string{"w.txt"}})

	got := s.unmerged(row)

	s.Require().Len(got.Jobs, 1)
	s.True(strings.HasPrefix(got.Jobs[0].Why, "the status of its checkout cannot be read: "), got.Jobs[0].Why)
	s.Equal("commit what w holds, or state why it stays off", got.Jobs[0].Remedy[:strings.Index(got.Jobs[0].Remedy, ": `")])
}

func (s *unmergedSuite) TestARepositoryWithNoCommitIsUnknownByName() {
	bare := s.T().TempDir()
	s.Require().NoError(exec.CommandContext(s.T().Context(), "git", "-C", bare, "init", "-q").Run())

	got := Unmerged(s.T().Context(), bare, []job.Job{{ID: "w", State: job.StatePass}}, job.Attempts{"w": {filedTry("w", &job.Result{ChangedPaths: []string{"w.txt"}})}})

	s.Equal("the branch of the repository: ", got.Unknown[:len("the branch of the repository: ")])
	s.NotContains(got.Unknown, bare)
}

func (s *unmergedSuite) TestACheckoutInsideTheRepositoryIsNamedByItsRelativePath() {
	where := filepath.Join(s.repo.Root(), "trees", "w")
	s.repo.Git("worktree", "add", "-q", where, "-b", "w")
	s.Require().NoError(os.WriteFile(filepath.Join(where, "w.txt"), []byte("dirty\n"), 0o644))
	row := job.Job{ID: "w", State: job.StateExited, Paths: job.Paths{Write: []string{"w.txt"}}, CheckoutRoot: where}
	s.filed("w", &job.Result{ChangedPaths: []string{"w.txt"}})

	got := s.unmerged(row)

	s.Require().Len(got.Jobs, 1)
	s.Equal("trees/w holds 1 uncommitted path(s), first w.txt", got.Jobs[0].Why)
	s.Contains(got.Jobs[0].Remedy, "commit them in trees/w,")
	s.NotContains(got.Jobs[0].Remedy, s.repo.Root())
}

func (s *unmergedSuite) TestAFiledRowWithNoCheckoutOrBranchIsNeverReadAsMerged() {
	s.filed("gone", &job.Result{ChangedPaths: []string{"a.go"}})
	got := s.unmerged(job.Job{ID: "gone", State: job.StatePass})

	s.Require().Len(got.Jobs, 1)
	s.Empty(got.Jobs[0].Head)
	s.Contains(got.Jobs[0].Why, "cannot be read")
}

func (s *unmergedSuite) TestAnEndedRowThatFiledNoPathAndLeftNoBranchHasNothingToLose() {
	s.Empty(s.unmerged(job.Job{ID: "gone", State: job.StateNoReturn}).Jobs)
}

func (s *unmergedSuite) TestAGoneHeadAPassedIntegrationNamesIsMerged() {
	s.filed("gone", &job.Result{ChangedPaths: []string{"a.go"}})
	got := s.unmerged(
		job.Job{ID: "gone", State: job.StatePass},
		job.Job{ID: "merge", State: job.StatePass, Integrates: []string{"gone"}, ReadOnly: true},
	)

	s.Empty(got.Jobs)
}

func (s *unmergedSuite) TestADetachedCheckoutIsUnknownNeverMerged() {
	s.branch("w")
	s.repo.Git("switch", "-q", "--detach")

	got := s.unmerged(job.Job{ID: "w", State: job.StatePass})

	s.Equal("the repository has no branch checked out, so nothing can be merged onto one", got.Unknown)
	s.Empty(got.Jobs)
}

func (s *unmergedSuite) TestAFollowUpStandsUntilAnswered() {
	row := job.Job{ID: "r", State: job.StatePass, ReadOnly: true}
	s.filed("r", &job.Result{FollowUps: []string{"a follow-up"}})

	got := s.unmerged(row)

	s.Require().Len(got.Jobs, 1)
	s.Equal("a follow-up", got.Jobs[0].Item)
	s.Empty(got.Risks)
}

func (s *unmergedSuite) TestARowWithOnlyUnresolvedRisksHasNothingUnmerged() {
	row := job.Job{ID: "r", State: job.StatePass, ReadOnly: true}
	s.filed("r", &job.Result{UnresolvedRisks: []string{"a risk"}})

	got := s.unmerged(row)

	s.Empty(got.Jobs)
	s.Require().Len(got.Risks, 1)
	s.Equal("a risk", got.Risks[0].Item)
}

func (s *unmergedSuite) TestAnAnsweredRiskLeavesTheList() {
	row := job.Job{ID: "r", State: job.StatePass, ReadOnly: true,
		Deferred: []job.Deferred{{Item: "a risk", Reason: "accepted"}}}
	s.filed("r", &job.Result{UnresolvedRisks: []string{"a risk"}})

	s.Empty(s.unmerged(row).Risks)
}

func (s *unmergedSuite) TestAReasonOrADeclaredRowAnswersAnItem() {
	row := job.Job{ID: "r", State: job.StatePass, ReadOnly: true,
		Deferred: []job.Deferred{{Item: "a risk", Reason: "accepted"}, {Item: "a follow-up", Job: "next"}}}
	s.filed("r", &job.Result{UnresolvedRisks: []string{"a risk"}, FollowUps: []string{"a follow-up"}})

	got := s.unmerged(row, job.Job{ID: "next", State: job.StateDeclared})

	s.Empty(got.Jobs)
	s.Empty(got.Risks)
}

func (s *unmergedSuite) TestAnAnswerNamingAMissingRowLeavesTheItemStanding() {
	row := job.Job{ID: "r", State: job.StatePass, ReadOnly: true,
		Deferred: []job.Deferred{{Item: "a follow-up", Job: "gone"}}}
	s.filed("r", &job.Result{FollowUps: []string{"a follow-up"}})

	s.Len(s.unmerged(row).Jobs, 1)
}

func (s *unmergedSuite) TestAnotherCheckoutsTreeIsNotThisOnes() {
	s.branch("w")
	peer := job.Job{ID: "w", State: job.StatePass, Worktree: &job.Worktree{Source: s.T().TempDir()}}

	s.Empty(s.unmerged(peer).Jobs)
}

func (s *unmergedSuite) settle(rec job.Declaration) ([]job.Job, error) {
	rec.Schema = job.Schema{Version: job.APIVersion}
	return s.store.Settle(s.T().Context(), []job.Declaration{rec}, false)
}

func (s *unmergedSuite) TestSettleWritesAnswersOntoAnEndedRowAndKeepsItsState() {
	want := seed(s.T(), s.store, job.Job{ID: "r", State: job.StatePass})
	fileTry(s.T(), s.store, "r", &job.Result{FollowUps: []string{"later"}})
	want.Deferred = []job.Deferred{{Item: "later", Reason: "not worth it"}}

	got, err := s.settle(job.Declaration{ID: "r", Deferred: want.Deferred})

	s.Require().NoError(err)
	s.Require().Len(got, 1)
	want.Updated = got[0].Updated
	s.Equal(want, got[0])
}

func (s *unmergedSuite) TestSettleRecordsAStatedReason() {
	seed(s.T(), s.store, job.Job{ID: "r", State: job.StateNoReturn})

	got, err := s.settle(job.Declaration{ID: "r", LeftUnmerged: "dropped"})

	s.Require().NoError(err)
	s.Equal("dropped", got[0].LeftUnmerged)
}

// An agent's reason alone must not forget work only this repository or a
// remote branch holds: a person disposes the request first.
func (s *unmergedSuite) TestSettingAsideUnmergedWorkWaitsOnAPerson() {
	head := strings.TrimSpace(s.repo.Git("rev-parse", s.branch("w")))
	s.repo.Git("update-ref", "refs/remotes/origin/w", head)
	plant(s.T(), s.store, job.Job{ID: "w", State: job.StateNoReturn, Registered: 1, Head: head})

	_, err := s.settle(job.Declaration{ID: "w", LeftUnmerged: "dropped"})

	s.Require().ErrorContains(err, "reads pushed, so setting it aside forgets it")
	s.Empty(storedRows(s.T(), s.store)["w"].LeftUnmerged, "the row stays listed")
	requests := slices.DeleteFunc(attentionRows(s.T(), s.repo.Root()), func(r sessions.AttentionRequest) bool { return r.Source != asideSource })
	s.Require().Len(requests, 1)

	dispose(s.T(), s.repo.Root(), requests[0].ID)
	got, err := s.settle(job.Declaration{ID: "w", LeftUnmerged: "dropped"})

	s.Require().NoError(err)
	s.Equal("dropped", got[0].LeftUnmerged)
}

func (s *unmergedSuite) TestSettingAsideWorkABaseHoldsWritesAtOnce() {
	plant(s.T(), s.store, job.Job{ID: "w", State: job.StateNoReturn, Registered: 1, Head: s.repo.Head()})

	got, err := s.settle(job.Declaration{ID: "w", LeftUnmerged: "landed by hand"})

	s.Require().NoError(err)
	s.Equal("landed by hand", got[0].LeftUnmerged)
}

func (s *unmergedSuite) TestSettleRefusesAnItemTheResultDoesNotDefer() {
	seed(s.T(), s.store, job.Job{ID: "r", State: job.StatePass})
	fileTry(s.T(), s.store, "r", &job.Result{FollowUps: []string{"later"}})

	_, err := s.settle(job.Declaration{ID: "r", Deferred: []job.Deferred{{Item: "typo", Reason: "x"}}})

	s.Require().ErrorContains(err, `defers no item "typo"`)
}

func (s *unmergedSuite) TestSettleRefusesARowNothingDeclares() {
	seed(s.T(), s.store, job.Job{ID: "r", State: job.StatePass})
	fileTry(s.T(), s.store, "r", &job.Result{FollowUps: []string{"later"}})

	_, err := s.settle(job.Declaration{ID: "r", Deferred: []job.Deferred{{Item: "later", Job: "gone"}}})

	s.Require().ErrorContains(err, "which nothing declares")
}

func (s *unmergedSuite) TestSettleRefusesARecordAlsoDeclaringASpec() {
	seed(s.T(), s.store, job.Job{ID: "r", State: job.StatePass})

	_, err := s.settle(job.Declaration{ID: "r", LeftUnmerged: "dropped", Agent: job.Instructed("more")})

	s.Require().ErrorContains(err, "settles a job and declares nothing")
}

func (s *unmergedSuite) TestSettleRefusesBothARowAndAReason() {
	seed(s.T(), s.store, job.Job{ID: "r", State: job.StatePass})
	fileTry(s.T(), s.store, "r", &job.Result{FollowUps: []string{"later"}})

	_, err := s.settle(job.Declaration{ID: "r", Deferred: []job.Deferred{{Item: "later", Job: "r", Reason: "x"}}})

	s.Require().ErrorContains(err, "neither or both")
}

func (s *unmergedSuite) TestSettleRefusesALeaseHolder() {
	held := NewStore(Location{Root: s.repo.Root(), StateBase: s.T().TempDir(), Actor: &job.Actor{Lease: "worker"}})

	_, err := held.Settle(s.T().Context(), []job.Declaration{{ID: "r", LeftUnmerged: "dropped"}}, false)

	s.Require().ErrorContains(err, "only its orchestrator")
}

// A checkout on the base tip holds the base, not work of its own. Every path
// reads the same there because the checkout is the base. A job forked from an
// older checkpoint that adopts the orchestrator's checkout is such a row.
func TestARowWhoseCheckoutIsTheBaseTipIsNotLanded(t *testing.T) {
	repo := testkit.NewRepository(t, map[string]string{"a.go": "package a\n\nfunc A()   {}\n"})
	seedRev := repo.Head()
	wt := repo.AddWorktree("job")
	repo.Commit("other work", map[string]string{"a.go": "package a\n\nfunc A() { _ = 1 }\n"})
	repo.Git("update-ref", "refs/remotes/origin/main", repo.Head())
	wt.Git("reset", "-q", "--hard", repo.Head())

	s := tempStore(t, repo.Root())
	s.notices = io.Discard
	plant(t, s, job.Job{ID: "job", State: job.StateRunning, Checkpoint: seedRev, CheckoutRoot: wt.Root(),
		Paths: job.Paths{Write: []string{"a.go"}}, Updated: 1})
	got := states(t, s)["job"]

	assert.False(t, got.Landed, got.EndReason)
	assert.Equal(t, job.StateRunning, got.State)
}
