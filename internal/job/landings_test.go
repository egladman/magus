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

type unmergedSuite struct {
	suite.Suite
	store *Store
	repo  *testkit.Repo
	main  string
}

func TestUnmerged(t *testing.T) { suite.Run(t, new(unmergedSuite)) }

func (s *unmergedSuite) SetupTest() {
	s.store, _, s.repo = worktreeStore(s.T())
	s.main = s.repo.Git("symbolic-ref", "--short", "HEAD")
}

func (s *unmergedSuite) branch(name string) string {
	s.repo.Git("switch", "-q", "-c", name, s.main)
	s.repo.Commit(name, map[string]string{name + ".txt": name + "\n"})
	s.repo.Git("switch", "-q", s.main)
	return name
}

func (s *unmergedSuite) unmerged(rows ...types.Job) types.JobUnmergedReport {
	return Unmerged(s.T().Context(), s.repo.Root(), rows)
}

func (s *unmergedSuite) TestAFiledRowWithCommitsOffTheBranchIsNamedWithItsHeadAndRemedy() {
	head := s.repo.Git("rev-parse", "--short=12", s.branch("w"))
	result := &types.JobResult{ChangedPaths: []string{"w.txt"}}

	got := s.unmerged(types.Job{ID: "w", State: types.StateExited, Result: result})

	s.Equal(s.main, got.Branch)
	s.Require().Len(got.Rows, 1)
	s.Equal("w", got.Rows[0].Job)
	s.True(strings.HasPrefix(head, got.Rows[0].Head), got.Rows[0].Head)
	s.Contains(got.Rows[0].Why, "not on "+s.main)
	s.Contains(got.Rows[0].Remedy, fmt.Sprintf("merge %s into %s", got.Rows[0].Head, s.main))
	s.Contains(got.Rows[0].Remedy, "left_unmerged")
}

func (s *unmergedSuite) TestAnAncestorOfTheBranchIsMerged() {
	s.branch("w")
	s.repo.Git("merge", "-q", "--no-ff", "-m", "merge", "w")

	got := s.unmerged(types.Job{ID: "w", State: types.StatePass, Result: &types.JobResult{ChangedPaths: []string{"w.txt"}}})

	s.Empty(got.Rows)
	s.Empty(got.Unknown)
}

func (s *unmergedSuite) TestASquashedBranchIsMergedByContent() {
	s.branch("w")
	s.repo.Commit("squash", map[string]string{"w.txt": "w\n"})

	s.Empty(s.unmerged(types.Job{ID: "w", State: types.StatePass}).Rows)
}

func (s *unmergedSuite) TestAReplayedBranchIsMergedAfterTheBranchWentOnToChangeItsFile() {
	s.repo.Commit("shared", map[string]string{"shared.txt": "1\n2\n3\n4\n5\n6\n7\n8\n9\n"})
	s.repo.Git("switch", "-q", "-c", "w", s.main)
	s.repo.Commit("edit top", map[string]string{"shared.txt": "one\n2\n3\n4\n5\n6\n7\n8\n9\n"})
	s.repo.Git("switch", "-q", s.main)
	s.repo.Git("cherry-pick", "w")
	s.repo.Commit("edit bottom", map[string]string{"shared.txt": "one\n2\n3\n4\n5\n6\n7\n8\nnine\n"})

	s.Empty(s.unmerged(types.Job{ID: "w", State: types.StatePass}).Rows)
}

func (s *unmergedSuite) TestAReplayedCommitIsMergedAfterTheBranchRewroteItsLine() {
	s.repo.Git("switch", "-q", "-c", "w", s.main)
	s.repo.Commit("write the line", map[string]string{"line.txt": "first\n"})
	s.repo.Git("switch", "-q", s.main)
	s.repo.Git("cherry-pick", "w")
	s.repo.Commit("rewrite the line", map[string]string{"line.txt": "second\n"})

	s.Empty(s.unmerged(types.Job{ID: "w", State: types.StatePass}).Rows)
}

func (s *unmergedSuite) TestACommitThatOnlySharesItsSubjectWithAnotherAuthorIsNotReplayed() {
	s.repo.Git("switch", "-q", "-c", "w", s.main)
	s.repo.Commit("write the line", map[string]string{"line.txt": "first\n"})
	s.repo.Git("switch", "-q", s.main)
	s.repo.Git("-c", "user.email=other@example.com", "commit", "-q", "--allow-empty", "-m", "write the line")

	s.Len(s.unmerged(types.Job{ID: "w", State: types.StatePass}).Rows, 1)
}

func (s *unmergedSuite) TestAReadPastItsDeadlineNamesWhatItLeftAndTheNextCarriesOn() {
	s.branch("w")
	row := types.Job{ID: "w", State: types.StateExited, Result: &types.JobResult{ChangedPaths: []string{"w.txt"}}}
	spent, cancel := context.WithCancel(s.T().Context())
	cancel()

	got := Unmerged(spent, s.repo.Root(), []types.Job{row})

	if got.Unknown == "" {
		s.Fail("a read that answered nothing must say so", "%+v", got)
	}
	s.Empty(got.Rows, "nothing is claimed of a row it did not read")
	s.Len(s.unmerged(row).Rows, 1)
}

