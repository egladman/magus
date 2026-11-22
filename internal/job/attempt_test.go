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

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

// filedTry is job id's first try, having filed result.
func filedTry(id string, result *job.Result) job.Attempt {
	return job.Attempt{Job: id, N: 1, Status: job.AttemptStatus{Result: result}}
}

// fileTry files result on job id's try in progress, as an exit does.
func fileTry(t *testing.T, s *Store, id string, result *job.Result) {
	t.Helper()
	require.NoError(t, s.fileResult(t.Context(), id, result, nil, time.Now().Unix()))
}

// triesIn are the tries s holds, by job.
func triesIn(t *testing.T, s *Store) job.Attempts {
	t.Helper()
	all, err := s.Attempts(t.Context())
	require.NoError(t, err)
	return job.NewAttempts(all)
}

// liveTry is job id's try in progress in s.
func liveTry(t *testing.T, s *Store, id string) job.Attempt {
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
	all, err := s.store.Attempts(s.T().Context())
	s.Require().NoError(err)
	s.Empty(all)
}

func (s *attemptStoreSuite) TestASpawnStartsTheLiveTry() {
	seed(s.T(), s.store, running("a"))

	got, err := s.store.StartAttempt(s.T().Context(), "a", s.at(10))

	s.Require().NoError(err)
	s.Equal(job.Attempt{Job: "a", N: 1, Status: job.AttemptStatus{Started: 10}}, got)
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
	s.Require().NoError(s.store.EndAttempt(s.T().Context(), "a", job.AttemptEndHandback, s.at(15)))

	got, err := s.store.StartAttempt(s.T().Context(), "a", s.at(20))

	s.Require().NoError(err)
	s.Equal(job.Attempt{Job: "a", N: 1, Status: job.AttemptStatus{Started: 20}}, got)
	s.True(got.Running())
}

func (s *attemptStoreSuite) TestAStartNamingNoLiveJobIsRefused() {
	ended := running("a")
	ended.State = job.StatePass
	seed(s.T(), s.store, ended)

	_, err := s.store.StartAttempt(s.T().Context(), "a", s.at(10))
	s.Require().ErrorIs(err, job.ErrUnknownJob)
	_, err = s.store.StartAttempt(s.T().Context(), "nobody", s.at(10))
	s.Require().ErrorIs(err, job.ErrUnknownJob)
	s.Empty(triesIn(s.T(), s.store))
}

func (s *attemptStoreSuite) TestTheFirstEndObservedStands() {
	seed(s.T(), s.store, running("a"))
	_, err := s.store.StartAttempt(s.T().Context(), "a", s.at(10))
	s.Require().NoError(err)

	s.Require().NoError(s.store.EndAttempt(s.T().Context(), "a", job.AttemptEndHandback, s.at(15)))
	s.Require().NoError(s.store.EndAttempt(s.T().Context(), "a", job.AttemptEndStop, s.at(16)))

	got := liveTry(s.T(), s.store, "a")
	s.Equal(job.AttemptStatus{Started: 10, Ended: 15, End: job.AttemptEndHandback}, got.Status)
}

func (s *attemptStoreSuite) TestACallerBoundToNoJobEndsNothing() {
	seed(s.T(), s.store, running("a"))

	s.Require().NoError(s.store.EndCallerAttempt(s.T().Context(), Caller{}, job.AttemptEndStop, s.at(15)))

	s.Empty(triesIn(s.T(), s.store))
}

func (s *attemptStoreSuite) TestAFiledResultEndsTheTryByExit() {
	seed(s.T(), s.store, running("a"))
	_, err := s.store.StartAttempt(s.T().Context(), "a", s.at(10))
	s.Require().NoError(err)
	result := &job.Result{Job: "a", ChangedPaths: []string{"a.go"}}

	s.Require().NoError(s.store.fileResult(s.T().Context(), "a", result, nil, 30))

	got := liveTry(s.T(), s.store, "a")
	s.Equal(job.AttemptStatus{Started: 10, Ended: 30, End: job.AttemptEndExit, Result: result}, got.Status)
}

