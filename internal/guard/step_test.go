package guard

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	typesagent "github.com/egladman/magus/types/agent"
)

type jsonPointerSuite struct {
	suite.Suite
}

func TestJSONPointer(t *testing.T) { suite.Run(t, new(jsonPointerSuite)) }

func (s *jsonPointerSuite) TestEscapedTokensReadTheirKeys() {
	ptr, err := parseJSONPointer("/a~1b/c~0d")
	s.Require().NoError(err)

	s.Equal("x", ptr.get(map[string]any{"a/b": map[string]any{"c~d": "x"}}))
}

func (s *jsonPointerSuite) TestAnArrayIndexReadsItsElement() {
	ptr, err := parseJSONPointer("/files/1")
	s.Require().NoError(err)

	s.Equal("b", ptr.get(map[string]any{"files": []any{"a", "b"}}))
	s.Nil(ptr.get(map[string]any{"files": []any{"a"}}), "an index past the end names nothing")
}

func (s *jsonPointerSuite) TestAPointerWithoutALeadingSlashIsRefused() {
	_, err := parseJSONPointer("command")
	s.ErrorContains(err, "must start with /")
}

func (s *jsonPointerSuite) TestABadEscapeIsRefused() {
	_, err := parseJSONPointer("/a~2")
	s.ErrorContains(err, "~ escapes only 0 or 1")
}

type stepTableSuite struct {
	suite.Suite
}

func TestStepTable(t *testing.T) { suite.Run(t, new(stepTableSuite)) }

func passStep(context.Context, typesagent.StepRequest) (types.GuardVerdict, error) {
	return types.GuardVerdict{}, nil
}

func (s *stepTableSuite) TestANamedRowWinsOverItsKindsCatchAll() {
	all := typesagent.StepRow{Kind: "tool", Capabilities: []string{"inert"}}
	named := typesagent.StepRow{Kind: "tool", Name: "refund", Capabilities: []string{"network"}}
	table, err := NewStepTable([]typesagent.StepRow{all, named}, []StepRule{passStep})
	s.Require().NoError(err)

	refund, ok := table.covering("tool", "refund")
	s.Require().True(ok)
	other, ok := table.covering("tool", "search")
	s.Require().True(ok)

	s.Equal([]string{"network"}, refund.Capabilities)
	s.Equal([]string{"inert"}, other.Capabilities)
}

var networkRow = typesagent.StepRow{Kind: "refund", Capabilities: []string{"network"}}

func (s *stepTableSuite) TestARowNothingJudgesIsRefusedWithNoKindRule() {
	table, err := NewStepTable([]typesagent.StepRow{networkRow}, nil)
	s.Require().NoError(err)
	err = table.CheckJudged([]string{"egress"})
	s.ErrorContains(err, `kind "refund"`)
	s.ErrorContains(err, `magus\guard.step`)
}

func (s *stepTableSuite) TestAKindRuleJudgesARowNothingElseJudges() {
	table, err := NewStepTable([]typesagent.StepRow{networkRow}, nil)
	s.Require().NoError(err)
	s.NoError(table.CheckJudged([]string{"refund"}))
}

func (s *stepTableSuite) TestAGoRuleJudgesEveryRow() {
	table, err := NewStepTable([]typesagent.StepRow{networkRow}, []StepRule{passStep})
	s.Require().NoError(err)
	s.NoError(table.CheckJudged(nil))
}

func (s *stepTableSuite) TestAnUnknownCapabilityIsRefused() {
	_, err := NewStepTable([]typesagent.StepRow{{Kind: "tool", Capabilities: []string{"teleports"}}}, nil)
	s.ErrorContains(err, `unknown capability "teleports"`)
}

func (s *stepTableSuite) TestAnInertRowWithAnotherCapabilityIsRefused() {
	_, err := NewStepTable([]typesagent.StepRow{{Kind: "tool", Capabilities: []string{"inert", "network"}}}, []StepRule{passStep})
	s.ErrorContains(err, "inert says nothing judges the row")
}

func (s *stepTableSuite) TestASpawnsRowNeedsNoRule() {
	_, err := NewStepTable([]typesagent.StepRow{{Kind: "delegate", Capabilities: []string{"spawns"}, SpawnDescriptionField: "/title"}}, nil)
	s.NoError(err)
}

func (s *stepTableSuite) TestASpawnsRowThatAlsoRunsIsRefused() {
	_, err := NewStepTable([]typesagent.StepRow{{Kind: "delegate", Capabilities: []string{"spawns", "runs"}, Command: "/command"}}, nil)
	s.ErrorContains(err, "does not share a row with runs")
}

func (s *stepTableSuite) TestADescriptionFieldWithoutSpawnsIsRefused() {
	_, err := NewStepTable([]typesagent.StepRow{{Kind: "plan", Capabilities: []string{"inert"}, SpawnDescriptionField: "/title"}}, nil)
	s.ErrorContains(err, "spawn_description_field goes with spawns")
}

func (s *stepTableSuite) TestADescriptionFieldThatDoesNotParseIsRefused() {
	_, err := NewStepTable([]typesagent.StepRow{{Kind: "delegate", Capabilities: []string{"spawns"}, SpawnDescriptionField: "title"}}, nil)
	s.ErrorContains(err, "spawn_description_field")
}

// stepRecordSuite pins how a hook or `magus shell` reads a Step record.
type stepRecordSuite struct {
	suite.Suite
}

