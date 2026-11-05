package guard

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const reworkTitle = "orchestrator/integrator guard-facts"

type reworkFixture struct {
	at    location
	facts hint.Gate
	store *job.Store
}

func newReworkFixture(t *testing.T) reworkFixture {
	t.Helper()
	testkit.Isolate(t)
	root := testkit.NewWorkspace(t).Root()
	cacheDir := t.TempDir()
	return reworkFixture{
		at:    location{cacheDir: cacheDir, workspace: root, dir: root},
		facts: hint.NewGate(cacheDir, FactsKey("claude-code", "8f2c6a1e")),
		store: job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}),
	}
}

func (f reworkFixture) putJob(t *testing.T, state types.JobState) {
	t.Helper()
	row := types.Job{ID: "guard-facts", Criteria: "prove it", WritePaths: []string{"a.go"}, Validation: "magus run test", State: state}
	_, err := f.store.Update(f.testContext(t), row.ID, func(cur *types.Job) { *cur = row })
	require.NoError(t, err)
}

func (f reworkFixture) rounds(t *testing.T) int {
	t.Helper()
	rows, err := f.store.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	return rows[0].ReworkRounds
}

func TestRecordReworkCountsAContinuationSentAfterTheWorkerHandedBack(t *testing.T) {
	tests := []struct {
		name  string
		state types.JobState
		want  int
	}{
		{"an exited job", types.StateExited, 1},
		{"a passed job", types.StatePass, 1},
		{"a failed job", types.StateFail, 1},
		{"a job still running", types.StateRunning, 0},
		{"a job nobody took", types.StateDeclared, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newReworkFixture(t)
			f.putJob(t, tc.state)
			writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{Description: reworkTitle, Name: "brisk-heron"})

			recordRework(f.testContext(t), f.at, f.facts, "a1b2c3")

			assert.Equal(t, tc.want, f.rounds(t))
		})
	}
}

func TestRecordReworkAttributesOnlyWhatTheSpawnRecordNames(t *testing.T) {
	f := newReworkFixture(t)
	f.putJob(t, types.StateExited)

	recordRework(f.testContext(t), f.at, f.facts, "ghost")
	assert.Zero(t, f.rounds(t), "an agent magus never saw names no job")

	writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{Description: reworkTitle, UntrustedJob: "guard-facts"})
	recordRework(f.testContext(t), f.at, f.facts, "a1b2c3")
	assert.Zero(t, f.rounds(t), "a title its spawner could not hand out attributes nothing")

	writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{Description: "orchestrator/integrator another-job"})
	recordRework(f.testContext(t), f.at, f.facts, "a1b2c3")
	assert.Zero(t, f.rounds(t), "a title naming a job that does not exist")

	recordRework(f.testContext(t), location{}, f.facts, "a1b2c3")
	recordRework(f.testContext(t), f.at, f.facts, "")
	assert.Zero(t, f.rounds(t), "no cache or no address is no record")
}

func TestRecordReworkResolvesAnAgentByItsName(t *testing.T) {
	f := newReworkFixture(t)
	f.putJob(t, types.StateExited)
	writeAgentMarker(f.facts, agentAliasKind("brisk-heron"), []byte("a1b2c3"))
	writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{Description: reworkTitle, Name: "brisk-heron"})

	recordRework(f.testContext(t), f.at, f.facts, "brisk-heron")

	assert.Equal(t, 1, f.rounds(t))
}

func TestRecordReworkWritesNothingInADryRun(t *testing.T) {
	f := newReworkFixture(t)
	f.putJob(t, types.StateExited)
	writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{Description: reworkTitle})
	dry, cancel := withDryRunDeadline(f.testContext(t))
	defer cancel()

	recordRework(dry, f.at, f.facts, "a1b2c3")

	assert.Zero(t, f.rounds(t))
}

