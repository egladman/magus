package job

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/ci/volatility"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// outcomeStore is a Store whose workspace names its own history file, so a test reads back
// exactly what the store recorded.
func outcomeStore(t *testing.T, set *tier.Set, matrix func(context.Context, types.ModelTierRequest) (*tier.Assignment, error), dryRun bool) (*Store, string) {
	t.Helper()
	root := execRepository(t).Root()
	history := filepath.Join(t.TempDir(), "history.json")
	require.NoError(t, os.WriteFile(filepath.Join(root, config.Filename), []byte(fmt.Sprintf("history_path: %s\n", strconv.Quote(history))), 0o644))
	loc := tempLocation(t, root)
	loc.ModelTiers, loc.ModelTierMatrix, loc.SpawnRule, loc.DryRun = set, matrix, tierWithin, dryRun
	return NewStore(loc), history
}

func recordedJobs(t *testing.T, history string) []types.JobOutcome {
	t.Helper()
	var h forecast.History
	require.NoError(t, h.Load(t.Context(), history))
	return h.Jobs
}

func endWith(t *testing.T, s *Store, row job.Job, state job.State) job.Job {
	t.Helper()
	row.State = state
	return seed(t, s, row)
}

func TestNewJobOutcomeReadsTheEndedRow(t *testing.T) {
	t.Parallel()

	row := lease("a")
	row.Agent.Model = job.Model{ID: "sonnet"}
	row.State = job.StateFail
	row.Created = 100
	row.Check = &job.LeaseCheck{Target: "test", Project: "."}
	row.Paths.Write = []string{"pkg/a.go#func A", "pkg/a.go#func B", "pkg/b.go"}
	row.Attempt = &job.CapturedRun{}
	row.GateAttempts = []job.GateAttempt{{}, {}}
	row.ReworkRounds, row.Widenings = 2, 1
	row.ModelTier = &tier.Assignment{
		Name: "routine", Rank: 0, Skills: tier.SkillVariantFull, Source: tier.SourceMatrix,
		Reason: "small checked job", Site: "magusfile.buzz:20", Model: "haiku",
	}

	got := newJobOutcome(row, nil, 160_000)

	assert.Equal(t, types.JobOutcome{
		Job: "a", ModelTier: "routine", Kind: types.JobKindCheck, Skills: tier.SkillVariantFull,
		Model: "haiku", Source: tier.SourceMatrix, Reason: "small checked job", Site: "magusfile.buzz:20",
		WriteFiles: 2, State: job.StateFail, ChecksRun: 3, DurationMs: 60_000, Ended: 160_000,
		ReworkRounds: 2, Widenings: 1, Attempt: 1,
	}, got)
}

// The ending's attempt number and its classification ride the outcome,
// from the row and its tries.
func TestNewJobOutcomeNumbersTheAttemptAndClassifiesIt(t *testing.T) {
	t.Parallel()

	row := lease("a")
	row.State = job.StateFail
	row.Rejection = &job.Rejection{Unmet: []job.VerificationStatus{{ID: "check"}}}
	tries := job.NewAttempts([]job.Attempt{{Job: "a", N: 1, Status: job.AttemptStatus{
		State: job.StateFail, Unmet: []job.VerificationStatus{{ID: "check"}}}}})

	got := newJobOutcome(row, tries, 5_000)

	want := got
	want.Attempt = 2
	want.Retry = job.RetryAfterPerson // a goal unmet twice needs a person
	assert.Equal(t, want, got)
}

func TestNewJobOutcomeOfARowWithNoTierNamesNone(t *testing.T) {
	t.Parallel()

	row := lease("a")
	row.State = job.StatePass

	got := newJobOutcome(row, nil, 5)

	assert.Empty(t, got.ModelTier)
	assert.Equal(t, "bounded", got.Model, "the model the row named")
	assert.Zero(t, got.DurationMs, "a row with no creation time has no duration")
}

func outcome(id, name string, kind types.JobKind, state job.State) types.JobOutcome {
	return types.JobOutcome{Job: id, ModelTier: name, Kind: kind, State: state}
}

