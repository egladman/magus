package sessions

import (
	"fmt"
	"testing"

	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/stretchr/testify/suite"
)

type GateFoldSuite struct{ suite.Suite }

func TestGateFold(t *testing.T) { suite.Run(t, new(GateFoldSuite)) }

// The gate read folds only gate verdicts, and a gate result appended later is seen by
// the next read.
func (s *GateFoldSuite) TestReadGateResultsFoldsOnlyGateVerdicts() {
	t := s.T()
	dir := t.TempDir()
	writeRaw(t, dir, "a", []session.Record{{
		V: session.APIVersion, Invocation: "a", Seq: 1, Kind: session.KindAgentEvent, Ts: 1,
		Payload: fmt.Appendf(nil, `{"host":"h","event":"file.read","ref":"1","at":1,"text":%q}`, "x.go"),
	}})
	s.Require().NoError(RecordGate(s.T().Context(), dir, GateResult{Target: "ci", Scope: GateScopeAffected, Commit: "abc123", Outcome: session.OutcomePass}, session.InvocationStart{}))

	_, err := session.FoldAgentEvents(s.T().Context(), dir)
	s.Require().NoError(err)
	gates, err := ReadGateResults(s.T().Context(), dir)
	s.Require().NoError(err)
	s.Require().Len(gates.Records, 1)
	s.Equal(KindGateResult, gates.Records[0].Kind)

	s.Require().NoError(RecordGate(s.T().Context(), dir, GateResult{Target: "ci", Scope: GateScopeAffected, Commit: "def456", Outcome: session.OutcomeFail}, session.InvocationStart{}))
	gates, err = ReadGateResults(s.T().Context(), dir)
	s.Require().NoError(err)
	rec, ok := GateAt(gates, "def456", "ci")
	s.Require().True(ok)
	s.Equal(session.OutcomeFail, rec.Outcome)
}