func TestAContinueJudgedByTheGuardCountsAsReworkOnce(t *testing.T) {
	f := newReworkFixture(t)
	f.putJob(t, types.StateExited)
	deps := Dependencies{}
	Judge(f.testContext(t), deps, Request{Input: finishedSpawn(t, reworkTitle, "brisk-heron", "sonnet", "", "a1b2c3"), Host: "claude-code"})
	require.Zero(t, f.rounds(t), "the spawn is not a continuation")

	continueTo := `{"session_id":"8f2c6a1e","cwd":"/Users/dev/repo","hook_event_name":"PreToolUse",` +
		`"tool_name":"SendMessage","tool_input":{"to":"brisk-heron","message":"Now fix the review comments."},"tool_use_id":"toolu_02"}`
	Judge(f.testContext(t), deps, Request{Input: continueTo, Host: "claude-code"})

	assert.Equal(t, 1, f.rounds(t))
}

// ContinueToExitedWorkerSuite pins the advice the root gets when it continues
// a worker whose job exited or ended. It informs and never denies.
type ContinueToExitedWorkerSuite struct {
	suite.Suite
	f reworkFixture
}

func TestContinueToExitedWorkerSuite(t *testing.T) {
	suite.Run(t, new(ContinueToExitedWorkerSuite))
}

func (s *ContinueToExitedWorkerSuite) SetupTest() {
	s.f = newReworkFixture(s.T())
	s.f.putJob(s.T(), types.StateRunning)
	Judge(s.f.testContext(s.T()), Dependencies{}, Request{Input: finishedSpawn(s.T(), reworkTitle, "brisk-heron", "sonnet", "", "a1b2c3"), Host: "claude-code"})
}

func (s *ContinueToExitedWorkerSuite) continueWorker(state types.JobState) types.GuardJudgment {
	s.f.putJob(s.T(), state)
	continueTo := `{"session_id":"8f2c6a1e","cwd":"/Users/dev/repo","hook_event_name":"PreToolUse",` +
		`"tool_name":"SendMessage","tool_input":{"to":"brisk-heron","message":"Widen with job apply and fix the review comments."},"tool_use_id":"toolu_02"}`
	return Judge(s.f.testContext(s.T()), Dependencies{}, Request{Input: continueTo, Host: "claude-code"})
}

func (s *ContinueToExitedWorkerSuite) TestContinueToAnExitedWorkerAdvisesTheRoot() {
	v := s.continueWorker(types.StateExited)
	s.Equal(decisionAdvise, v.Decision)
	s.Contains(v.Context, "job guard-facts is exited; widen it yourself with `")
	s.Contains(v.Context, "job exec guard-facts")
}

func (s *ContinueToExitedWorkerSuite) TestContinueToAnEndedWorkerAdvisesTheRoot() {
	v := s.continueWorker(types.StatePass)
	s.Equal(decisionAdvise, v.Decision)
	s.Contains(v.Context, "job guard-facts is pass; fork a new job for more work")
}

// A continued worker is still bound: no advice and no rework round.
func (s *ContinueToExitedWorkerSuite) TestContinueToAStoppedWorkerSaysNothing() {
	v := s.continueWorker(types.StateRunning)
	s.NotContains(v.Context, "job guard-facts is")
	s.Zero(s.f.rounds(s.T()))
}

func (s *ContinueToExitedWorkerSuite) TestContinueToAnOtherwiseEndedNoReturnWorkerAdvisesFork() {
	v := s.continueWorker(types.StateNoReturn)
	s.Equal(decisionAdvise, v.Decision)
	s.Contains(v.Context, "fork a new job for more work")
	s.NotContains(v.Context, "job exec guard-facts")
}

func (s *ContinueToExitedWorkerSuite) TestContinueToARunningWorkerSaysNothing() {
	v := s.continueWorker(types.StateRunning)
	s.NotContains(v.Context, "job guard-facts is")
}

func (s *ContinueToExitedWorkerSuite) TestContinueToAnExitedWorkerNeverDenies() {
	v := s.continueWorker(types.StateExited)
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
}

func (f reworkFixture) testContext(t *testing.T) context.Context {
	return WithLocation(t.Context(), f.at.cacheDir, f.at.workspace, f.at.dir)
}
