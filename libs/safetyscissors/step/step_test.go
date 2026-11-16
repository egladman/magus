package step

import (
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
)

type RecordSuite struct{ suite.Suite }

func TestRecord(t *testing.T) { suite.Run(t, new(RecordSuite)) }

// The record is what a caller outside Go sends, so its keys are the contract.
func (s *RecordSuite) TestAStepRoundTripsAsJSON() {
	st := Step{APIVersion: APIVersion, ID: "c-1", Kind: "tool", Name: "refund_order", Input: map[string]any{"order": 42.0}}

	raw, err := codec.Marshal(st)
	s.Require().NoError(err)
	var got Step
	s.Require().NoError(codec.Unmarshal(raw, &got))

	s.JSONEq(`{"api_version":"magus.step/v1alpha1","id":"c-1","kind":"tool","name":"refund_order","input":{"order":42}}`, string(raw))
	s.Equal(st, got)
}

// A zero At is left out rather than written as the year 1.
func (s *RecordSuite) TestAStepWritesAtOnlyWhenSet() {
	at := time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC)
	raw, err := codec.Marshal(Step{APIVersion: APIVersion, Kind: "tool", At: at})
	s.Require().NoError(err)
	s.JSONEq(`{"api_version":"magus.step/v1alpha1","kind":"tool","at":"2026-11-16T01:02:03Z"}`, string(raw))
}

func (s *RecordSuite) TestASpecRoundTripsAsJSON() {
	spec := Spec{Kind: "tool", Name: "fetch", Capabilities: []string{"network"}, Paths: []string{"/path"},
		Command: "/cmd", SpawnDescriptionField: "/description", InputSchema: []byte(`{"type":"object"}`)}

	raw, err := codec.Marshal(spec)
	s.Require().NoError(err)
	var got Spec
	s.Require().NoError(codec.Unmarshal(raw, &got))
	s.Equal(spec, got)
	s.Contains(string(raw), `"input_schema":{"type":"object"}`, "the schema is JSON, not base64")

	bare, err := codec.Marshal(Spec{Kind: "tool"})
	s.Require().NoError(err)
	s.JSONEq(`{"kind":"tool","capabilities":null}`, string(bare))
}
