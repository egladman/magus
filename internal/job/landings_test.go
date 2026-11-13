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
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
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
	exited := func(id string) types.Job {
		return types.Job{ID: id, State: types.StateExited, Checkpoint: "c0", CheckoutRoot: "/w/" + id, Updated: 90_000}
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
	assert.Equal(t, []string{"landed", "busy", "pending"}, probed, "only exited rows in a checkout are probed")
	landed := exited("landed")
	landed.State, landed.EndReason, landed.Updated, landed.Landed = types.StateNoReturn, "its work landed on origin/main at base1", 100_000, true
	assert.Equal(t, landed, got["landed"])
	assert.False(t, got["busy/child"].Landed, "another ending is no landing")
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
			CheckoutRoot: "/w", Updated: 90_000})
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
			WritePaths: []string{file}, Updated: 1}
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
		WritePaths: []string{"a.go", "b.go"}, Updated: 1})
	fileTry(t, s, "job", &types.JobResult{ChangedPaths: []string{"a.go"}})

	assert.Equal(t, types.StateExited, states(t, s)["job"].State)
}

func landedWithNoResult(t *testing.T, dirty, differs bool) types.Job {
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
	plant(t, s, types.Job{ID: "job", State: types.StateRunning, Checkpoint: seedRev, CheckoutRoot: wt.Root(),
		WritePaths: []string{"a.go"}, Updated: 1})
	return states(t, s)["job"]
}

// A running row with no filed result ends once its committed diff is on base.
func TestARunningRowWithNoResultEndsWhenItsCommittedWorkLanded(t *testing.T) {
	got := landedWithNoResult(t, false, false)

	assert.Equal(t, types.StateNoReturn, got.State)
	assert.True(t, got.Landed)
	assert.Contains(t, got.EndReason, "its work landed on origin/main")
}

func TestARunningRowWithNoResultAndUncommittedEditsKeepsItsLease(t *testing.T) {
	got := landedWithNoResult(t, true, false)

	assert.Equal(t, types.StateRunning, got.State)
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
	plant(t, s, types.Job{ID: "job", State: types.StateRunning, Checkpoint: seedRev, CheckoutRoot: wt.Root(),
		WritePaths: []string{"a.go"}, Updated: 1})
	got := states(t, s)["job"]

	assert.Equal(t, types.StateNoReturn, got.State)
	assert.True(t, got.Landed)
	assert.Contains(t, got.EndReason, "merge there changing nothing, though 1 read otherwise since the base edited them")
}

func TestARunningRowWithNoResultAndAPathDifferingAtTheBaseKeepsItsLease(t *testing.T) {
	got := landedWithNoResult(t, false, true)

	assert.Equal(t, types.StateRunning, got.State)
	assert.False(t, got.Landed)
}

// A worker whose branch a live integration job merged ends as landed before the base has
// the work: the integration row's checkout reads the worker's paths as the worker does. A
// worker no live integration row names stays exited.
func TestAWorkerMergedIntoALiveIntegrationCheckoutEnds(t *testing.T) {
	workerMergedIntoIntegration(t, types.StateExited, false)
}

func TestARunningWorkerWithAFiledResultEndsWhenItsCleanWorkLanded(t *testing.T) {
	workerMergedIntoIntegration(t, types.StateRunning, false)
}

func TestARunningWorkerWithUncommittedEditsKeepsItsLease(t *testing.T) {
	workerMergedIntoIntegration(t, types.StateRunning, true)
}

func workerMergedIntoIntegration(t *testing.T, state types.JobState, dirty bool) {
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
	exited := func(id string, wt *testkit.Repo, file string) types.Job {
		return types.Job{ID: id, State: state, Checkpoint: seedRev, CheckoutRoot: wt.Root(),
			WritePaths: []string{file}, Updated: 1}
	}
	plant(t, s, exited("job-a", wa, "a.go"), exited("job-b", wb, "b.go"),
		types.Job{ID: "integration", State: types.StateRunning, Checkpoint: seedRev, CheckoutRoot: integration.Root(),
			Integrates: []string{"job-a", "job-b"}, Updated: 1})
	fileTry(t, s, "job-a", &types.JobResult{ChangedPaths: []string{"a.go"}})
	fileTry(t, s, "job-b", &types.JobResult{ChangedPaths: []string{"b.go"}})

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
	ended.State, ended.Updated = types.StateNoReturn, got["job-a"].Updated
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
	tries types.JobAttempts
}

