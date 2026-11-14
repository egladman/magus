package apiversion

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type ConditionSuite struct{ suite.Suite }

func TestConditionSuite(t *testing.T) { suite.Run(t, new(ConditionSuite)) }

func (s *ConditionSuite) TestMarshalUsesMetaV1Keys() {
	data, err := json.Marshal(Condition{
		Type: "Ready", Status: ConditionTrue, ObservedGeneration: 2,
		LastTransitionTime: time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC),
		Reason:             "Done", Message: "ok",
	})
	s.Require().NoError(err)
	s.JSONEq(`{"type":"Ready","status":"True","observedGeneration":2,
		"lastTransitionTime":"2026-11-11T00:00:00Z","reason":"Done","message":"ok"}`, string(data))
}

func (s *ConditionSuite) TestStatusConstants() {
	s.Equal(ConditionStatus("False"), ConditionFalse)
	s.Equal(ConditionStatus("Unknown"), ConditionUnknown)
}
