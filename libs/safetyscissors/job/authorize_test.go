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

	s.NoError(AuthorizeRow(Actor{}, "w", AsDeclaration, prev, next, true, []Job{prev}))
}

func (s *AuthorizeSuite) TestABoundWorkerShrinksButNeverWidensItsWritePaths() {
	prev := s.worker()
	shrunk, widened := prev, prev
	shrunk.Paths.Write = []string{"a.go"}
	widened.Paths.Write = []string{"internal/x", "a.go", "b.go"}

	s.Require().NoError(AuthorizeRow(Actor{Lease: "w"}, "w", AsDeclaration, prev, shrunk, true, []Job{prev}))
	var refused *RefusedError
	s.Require().ErrorAs(AuthorizeRow(Actor{Lease: "w"}, "w", AsDeclaration, prev, widened, true, []Job{prev}), &refused)
	s.Contains(refused.Rule, "SHRINK")
	s.Equal("ask your orchestrator to make this write", refused.Remedy)
}

func (s *AuthorizeSuite) TestABoundWorkerEndsItsRowButNeverPassesIt() {
	prev := s.worker()
	failed, passed := prev, prev
	failed.State, passed.State = StateFail, StatePass

	s.Require().NoError(AuthorizeRow(Actor{Lease: "w"}, "w", AsDeclaration, prev, failed, true, []Job{prev}))
	s.Error(AuthorizeRow(Actor{Lease: "w"}, "w", AsDeclaration, prev, passed, true, []Job{prev}))
}

func (s *AuthorizeSuite) TestAChildStaysInsideItsParentsBoundary() {
	parent := s.worker()
	inside := Job{ID: "c", Parent: "w", Paths: Paths{Write: []string{"internal/x/y.go"}, Deny: []string{"secret"}}}
	wider := Job{ID: "c", Parent: "w", Paths: Paths{Write: []string{"elsewhere.go"}, Deny: []string{"secret"}}}
	dropsDeny := Job{ID: "c", Parent: "w", Paths: Paths{Write: []string{"a.go"}}}
	actor, rows := Actor{Lease: "w"}, []Job{parent}

	s.Require().NoError(AuthorizeRow(actor, "c", AsDeclaration, Job{}, inside, false, rows))
	s.Error(AuthorizeRow(actor, "c", AsDeclaration, Job{}, wider, false, rows))
	s.Error(AuthorizeRow(actor, "c", AsDeclaration, Job{}, dropsDeny, false, rows))
}

func (s *AuthorizeSuite) TestAVerdictPassesOnlyARowBelowTheHolder() {
	parent, child := s.worker(), Job{ID: "c", Parent: "w", State: StateExited}
	passed := child
	passed.State = StatePass
	rows := []Job{parent, child}

	s.Require().NoError(AuthorizeRow(Actor{Lease: "w"}, "c", AsVerdict, child, passed, true, rows))
	s.Error(AuthorizeRow(Actor{Lease: "c"}, "c", AsVerdict, child, passed, true, rows), "a holder never grades its own row")
}

func (s *AuthorizeSuite) TestAnUnstampedCallerWritesNoRow() {
	prev := s.worker()

	var refused *RefusedError
	s.Require().ErrorAs(AuthorizeRow(Actor{Unstamped: true}, "w", AsDeclaration, prev, prev, true, []Job{prev}), &refused)
	s.Equal(unstampedRemedy, refused.Remedy)
}

func (s *AuthorizeSuite) TestAClaimIsRefusedWithNoClaimRule() {
	prev := s.worker()

	s.Error(AuthorizeRow(Actor{}, "w", AsClaim, prev, prev, true, []Job{prev}), "a claim graded by nothing is refused")
}

func (s *AuthorizeSuite) TestOnlyTheRootOrItsIntegratorAdvancesAuthority() {
	row, integrator := s.worker(), Job{ID: "i", State: StateRunning, Integrates: []string{"w"}}
	rows := []Job{row, integrator}

	s.NoError(AuthorizeRow(Actor{}, "w", AsAdvance, row, row, true, rows))
	s.NoError(AuthorizeRow(Actor{Lease: "i"}, "w", AsAdvance, row, row, true, rows))
	s.Error(AuthorizeRow(Actor{Lease: "w"}, "w", AsAdvance, row, row, true, rows))
}

func (s *AuthorizeSuite) TestChangedFieldsNamesTheDeclaredFieldsInWireSpelling() {
	prev := s.worker()
	next := prev
	next.Paths.Read, next.MaxAttempts = []string{"docs"}, 2

	s.Equal([]string{"paths.read", "max_attempts"}, ChangedFields(prev, next))
}

func (s *AuthorizeSuite) TestSubsetNarrowsAGlobButNeverWidensIt() {
	s.True(Subset([]string{"internal/job/store.go"}, []string{"internal/**"}))
	s.False(Subset([]string{"internal/**"}, []string{"internal/job/*.go"}))
}

func (s *AuthorizeSuite) TestReadBoundaryIsTheWritePathsWithTheReadPaths() {
	s.Equal([]string{"docs", "a.go"}, ReadBoundary(Job{Paths: Paths{Write: []string{"a.go"}, Read: []string{"docs"}}}))
}
