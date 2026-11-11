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
	layers ctxLayers
	root   string
}

func TestStopRuleSuite(t *testing.T) { suite.Run(t, new(StopRuleSuite)) }

func (s *StopRuleSuite) SetupTest() {
	writeCtx, root, _ := writeFixture(s.T())
	s.layers, s.root = locationLayers(writeCtx), root
}

// stop is a host stop envelope in the fixture's workspace.
func (s *StopRuleSuite) stop(event string, fields map[string]any) string {
	env := map[string]any{"session_id": "8f2c6a1e", "transcript_path": "/nonexistent/root.jsonl", "cwd": s.root, "hook_event_name": event, "stop_hook_active": false}
	for k, v := range fields {
		env[k] = v
	}
	return hookJSON(s.T(), env)
}

// judge is the stop's judgment with the fields the lease and the guard's version decide
// cleared, so a case compares the rest whole.
func (s *StopRuleSuite) judge(deps Dependencies, input string) types.GuardJudgment {
	v := Judge(s.testContext(), deps, Request{Host: "claude-code", Input: input})
	v.APIVersion, v.Lease, v.LeaseFrom, v.Next = "", "", "", nil
	return v
}

// asked is the one request probe saw, with the fields the fixture's checkout, transcript
// and context window decide cleared.
func (s *StopRuleSuite) asked(probe *stopRuleProbe) types.StopRequest {
	s.Require().Len(probe.asked, 1)
	req := probe.asked[0]
	var zero types.StopRequest
	req.Checkout, req.Window, req.Thread, req.ThreadFrom, req.Orchestration, req.Dir = nil, nil, zero.Thread, zero.ThreadFrom, zero.Orchestration, ""
	return req
}

func (s *StopRuleSuite) TestARootStopAsksTheRule() {
	probe := &stopRuleProbe{}
	v := s.judge(Dependencies{StopRule: probe.rule()}, s.stop("Stop", map[string]any{"stop_hook_active": true}))
	s.Equal(types.GuardJudgment{Decision: decisionPass}, v)
	s.Equal(types.StopRequest{Host: "claude-code", Session: "8f2c6a1e", Role: types.AgentRoleRoot, Event: "Stop", StopHookActive: true,
		Transcript: "/nonexistent/root.jsonl", Workspace: s.root}, s.asked(probe))
}

func (s *StopRuleSuite) TestTheRulesDenyRefusesTheStop() {
	probe := &stopRuleProbe{answer: types.GuardVerdict{Decision: types.GuardDeny, Reason: "row naming-lint is forkable", Rule: "idle-stop"}}
	v := s.judge(Dependencies{StopRule: probe.rule()}, s.stop("Stop", nil))
	s.Equal(types.GuardJudgment{Decision: decisionDeny, Reason: "row naming-lint is forkable", Rule: workspaceShellPrefix + "idle-stop"}, v)
}

func (s *StopRuleSuite) TestTheRulesAdviceBecomesContext() {
	probe := &stopRuleProbe{answer: types.GuardVerdict{Decision: types.GuardAdvise, Reason: "two rows wait"}}
	v := s.judge(Dependencies{StopRule: probe.rule()}, s.stop("Stop", nil))
	s.Equal(types.GuardJudgment{Decision: decisionAdvise, Context: "two rows wait", Rule: workspaceStopRule}, v)
}

func (s *StopRuleSuite) TestTheApprovedRuleIsAsked() {
	probe := &stopRuleProbe{answer: types.GuardVerdict{Decision: types.GuardDeny, Reason: "committed rule"}}
	deps := Dependencies{ApprovedStopRule: func(context.Context) (workspace.StopRule, error) { return probe.rule(), nil }}
	v := s.judge(deps, s.stop("Stop", nil))
	s.Equal(types.GuardJudgment{Decision: decisionDeny, Reason: "committed rule", Rule: workspaceStopRule}, v)
}

func (s *StopRuleSuite) TestABuiltinDenyIsNotAskedAbout() {
	probe := &stopRuleProbe{}
	input := s.stop("SubagentStop", map[string]any{"agent_id": "a05d44b006f8f68af", "agent_transcript_path": writeTranscript(s.T(), bgSearchCall, bgSearchResult)})
	v := s.judge(Dependencies{StopRule: probe.rule()}, input)
	s.Equal(string(denyRuleBackgroundLeft), v.Rule, v.Reason)
	s.Equal(types.GuardJudgment{Decision: decisionDeny, Reason: v.Reason, Rule: string(denyRuleBackgroundLeft)}, v)
	s.Empty(probe.asked)
}

func (s *StopRuleSuite) TestASubagentStopAsksTheRule() {
	probe := &stopRuleProbe{}
	transcript := writeTranscript(s.T())
	s.judge(Dependencies{StopRule: probe.rule()}, s.stop("SubagentStop", map[string]any{"agent_id": "a05d44b006f8f68af", "agent_transcript_path": transcript}))
	s.Equal(types.StopRequest{Host: "claude-code", Session: "8f2c6a1e", Agent: "a05d44b006f8f68af", Role: types.AgentRoleRoot, Event: "SubagentStop",
		Transcript: transcript, Workspace: s.root}, s.asked(probe))
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

func (s *StopRuleSuite) testContext() context.Context { return s.layers.of(s.T()) }
