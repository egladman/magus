package guard

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// UnboundWriteInJobCheckoutSuite grades writes by a caller bound to no job
// into the checkout a job's fork made: a subagent is denied, and a person or
// the root session writes freely.
type UnboundWriteInJobCheckoutSuite struct {
	suite.Suite
	ctx     context.Context
	session string
	own     string
}

func TestUnboundWriteInJobCheckoutSuite(t *testing.T) {
	suite.Run(t, new(UnboundWriteInJobCheckoutSuite))
}

func (s *UnboundWriteInJobCheckoutSuite) SetupTest() {
	s.ctx, s.session = fleetFixture(s.T())
	s.own = s.T().TempDir()
	for _, dir := range []string{s.session, s.own} {
		s.Require().NoError(os.MkdirAll(filepath.Join(dir, "std"), 0o755))
		s.Require().NoError(os.WriteFile(filepath.Join(dir, "magusfile.buzz"), nil, 0o644))
	}
	s.pin(types.StateRunning)
}

// pin holds the job store at one row in state whose fork made s.own.
func (s *UnboundWriteInJobCheckoutSuite) pin(state types.JobState) {
	row := types.Job{ID: "w", WritePaths: []string{"std/*.go"}, State: state,
		Checkpoint: "rev-a", ReportedBase: "rev-a", BaseVerdict: types.BaseMatch, Registered: 1,
		CheckoutRoot: s.own, Worktree: &types.JobWorktree{Root: s.own, Source: s.session}}
	s.ctx = job.WithSnapshot(s.ctx, job.Snapshot{Rows: []types.Job{row}})
}

// write is an editor Write of path by agent, or by the root session when
// agent is "".
func (s *UnboundWriteInJobCheckoutSuite) write(agent, path string) types.GuardJudgment {
	env := map[string]any{"session_id": "8f2c6a1e", "cwd": s.session, "hook_event_name": "PreToolUse",
		"tool_name": "Write", "tool_input": map[string]any{"file_path": path, "content": "package std\n"}}
	if agent != "" {
		env["agent_id"], env["agent_type"] = agent, "general-purpose"
	}
	return Judge(s.ctx, Dependencies{}, Request{Input: hookJSON(s.T(), env), Host: "claude-code"})
}

func (s *UnboundWriteInJobCheckoutSuite) TestUnboundSubagentWriteInAJobMadeCheckoutIsDenied() {
	v := s.write("a1b2c3", filepath.Join(s.own, "std", "module.go"))
	s.Equal(decisionDeny, v.Decision)
	s.Equal(string(denyRuleLeaseWrite), v.Rule)
}

func (s *UnboundWriteInJobCheckoutSuite) TestDenyNamesTheJobAndJobExec() {
	v := s.write("a1b2c3", filepath.Join(s.own, "std", "module.go"))
	s.Contains(v.Reason, "the checkout job w's fork made")
	s.Contains(v.Reason, "job exec w")
}

func (s *UnboundWriteInJobCheckoutSuite) TestUnboundSubagentWriteAfterTerminalWaitIsDenied() {
	s.pin(types.StatePass)
	v := s.write("a1b2c3", filepath.Join(s.own, "std", "module.go"))
	s.Equal(decisionDeny, v.Decision)
	s.Contains(v.Reason, "that job is pass")
	s.Contains(v.Reason, "ask your orchestrator to fork a job for this write")
}

func (s *UnboundWriteInJobCheckoutSuite) TestPersonWriteInAJobMadeCheckoutPasses() {
	v := Judge(s.ctx, Dependencies{}, Request{Input: filepath.Join(s.own, "std", "module.go"), IsPath: true})
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
}

func (s *UnboundWriteInJobCheckoutSuite) TestRootSessionWriteInAJobMadeCheckoutPasses() {
	v := s.write("", filepath.Join(s.own, "std", "module.go"))
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
}

func (s *UnboundWriteInJobCheckoutSuite) TestRootSessionWriteInItsOwnCheckoutPasses() {
	v := s.write("", filepath.Join(s.session, "std", "module.go"))
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
}

func (s *UnboundWriteInJobCheckoutSuite) TestUnboundSubagentWriteOutsideEveryCheckoutPasses() {
	v := s.write("a1b2c3", filepath.Join(s.T().TempDir(), "notes.txt"))
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
}

// The empty-lease arm of denyOtherCheckout: a magus checkout no job's fork
// made is nobody's to grade for a caller holding no lease.
func (s *UnboundWriteInJobCheckoutSuite) TestUnboundSubagentWriteInAnotherCheckoutNoJobMadePasses() {
	other := s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(other, "magusfile.buzz"), nil, 0o644))
	v := s.write("a1b2c3", filepath.Join(other, "std", "module.go"))
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
}

func (s *UnboundWriteInJobCheckoutSuite) TestDenyOtherCheckoutLeavesAnUnleasedWrite() {
	other := s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(other, "magusfile.buzz"), nil, 0o644))
	s.Empty(denyOtherCheckout(s.session, "", filepath.Join(other, "std", "module.go")).Decision)
}

func (s *UnboundWriteInJobCheckoutSuite) TestDenyOtherCheckoutLeavesARelativePath() {
	s.Empty(denyOtherCheckout(s.session, "w", filepath.Join("std", "module.go")).Decision)
}

func (s *UnboundWriteInJobCheckoutSuite) TestDenyOtherCheckoutLeavesAPathInNoMagusCheckout() {
	s.Empty(denyOtherCheckout(s.session, "w", filepath.Join(s.T().TempDir(), "notes.txt")).Decision)
}

func (s *UnboundWriteInJobCheckoutSuite) TestDenyOtherCheckoutLeavesTheLeasesOwnCheckout() {
	s.Empty(denyOtherCheckout(s.own, "w", filepath.Join(s.own, "std", "module.go")).Decision)
}
