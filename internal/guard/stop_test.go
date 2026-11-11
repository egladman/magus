package guard

import (
	"context"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

type StopUnfiledSuite struct {
	suite.Suite
}

func TestStopUnfiledSuite(t *testing.T) { suite.Run(t, new(StopUnfiledSuite)) }

func (s *StopUnfiledSuite) row() types.Job {
	return types.Job{ID: "spec/worker", Criteria: "build the spec", WritePaths: []string{"types/**"},
		State: types.StateRunning, Registered: 1}
}

// stop is a subagent's stop for the leased row, judged in a store holding row.
func (s *StopUnfiledSuite) stop(row types.Job, event string, fields map[string]any) types.GuardJudgment {
	ctx, root := fleetFixture(s.T(), row)
	env := map[string]any{"session_id": "8f2c6a1e", "transcript_path": "/nonexistent/root.jsonl", "cwd": root,
		"hook_event_name": event, "stop_hook_active": false}
	for k, v := range fields {
		env[k] = v
	}
	return s.judge(ctx, hookJSON(s.T(), env), row.ID)
}

func (s *StopUnfiledSuite) judge(ctx context.Context, input, lease string) types.GuardJudgment {
	return Judge(ctx, Dependencies{}, Request{Host: "claude-code", Input: input, Lease: lease})
}

func (s *StopUnfiledSuite) subagent() map[string]any {
	return map[string]any{"agent_id": "a05d44b006f8f68af", "agent_transcript_path": "/nonexistent/child.jsonl"}
}

func (s *StopUnfiledSuite) TestAnUnfiledLeasedChildIsRefusedNamingTheExit() {
	v := s.stop(s.row(), "SubagentStop", s.subagent())
	s.Equal(decisionDeny, v.Decision)
	s.Equal(string(denyRuleStopUnfiled), v.Rule)
	s.Contains(v.Reason, "`magus job exit spec/worker --stdin`")
	s.Contains(v.Reason, "`magus job exit --schema`")
}

func (s *StopUnfiledSuite) TestAReadOnlyRowFilesAnEmptyChangeList() {
	row := s.row()
	row.ReadOnly, row.WritePaths = true, nil
	v := s.stop(row, "SubagentStop", s.subagent())
	s.Equal(decisionDeny, v.Decision)
	s.Contains(v.Reason, `"changed_paths": []`)
}

func (s *StopUnfiledSuite) TestTheSecondStopPassesAndSaysSo() {
	fields := s.subagent()
	fields["stop_hook_active"] = true
	v := s.stop(s.row(), "SubagentStop", fields)
	s.Equal(decisionAdvise, v.Decision)
	s.Equal(string(denyRuleStopUnfiled), v.Rule)
	s.Contains(v.Context, "`magus job wait spec/worker`")
}

func (s *StopUnfiledSuite) TestAFiledResultLetsTheChildStop() {
	row := s.row()
	row.Result = &types.JobResult{Job: row.ID}
	v := s.stop(row, "SubagentStop", s.subagent())
	s.NotEqual(string(denyRuleStopUnfiled), v.Rule)
	s.NotEqual(decisionDeny, v.Decision)
}

func (s *StopUnfiledSuite) TestARootStopIsNotJudged() {
	v := s.stop(s.row(), "Stop", nil)
	s.NotEqual(string(denyRuleStopUnfiled), v.Rule)
}
