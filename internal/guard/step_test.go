package guard

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

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

func (s *stepTableSuite) TestAnUnknownCapabilityIsRefused() {
	_, err := NewStepTable([]typesagent.StepRow{{Kind: "tool", Capabilities: []string{"teleports"}}}, nil)
	s.ErrorContains(err, `unknown capability "teleports"`)
}

func (s *stepTableSuite) TestAnInertRowWithAnotherCapabilityIsRefused() {
	_, err := NewStepTable([]typesagent.StepRow{{Kind: "tool", Capabilities: []string{"inert", "network"}}}, []StepRule{passStep})
	s.ErrorContains(err, "inert says nothing judges the row")
}

func (s *stepTableSuite) TestASpawnsRowIsRefusedUntilJudgeJudgesSpawns() {
	_, err := NewStepTable([]typesagent.StepRow{{Kind: "delegate", Capabilities: []string{"spawns"}}}, nil)
	s.ErrorContains(err, "spawns is not judged through Judge yet")
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
