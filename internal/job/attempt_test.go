package job

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// filedTry is job id's first try, having filed result.
func filedTry(id string, result *types.JobResult) types.Attempt {
	return types.Attempt{Job: id, N: 1, Status: types.AttemptStatus{Result: result}}
}

// fileTry files result on job id's try in progress, as an exit does.
func fileTry(t *testing.T, s *Store, id string, result *types.JobResult) {
	t.Helper()
	require.NoError(t, s.fileResult(t.Context(), id, result, time.Now().Unix()))
}

// triesIn are the tries s holds, by job.
func triesIn(t *testing.T, s *Store) types.JobAttempts {
	t.Helper()
	all, err := s.Attempts()
	require.NoError(t, err)
	return types.NewJobAttempts(all)
}

// liveTry is job id's try in progress in s.
func liveTry(t *testing.T, s *Store, id string) types.Attempt {
	t.Helper()
	return triesIn(t, s).Live(id)
}

type attemptStoreSuite struct {
	suite.Suite
	store *Store
}

func TestAttemptStore(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(attemptStoreSuite))
}

func (s *attemptStoreSuite) SetupTest() {
	s.store = tempStore(s.T(), s.T().TempDir())
}

func (s *attemptStoreSuite) at(sec int64) time.Time { return time.Unix(sec, 0) }

func (s *attemptStoreSuite) TestAnUnwrittenStoreHoldsNoTries() {
	all, err := s.store.Attempts()
	s.Require().NoError(err)
	s.Empty(all)
}

func (s *attemptStoreSuite) TestASpawnStartsTheLiveTry() {
	seed(s.T(), s.store, running("a"))

	got, err := s.store.StartAttempt(s.T().Context(), "a", s.at(10))

	s.Require().NoError(err)
	s.Equal(types.Attempt{Job: "a", N: 1, Status: types.AttemptStatus{Started: 10}}, got)
	s.Equal(got, liveTry(s.T(), s.store, "a"))
}

func (s *attemptStoreSuite) TestASecondStartOfARunningTryKeepsTheFirst() {
	seed(s.T(), s.store, running("a"))
	_, err := s.store.StartAttempt(s.T().Context(), "a", s.at(10))
	s.Require().NoError(err)

	got, err := s.store.StartAttempt(s.T().Context(), "a", s.at(20))

	s.Require().NoError(err)
	s.Equal(int64(10), got.Status.Started, "a hook fired twice records one start")
}

func (s *attemptStoreSuite) TestAStartAfterAnEndStartsTheTryAgain() {
	seed(s.T(), s.store, running("a"))
	_, err := s.store.StartAttempt(s.T().Context(), "a", s.at(10))
	s.Require().NoError(err)
	s.Require().NoError(s.store.EndAttempt(s.T().Context(), "a", types.AttemptEndHandback, s.at(15)))

	got, err := s.store.StartAttempt(s.T().Context(), "a", s.at(20))

	s.Require().NoError(err)
	s.Equal(types.Attempt{Job: "a", N: 1, Status: types.AttemptStatus{Started: 20}}, got)
	s.True(got.Running())
}

func (s *attemptStoreSuite) TestAStartNamingNoLiveJobIsRefused() {
	ended := running("a")
	ended.State = types.StatePass
	seed(s.T(), s.store, ended)

	_, err := s.store.StartAttempt(s.T().Context(), "a", s.at(10))
	s.Require().ErrorIs(err, ErrUnknownJob)
	_, err = s.store.StartAttempt(s.T().Context(), "nobody", s.at(10))
	s.Require().ErrorIs(err, ErrUnknownJob)
	s.Empty(triesIn(s.T(), s.store))
}

func (s *attemptStoreSuite) TestTheFirstEndObservedStands() {
	seed(s.T(), s.store, running("a"))
	_, err := s.store.StartAttempt(s.T().Context(), "a", s.at(10))
	s.Require().NoError(err)

	s.Require().NoError(s.store.EndAttempt(s.T().Context(), "a", types.AttemptEndHandback, s.at(15)))
	s.Require().NoError(s.store.EndAttempt(s.T().Context(), "a", types.AttemptEndStop, s.at(16)))

	got := liveTry(s.T(), s.store, "a")
	s.Equal(types.AttemptStatus{Started: 10, Ended: 15, End: types.AttemptEndHandback}, got.Status)
}

func (s *attemptStoreSuite) TestACallerBoundToNoJobEndsNothing() {
	seed(s.T(), s.store, running("a"))

	s.Require().NoError(s.store.EndCallerAttempt(s.T().Context(), Caller{}, types.AttemptEndStop, s.at(15)))

	s.Empty(triesIn(s.T(), s.store))
}