func TestStepRecord(t *testing.T) { suite.Run(t, new(stepRecordSuite)) }

func (s *stepRecordSuite) record(step typesagent.Step) string {
	raw, err := json.Marshal(step)
	s.Require().NoError(err)
	return string(raw)
}

func (s *stepRecordSuite) toolStep(name string, input map[string]any) typesagent.Step {
	return typesagent.Step{APIVersion: typesagent.StepAPIVersion, Kind: hostToolKind, Name: name, Input: input, Session: "s1", Dir: "/r"}
}

func (s *stepRecordSuite) TestAToolStepReadsAsItsEnvelope() {
	req, ok := decodeHookEnvelope(s.record(s.toolStep("Bash", map[string]any{"command": "ls"})), agent.ToolRows{})

	s.Require().True(ok)
	s.Equal(hookRequest{Value: "ls", Cwd: "/r", Who: hookAttribution{Session: "s1"}}, req)
}

func (s *stepRecordSuite) TestAnotherKindIsNoEnvelope() {
	step := s.toolStep("card", nil)
	step.Kind = "refund"

	_, ok := decodeHookEnvelope(s.record(step), agent.ToolRows{})

	s.False(ok)
}

func (s *stepRecordSuite) TestAToolStepNoRowCoversIsLeftToTheEnvelope() {
	_, ok := declaredStep(s.T().Context(), s.record(s.toolStep("Bash", nil)), StepTable{})

	s.False(ok)
}

func (s *stepRecordSuite) TestAToolStepARowCoversIsJudgedAsAStep() {
	table, err := NewStepTable([]typesagent.StepRow{{Kind: hostToolKind, Capabilities: []string{"inert"}}}, nil)
	s.Require().NoError(err)

	step, ok := declaredStep(s.T().Context(), s.record(s.toolStep("Bash", nil)), table)

	s.Require().True(ok)
	s.Equal("Bash", step.Name)
}

func (s *stepRecordSuite) TestANamelessToolStepIsJudgedAsAStep() {
	_, ok := declaredStep(s.T().Context(), s.record(s.toolStep("", nil)), StepTable{})

	s.True(ok, "a tool step naming no tool has no envelope to stand for")
}

func (s *stepRecordSuite) TestAHostEnvelopeIsNoStepRecord() {
	_, ok := stepRecord(`{"tool_name":"Bash","tool_input":{"command":"echo magus.step/v1alpha1"}}`)

	s.False(ok)
}

func (s *stepRecordSuite) TestAnUndeclaredKindSentToJudgeIsDenied() {
	ctx, _ := spawnFixture(s.T())
	step := s.toolStep("card", nil)
	step.Kind = "refund"

	j := Judge(ctx, Dependencies{}, Request{Input: s.record(step)})

	s.Equal(types.GuardJudgment{APIVersion: agent.GuardAPIVersion, Decision: decisionDeny, Reason: j.Reason, Rule: string(denyRuleUnjudgedStep)}, j)
	s.Contains(j.Reason, `kind "refund"`)
}

func (s *stepRecordSuite) TestAStepJudgeCannotReadIsDenied() {
	ctx, _ := spawnFixture(s.T())
	step := s.toolStep("Bash", nil)
	step.APIVersion = "magus.step/v9"

	j := Judge(ctx, Dependencies{}, Request{Input: s.record(step)})

	s.Equal(types.GuardJudgment{APIVersion: agent.GuardAPIVersion, Decision: decisionDeny, Reason: j.Reason, Rule: string(denyRuleStepInput)}, j)
	s.Contains(j.Reason, "api_version")
}

func (s *stepTableSuite) TestARunsRowWithoutACommandIsRefused() {
	_, err := NewStepTable([]typesagent.StepRow{{Kind: "shell", Capabilities: []string{"runs"}}}, nil)
	s.ErrorContains(err, "runs and command go together")
}

func (s *stepTableSuite) TestANilRuleIsRefused() {
	_, err := NewStepTable(nil, []StepRule{nil})
	s.ErrorContains(err, "a step rule is nil")
}

type stricterStepSuite struct {
	suite.Suite
}

func TestStricterStep(t *testing.T) { suite.Run(t, new(stricterStepSuite)) }

func stepJudged(decision, text string) types.GuardJudgment {
	if decision == decisionAdvise {
		return types.GuardJudgment{Decision: decision, Context: text}
	}
	return types.GuardJudgment{Decision: decision, Reason: text}
}

func (s *stricterStepSuite) TestADenyOutranksAnAsk() {
	s.Equal(decisionDeny, stricterStep(stepJudged(decisionAsk, "a"), stepJudged(decisionDeny, "d")).Decision)
}

func (s *stricterStepSuite) TestTwoAdvisoriesKeepBothContexts() {
	s.Equal("one\n\ntwo", stricterStep(stepJudged(decisionAdvise, "one"), stepJudged(decisionAdvise, "two")).Context)
}

func (s *stricterStepSuite) TestTwoDeniesKeepTheFirst() {
	s.Equal("first", stricterStep(stepJudged(decisionDeny, "first"), stepJudged(decisionDeny, "second")).Reason)
}

func (s *stricterStepSuite) TestTheStricterKeepsTheFirstLease() {
	a := stepJudged(decisionPass, "")
	a.Lease = "task"

	s.Equal("task", stricterStep(a, stepJudged(decisionDeny, "d")).Lease)
}