func (s *attemptStoreSuite) TestAResultFiledAfterAnEndKeepsThatEnd() {
	seed(s.T(), s.store, running("a"))
	s.Require().NoError(s.store.EndAttempt(s.T().Context(), "a", job.AttemptEndHandback, s.at(15)))

	s.Require().NoError(s.store.fileResult(s.T().Context(), "a", &job.Result{Job: "a"}, nil, 30))

	got := liveTry(s.T(), s.store, "a")
	s.Equal(job.AttemptStatus{Ended: 15, End: job.AttemptEndHandback, Result: &job.Result{Job: "a"}}, got.Status)
}

// An id declared again after its row was dropped is a new job, so it
// starts on its first try with nothing filed.
func (s *attemptStoreSuite) TestADroppedRowTakesItsTriesWithIt() {
	plant(s.T(), s.store, running("a"), running("b"))
	fileTry(s.T(), s.store, "a", &job.Result{Job: "a"})
	fileTry(s.T(), s.store, "b", &job.Result{Job: "b"})

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
	s.Require().NoError(os.WriteFile(path, []byte(`{"api_version":"batch.magus/v1alpha99","kind":"Attempt","spec":{"job":"a","n":1},"status":{}}`+"\n"), 0o600))

	_, err := s.store.Attempts(s.T().Context())

	s.Require().Error(err)
	s.Contains(err.Error(), "attempt 1 of a is batch.magus/v1alpha99 Attempt")
}

func (s *attemptStoreSuite) TestATryIsStoredAsAnAttemptRecord() {
	seed(s.T(), s.store, running("a"))
	s.Require().NoError(s.store.fileResult(s.T().Context(), "a", &job.Result{Job: "a", ChangedPaths: []string{"a.go"}}, nil, 30))

	raw, err := os.ReadFile(filepath.Join(filepath.Dir(s.storePath()), attemptsFileName))
	s.Require().NoError(err)
	var line map[string]any
	s.Require().NoError(codec.Unmarshal([]byte(strings.TrimSpace(string(raw))), &line))
	s.Equal(map[string]any{
		"api_version": "safetyscissors.job/v1alpha1", "kind": "Attempt",
		"spec": map[string]any{"job": "a", "n": float64(1)},
		"status": map[string]any{"ended": float64(30), "end": "exit", "result": map[string]any{
			"job": "a", "changed_paths": []any{"a.go"}, "unresolved_risks": nil,
		}},
	}, line, "snake_case keys, Go enum values")
}

// An attempts.jsonl an older magus wrote takes the envelope once, and a
// line already in it is kept.
func (s *attemptStoreSuite) TestEnvelopingAttemptsRewritesTheOlderLines() {
	path := filepath.Join(filepath.Dir(s.storePath()), attemptsFileName)
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	current, err := job.MarshalAttempt(job.Attempt{Job: "b", N: 1, Status: job.AttemptStatus{Started: 3}}, false)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(path, []byte(fmt.Sprintf("%s\n%s\n",
		`{"api_version":"safetyscissors.job/v1alpha1","job":"a","n":2,"status":{"state":"no_return","ended":5,"end":"job"},"dropped":true}`, current)), 0o600))

	s.Require().NoError(s.store.envelopeAttempts(s.T().Context()))
	s.Require().NoError(s.store.envelopeAttempts(s.T().Context()))

	got, err := s.store.Attempts(s.T().Context())
	s.Require().NoError(err)
	s.Require().Len(got, 1, "the dropped try reads as gone")
	s.Equal(job.Attempt{Job: "b", N: 1, Status: job.AttemptStatus{Started: 3}}, got[0])
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)
	s.Contains(string(raw), `"job_state":"no_return"`)
	s.Contains(string(raw), `"dropped":true`)
}

func (s *attemptStoreSuite) TestBoundAttemptsReadsATryPastItsJobsDeadlineAsEnded() {
	row := running("a")
	row.Deadline = 50
	tries := []job.Attempt{
		{Job: "a", N: 1, Status: job.AttemptStatus{Started: 10}},
		{Job: "gone", N: 1, Status: job.AttemptStatus{Started: 10}},
	}

	got := BoundAttempts([]job.Job{row}, tries, 60)

	s.Equal([]job.Attempt{{Job: "a", N: 1, Status: job.AttemptStatus{Started: 10, Ended: 50, End: job.AttemptEndDeadline}}}, got,
		"a try of a job the rows lack is left out")
}

func (s *attemptStoreSuite) storePath() string {
	path, err := s.store.Path()
	s.Require().NoError(err)
	return path
}