func TestModelTierRecordsFoldsEachTierAndKind(t *testing.T) {
	t.Parallel()

	routinePass := func(id string) types.JobOutcome {
		o := outcome(id, "routine", types.JobKindCheck, job.StatePass)
		o.ChecksRun, o.CostUSD, o.Tokens = 2, new(0.5), new(int64(100))
		return o
	}
	failed := outcome("a", "routine", types.JobKindCheck, job.StateFail)
	failed.ChecksRun, failed.CostUSD, failed.Tokens, failed.ReworkRounds, failed.Widenings = 4, new(1.5), new(int64(300)), 3, 1
	outcomes := []types.JobOutcome{
		routinePass("a"), failed, routinePass("b"),
		outcome("c", "routine", types.JobKindProse, job.StateNoReturn),
		outcome("d", "design", types.JobKindCheck, job.StatePass),
		outcome("e", "retired", types.JobKindCheck, job.StatePass),
		outcome("f", "", types.JobKindCheck, job.StatePass),
		outcome("g", "routine", types.JobKindCheck, job.StateRunning),
	}

	got := ModelTierRecords(outcomes, tierSet(t))

	require.Len(t, got, 4)
	// Within a delta: arm64 may fuse the bound's multiply-add differently once inlined, so
	// the same call can differ in the last bit.
	assert.InDelta(t, volatility.WilsonLower(2, 3), got[0].PassLower, 1e-12)
	assert.InDelta(t, volatility.WilsonLower(1, 1), got[3].PassLower, 1e-12)
	assert.InDelta(t, volatility.WilsonLower(2, 3), got[0].FirstPassLower, 1e-12)
	assert.InDelta(t, volatility.WilsonLower(1, 1), got[3].FirstPassLower, 1e-12)
	got[0].PassLower, got[3].PassLower, got[0].FirstPassLower, got[3].FirstPassLower = 0, 0, 0, 0
	assert.Equal(t, types.ModelTierRecord{
		Rank: 0, ModelTier: "routine", Kind: types.JobKindCheck,
		Jobs: 3, Pass: 2, Fail: 1,
		ChecksPerJob: 8.0 / 3, ForksPerID: 3.0 / 2, ReworkPerJob: 1, WideningsPerJob: 1.0 / 3,
		MeanCostUSD: new(2.5 / 3), MedianTokens: new(int64(100)), FirstAttempts: 3, FirstPass: 2,
	}, got[0], "a was forked twice, and the running and untiered outcomes do not count")
	assert.Nil(t, got[2].MeanCostUSD, "no outcome carries a cost, so it is unknown and not 0")
	assert.Nil(t, got[2].MedianTokens)
	wantProse := got[1]
	wantProse.ModelTier, wantProse.Kind, wantProse.NoReturn = "routine", types.JobKindProse, 1
	assert.Equal(t, wantProse, got[1])
	assert.Zero(t, got[1].PassLower)
	assert.Equal(t, 2, got[2].Rank, "design ranks after routine")
	assert.Equal(t, types.ModelTierRecord{Rank: -1, ModelTier: "retired", Kind: types.JobKindCheck, Jobs: 1, Pass: 1, ChecksPerJob: 0, ForksPerID: 1,
		FirstAttempts: 1, FirstPass: 1}, got[3],
		"a tier the declaration dropped keeps its record, last, at rank -1")
}

// A first attempt is attempt 1 or an outcome recorded before attempts were numbered; a later
// attempt counts toward the pass rate and not the first-attempt one. An ending classified as
// needing a change counts as not retryable whatever its state.
func TestModelTierRecordsCountsFirstAttemptsAndNotRetryableEndings(t *testing.T) {
	t.Parallel()

	attempt := func(id string, n int, state job.State, retry job.RetryChange) types.JobOutcome {
		o := outcome(id, "routine", types.JobKindCheck, state)
		o.Attempt, o.Retry = n, retry
		return o
	}
	got := ModelTierRecords([]types.JobOutcome{
		attempt("a", 1, job.StateFail, job.RetryAfterWidening),
		attempt("a", 2, job.StatePass, job.RetryAsIs),
		attempt("b", 0, job.StatePass, job.RetryAsIs),
		attempt("c", 3, job.StateFail, job.RetryAfterPerson),
	}, tierSet(t))

	require.Len(t, got, 1)
	wantFirst := got[0]
	wantFirst.Jobs, wantFirst.FirstAttempts, wantFirst.FirstPass, wantFirst.NotRetryable = 4, 2, 1, 2
	assert.Equal(t, wantFirst, got[0])
	assert.InDelta(t, volatility.WilsonLower(1, 2), got[0].FirstPassLower, 1e-12)
	assert.InDelta(t, 4.0/3, got[0].ForksPerID, 1e-12, "forks per id keeps its meaning")
}

