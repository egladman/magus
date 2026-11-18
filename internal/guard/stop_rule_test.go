package guard

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/workspace"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

// stopRuleProbe is a workspace stop rule that records what it was asked and answers as told.
type stopRuleProbe struct {
	asked  []types.StopRequest
	answer verdict.Verdict
	err    error
}

func (p *stopRuleProbe) rule() workspace.StopRule {
	return func(_ context.Context, req types.StopRequest, _ hint.Gate) (verdict.Verdict, error) {
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
func (s *StopRuleSuite) judge(deps Dependencies, input string) verdict.Judgment {
	v := Judge(s.testContext(), deps, Request{Host: "claude-code", Input: input})
	v.APIVersion, v.Lease, v.LeaseFrom, v.NextSteps = "", "", "", nil
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
	s.Equal(verdict.Judgment{Decision: verdict.Defer}, v)
	s.Equal(types.StopRequest{Host: "claude-code", Session: "8f2c6a1e", Role: types.AgentRoleRoot, Event: "Stop", StopHookActive: true,
		Transcript: "/nonexistent/root.jsonl", Workspace: s.root}, s.asked(probe))
}

func (s *StopRuleSuite) TestTheRulesDenyRefusesTheStop() {
	probe := &stopRuleProbe{answer: verdict.Verdict{Decision: verdict.Deny, Reason: "row naming-lint is forkable", Rule: "idle-stop"}}
	v := s.judge(Dependencies{StopRule: probe.rule()}, s.stop("Stop", nil))
	s.Equal(verdict.Judgment{Decision: verdict.Deny, Reason: "row naming-lint is forkable", Rule: workspaceShellPrefix + "idle-stop"}, v)
}

func (s *StopRuleSuite) TestTheRulesAdviceBecomesContext() {
	probe := &stopRuleProbe{answer: verdict.Verdict{Decision: verdict.Advise, Reason: "two rows wait"}}
	v := s.judge(Dependencies{StopRule: probe.rule()}, s.stop("Stop", nil))
	s.Equal(verdict.Judgment{Decision: verdict.Advise, Context: "two rows wait", Rule: workspaceStopRule}, v)
}

func (s *StopRuleSuite) TestTheApprovedRuleIsAsked() {
	probe := &stopRuleProbe{answer: verdict.Verdict{Decision: verdict.Deny, Reason: "committed rule"}}
	deps := Dependencies{ApprovedStopRule: func(context.Context) (workspace.StopRule, error) { return probe.rule(), nil }}
	v := s.judge(deps, s.stop("Stop", nil))
	s.Equal(verdict.Judgment{Decision: verdict.Deny, Reason: "committed rule", Rule: workspaceStopRule}, v)
}

func (s *StopRuleSuite) TestABuiltinDenyIsNotAskedAbout() {
	probe := &stopRuleProbe{}
	input := s.stop("SubagentStop", map[string]any{"agent_id": "a05d44b006f8f68af", "agent_transcript_path": writeTranscript(s.T(), bgSearchCall, bgSearchResult)})
	v := s.judge(Dependencies{StopRule: probe.rule()}, input)
	s.Equal(string(denyRuleBackgroundLeft), v.Rule, v.Reason)
	s.Equal(verdict.Judgment{Decision: verdict.Deny, Reason: v.Reason, Rule: string(denyRuleBackgroundLeft)}, v)
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
	s.Equal(verdict.Advise, v.Decision)
	s.Contains(v.Context, `magus\guard.stop judged nothing`)
	s.Contains(v.Context, "index out of range")
}

func (s *StopRuleSuite) TestTheRulesDenyOfAHeldStopPassesAsAdvice() {
	probe := &stopRuleProbe{answer: verdict.Verdict{Decision: verdict.Deny, Reason: "row naming-lint is forkable", Rule: "idle-stop"}}
	v := s.judge(Dependencies{StopRule: probe.rule()}, s.stop("Stop", map[string]any{"stop_hook_active": true}))
	s.Equal(verdict.Judgment{Decision: verdict.Advise, Rule: workspaceShellPrefix + "idle-stop",
		Context: "magus workspace: the host marks this as a second stop, so " + workspaceShellPrefix + "idle-stop lets it through.\nrow naming-lint is forkable"}, v)
}

func (s *StopRuleSuite) TestTheRulesAdviceOfAHeldStopStaysAdvice() {
	probe := &stopRuleProbe{answer: verdict.Verdict{Decision: verdict.Advise, Reason: "two rows wait"}}
	v := s.judge(Dependencies{StopRule: probe.rule()}, s.stop("Stop", map[string]any{"stop_hook_active": true}))
	s.Equal(verdict.Judgment{Decision: verdict.Advise, Context: "two rows wait", Rule: workspaceStopRule}, v)
}

// spawnInBackground starts a try of a live job id the way a passing spawn does, the root
// session's spawn asking for the host's background mode when background is true.
func (s *StopRuleSuite) spawnInBackground(id, spawner string, background bool) {
	at := hookLocation(s.testContext(), Dependencies{})
	_, err := job.NewStore(at.jobs()).Update(s.T().Context(), id, func(row *jobtypes.Job) { *row = jobtypes.Job{ID: id, State: jobtypes.StateDeclared} })
	s.Require().NoError(err)
	facts := hint.NewGate(at.cacheDir, FactsKey("claude-code", "8f2c6a1e"))
	env := hookRequest{IsSpawn: true, Spawn: spawnFields{Description: "root/worker " + id, Background: background}}
	titled := func() (jobtypes.Job, bool) { return jobtypes.Job{ID: id}, true }
	s.Require().Empty(startNamedAttempt(s.testContext(), at, facts, env, spawner, titled, time.Now()))
}

func (s *StopRuleSuite) TestARootStopOverABackgroundSpawnNamesItAndPassesTheRulesDeny() {
	s.spawnInBackground("naming-lint", "", true)
	s.spawnInBackground("go-indexer", "", false)
	probe := &stopRuleProbe{answer: verdict.Verdict{Decision: verdict.Deny, Reason: "row go-indexer stopped with no result", Rule: "idle-stop"}}
	v := s.judge(Dependencies{StopRule: probe.rule()}, s.stop("Stop", nil))
	s.Equal([]string{"naming-lint"}, s.asked(probe).Background, "only the background spawn is tracked")
	s.Equal(verdict.Judgment{Decision: verdict.Advise, Rule: workspaceShellPrefix + "idle-stop",
		Context: "magus workspace: the host wakes this session when the job it spawned in the background ends (naming-lint), so " +
			workspaceShellPrefix + "idle-stop lets it through.\nrow go-indexer stopped with no result"}, v)
}

func (s *StopRuleSuite) TestAnEndedBackgroundSpawnNoLongerHoldsTheStopOpen() {
	s.spawnInBackground("naming-lint", "", true)
	at := hookLocation(s.testContext(), Dependencies{})
	s.Require().NoError(job.NewStore(at.jobs()).EndAttempt(s.T().Context(), "naming-lint", jobtypes.AttemptEndHandback, time.Now()))
	probe := &stopRuleProbe{answer: verdict.Verdict{Decision: verdict.Deny, Reason: "stopped with no result", Rule: "idle-stop"}}
	v := s.judge(Dependencies{StopRule: probe.rule()}, s.stop("Stop", nil))
	s.Empty(s.asked(probe).Background)
	s.Equal(verdict.Deny, v.Decision)
}

func (s *StopRuleSuite) TestASubagentsBackgroundSpawnIsItsOwnAndItsStopStaysRefused() {
	s.spawnInBackground("naming-lint", "a05d44b006f8f68af", true)
	probe := &stopRuleProbe{answer: verdict.Verdict{Decision: verdict.Deny, Reason: "file a result", Rule: "idle-stop"}}
	transcript := writeTranscript(s.T())
	v := s.judge(Dependencies{StopRule: probe.rule()}, s.stop("SubagentStop", map[string]any{"agent_id": "a05d44b006f8f68af", "agent_transcript_path": transcript}))
	s.Equal([]string{"naming-lint"}, s.asked(probe).Background)
	s.Equal(verdict.Deny, v.Decision, "nothing says the host wakes a subagent for its spawn")

	root := &stopRuleProbe{}
	s.judge(Dependencies{StopRule: root.rule()}, s.stop("Stop", nil))
	s.Empty(s.asked(root).Background, "the root did not spawn it")
}

func (s *StopRuleSuite) testContext() context.Context { return s.layers.of(s.T()) }
