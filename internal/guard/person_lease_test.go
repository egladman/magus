package guard

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// PersonLeaseSuite grades the rewrite a magus process makes in a checkout
// where jobs hold leases and the process names none.
type PersonLeaseSuite struct {
	suite.Suite
	ctx  context.Context
	root string
}

func TestPersonLease(t *testing.T) { suite.Run(t, new(PersonLeaseSuite)) }

func (s *PersonLeaseSuite) SetupTest() {
	s.ctx, s.root = fleetFixture(s.T(), fixture.FleetLeases()...)
	s.T().Setenv(trail.EnvBaggage, "")
}

// holdHere records id as taken in this checkout, as `job exec` does.
func (s *PersonLeaseSuite) holdHere(id string, paths ...string) {
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(s.T()), CacheDir: hookLocation(s.ctx, Dependencies{}).cacheDir, Root: s.root})
	_, err := store.Update(s.T().Context(), id, func(cur *types.Job) {
		*cur = types.Job{ID: id, WritePaths: paths, State: types.StateRunning, CheckoutRoot: s.root}
	})
	s.Require().NoError(err)
}

func (s *PersonLeaseSuite) edit(rel string) types.GuardJudgment {
	return judgeEdit(s.ctx, Dependencies{}, s.root, rel, []byte("package x\n"), []byte("package y\n"), false)
}

func (s *PersonLeaseSuite) TestOneLeaseHeldHereDoesNotBindThePerson() {
	s.holdHere("lease-c", "web/**")
	v := s.edit("docs/readme.md")
	s.Equal(graded{}, gradedAs(v))
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
}

func (s *PersonLeaseSuite) TestSeveralLeasesHeldHereDoNotDenyThePerson() {
	s.holdHere("lease-c", "web/**")
	s.holdHere("lease-d", "docs/**")
	v := s.edit("docs/readme.md")
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
}

func (s *PersonLeaseSuite) TestAPersonMayWriteTheHookWiringInALeasedCheckout() {
	s.holdHere("lease-c", "web/**")
	v := s.edit(".codex/hooks.json")
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
	s.NotEqual(string(denyRuleHookWiringWrite), v.Rule)
}

func (s *PersonLeaseSuite) TestAClaimStillBindsTheProcess() {
	s.holdHere("lease-c", "web/**")
	s.T().Setenv(trail.EnvBaggage, trail.BaggageLease+"=lease-a")
	s.Equal(graded{true, "lease-a", types.LeaseSourceEnv}, gradedAs(s.edit("cmd/magus/main.go")))
}

func (s *PersonLeaseSuite) TestTheWiringDenialToAnAgentNamesTheOrchestrator() {
	g := gradeHookWiringWrite("/repo", "", true, ".codex/hooks.json")
	s.Equal(decisionDeny, g.Decision)
	s.Contains(g.Reason, "orchestrator")
}

func (s *PersonLeaseSuite) TestTheWiringAdviceToAPersonNamesNoOrchestrator() {
	g := gradeHookWiringWrite("/repo", "", false, ".codex/hooks.json")
	s.Equal(decisionAdvise, g.Decision)
	s.NotContains(g.Context+g.Why, "orchestrator")
}