func TestModelTierRecordsWithoutADeclarationRanksEveryTierMinusOne(t *testing.T) {
	t.Parallel()

	got := ModelTierRecords([]types.JobOutcome{outcome("a", "routine", types.JobKindCheck, job.StatePass)}, nil)

	require.Len(t, got, 1)
	assert.Equal(t, -1, got[0].Rank)
	assert.Empty(t, ModelTierRecords(nil, tierSet(t)), "no outcomes is no rows, not an error")
}

func TestModelTierRecordsMedianOfAnEvenCountIsTheMiddlePairsMean(t *testing.T) {
	t.Parallel()

	var outcomes []types.JobOutcome
	for i, tokens := range []int64{400, 100, 300, 200} {
		o := outcome("job"+strconv.Itoa(i), "routine", types.JobKindCheck, job.StatePass)
		o.Tokens = &tokens
		outcomes = append(outcomes, o)
	}

	assert.Equal(t, new(int64(250)), ModelTierRecords(outcomes, tierSet(t))[0].MedianTokens)
}

// One outcome without a figure makes the group's figure unknown: a mean over the others
// would read as the group's.
func TestModelTierRecordsFigureSomeOutcomesLackIsUnknown(t *testing.T) {
	t.Parallel()

	known := outcome("a", "routine", types.JobKindCheck, job.StatePass)
	known.CostUSD, known.Tokens = new(1.0), new(int64(100))
	got := ModelTierRecords([]types.JobOutcome{known, outcome("b", "routine", types.JobKindCheck, job.StatePass)}, tierSet(t))

	require.Len(t, got, 1)
	assert.Nil(t, got[0].MeanCostUSD)
	assert.Nil(t, got[0].MedianTokens)
}

func TestStoreRecordsAnOutcomeWhenARowEnds(t *testing.T) {
	t.Parallel()

	s, history := outcomeStore(t, tierSet(t), nil, false)
	row := lease("a")
	row.Agent.Model = job.Model{}
	running := endWith(t, s, row, job.StateRunning)
	require.Empty(t, recordedJobs(t, history), "a row that is still taken has no outcome")

	row = running
	ended := endWith(t, s, row, job.StateFail)

	jobs := recordedJobs(t, history)
	require.Len(t, jobs, 1)
	wantJob := newJobOutcome(ended, nil, jobs[0].Ended)
	wantJob.ModelTier, wantJob.State = "bounded", job.StateFail
	assert.Equal(t, wantJob, jobs[0])
	assert.Positive(t, jobs[0].Ended)

	row.Agent = job.Instructed("reworded after it failed")
	endWith(t, s, row, job.StateFail)
	assert.Len(t, recordedJobs(t, history), 1, "a row that was already over records nothing again")
}

func TestStoreRecordsNothingForADryRunOrAnUntieredRow(t *testing.T) {
	t.Parallel()

	dry, dryHistory := outcomeStore(t, tierSet(t), nil, true)
	row := lease("a")
	row.Agent.Model = job.Model{}
	endWith(t, dry, endWith(t, dry, row, job.StateRunning), job.StatePass)
	assert.NoFileExists(t, dryHistory, "a dry run writes no history")

	untiered, untieredHistory := outcomeStore(t, nil, nil, false)
	endWith(t, untiered, endWith(t, untiered, row, job.StateRunning), job.StatePass)
	assert.NoFileExists(t, untieredHistory, "a workspace with no tiers has nothing for the lens to rank")
}