func (s *attemptStoreSuite) TestAFiledResultEndsTheTryByExit() {
	seed(s.T(), s.store, running("a"))
	_, err := s.store.StartAttempt(s.T().Context(), "a", s.at(10))
	s.Require().NoError(err)
	result := &types.JobResult{Job: "a", ChangedPaths: []string{"a.go"}}

	s.Require().NoError(s.store.fileResult(s.T().Context(), "a", result, 30))

	got := liveTry(s.T(), s.store, "a")
	s.Equal(types.AttemptStatus{Started: 10, Ended: 30, End: types.AttemptEndExit, Result: result}, got.Status)
}

func (s *attemptStoreSuite) TestAResultFiledAfterAnEndKeepsThatEnd() {
	seed(s.T(), s.store, running("a"))
	s.Require().NoError(s.store.EndAttempt(s.T().Context(), "a", types.AttemptEndHandback, s.at(15)))

	s.Require().NoError(s.store.fileResult(s.T().Context(), "a", &types.JobResult{Job: "a"}, 30))

	got := liveTry(s.T(), s.store, "a")
	s.Equal(types.AttemptStatus{Ended: 15, End: types.AttemptEndHandback, Result: &types.JobResult{Job: "a"}}, got.Status)
}

// An id declared again after its row was dropped is a new job, so it
// starts on its first try with nothing filed.
func (s *attemptStoreSuite) TestADroppedRowTakesItsTriesWithIt() {
	plant(s.T(), s.store, running("a"), running("b"))
	fileTry(s.T(), s.store, "a", &types.JobResult{Job: "a"})
	fileTry(s.T(), s.store, "b", &types.JobResult{Job: "b"})

	plant(s.T(), s.store, running("b"))

	tries := triesIn(s.T(), s.store)
	s.NotContains(tries, "a")
	s.NotNil(tries.Result("b"), "a kept row keeps its tries")
	plant(s.T(), s.store, running("a"), running("b"))
	s.Nil(triesIn(s.T(), s.store).Result("a"))
}

func (s *attemptStoreSuite) TestALineAnotherVersionWroteIsRefused() {
	path := filepath.Join(filepath.Dir(s.storePath()), attemptsFileName)
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(os.WriteFile(path, []byte(`{"apiVersion":"batch.magus/v1alpha99","kind":"Attempt","spec":{"job":"a","n":1},"status":{}}`+"\n"), 0o600))

	_, err := s.store.Attempts()

	s.Require().Error(err)
	s.Contains(err.Error(), "attempt 1 of a is batch.magus/v1alpha99 Attempt")
}

func (s *attemptStoreSuite) TestATryIsStoredAsAnAttemptRecord() {
	seed(s.T(), s.store, running("a"))
	s.Require().NoError(s.store.fileResult(s.T().Context(), "a", &types.JobResult{Job: "a", ChangedPaths: []string{"a.go"}}, 30))

	raw, err := os.ReadFile(filepath.Join(filepath.Dir(s.storePath()), attemptsFileName))
	s.Require().NoError(err)
	var line map[string]any
	s.Require().NoError(json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &line))
	s.Equal(map[string]any{
		"apiVersion": "batch.magus/v1alpha1", "kind": "Attempt",
		"spec": map[string]any{"job": "a", "n": float64(1)},
		"status": map[string]any{"ended": float64(30), "end": "Exit", "result": map[string]any{
			"job": "a", "changedPaths": []any{"a.go"}, "unresolvedRisks": nil,
		}},
	}, line, "camelCase keys, CamelCase enum values")
}

// An attempts.jsonl an older magus wrote takes the envelope once, and a
// line already in it is kept.
func (s *attemptStoreSuite) TestEnvelopingAttemptsRewritesTheOlderLines() {
	path := filepath.Join(filepath.Dir(s.storePath()), attemptsFileName)
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	current, err := json.Marshal(recordAttempt(types.Attempt{Job: "b", N: 1, Status: types.AttemptStatus{Started: 3}}))
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(path, []byte(fmt.Sprintf("%s\n%s\n",
		`{"api_version":"magus.job/v1alpha1","job":"a","n":2,"status":{"state":"no_return","ended":5,"end":"job"},"dropped":true}`, current)), 0o600))

	s.Require().NoError(s.store.envelopeAttempts())
	s.Require().NoError(s.store.envelopeAttempts())

	log, err := s.store.attemptsLog()
	s.Require().NoError(err)
	recs, err := log.Load()
	s.Require().NoError(err)
	s.Require().Len(recs, 1, "the dropped try reads as gone")
	got, err := recs[0].attempt()
	s.Require().NoError(err)
	s.Equal(types.Attempt{Job: "b", N: 1, Status: types.AttemptStatus{Started: 3}}, got)
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)
	s.Contains(string(raw), `"jobState":"NoReturn"`)
	s.Contains(string(raw), `"dropped":true`)
}

