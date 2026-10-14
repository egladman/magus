package job

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/ci/volatility"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// outcomeStore is a Store whose workspace names its own history file, so a test reads back
// exactly what the store recorded.
func outcomeStore(t *testing.T, set *types.ModelTierSet, matrix func(context.Context, types.ModelTierRequest) (*types.JobModelTier, error), dryRun bool) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	history := filepath.Join(t.TempDir(), "history.json")
	require.NoError(t, os.WriteFile(filepath.Join(root, config.Filename), []byte("history_path: "+strconv.Quote(history)+"\n"), 0o644))
	loc := tmpLoc(t, root)
	loc.ModelTiers, loc.ModelTierMatrix, loc.DryRun = set, matrix, dryRun
	return NewStore(loc), history
}

func recordedJobs(t *testing.T, history string) []types.JobOutcome {
	t.Helper()
	var h forecast.History
	require.NoError(t, h.Load(t.Context(), history))
	return h.Jobs
}

func endWith(t *testing.T, s *Store, row types.Job, state types.JobState) types.Job {
	t.Helper()
	row.State = state
	return seed(t, s, row)
}

func TestNewJobOutcomeReadsTheEndedRow(t *testing.T) {
	t.Parallel()

	row := lease("a")
	row.Model = "sonnet"
	row.State = types.StateFail
	row.Created = 100
	row.Check = &types.LeaseCheck{Target: "test", Project: "."}
	row.WritePaths = []string{"pkg/a.go#func A", "pkg/a.go#func B", "pkg/b.go"}
	row.Attempt = &types.JobAttempt{}
	row.GateAttempts = []types.JobGateAttempt{{}, {}}
	row.ReworkRounds, row.Widenings = 2, 1
	row.ModelTier = &types.JobModelTier{
		Name: "economy", Rank: 0, Skills: types.SkillVariantFull, Source: types.ModelTierSourceMatrix,
		Reason: "small checked job", Site: "magusfile.buzz:20", Model: "haiku",
	}

	got := NewJobOutcome(row, 160_000)

	assert.Equal(t, types.JobOutcome{
		Job: "a", ModelTier: "economy", Kind: types.JobKindCheck, Skills: types.SkillVariantFull,
		Model: "haiku", Source: types.ModelTierSourceMatrix, Reason: "small checked job",
		WriteFiles: 2, State: types.StateFail, ChecksRun: 3, DurationMs: 60_000, Ended: 160_000,
		ReworkRounds: 2, Widenings: 1,
	}, got)
}

func TestNewJobOutcomeOfARowWithNoTierNamesNone(t *testing.T) {
	t.Parallel()

	row := lease("a")
	row.State = types.StatePass

	got := NewJobOutcome(row, 5)

	assert.Empty(t, got.ModelTier)
	assert.Equal(t, "standard", got.Model, "the model the row named")
	assert.Zero(t, got.DurationMs, "a row with no creation time has no duration")
}

func outcome(job, tier string, kind types.JobKind, state types.JobState) types.JobOutcome {
	return types.JobOutcome{Job: job, ModelTier: tier, Kind: kind, State: state}
}