// triesMovedFrom is a store at the version of migration 2 holding row a as an older magus
// wrote it, with its tries on the row.
const triesMovedFrom = `{"api_version":"safetyscissors.job/v1alpha2"}
{"id":"a","state":"running","created":1,"updated":1,"attempts":3,` +
	`"result":{"job":"a","changed_paths":["a.go"]},` +
	`"prior":[{"n":1,"state":"no_return","ended":5,"retry":{"retryable":true}},` +
	`{"n":2,"state":"fail","ended":7,"unresolved_risks":["not tried"],"retry":{"retryable":true}}]}`

func (s *attemptStoreSuite) TestMigrationThreeMovesARowsTriesToAttempts() {
	path := plantRaw(s.T(), s.store, triesMovedFrom)

	rows, err := s.store.List(s.T().Context())
	s.Require().NoError(err)
	s.Require().Len(rows, 1)

	tries := triesIn(s.T(), s.store)
	s.Equal([]job.Attempt{
		{Job: "a", N: 1, Status: job.AttemptStatus{State: job.StateNoReturn, Ended: 5, End: job.AttemptEndJob,
			Retry: job.RetryVerdict{Retryable: true}}},
		{Job: "a", N: 2, Status: job.AttemptStatus{State: job.StateFail, Ended: 7, End: job.AttemptEndJob,
			Result: &job.Result{Job: "a", UnresolvedRisks: []string{"not tried"}}, Retry: job.RetryVerdict{Retryable: true}}},
		{Job: "a", N: 3, Status: job.AttemptStatus{Result: &job.Result{Job: "a", ChangedPaths: []string{"a.go"}}}},
	}, tries["a"])
	s.Equal(3, tries.LiveN("a"))

	row := storedRow(s.T(), path, "a")
	s.NotContains(row, "result")
	s.NotContains(row, "prior")
	s.NotContains(row, "attempts")
	s.Equal(string(currentStoreVersion()), storedLines(s.T(), path)[0]["api_version"])
}

// A migration cut short runs again, and the lines it appends twice read once.
func (s *attemptStoreSuite) TestMovingTriesTwiceReadsThemOnce() {
	plantRaw(s.T(), s.store, triesMovedFrom)

	s.Require().NoError(s.store.moveTries(s.T().Context()))
	s.Require().NoError(s.store.moveTries(s.T().Context()))

	all, err := s.store.Attempts(s.T().Context())
	s.Require().NoError(err)
	s.Len(all, 3)
}

// A store whose rows still carry their tries can sit beside an attempts.jsonl a later magus
// wrote in the camelCase spelling. Moving the tries appends without reading that file, and
// the later rewrites bring its lines forward.
func (s *attemptStoreSuite) TestMovingTriesBesideACamelCaseAttemptsFileReadsBoth() {
	path := plantRaw(s.T(), s.store, triesMovedFrom)
	attempts, err := s.store.AttemptsPath()
	s.Require().NoError(err)
	camel := `{"apiVersion":"safetyscissors.job/v1alpha1","kind":"Attempt","spec":{"job":"b","n":1},` +
		`"status":{"result":{"changedPaths":["b.go"],"validation":{"command":"magus run test .","outputRef":"out1"}}}}`
	s.Require().NoError(os.WriteFile(attempts, []byte(camel+"\n"), 0o600))

	_, err = s.store.List(s.T().Context())
	s.Require().NoError(err)

	tries := triesIn(s.T(), s.store)
	s.Len(tries["a"], 3, "the row's tries moved")
	s.Require().Len(tries["b"], 1, "the camelCase line reads")
	s.Equal("out1", tries["b"][0].Status.Result.Validation.OutputRef)
	s.Equal(string(currentStoreVersion()), storedLines(s.T(), path)[0]["api_version"])
}

func (s *attemptStoreSuite) TestARowWithNoTriesMovesNothing() {
	plantRaw(s.T(), s.store, `{"api_version":"safetyscissors.job/v1alpha2"}
{"id":"a","state":"running","created":1,"updated":1}`)

	_, err := s.store.List(s.T().Context())
	s.Require().NoError(err)

	s.Empty(triesIn(s.T(), s.store))
}

func running(id string) job.Job {
	row := lease(id)
	row.State = job.StateRunning
	return row
}