func TestUnmerged(t *testing.T) { suite.Run(t, new(unmergedSuite)) }

func (s *unmergedSuite) SetupTest() {
	s.store, _, s.repo = worktreeStore(s.T())
	s.main = s.repo.Git("symbolic-ref", "--short", "HEAD")
	s.tries = types.JobAttempts{}
}

func (s *unmergedSuite) branch(name string) string {
	s.repo.Git("switch", "-q", "-c", name, s.main)
	s.repo.Commit(name, map[string]string{name + ".txt": name + "\n"})
	s.repo.Git("switch", "-q", s.main)
	return name
}

func (s *unmergedSuite) unmerged(rows ...types.Job) types.JobUnmergedReport {
	return Unmerged(s.T().Context(), s.repo.Root(), rows, s.tries)
}

// filed has job id's live try file result.
func (s *unmergedSuite) filed(id string, result *types.JobResult) {
	s.tries[id] = []types.Attempt{filedTry(id, result)}
}

func (s *unmergedSuite) TestAFiledRowWithCommitsOffTheBranchIsNamedWithItsHeadAndRemedy() {
	head := s.repo.Git("rev-parse", "--short=12", s.branch("w"))
	s.filed("w", &types.JobResult{ChangedPaths: []string{"w.txt"}})

	got := s.unmerged(types.Job{ID: "w", State: types.StateExited})

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

	s.filed("w", &types.JobResult{ChangedPaths: []string{"w.txt"}})
	got := s.unmerged(types.Job{ID: "w", State: types.StatePass})

	s.Empty(got.Jobs)
	s.Empty(got.Unknown)
}

func (s *unmergedSuite) TestASquashedBranchIsMergedByContent() {
	s.branch("w")
	s.repo.Commit("squash", map[string]string{"w.txt": "w\n"})

	s.Empty(s.unmerged(types.Job{ID: "w", State: types.StatePass}).Jobs)
}

func (s *unmergedSuite) TestAReplayedBranchIsMergedAfterTheBranchWentOnToChangeItsFile() {
	s.repo.Commit("shared", map[string]string{"shared.txt": "1\n2\n3\n4\n5\n6\n7\n8\n9\n"})
	s.repo.Git("switch", "-q", "-c", "w", s.main)
	s.repo.Commit("edit top", map[string]string{"shared.txt": "one\n2\n3\n4\n5\n6\n7\n8\n9\n"})
	s.repo.Git("switch", "-q", s.main)
	s.repo.Git("cherry-pick", "w")
	s.repo.Commit("edit bottom", map[string]string{"shared.txt": "one\n2\n3\n4\n5\n6\n7\n8\nnine\n"})

	s.Empty(s.unmerged(types.Job{ID: "w", State: types.StatePass}).Jobs)
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

	got := s.unmerged(types.Job{ID: "w", State: types.StatePass})

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

	s.Empty(s.unmerged(types.Job{ID: "w", State: types.StatePass}).Jobs)
}

func (s *unmergedSuite) TestACommitThatOnlySharesItsSubjectWithAnotherAuthorIsNotReplayed() {
	s.repo.Git("switch", "-q", "-c", "w", s.main)
	s.repo.Commit("write the line", map[string]string{"line.txt": "first\n"})
	s.repo.Git("switch", "-q", s.main)
	s.repo.Git("-c", "user.email=other@example.com", "commit", "-q", "--allow-empty", "-m", "write the line")

	s.Len(s.unmerged(types.Job{ID: "w", State: types.StatePass}).Jobs, 1)
}

func (s *unmergedSuite) TestAReadPastItsDeadlineNamesWhatItLeftAndTheNextCarriesOn() {
	s.branch("w")
	row := types.Job{ID: "w", State: types.StateExited}
	s.filed("w", &types.JobResult{ChangedPaths: []string{"w.txt"}})
	spent, cancel := context.WithCancel(s.T().Context())
	cancel()

	got := Unmerged(spent, s.repo.Root(), []types.Job{row}, s.tries)

	if got.Unknown == "" {
		s.Fail("a read that answered nothing must say so", "%+v", got)
	}
	s.Empty(got.Jobs, "nothing is claimed of a row it did not read")
	s.Len(s.unmerged(row).Jobs, 1)
}

