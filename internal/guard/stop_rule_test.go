package guard

import (
	"context"
	"errors"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

// stopRuleProbe is a workspace stop rule that records what it was asked and answers as told.
type stopRuleProbe struct {
	asked  []types.StopRequest
	answer types.GuardVerdict
	err    error
}

func (p *stopRuleProbe) rule() workspace.StopRule {
	return func(_ context.Context, req types.StopRequest, _ hint.Gate) (types.GuardVerdict, error) {
		p.asked = append(p.asked, req)
		return p.answer, p.err
	}
}

type StopRuleSuite struct {
	suite.Suite
	ctx  context.Context
	root string
}

func TestStopRuleSuite(t *testing.T) { suite.Run(t, new(StopRuleSuite)) }

func (s *StopRuleSuite) SetupTest() {
	s.ctx, s.root, _ = writeFixture(s.T())
}

// stop is a host stop envelope in the fixture's workspace.
func (s *StopRuleSuite) stop(event string, fields map[string]any) string {
	env := map[string]any{"session_id": "8f2c6a1e", "transcript_path": "/nonexistent/root.jsonl", "cwd": s.root, "hook_event_name": event, "stop_hook_active": false}
	for k, v := range fields {
		env[k] = v
	}
	return hookJSON(s.T(), env)
}

func (s *StopRuleSuite) judge(deps Dependencies, input string) types.GuardJudgment {
	return Judge(s.ctx, deps, Request{Host: "claude-code", Input: input})
}

func (s *StopRuleSuite) TestARootStopAsksTheRule() {
	probe := &stopRuleProbe{}
	v := s.judge(Dependencies{StopRule: probe.rule()}, s.stop("Stop", map[string]any{"stop_hook_active": true}))
	s.Equal(decisionPass, v.Decision, v.Reason+v.Context)
	s.Require().Len(probe.asked, 1)
	req := probe.asked[0]
	s.Equal("Stop", req.Event)
	s.True(req.StopHookActive)
	s.Equal("/nonexistent/root.jsonl", req.Transcript)
	s.Equal(types.AgentRoleRoot, req.Role)
	s.Equal(s.root, req.Workspace)
	s.Equal("8f2c6a1e", req.Session)
}

func (s *StopRuleSuite) TestTheRulesDenyRefusesTheStop() {
	probe := &stopRuleProbe{answer: types.GuardVerdict{Decision: types.GuardDeny, Reason: "row naming-lint is forkable", Rule: "idle-stop"}}
	v := s.judge(Dependencies{StopRule: probe.rule()}, s.stop("Stop", nil))
	s.Equal(decisionDeny, v.Decision)
	s.Equal("row naming-lint is forkable", v.Reason)
	s.Equal(workspaceShellPrefix+"idle-stop", v.Rule)
}

func (s *StopRuleSuite) TestTheRulesAdviceBecomesContext() {
	probe := &stopRuleProbe{answer: types.GuardVerdict{Decision: types.GuardAdvise, Reason: "two rows wait"}}
	v := s.judge(Dependencies{StopRule: probe.rule()}, s.stop("Stop", nil))
	s.Equal(decisionAdvise, v.Decision)
	s.Equal("two rows wait", v.Context)
	s.Equal(workspaceStopRule, v.Rule)
}

func (s *StopRuleSuite) TestTheApprovedRuleIsAsked() {
	probe := &stopRuleProbe{answer: types.GuardVerdict{Decision: types.GuardDeny, Reason: "committed rule"}}
	deps := Dependencies{ApprovedStopRule: func(context.Context) (workspace.StopRule, error) { return probe.rule(), nil }}
	v := s.judge(deps, s.stop("Stop", nil))
	s.Equal(decisionDeny, v.Decision)
	s.Equal("committed rule", v.Reason)
}

func (s *StopRuleSuite) TestABuiltinDenyIsNotAskedAbout() {
	probe := &stopRuleProbe{}
	input := s.stop("SubagentStop", map[string]any{"agent_id": "a05d44b006f8f68af", "agent_transcript_path": writeTranscript(s.T(), bgSearchCall, bgSearchResult)})
	v := s.judge(Dependencies{StopRule: probe.rule()}, input)
	s.Equal(decisionDeny, v.Decision)
	s.Equal(string(denyRuleBackgroundLeft), v.Rule)
	s.Empty(probe.asked)
}

func (s *StopRuleSuite) TestASubagentStopAsksTheRule() {
	probe := &stopRuleProbe{}
	input := s.stop("SubagentStop", map[string]any{"agent_id": "a05d44b006f8f68af", "agent_transcript_path": writeTranscript(s.T())})
	s.judge(Dependencies{StopRule: probe.rule()}, input)
	s.Require().Len(probe.asked, 1)
	s.Equal("SubagentStop", probe.asked[0].Event)
	s.Equal("a05d44b006f8f68af", probe.asked[0].Agent)
}

func (s *StopRuleSuite) TestAnotherEventIsNotAskedAbout() {
	probe := &stopRuleProbe{}
	s.judge(Dependencies{StopRule: probe.rule()}, s.stop("SessionEnd", nil))
	s.Empty(probe.asked)
}

func (s *StopRuleSuite) TestAFailingRuleLetsTheStopThroughAndSaysSo() {
	probe := &stopRuleProbe{err: errors.New("index out of range")}
	v := s.judge(Dependencies{StopRule: probe.rule()}, s.stop("Stop", nil))
	s.Equal(decisionAdvise, v.Decision)
	s.Contains(v.Context, `magus\guard.stop judged nothing`)
	s.Contains(v.Context, "index out of range")
}