func TestStoreRefusesToEndARowWhoseConfigDoesNotRead(t *testing.T) {
	t.Parallel()

	s, _ := outcomeStore(t, tierSet(t), nil, false)
	row := lease("a")
	row.Agent.Model = job.Model{}
	running := endWith(t, s, row, job.StateRunning)
	require.NoError(t, os.WriteFile(filepath.Join(s.root, config.Filename), []byte("history_path: [not, a, path]\n"), 0o644))

	running.State = job.StatePass
	_, err := s.Update(t.Context(), running.ID, func(cur *job.Job) { *cur = running })

	require.Error(t, err)
	assert.ErrorContains(t, err, "history_path")
	rows, lerr := s.List(t.Context())
	require.NoError(t, lerr)
	assert.Equal(t, job.StateRunning, rows[0].State, "the row did not move, so the outcome is not lost")
}

func TestStoreFeedsRecordedOutcomesToTheMatrix(t *testing.T) {
	t.Parallel()

	matrix := countingMatrix{answer: routineAnswer()}
	s, _ := outcomeStore(t, tierSet(t), matrix.choose, false)
	first := lease("a")
	first.Agent.Model = job.Model{}
	endWith(t, s, endWith(t, s, first, job.StateRunning), job.StatePass)
	require.Empty(t, matrix.seen.History, "the first fork has no history to read")

	second := lease("b")
	second.Agent.Model = job.Model{}
	seed(t, s, second)

	require.Len(t, matrix.seen.History, 1)
	wantHistory := matrix.seen.History[0]
	wantHistory.ModelTier, wantHistory.Pass = "routine", 1
	assert.Equal(t, wantHistory, matrix.seen.History[0])
}

func TestStoreCountsAWideningOnlyForATakenRowThatGainsWritePaths(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	row := lease("a")

	declared := seed(t, s, row)
	row.Paths.Write = []string{"internal/a", "internal/more"}
	row.State = job.StateDeclared
	stillDeclared := seed(t, s, row)
	assert.Zero(t, declared.Widenings)
	assert.Zero(t, stillDeclared.Widenings, "a row nobody took yet is not widened, it is declared")

	row.State = job.StateRunning
	taken := seed(t, s, row)
	assert.Zero(t, taken.Widenings, "taking a row adds no path")

	row.Paths.Write = []string{"internal/a", "internal/more", "internal/other"}
	widened := seed(t, s, row)
	assert.Equal(t, 1, widened.Widenings)

	row.Paths.Write = []string{"internal/a"}
	narrowed := seed(t, s, row)
	assert.Equal(t, 1, narrowed.Widenings, "giving a path up is not a widening")

	row.Widenings = 9
	forged := seed(t, s, row)
	assert.Equal(t, 1, forged.Widenings, "a caller cannot set the counter")

	row.State = job.StateExited
	row.Paths.Write = []string{"internal/a", "internal/late"}
	exited := seed(t, s, row)
	assert.Equal(t, 2, exited.Widenings, "an exited row has been taken too")
}

func TestStoreCountsReworkRoundsAndKeepsThemAgainstAWholeRowWrite(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	row := lease("a")
	row.State = job.StateExited
	seed(t, s, row)

	require.NoError(t, s.RecordRework(t.Context(), "a"))
	require.NoError(t, s.RecordRework(t.Context(), "a"))

	row.ReworkRounds = 9
	again := seed(t, s, row)
	assert.Equal(t, 2, again.ReworkRounds, "a caller's count is replaced by the stored one")

	require.NoError(t, s.RecordRework(t.Context(), "gone"), "a row removed since the message is nothing to report")
	rows, err := s.List(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1, "counting rework creates no row")
}

// staleTieredRow is a declared row nobody took, untouched for three hours: past a
// two-hour jobs.stale_after, so the sweep and Prune both end it.
func staleTieredRow(s *Store, now int64) job.Job {
	staleAfter := 2 * time.Hour
	s.clock = func() time.Time { return time.Unix(now, 0) }
	s.staleAfter = &staleAfter
	s.notices = &strings.Builder{}
	s.landed = func(context.Context, job.Job) string { return "" }
	return job.Job{
		ID: "idle", State: job.StateDeclared, Updated: now - int64((3 * time.Hour).Seconds()),
		ModelTier: &tier.Assignment{Name: "routine", Source: tier.SourceMatrix, Site: "magusfile.buzz:40"},
	}
}

