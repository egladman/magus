package job

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type AuthorizeSuite struct {
	suite.Suite
}

func TestAuthorizeSuite(t *testing.T) { suite.Run(t, new(AuthorizeSuite)) }

func (s *AuthorizeSuite) worker() Job {
	return Job{ID: "w", State: StateRunning, Paths: Paths{Write: []string{"internal/x", "a.go"}, Deny: []string{"secret"}}}
}

func (s *AuthorizeSuite) TestAnUnboundActorWritesAnything() {
	prev := s.worker()
	next := prev
	next.Paths.Write = append(next.Paths.Write, "b.go")

	s.NoError(AuthorizeRow(Actor{}, "w", AsDeclaration, prev, next, true, []Job{prev}, specChecks{}))
}

func (s *AuthorizeSuite) TestABoundWorkerShrinksButNeverWidensItsWritePaths() {
	prev := s.worker()
	shrunk, widened := prev, prev
	shrunk.Paths.Write = []string{"a.go"}
	widened.Paths.Write = []string{"internal/x", "a.go", "b.go"}

	s.Require().NoError(AuthorizeRow(Actor{Lease: "w"}, "w", AsDeclaration, prev, shrunk, true, []Job{prev}, specChecks{}))
	var refused *RefusedError
	s.Require().ErrorAs(AuthorizeRow(Actor{Lease: "w"}, "w", AsDeclaration, prev, widened, true, []Job{prev}, specChecks{}), &refused)
	s.Contains(refused.Rule, "SHRINK")
	s.Equal("ask your orchestrator to make this write", refused.Remedy)
}

func (s *AuthorizeSuite) TestABoundWorkerEndsItsRowButNeverPassesIt() {
	prev := s.worker()
	failed, passed := prev, prev
	failed.State, passed.State = StateFail, StatePass

	s.Require().NoError(AuthorizeRow(Actor{Lease: "w"}, "w", AsDeclaration, prev, failed, true, []Job{prev}, specChecks{}))
	s.Error(AuthorizeRow(Actor{Lease: "w"}, "w", AsDeclaration, prev, passed, true, []Job{prev}, specChecks{}))
}

func (s *AuthorizeSuite) TestAChildStaysInsideItsParentsBoundary() {
	parent := s.worker()
	inside := Job{ID: "c", Parent: "w", Paths: Paths{Write: []string{"internal/x/y.go"}, Deny: []string{"secret"}}}
	wider := Job{ID: "c", Parent: "w", Paths: Paths{Write: []string{"elsewhere.go"}, Deny: []string{"secret"}}}
	dropsDeny := Job{ID: "c", Parent: "w", Paths: Paths{Write: []string{"a.go"}}}
	actor, rows := Actor{Lease: "w"}, []Job{parent}

	s.Require().NoError(AuthorizeRow(actor, "c", AsDeclaration, Job{}, inside, false, rows, specChecks{}))
	s.Error(AuthorizeRow(actor, "c", AsDeclaration, Job{}, wider, false, rows, specChecks{}))
	s.Error(AuthorizeRow(actor, "c", AsDeclaration, Job{}, dropsDeny, false, rows, specChecks{}))
}

func (s *AuthorizeSuite) TestAVerdictPassesOnlyARowBelowTheHolder() {
	parent, child := s.worker(), Job{ID: "c", Parent: "w", State: StateExited}
	passed := child
	passed.State = StatePass
	rows := []Job{parent, child}

	s.Require().NoError(AuthorizeRow(Actor{Lease: "w"}, "c", AsVerdict, child, passed, true, rows, specChecks{}))
	s.Error(AuthorizeRow(Actor{Lease: "c"}, "c", AsVerdict, child, passed, true, rows, specChecks{}), "a holder never grades its own row")
}

func (s *AuthorizeSuite) TestAnUnstampedCallerWritesNoRow() {
	prev := s.worker()

	var refused *RefusedError
	s.Require().ErrorAs(AuthorizeRow(Actor{Unstamped: true}, "w", AsDeclaration, prev, prev, true, []Job{prev}, specChecks{}), &refused)
	s.Equal(unstampedRemedy, refused.Remedy)
}

func (s *AuthorizeSuite) TestAClaimIsRefusedWithNoClaimRule() {
	prev := s.worker()

	s.Error(AuthorizeRow(Actor{}, "w", AsClaim, prev, prev, true, []Job{prev}, specChecks{}), "a claim graded by nothing is refused")
}

