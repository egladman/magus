package guard

import (
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// UnrunGateSuite pins a gate that could not run a target here. A stopped
// docker daemon on 11-05 recorded a failed gate, and no narrow run could clear
// the push.
type UnrunGateSuite struct {
	suite.Suite
	root, dir string
}

const unrunCommit = "42a1c0cc84b21f0e9d8c7b6a5f4e3d2c1b0a9f8"

func TestUnrunGateSuite(t *testing.T) {
	suite.Run(t, new(UnrunGateSuite))
}

func (s *UnrunGateSuite) SetupTest() {
	testkit.Isolate(s.T())
	s.root = s.T().TempDir()
	dir, err := sessions.Dir(s.root)
	s.Require().NoError(err)
	s.dir = dir
}

func (s *UnrunGateSuite) record(outcome string, scope sessions.GateScope) {
	s.Require().NoError(sessions.RecordGate(s.dir, sessions.GateResult{
		Target: types.TargetCI, Scope: scope, Commit: unrunCommit, Outcome: outcome,
		Unrun: []string{"images/magus:image-build: MGS3004 docker is installed but not ready"},
	}, sessions.InvocationStart{Workspace: s.root}))
	// Records order by millisecond.
	time.Sleep(2 * time.Millisecond)
}

func (s *UnrunGateSuite) TestAPushTheGateCouldNotFullyRunPassesNamingWhatIsOwed() {
	s.record(sessions.OutcomeUnrun, sessions.GateScopeAffected)
	cover, rec := gateRecordAt(s.root, unrunCommit[:12])
	s.Require().Equal(gateUnrun, cover)

	ev := pushEvidence{cover: cover, commit: unrunCommit[:12], unrun: rec}
	decision, _ := gradePushWithoutGate(ev, "")
	s.Empty(decision)
	s.Contains(ev.admitted(), "PR CI owes the rest, which could not run here:\n  images/magus:image-build: MGS3004")
}

func (s *UnrunGateSuite) TestAFailedGateAtTheCommitOutranksALaterUnrunOne() {
	s.record(sessions.OutcomeFail, sessions.GateScopeAffected)
	s.record(sessions.OutcomeUnrun, sessions.GateScopeAffected)
	s.Equal(gateFailed, gateVerdictAt(s.root, unrunCommit[:12]))
}

func (s *UnrunGateSuite) TestAnUnrunGateOverNamedProjectsIsProjectScope() {
	s.record(sessions.OutcomeUnrun, sessions.GateScopeProject)
	s.Equal(gateProjectScoped, gateVerdictAt(s.root, unrunCommit[:12]))
}

func (s *UnrunGateSuite) TestAnUnrunBranchGateOutranksAProjectPass() {
	s.record(sessions.OutcomeUnrun, sessions.GateScopeAffected)
	s.record(sessions.OutcomePass, sessions.GateScopeProject)
	s.Equal(gateUnrun, gateVerdictAt(s.root, unrunCommit[:12]))
}