func TestStoreRecordsAnOutcomeWhenTheSweepEndsARow(t *testing.T) {
	t.Parallel()

	const now = int64(100_000)
	s, history := outcomeStore(t, tierSet(t), nil, false)
	plant(t, s, staleTieredRow(s, now))

	_, err := s.List(t.Context())
	require.NoError(t, err)

	jobs := recordedJobs(t, history)
	require.Len(t, jobs, 1)
	wantJob := jobs[0]
	wantJob.State, wantJob.Site, wantJob.Ended = job.StateNoReturn, "magusfile.buzz:40", now*1000
	assert.Equal(t, wantJob, jobs[0])
}

func TestStoreRecordsAnOutcomeWhenPruneEndsARow(t *testing.T) {
	t.Parallel()

	const now = int64(100_000)
	s, history := outcomeStore(t, tierSet(t), nil, false)
	plant(t, s, staleTieredRow(s, now))

	got, err := s.Prune(t.Context(), PruneOptions{DryRun: true})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.NoFileExists(t, history, "a dry prune ends nothing, so it records nothing")

	got, err = s.Prune(t.Context(), PruneOptions{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	jobs := recordedJobs(t, history)
	require.Len(t, jobs, 1)
	assert.Equal(t, job.StateNoReturn, jobs[0].State)
}

func TestStoreRecordsAnOutcomeWhenTheHoldingSessionEnds(t *testing.T) {
	t.Parallel()

	s, history := outcomeStore(t, tierSet(t), nil, false)
	seed(t, s, job.Job{ID: "w", Checkpoint: baseA, State: job.StateDeclared})
	_, err := s.Exec(t.Context(), "w")
	require.NoError(t, err)
	worker := Caller{Host: "claude-code", Session: "s1", Agent: "a1"}
	require.NoError(t, s.Bind(worker, "w"))

	_, ok, err := s.EndSession(t.Context(), worker)
	require.NoError(t, err)
	require.True(t, ok)

	jobs := recordedJobs(t, history)
	require.Len(t, jobs, 1)
	wantJob := jobs[0]
	wantJob.Job, wantJob.State = "w", job.StateNoReturn
	assert.Equal(t, wantJob, jobs[0])
}

// The outcome carries what the grouping lens reads: the attempt its parent was on, the
// projects Owners resolves, and the holder's work folded from its checkout's trail.
func TestStoreRecordsTheParentAttemptProjectsAndWork(t *testing.T) {
	t.Parallel()

	s, history := outcomeStore(t, tierSet(t), nil, false)
	s.owners = func(context.Context, []string) ([]string, error) { return []string{"internal/job", "types"}, nil }
	parent := lease("root")
	parent.Agent.Model = job.Model{}
	seed(t, s, parent)
	child := lease("w")
	child.Agent.Model, child.Parent = job.Model{}, "root"
	running := endWith(t, s, child, job.StateRunning)
	call(t, s.cacheDir, "w", "file.read", 10_000, "pass")
	call(t, s.cacheDir, "w", "file.write", 30_000, "pass")

	endWith(t, s, running, job.StateFail)

	jobs := recordedJobs(t, history)
	require.Len(t, jobs, 1)
	got := jobs[0]
	require.NotNil(t, got.Work)
	want := got
	want.Parent, want.ParentAttempt, want.Projects = "root", 1, []string{"internal/job", "types"}
	want.Tokens, want.CostUSD = nil, nil // no model table to price against
	want.Work = &types.JobWork{
		Calls: 2, Writes: 1, SetupCalls: 1,
		SetupContext: new(int64(10_000)), SetupTokens: new(int64(30_000)),
		ContextTokens: new(int64(40_000)), PeakTokens: new(int64(30_000)),
		SetupMs: got.Work.SetupMs,
	}
	assert.Equal(t, want, got)
}

// A holder the guard never saw records no work profile: unknown, never an idle holder.
func TestStoreRecordsNoWorkForAHolderWithNoObservedCall(t *testing.T) {
	t.Parallel()

	s, history := outcomeStore(t, tierSet(t), nil, false)
	row := lease("a")
	row.Agent.Model = job.Model{}
	endWith(t, s, endWith(t, s, row, job.StateRunning), job.StatePass)

	jobs := recordedJobs(t, history)
	require.Len(t, jobs, 1)
	assert.Nil(t, jobs[0].Work)
	assert.Nil(t, jobs[0].Projects, "no Owners resolves them")
}
