package agent

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/json"
)

type stepSuite struct {
	suite.Suite
}

func TestStep(t *testing.T) { suite.Run(t, new(stepSuite)) }

// The record is what a caller outside Go sends, so its keys are the
// contract.
func (s *stepSuite) TestAStepRoundTripsAsJSON() {
	step := Step{APIVersion: StepAPIVersion, ID: "c-1", Kind: "tool", Name: "refund_order", Input: map[string]any{"order": 42.0}}

	raw, err := json.Marshal(step)
	s.Require().NoError(err)
	var got Step
	s.Require().NoError(json.Unmarshal(raw, &got))

	s.JSONEq(`{"api_version":"magus.step/v1alpha1","id":"c-1","kind":"tool","name":"refund_order","input":{"order":42}}`, string(raw))
	s.Equal(step, got)
}
