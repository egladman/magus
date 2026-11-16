package job

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
)

type agentSuite struct {
	suite.Suite
}

func TestAgent(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(agentSuite))
}

func (s *agentSuite) TestAResumedWorkerIsValid() {
	s.NoError(Instructed("ship it").Validate())
}

func (s *agentSuite) TestAnEmptyWorkerTypeIsRefused() {
	s.ErrorContains(Agent{Instructions: "ship it"}.Validate(), "names no worker.type, one of resumed, new")
}

func (s *agentSuite) TestAnUnknownWorkerTypeIsRefused() {
	s.ErrorContains(Agent{Worker: Worker{Type: "warm"}}.Validate(), `worker.type "warm" is not one of resumed, new`)
}

func (s *agentSuite) TestANewWorkerNeedsAReason() {
	s.ErrorContains(Agent{Worker: Worker{Type: WorkerTypeNew}}.Validate(), "worker.reason")
}

func (s *agentSuite) TestAResumedWorkerCarriesNoReason() {
	s.ErrorContains(Agent{Worker: Worker{Type: WorkerTypeResumed, Reason: "why"}}.Validate(), "carries none")
}

func (s *agentSuite) TestTheWorkerTypeIsAlwaysWritten() {
	body, err := codec.Marshal(Agent{Worker: Worker{Type: WorkerTypeResumed}})
	s.Require().NoError(err)
	s.JSONEq(`{"worker": {"type": "resumed"}}`, string(body), "a zero model and no instructions are left out")
}

func (s *agentSuite) TestAModelNamesItsTierThenItsID() {
	s.Equal("principal", Model{Tier: "principal"}.String())
	s.Equal("opus", Model{ID: "opus"}.String())
	s.Equal("principal (opus)", Model{ID: "opus", Tier: "principal"}.String())
}

func (s *agentSuite) TestANilSectionRequestsNoModel() {
	var spec *Agent
	s.True(spec.RequestedModel().IsZero())
	s.Nil(spec.Clone())
}

func (s *agentSuite) TestTrimmedTrimsTheModelAndReason() {
	got := (&Agent{Model: Model{ID: " opus "}, Worker: Worker{Type: WorkerTypeNew, Reason: " why "}}).Trimmed()
	s.Equal(&Agent{Model: Model{ID: "opus"}, Worker: Worker{Type: WorkerTypeNew, Reason: "why"}}, got)
}