func (s *unmergedSuite) TestAStoredMergeIsReadInsteadOfAskingAgain() {
	head := s.repo.Git("rev-parse", s.branch("w"))
	line, err := json.Marshal(unmergedFact{ID: "w", Branch: s.main, Head: head, Tip: s.repo.Git("rev-parse", "HEAD"), Merged: true, MergedAt: time.Now().Unix()})
	s.Require().NoError(err)
	path := unmergedFactsPath(s.repo.Root())
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(file.AppendLines(path, [][]byte{line}, 0o644))

	s.Empty(s.unmerged(types.Job{ID: "w", State: types.StatePass}).Rows)
}

func (s *unmergedSuite) TestAStoredMergeOfAnOlderHeadStandsForNothing() {
	head := s.repo.Git("rev-parse", s.branch("w"))
	s.Empty(s.unmerged(types.Job{ID: "other", State: types.StatePass}).Rows)
	line, err := json.Marshal(unmergedFact{ID: "w", Branch: s.main, Head: head + "0", Tip: s.repo.Git("rev-parse", "HEAD"), Merged: true})
	s.Require().NoError(err)
	path := unmergedFactsPath(s.repo.Root())
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(file.AppendLines(path, [][]byte{line}, 0o644))

	s.Len(s.unmerged(types.Job{ID: "w", State: types.StatePass}).Rows, 1)
}

func (s *unmergedSuite) TestARowEndedWithWorkOnItsBranchAndNoStatedReasonCounts() {
	s.branch("w")

	got := s.unmerged(types.Job{ID: "w", State: types.StateNoReturn, EndReason: "abandoned by its holder, which filed no result"})

	s.Require().Len(got.Rows, 1)
	s.Equal("w", got.Rows[0].Job)
}

func (s *unmergedSuite) TestAStatedReasonLeavesTheRowOut() {
	s.branch("w")

	got := s.unmerged(types.Job{ID: "w", State: types.StateNoReturn, LeftUnmerged: "the approach was dropped"})

	s.Empty(got.Rows)
}

func (s *unmergedSuite) TestARowALandingProbeEndedIsLeftOut() {
	s.branch("w")

	got := s.unmerged(types.Job{ID: "w", State: types.StateNoReturn, EndReason: landedReasonPrefix + "on main at abc"})

	s.Empty(got.Rows)
}

func (s *unmergedSuite) TestARunningRowIsStillWorking() {
	s.branch("w")

	s.Empty(s.unmerged(types.Job{ID: "w", State: types.StateRunning}).Rows)
}

func (s *unmergedSuite) TestAFiledCheckoutHoldingUncommittedChangesCounts() {
	where := filepath.Join(s.T().TempDir(), "w")
	s.repo.Git("worktree", "add", "-q", where, "-b", "w")
	s.Require().NoError(os.WriteFile(filepath.Join(where, "w.txt"), []byte("dirty\n"), 0o644))
	row := types.Job{ID: "w", State: types.StateExited, WritePaths: []string{"w.txt"}, CheckoutRoot: where,
		Result: &types.JobResult{ChangedPaths: []string{"w.txt"}}}

	got := s.unmerged(row)

	s.Require().Len(got.Rows, 1)
	s.NotEmpty(got.Rows[0].Head)
	s.Contains(got.Rows[0].Why, "1 uncommitted path(s), first w.txt")
}

func (s *unmergedSuite) TestAFiledRowWithNoCheckoutOrBranchIsNeverReadAsMerged() {
	got := s.unmerged(types.Job{ID: "gone", State: types.StatePass, Result: &types.JobResult{ChangedPaths: []string{"a.go"}}})

	s.Require().Len(got.Rows, 1)
	s.Empty(got.Rows[0].Head)
	s.Contains(got.Rows[0].Why, "cannot be read")
}

func (s *unmergedSuite) TestAnEndedRowThatFiledNoPathAndLeftNoBranchHasNothingToLose() {
	s.Empty(s.unmerged(types.Job{ID: "gone", State: types.StateNoReturn}).Rows)
}

func (s *unmergedSuite) TestAGoneHeadAPassedIntegrationNamesIsMerged() {
	got := s.unmerged(
		types.Job{ID: "gone", State: types.StatePass, Result: &types.JobResult{ChangedPaths: []string{"a.go"}}},
		types.Job{ID: "merge", State: types.StatePass, Integrates: []string{"gone"}, ReadOnly: true},
	)

	s.Empty(got.Rows)
}

func (s *unmergedSuite) TestADetachedCheckoutIsUnknownNeverMerged() {
	s.branch("w")
	s.repo.Git("switch", "-q", "--detach")

	got := s.unmerged(types.Job{ID: "w", State: types.StatePass})

	s.Contains(got.Unknown, "no branch checked out")
	s.Empty(got.Rows)
}