func (s *attemptStoreSuite) TestAnArchiveKeepsTheTriesOfTheRowsItHolds() {
	plant(s.T(), s.store, running("a"), running("b"))
	fileTry(s.T(), s.store, "a", &types.JobResult{Job: "a"})
	fileTry(s.T(), s.store, "b", &types.JobResult{Job: "b"})
	path := filepath.Join(s.T().TempDir(), "jobs-archive.attempts.jsonl")

	s.Require().NoError(s.store.archiveAttempts(path, []types.Job{running("a")}))

	raw, err := os.ReadFile(path)
	s.Require().NoError(err)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	s.Require().Len(lines, 1)
	s.Contains(lines[0], `"job":"a"`)
}

func (s *attemptStoreSuite) TestAnArchiveOfRowsWithNoTriesWritesNoFile() {
	path := filepath.Join(s.T().TempDir(), "jobs-archive.attempts.jsonl")

	s.Require().NoError(s.store.archiveAttempts(path, []types.Job{running("a")}))

	s.NoFileExists(path)
}

func (s *attemptStoreSuite) TestBoundAttemptsReadsATryPastItsJobsDeadlineAsEnded() {
	row := running("a")
	row.Deadline = 50
	tries := []types.Attempt{
		{Job: "a", N: 1, Status: types.AttemptStatus{Started: 10}},
		{Job: "gone", N: 1, Status: types.AttemptStatus{Started: 10}},
	}

	got := BoundAttempts([]types.Job{row}, tries, 60)

	s.Equal([]types.Attempt{{Job: "a", N: 1, Status: types.AttemptStatus{Started: 10, Ended: 50, End: types.AttemptEndDeadline}}}, got,
		"a try of a job the rows lack is left out")
}

func (s *attemptStoreSuite) storePath() string {
	path, err := s.store.Path()
	s.Require().NoError(err)
	return path
}

// triesMovedFrom is a store at migration 2 holding row a as an older magus
// wrote it, with its tries on the row.
const triesMovedFrom = `{"api_version":"magus.job/v1alpha1","migration":2}
{"id":"a","state":"running","created":1,"updated":1,"attempts":3,` +
	`"result":{"job":"a","changed_paths":["a.go"]},` +
	`"prior":[{"n":1,"state":"no_return","ended":5,"retry":{"retryable":true}},` +
	`{"n":2,"state":"fail","ended":7,"unresolved_risks":["not tried"],"retry":{"retryable":true}}]}`

func (s *attemptStoreSuite) TestMigrationThreeMovesARowsTriesToAttempts() {
	path := plantRaw(s.T(), s.store, triesMovedFrom)

	rows, err := s.store.List()
	s.Require().NoError(err)
	s.Require().Len(rows, 1)

	tries := triesIn(s.T(), s.store)
	s.Equal([]types.Attempt{
		{Job: "a", N: 1, Status: types.AttemptStatus{State: types.StateNoReturn, Ended: 5, End: types.AttemptEndJob,
			Retry: types.RetryVerdict{Retryable: true}}},
		{Job: "a", N: 2, Status: types.AttemptStatus{State: types.StateFail, Ended: 7, End: types.AttemptEndJob,
			Result: &types.JobResult{Job: "a", UnresolvedRisks: []string{"not tried"}}, Retry: types.RetryVerdict{Retryable: true}}},
		{Job: "a", N: 3, Status: types.AttemptStatus{Result: &types.JobResult{Job: "a", ChangedPaths: []string{"a.go"}}}},
	}, tries["a"])
	s.Equal(3, tries.LiveN("a"))

	row := storedRow(s.T(), path, "a")
	s.NotContains(row, "result")
	s.NotContains(row, "prior")
	s.NotContains(row, "attempts")
	s.InDelta(len(jobMigrations), storedLines(s.T(), path)[0]["migration"], 0)
}

// A migration cut short runs again, and the lines it appends twice read once.
func (s *attemptStoreSuite) TestMovingTriesTwiceReadsThemOnce() {
	plantRaw(s.T(), s.store, triesMovedFrom)

	s.Require().NoError(s.store.moveTries(s.T().Context()))
	s.Require().NoError(s.store.moveTries(s.T().Context()))

	all, err := s.store.Attempts()
	s.Require().NoError(err)
	s.Len(all, 3)
}

func (s *attemptStoreSuite) TestARowWithNoTriesMovesNothing() {
	plantRaw(s.T(), s.store, `{"api_version":"magus.job/v1alpha1","migration":2}
{"id":"a","state":"running","created":1,"updated":1}`)

	_, err := s.store.List()
	s.Require().NoError(err)

	s.Empty(triesIn(s.T(), s.store))
}