func (s *unmergedSuite) TestAnAnswerForARowTheStoreLostIsDropped() {
	s.branch("gone")
	s.branch("w")
	gone := types.Job{ID: "gone", State: types.StatePass}
	kept := types.Job{ID: "w", State: types.StatePass}
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
	row := types.Job{ID: "w", State: types.StatePass}
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

// slowMissing answers MissingCommits after took, or gives up with its context.
type slowMissing struct {
	types.VCSDriver
	took time.Duration
}

func (r slowMissing) MissingCommits(ctx context.Context, dir, branch, head string) (int, error) {
	select {
	case <-time.After(r.took):
		return r.VCSDriver.MissingCommits(ctx, dir, branch, head)
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (s *unmergedSuite) readWith(ctx context.Context, took time.Duration, work ...types.Job) *unmergedRead {
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
	row := types.Job{ID: "w", State: types.StateExited}
	took := unmergedRowBudget + 200*time.Millisecond

	s.Equal(1, s.readWith(s.T().Context(), took, row).unread, "the first read cuts it at the row budget")

	second := s.readWith(s.T().Context(), took, row)
	s.Empty(second.unknown(), "the next read lets it spend what is left")
	s.Equal(1, second.rows["w"].fact.Commits)
}

func (s *unmergedSuite) TestARowARepeatedReadCutsIsNamedAsCutBefore() {
	head := s.repo.Git("rev-parse", s.branch("w"))
	row := types.Job{ID: "w", State: types.StateExited}
	line, err := json.Marshal(unmergedFact{ID: "w", Branch: s.main, Head: head, CutAt: 1})
	s.Require().NoError(err)
	path := unmergedFactsPath(s.repo.Root())
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(file.AppendLines(path, [][]byte{line}, 0o644))
	spent, cancel := context.WithTimeout(s.T().Context(), time.Second)
	defer cancel()

	got := s.readWith(spent, time.Minute, row).unknown()

	s.Contains(got, "1 of those a read had already cut")
}

func (s *unmergedSuite) TestAStoredMergeIsReadInsteadOfAskingAgain() {
	head := s.repo.Git("rev-parse", s.branch("w"))
	line, err := json.Marshal(unmergedFact{ID: "w", Branch: s.main, Head: head, Tip: s.repo.Git("rev-parse", "HEAD"), Merged: true, MergedAt: time.Now().Unix()})
	s.Require().NoError(err)
	path := unmergedFactsPath(s.repo.Root())
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(file.AppendLines(path, [][]byte{line}, 0o644))

	s.Empty(s.unmerged(types.Job{ID: "w", State: types.StatePass}).Jobs)
}

func (s *unmergedSuite) TestAStoredMergeOfAnOlderHeadStandsForNothing() {
	head := s.repo.Git("rev-parse", s.branch("w"))
	s.Empty(s.unmerged(types.Job{ID: "other", State: types.StatePass}).Jobs)
	line, err := json.Marshal(unmergedFact{ID: "w", Branch: s.main, Head: head + "0", Tip: s.repo.Git("rev-parse", "HEAD"), Merged: true})
	s.Require().NoError(err)
	path := unmergedFactsPath(s.repo.Root())
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(file.AppendLines(path, [][]byte{line}, 0o644))

	s.Len(s.unmerged(types.Job{ID: "w", State: types.StatePass}).Jobs, 1)
}

func (s *unmergedSuite) TestARowEndedWithWorkOnItsBranchAndNoStatedReasonCounts() {
	s.branch("w")

	got := s.unmerged(types.Job{ID: "w", State: types.StateNoReturn, EndReason: "abandoned by its holder, which filed no result"})

	s.Require().Len(got.Jobs, 1)
	s.Equal("w", got.Jobs[0].Job)
}

func (s *unmergedSuite) TestAStatedReasonLeavesTheRowOut() {
	s.branch("w")

	got := s.unmerged(types.Job{ID: "w", State: types.StateNoReturn, LeftUnmerged: "the approach was dropped"})

	s.Empty(got.Jobs)
}

func (s *unmergedSuite) TestARowALandingProbeEndedIsLeftOut() {
	s.branch("w")

	got := s.unmerged(types.Job{ID: "w", State: types.StateNoReturn, Landed: true})

	s.Empty(got.Jobs)
}

func (s *unmergedSuite) TestAnEndReasonThatOnlyReadsLikeALandingIsStillChecked() {
	s.branch("w")

	got := s.unmerged(types.Job{ID: "w", State: types.StateNoReturn, EndReason: "its work landed on main at abc"})

	s.Len(got.Jobs, 1)
}

func (s *unmergedSuite) TestARunningRowIsStillWorking() {
	s.branch("w")

	s.Empty(s.unmerged(types.Job{ID: "w", State: types.StateRunning}).Jobs)
}

func (s *unmergedSuite) TestAFiledCheckoutHoldingUncommittedChangesCounts() {
	where := filepath.Join(s.T().TempDir(), "w")
	s.repo.Git("worktree", "add", "-q", where, "-b", "w")
	s.Require().NoError(os.WriteFile(filepath.Join(where, "w.txt"), []byte("dirty\n"), 0o644))
	row := types.Job{ID: "w", State: types.StateExited, WritePaths: []string{"w.txt"}, CheckoutRoot: where}
	s.filed("w", &types.JobResult{ChangedPaths: []string{"w.txt"}})

	got := s.unmerged(row)

	s.Require().Len(got.Jobs, 1)
	s.NotEmpty(got.Jobs[0].Head)
	s.Contains(got.Jobs[0].Why, "1 uncommitted path(s), first w.txt")
}

func (s *unmergedSuite) TestAFiledRowWithNoCheckoutOrBranchIsNeverReadAsMerged() {
	s.filed("gone", &types.JobResult{ChangedPaths: []string{"a.go"}})
	got := s.unmerged(types.Job{ID: "gone", State: types.StatePass})

	s.Require().Len(got.Jobs, 1)
	s.Empty(got.Jobs[0].Head)
	s.Contains(got.Jobs[0].Why, "cannot be read")
}

func (s *unmergedSuite) TestAnEndedRowThatFiledNoPathAndLeftNoBranchHasNothingToLose() {
	s.Empty(s.unmerged(types.Job{ID: "gone", State: types.StateNoReturn}).Jobs)
}

func (s *unmergedSuite) TestAGoneHeadAPassedIntegrationNamesIsMerged() {
	s.filed("gone", &types.JobResult{ChangedPaths: []string{"a.go"}})
	got := s.unmerged(
		types.Job{ID: "gone", State: types.StatePass},
		types.Job{ID: "merge", State: types.StatePass, Integrates: []string{"gone"}, ReadOnly: true},
	)

	s.Empty(got.Jobs)
}

func (s *unmergedSuite) TestADetachedCheckoutIsUnknownNeverMerged() {
	s.branch("w")
	s.repo.Git("switch", "-q", "--detach")

	got := s.unmerged(types.Job{ID: "w", State: types.StatePass})

	s.Contains(got.Unknown, "no branch checked out")
	s.Empty(got.Jobs)
}

func (s *unmergedSuite) TestAFollowUpStandsUntilAnswered() {
	row := types.Job{ID: "r", State: types.StatePass, ReadOnly: true}
	s.filed("r", &types.JobResult{FollowUps: []string{"a follow-up"}})

	got := s.unmerged(row)

	s.Require().Len(got.Jobs, 1)
	s.Equal("a follow-up", got.Jobs[0].Item)
	s.Empty(got.Risks)
}

func (s *unmergedSuite) TestARowWithOnlyUnresolvedRisksHasNothingUnmerged() {
	row := types.Job{ID: "r", State: types.StatePass, ReadOnly: true}
	s.filed("r", &types.JobResult{UnresolvedRisks: []string{"a risk"}})

	got := s.unmerged(row)

	s.Empty(got.Jobs)
	s.Require().Len(got.Risks, 1)
	s.Equal("a risk", got.Risks[0].Item)
}

func (s *unmergedSuite) TestAnAnsweredRiskLeavesTheList() {
	row := types.Job{ID: "r", State: types.StatePass, ReadOnly: true,
		Deferred: []types.JobDeferred{{Item: "a risk", Reason: "accepted"}}}
	s.filed("r", &types.JobResult{UnresolvedRisks: []string{"a risk"}})

	s.Empty(s.unmerged(row).Risks)
}

func (s *unmergedSuite) TestAReasonOrADeclaredRowAnswersAnItem() {
	row := types.Job{ID: "r", State: types.StatePass, ReadOnly: true,
		Deferred: []types.JobDeferred{{Item: "a risk", Reason: "accepted"}, {Item: "a follow-up", Job: "next"}}}
	s.filed("r", &types.JobResult{UnresolvedRisks: []string{"a risk"}, FollowUps: []string{"a follow-up"}})

	got := s.unmerged(row, types.Job{ID: "next", State: types.StateDeclared})

	s.Empty(got.Jobs)
	s.Empty(got.Risks)
}

func (s *unmergedSuite) TestAnAnswerNamingAMissingRowLeavesTheItemStanding() {
	row := types.Job{ID: "r", State: types.StatePass, ReadOnly: true,
		Deferred: []types.JobDeferred{{Item: "a follow-up", Job: "gone"}}}
	s.filed("r", &types.JobResult{FollowUps: []string{"a follow-up"}})

	s.Len(s.unmerged(row).Jobs, 1)
}

func (s *unmergedSuite) TestAnotherCheckoutsTreeIsNotThisOnes() {
	s.branch("w")
	peer := types.Job{ID: "w", State: types.StatePass, Worktree: &types.JobWorktree{Source: s.T().TempDir()}}

	s.Empty(s.unmerged(peer).Jobs)
}

func (s *unmergedSuite) settle(rec types.Declaration) ([]types.Job, error) {
	rec.Schema = types.Schema{Version: types.JobAPIVersion}
	return s.store.Settle(s.T().Context(), []types.Declaration{rec}, false)
}

func (s *unmergedSuite) TestSettleWritesAnswersOntoAnEndedRowAndKeepsItsState() {
	want := seed(s.T(), s.store, types.Job{ID: "r", State: types.StatePass})
	fileTry(s.T(), s.store, "r", &types.JobResult{FollowUps: []string{"later"}})
	want.Deferred = []types.JobDeferred{{Item: "later", Reason: "not worth it"}}

	got, err := s.settle(types.Declaration{ID: "r", Deferred: want.Deferred})

	s.Require().NoError(err)
	s.Require().Len(got, 1)
	want.Updated = got[0].Updated
	s.Equal(want, got[0])
}

func (s *unmergedSuite) TestSettleRecordsAStatedReason() {
	seed(s.T(), s.store, types.Job{ID: "r", State: types.StateNoReturn})

	got, err := s.settle(types.Declaration{ID: "r", LeftUnmerged: "dropped"})

	s.Require().NoError(err)
	s.Equal("dropped", got[0].LeftUnmerged)
}

func (s *unmergedSuite) TestSettleRefusesAnItemTheResultDoesNotDefer() {
	seed(s.T(), s.store, types.Job{ID: "r", State: types.StatePass})
	fileTry(s.T(), s.store, "r", &types.JobResult{FollowUps: []string{"later"}})

	_, err := s.settle(types.Declaration{ID: "r", Deferred: []types.JobDeferred{{Item: "typo", Reason: "x"}}})

	s.Require().ErrorContains(err, `defers no item "typo"`)
}

func (s *unmergedSuite) TestSettleRefusesARowNothingDeclares() {
	seed(s.T(), s.store, types.Job{ID: "r", State: types.StatePass})
	fileTry(s.T(), s.store, "r", &types.JobResult{FollowUps: []string{"later"}})

	_, err := s.settle(types.Declaration{ID: "r", Deferred: []types.JobDeferred{{Item: "later", Job: "gone"}}})

	s.Require().ErrorContains(err, "which nothing declares")
}

func (s *unmergedSuite) TestSettleRefusesARecordAlsoDeclaringASpec() {
	seed(s.T(), s.store, types.Job{ID: "r", State: types.StatePass})

	_, err := s.settle(types.Declaration{ID: "r", LeftUnmerged: "dropped", Agent: agent.Instructed("more")})

	s.Require().ErrorContains(err, "settles a job and declares nothing")
}

func (s *unmergedSuite) TestSettleRefusesBothARowAndAReason() {
	seed(s.T(), s.store, types.Job{ID: "r", State: types.StatePass})
	fileTry(s.T(), s.store, "r", &types.JobResult{FollowUps: []string{"later"}})

	_, err := s.settle(types.Declaration{ID: "r", Deferred: []types.JobDeferred{{Item: "later", Job: "r", Reason: "x"}}})

	s.Require().ErrorContains(err, "neither or both")
}

func (s *unmergedSuite) TestSettleRefusesALeaseHolder() {
	held := NewStore(Location{Root: s.repo.Root(), StateBase: s.T().TempDir(), Actor: &Actor{Lease: "worker"}})

	_, err := held.Settle(s.T().Context(), []types.Declaration{{ID: "r", LeftUnmerged: "dropped"}}, false)

	s.Require().ErrorContains(err, "only its orchestrator")
}
