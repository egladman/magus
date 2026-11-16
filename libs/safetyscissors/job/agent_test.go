package job

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/suite"
)

type specSuite struct {
	suite.Suite
}

func TestSpec(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(specSuite))
}

func (s *specSuite) TestAResumedWorkerIsValid() {
	s.NoError(Instructed("ship it").Validate())
}

func (s *specSuite) TestAnEmptyWorkerTypeIsRefused() {
	s.ErrorContains(Agent{Instructions: "ship it"}.Validate(), "names no worker.type, one of resumed, new")
}

func (s *specSuite) TestAnUnknownWorkerTypeIsRefused() {
	s.ErrorContains(Agent{Worker: Worker{Type: "warm"}}.Validate(), `worker.type "warm" is not one of resumed, new`)
}

func (s *specSuite) TestANewWorkerNeedsAReason() {
	s.ErrorContains(Agent{Worker: Worker{Type: WorkerTypeNew}}.Validate(), "worker.reason")
}

func (s *specSuite) TestAResumedWorkerCarriesNoReason() {
	s.ErrorContains(Agent{Worker: Worker{Type: WorkerTypeResumed, Reason: "why"}}.Validate(), "carries none")
}

func (s *specSuite) TestTheWorkerTypeIsAlwaysWritten() {
	body, err := json.Marshal(Agent{Worker: Worker{Type: WorkerTypeResumed}})
	s.Require().NoError(err)
	s.JSONEq(`{"worker": {"type": "resumed"}}`, string(body), "a zero model and no instructions are left out")
}

func (s *specSuite) TestAModelNamesItsTierThenItsID() {
	s.Equal("principal", Model{Tier: "principal"}.String())
	s.Equal("opus", Model{ID: "opus"}.String())
	s.Equal("principal (opus)", Model{ID: "opus", Tier: "principal"}.String())
}

func (s *specSuite) TestANilSectionRequestsNoModel() {
	var spec *Agent
	s.True(spec.RequestedModel().IsZero())
	s.Nil(spec.Clone())
}

func (s *specSuite) TestTrimmedTrimsTheModelAndReason() {
	got := (&Agent{Model: Model{ID: " opus "}, Worker: Worker{Type: WorkerTypeNew, Reason: " why "}}).Trimmed()
	s.Equal(&Agent{Model: Model{ID: "opus"}, Worker: Worker{Type: WorkerTypeNew, Reason: "why"}}, got)
}