func (s *unmergedSuite) TestAFollowUpStandsUntilAnswered() {
	row := types.Job{ID: "r", State: types.StatePass, ReadOnly: true,
		Result: &types.JobResult{FollowUps: []string{"a follow-up"}}}

	got := s.unmerged(row)

	s.Require().Len(got.Rows, 1)
	s.Equal("a follow-up", got.Rows[0].Item)
	s.Empty(got.Risks)
}

func (s *unmergedSuite) TestARowWithOnlyUnresolvedRisksHasNothingUnmerged() {
	row := types.Job{ID: "r", State: types.StatePass, ReadOnly: true,
		Result: &types.JobResult{UnresolvedRisks: []string{"a risk"}}}

	got := s.unmerged(row)

	s.Empty(got.Rows)
	s.Require().Len(got.Risks, 1)
	s.Equal("a risk", got.Risks[0].Item)
}

func (s *unmergedSuite) TestAnAnsweredRiskLeavesTheList() {
	row := types.Job{ID: "r", State: types.StatePass, ReadOnly: true,
		Result:   &types.JobResult{UnresolvedRisks: []string{"a risk"}},
		Deferred: []types.JobDeferred{{Item: "a risk", Reason: "accepted"}}}

	s.Empty(s.unmerged(row).Risks)
}

func (s *unmergedSuite) TestAReasonOrADeclaredRowAnswersAnItem() {
	row := types.Job{ID: "r", State: types.StatePass, ReadOnly: true,
		Result:   &types.JobResult{UnresolvedRisks: []string{"a risk"}, FollowUps: []string{"a follow-up"}},
		Deferred: []types.JobDeferred{{Item: "a risk", Reason: "accepted"}, {Item: "a follow-up", Row: "next"}}}

	got := s.unmerged(row, types.Job{ID: "next", State: types.StateDeclared})

	s.Empty(got.Rows)
	s.Empty(got.Risks)
}

func (s *unmergedSuite) TestAnAnswerNamingAMissingRowLeavesTheItemStanding() {
	row := types.Job{ID: "r", State: types.StatePass, ReadOnly: true,
		Result:   &types.JobResult{FollowUps: []string{"a follow-up"}},
		Deferred: []types.JobDeferred{{Item: "a follow-up", Row: "gone"}}}

	s.Len(s.unmerged(row).Rows, 1)
}

func (s *unmergedSuite) TestAnotherCheckoutsTreeIsNotThisOnes() {
	s.branch("w")
	peer := types.Job{ID: "w", State: types.StatePass, Worktree: &types.JobWorktree{Source: s.T().TempDir()}}

	s.Empty(s.unmerged(peer).Rows)
}

func (s *unmergedSuite) settle(rec types.Declaration) ([]types.Job, error) {
	rec.Schema = types.Schema{Version: types.JobSchemaVersion}
	return s.store.Settle(s.T().Context(), []types.Declaration{rec}, false)
}

func (s *unmergedSuite) TestSettleWritesAnswersOntoAnEndedRowAndKeepsItsState() {
	want := seed(s.T(), s.store, types.Job{ID: "r", State: types.StatePass, Result: &types.JobResult{FollowUps: []string{"later"}}})
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
	seed(s.T(), s.store, types.Job{ID: "r", State: types.StatePass, Result: &types.JobResult{FollowUps: []string{"later"}}})

	_, err := s.settle(types.Declaration{ID: "r", Deferred: []types.JobDeferred{{Item: "typo", Reason: "x"}}})

	s.Require().ErrorContains(err, `defers no item "typo"`)
}

func (s *unmergedSuite) TestSettleRefusesARowNothingDeclares() {
	seed(s.T(), s.store, types.Job{ID: "r", State: types.StatePass, Result: &types.JobResult{FollowUps: []string{"later"}}})

	_, err := s.settle(types.Declaration{ID: "r", Deferred: []types.JobDeferred{{Item: "later", Row: "gone"}}})

	s.Require().ErrorContains(err, "which nothing declares")
}

func (s *unmergedSuite) TestSettleRefusesARecordAlsoDeclaringASpec() {
	seed(s.T(), s.store, types.Job{ID: "r", State: types.StatePass})

	_, err := s.settle(types.Declaration{ID: "r", LeftUnmerged: "dropped", Criteria: "more"})

	s.Require().ErrorContains(err, "settles a job and declares nothing")
}

func (s *unmergedSuite) TestSettleRefusesBothARowAndAReason() {
	seed(s.T(), s.store, types.Job{ID: "r", State: types.StatePass, Result: &types.JobResult{FollowUps: []string{"later"}}})

	_, err := s.settle(types.Declaration{ID: "r", Deferred: []types.JobDeferred{{Item: "later", Row: "r", Reason: "x"}}})

	s.Require().ErrorContains(err, "neither or both")
}

func (s *unmergedSuite) TestSettleRefusesALeaseHolder() {
	held := NewStore(Location{Root: s.repo.Root(), StateBase: s.T().TempDir(), Actor: &Actor{Lease: "worker"}})

	_, err := held.Settle(s.T().Context(), []types.Declaration{{ID: "r", LeftUnmerged: "dropped"}}, false)

	s.Require().ErrorContains(err, "only its orchestrator")
}
