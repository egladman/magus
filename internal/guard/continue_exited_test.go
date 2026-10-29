package guard

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

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
	Judge(s.f.ctx, Dependencies{}, Request{Input: finishedSpawn(s.T(), reworkTitle, "brisk-heron", "sonnet", "", "a1b2c3"), Host: "claude-code"})
}

func (s *ContinueToExitedWorkerSuite) continueWorker(state types.JobState) types.GuardJudgment {
	s.f.putJob(s.T(), state)
	continueTo := `{"session_id":"8f2c6a1e","cwd":"/Users/dev/repo","hook_event_name":"PreToolUse",` +
		`"tool_name":"SendMessage","tool_input":{"to":"brisk-heron","message":"Widen with job apply and fix the review comments."},"tool_use_id":"toolu_02"}`
	return Judge(s.f.ctx, Dependencies{}, Request{Input: continueTo, Host: "claude-code"})
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
	s.Contains(v.Context, "job guard-facts is pass, so this worker holds no write paths")
}

func (s *ContinueToExitedWorkerSuite) TestContinueToARunningWorkerSaysNothing() {
	v := s.continueWorker(types.StateRunning)
	s.NotContains(v.Context, "job guard-facts is")
}

func (s *ContinueToExitedWorkerSuite) TestContinueToAnExitedWorkerNeverDenies() {
	v := s.continueWorker(types.StateExited)
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
}