func TestModelTierRecordsFoldsEachTierAndKind(t *testing.T) {
	t.Parallel()

	economyPass := func(job string) types.JobOutcome {
		o := outcome(job, "economy", types.JobKindCheck, types.StatePass)
		o.ChecksRun, o.CostUSD, o.ContextTokens = 2, 0.5, 100
		return o
	}
	failed := outcome("a", "economy", types.JobKindCheck, types.StateFail)
	failed.ChecksRun, failed.CostUSD, failed.ContextTokens, failed.ReworkRounds, failed.Widenings = 4, 1.5, 300, 3, 1
	outcomes := []types.JobOutcome{
		economyPass("a"), failed, economyPass("b"),
		outcome("c", "economy", types.JobKindProse, types.StateNoReturn),
		outcome("d", "principal", types.JobKindCheck, types.StatePass),
		outcome("e", "retired", types.JobKindCheck, types.StatePass),
		outcome("f", "", types.JobKindCheck, types.StatePass),
		outcome("g", "economy", types.JobKindCheck, types.StateRunning),
	}

	got := ModelTierRecords(outcomes, tierSet(t))

	require.Len(t, got, 4)
	assert.Equal(t, types.ModelTierRecord{
		Rank: 0, ModelTier: "economy", Kind: types.JobKindCheck,
		Jobs: 3, Pass: 2, Fail: 1,
		PassLower:    volatility.WilsonLower(2, 3),
		ChecksPerJob: 8.0 / 3, ForksPerID: 3.0 / 2, ReworkPerJob: 1, WideningsPerJob: 1.0 / 3,
		MeanCostUSD: 2.5 / 3, MedianTokens: 100,
	}, got[0], "a was forked twice, and the running and untiered outcomes do not count")
	assert.Equal(t, "economy", got[1].ModelTier)
	assert.Equal(t, types.JobKindProse, got[1].Kind)
	assert.Equal(t, 1, got[1].NoReturn)
	assert.Zero(t, got[1].PassLower)
	assert.Equal(t, 2, got[2].Rank, "principal ranks after economy")
	assert.Equal(t, types.ModelTierRecord{Rank: -1, ModelTier: "retired", Kind: types.JobKindCheck, Jobs: 1, Pass: 1, PassLower: volatility.WilsonLower(1, 1), ChecksPerJob: 0, ForksPerID: 1}, got[3],
		"a tier the declaration dropped keeps its record, last, at rank -1")
}

func TestModelTierRecordsWithoutADeclarationRanksEveryTierMinusOne(t *testing.T) {
	t.Parallel()

	got := ModelTierRecords([]types.JobOutcome{outcome("a", "economy", types.JobKindCheck, types.StatePass)}, nil)

	require.Len(t, got, 1)
	assert.Equal(t, -1, got[0].Rank)
	assert.Empty(t, ModelTierRecords(nil, tierSet(t)), "no outcomes is no rows, not an error")
}

func TestModelTierRecordsMedianOfAnEvenCountIsTheMiddlePairsMean(t *testing.T) {
	t.Parallel()

	var outcomes []types.JobOutcome
	for i, tokens := range []int64{400, 100, 300, 200} {
		o := outcome("job"+strconv.Itoa(i), "economy", types.JobKindCheck, types.StatePass)
		o.ContextTokens = tokens
		outcomes = append(outcomes, o)
	}

	assert.Equal(t, int64(250), ModelTierRecords(outcomes, tierSet(t))[0].MedianTokens)
}

func TestStoreRecordsAnOutcomeWhenARowEnds(t *testing.T) {
	t.Parallel()

	s, history := outcomeStore(t, tierSet(t), nil, false)
	row := lease("a")
	row.Model = ""
	running := endWith(t, s, row, types.StateRunning)
	require.Empty(t, recordedJobs(t, history), "a row that is still taken has no outcome")

	row = running
	ended := endWith(t, s, row, types.StateFail)

	jobs := recordedJobs(t, history)
	require.Len(t, jobs, 1)
	assert.Equal(t, NewJobOutcome(ended, jobs[0].Ended), jobs[0])
	assert.Equal(t, "standard", jobs[0].ModelTier)
	assert.Equal(t, types.StateFail, jobs[0].State)
	assert.Positive(t, jobs[0].Ended)

	row.Criteria = "reworded after it failed"
	endWith(t, s, row, types.StateFail)
	assert.Len(t, recordedJobs(t, history), 1, "a row that was already over records nothing again")
}