func (s *AuthorizeSuite) TestOnlyTheRootOrItsIntegratorAdvancesAuthority() {
	row, integrator := s.worker(), Job{ID: "i", State: StateRunning, Integrates: []string{"w"}}
	rows := []Job{row, integrator}

	s.NoError(AuthorizeRow(Actor{}, "w", AsAdvance, row, row, true, rows, specChecks{}))
	s.NoError(AuthorizeRow(Actor{Lease: "i"}, "w", AsAdvance, row, row, true, rows, specChecks{}))
	s.Error(AuthorizeRow(Actor{Lease: "w"}, "w", AsAdvance, row, row, true, rows, specChecks{}))
}

func (s *AuthorizeSuite) TestChangedFieldsNamesTheDeclaredFieldsInWireSpelling() {
	prev := s.worker()
	next := prev
	next.Paths.Read, next.MaxAttempts = []string{"docs"}, 2

	changed, err := ChangedFields(prev, next, specChecks{})
	s.Require().NoError(err)
	s.Equal([]string{"paths.read", "max_attempts"}, changed)
}

// A worker that could rewrite its own goal's check could grade itself done, so a change
// the caller's CheckSpec sees is a change to the goals.
func (s *AuthorizeSuite) TestAWorkerMayNotRewriteItsOwnGoalsCheck() {
	prev := s.worker()
	prev.Goals = []Goal{{ID: "t", Kind: GoalKindCheck, Expect: ExpectPassed, Check: checkJSON("test", "."), Accept: []Basis{BasisWitnessed}}}
	respelled, rewritten := prev.Clone(), prev.Clone()
	respelled.Goals[0].Check = rawCheck(`{"target":"test"}`)
	rewritten.Goals[0].Check = checkJSON("test", ".", "-run", "Nothing")

	s.Require().NoError(AuthorizeRow(Actor{Lease: "w"}, "w", AsDeclaration, prev, respelled, true, []Job{prev}, specChecks{}))
	var refused *RefusedError
	s.Require().ErrorAs(AuthorizeRow(Actor{Lease: "w"}, "w", AsDeclaration, prev, rewritten, true, []Job{prev}, specChecks{}), &refused)
	s.Contains(refused.Rule, "goals")
}

// A worker that could widen its own goal's accept list could grade itself on a weaker
// basis, and one that could rewrite a signature could hold its symbol to any shape.
func (s *AuthorizeSuite) TestAWorkerMayNotRewriteItsOwnGoalsAcceptOrSignature() {
	prev := s.worker()
	prev.Goals = []Goal{
		{ID: "t", Kind: GoalKindCheck, Expect: ExpectPassed, Check: checkJSON("test", "."), Accept: []Basis{BasisWitnessed}},
		{ID: "sig", Kind: GoalKindSymbol, Expect: ExpectSignature, Symbols: []string{"pkg.F"}, Signature: "func F()"},
	}
	widened, reshaped := prev.Clone(), prev.Clone()
	widened.Goals[0].Accept = []Basis{BasisWitnessed, BasisReported}
	reshaped.Goals[1].Signature = "func F(int)"

	for name, next := range map[string]Job{"accept": widened, "signature": reshaped} {
		var refused *RefusedError
		s.Require().ErrorAs(AuthorizeRow(Actor{Lease: "w"}, "w", AsDeclaration, prev, next, true, []Job{prev}, specChecks{}), &refused, name)
		s.Contains(refused.Rule, "goals", name)
	}
	changed, err := ChangedFields(prev, widened, specChecks{})
	s.Require().NoError(err)
	s.Equal([]string{"goals"}, changed)
}

func (s *AuthorizeSuite) TestSubsetNarrowsAGlobButNeverWidensIt() {
	s.True(Subset([]string{"internal/job/store.go"}, []string{"internal/**"}))
	s.False(Subset([]string{"internal/**"}, []string{"internal/job/*.go"}))
}

func (s *AuthorizeSuite) TestReadBoundaryIsTheWritePathsWithTheReadPaths() {
	s.Equal([]string{"docs", "a.go"}, ReadBoundary(Job{Paths: Paths{Write: []string{"a.go"}, Read: []string{"docs"}}}))
}