func TestStoreRecordsNothingForADryRunOrAnUntieredRow(t *testing.T) {
	t.Parallel()

	dry, dryHistory := outcomeStore(t, tierSet(t), nil, true)
	row := lease("a")
	row.Model = ""
	endWith(t, dry, endWith(t, dry, row, types.StateRunning), types.StatePass)
	assert.NoFileExists(t, dryHistory, "a dry run writes no history")

	untiered, untieredHistory := outcomeStore(t, nil, nil, false)
	endWith(t, untiered, endWith(t, untiered, row, types.StateRunning), types.StatePass)
	assert.NoFileExists(t, untieredHistory, "a workspace with no tiers has nothing for the lens to rank")
}

func TestStoreRefusesToEndARowWhoseConfigDoesNotRead(t *testing.T) {
	t.Parallel()

	s, _ := outcomeStore(t, tierSet(t), nil, false)
	row := lease("a")
	row.Model = ""
	running := endWith(t, s, row, types.StateRunning)
	require.NoError(t, os.WriteFile(filepath.Join(s.root, config.Filename), []byte("history_path: [not, a, path]\n"), 0o644))

	running.State = types.StatePass
	_, err := s.Update(t.Context(), running.ID, func(cur *types.Job) { *cur = running })

	require.Error(t, err)
	assert.ErrorContains(t, err, "history_path")
	rows, lerr := s.List()
	require.NoError(t, lerr)
	assert.Equal(t, types.StateRunning, rows[0].State, "the row did not move, so the outcome is not lost")
}

func TestStoreFeedsRecordedOutcomesToTheMatrix(t *testing.T) {
	t.Parallel()

	matrix := countingMatrix{answer: economyAnswer()}
	s, _ := outcomeStore(t, tierSet(t), matrix.choose, false)
	first := lease("a")
	first.Model = ""
	endWith(t, s, endWith(t, s, first, types.StateRunning), types.StatePass)
	require.Empty(t, matrix.seen.History, "the first fork has no history to read")

	second := lease("b")
	second.Model = ""
	seed(t, s, second)

	require.Len(t, matrix.seen.History, 1)
	assert.Equal(t, "economy", matrix.seen.History[0].ModelTier)
	assert.Equal(t, 1, matrix.seen.History[0].Pass)
}

func TestStoreCountsAWideningOnlyForATakenRowThatGainsWritePaths(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, t.TempDir())
	row := lease("a")

	declared := seed(t, s, row)
	row.WritePaths = []string{"internal/a", "internal/more"}
	row.State = types.StateDeclared
	stillDeclared := seed(t, s, row)
	assert.Zero(t, declared.Widenings)
	assert.Zero(t, stillDeclared.Widenings, "a row nobody took yet is not widened, it is declared")

	row.State = types.StateRunning
	taken := seed(t, s, row)
	assert.Zero(t, taken.Widenings, "taking a row adds no path")

	row.WritePaths = []string{"internal/a", "internal/more", "internal/other"}
	widened := seed(t, s, row)
	assert.Equal(t, 1, widened.Widenings)

	row.WritePaths = []string{"internal/a"}
	narrowed := seed(t, s, row)
	assert.Equal(t, 1, narrowed.Widenings, "giving a path up is not a widening")

	row.Widenings = 9
	forged := seed(t, s, row)
	assert.Equal(t, 1, forged.Widenings, "a caller cannot set the counter")

	row.State = types.StateExited
	row.WritePaths = []string{"internal/a", "internal/late"}
	exited := seed(t, s, row)
	assert.Equal(t, 2, exited.Widenings, "an exited row has been taken too")
}

func TestStoreCountsReworkRoundsAndKeepsThemAgainstAWholeRowWrite(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, t.TempDir())
	row := lease("a")
	row.State = types.StateExited
	seed(t, s, row)

	require.NoError(t, s.RecordRework(t.Context(), "a"))
	require.NoError(t, s.RecordRework(t.Context(), "a"))

	row.ReworkRounds = 9
	again := seed(t, s, row)
	assert.Equal(t, 2, again.ReworkRounds, "a caller's count is replaced by the stored one")

	require.NoError(t, s.RecordRework(t.Context(), "gone"), "a row removed since the message is nothing to report")
	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 1, "counting rework creates no row")
}
