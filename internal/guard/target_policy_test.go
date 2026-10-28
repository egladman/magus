package guard

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

// magusRepoPolicies is what magus's own magusfile declares for the rules that read a
// target's policy: go-build relinks ./magus, and it and generate are an integrator's.
var magusRepoPolicies = types.TargetPolicyIndex{
	Projects: []string{"."},
	Targets: []types.TargetPolicy{
		{TargetRef: types.TargetRef{Project: ".", Target: "generate"}, Integrates: "regenerates the tree"},
		{TargetRef: types.TargetRef{Project: ".", Target: "go-build"}, Relinks: "./magus", Integrates: "links ./magus"},
	},
}

// magusRepoAt is a location whose workspace declares magusRepoPolicies.
var magusRepoAt = location{policies: func() declaredIndex { return declaredIndex{ix: magusRepoPolicies} }}

// targetPolicySuite tests the one target-policy lookup the guard's rules share.
type targetPolicySuite struct {
	suite.Suite
}

func TestTargetPolicySuite(t *testing.T) {
	suite.Run(t, new(targetPolicySuite))
}

func (s *targetPolicySuite) TestARunOfARelinkingTargetRelinks() {
	s.True(runRelinks(magusRepoPolicies, targetRun{verb: "run", target: "go-build", projects: []string{"."}}))
}

func (s *targetPolicySuite) TestATargetNamedBuildThatDeclaresNothingDoesNotRelink() {
	s.False(runRelinks(magusRepoPolicies, targetRun{verb: "run", target: "build", projects: []string{"."}}))
}

func (s *targetPolicySuite) TestARelinkingTargetInAnotherProjectDoesNotRelink() {
	s.False(runRelinks(magusRepoPolicies, targetRun{verb: "run", target: "go-build", projects: []string{"web"}}))
}

func (s *targetPolicySuite) TestADeclaredIntegrationTargetIntegrates() {
	s.True(runIntegrates(magusRepoPolicies, targetRun{verb: "run", target: "generate", projects: []string{"."}}))
}

func (s *targetPolicySuite) TestAnUndeclaredGenerateTargetDoesNotIntegrate() {
	s.False(runIntegrates(magusRepoPolicies, targetRun{verb: "run", target: "docs-generate", projects: []string{"."}}))
}

func (s *targetPolicySuite) TestTheRecordAnswersWithoutANote() {
	deps := Dependencies{TargetPolicies: func(context.Context, string) (types.TargetPolicyIndex, bool) { return magusRepoPolicies, true }}
	ix, note, ok := declaredPolicies(s.T().Context(), deps, "/r", nil)
	s.True(ok)
	s.Empty(note)
	s.Equal(magusRepoPolicies, ix)
}

// A record that cannot vouch, an old one decoding empty included, sends the guard to the
// workspace, and the verdict says it did.
func (s *targetPolicySuite) TestAnUnprovenRecordOpensTheWorkspaceAndSaysSo() {
	deps := Dependencies{TargetPolicies: func(context.Context, string) (types.TargetPolicyIndex, bool) { return types.TargetPolicyIndex{}, false }}
	_, note, ok := declaredPolicies(s.T().Context(), deps, "/r", func() (types.WorkspaceRepository, error) {
		return policyWorkspace{}, nil
	})
	s.True(ok)
	s.Equal(policyOpenedNote, note)
}

func (s *targetPolicySuite) TestNoRecordAndNoWorkspaceSaysNothingWasRead() {
	_, note, ok := declaredPolicies(s.T().Context(), Dependencies{}, "/r", func() (types.WorkspaceRepository, error) {
		return nil, errors.New("does not load")
	})
	s.False(ok)
	s.Equal(policyUnreadNote, note)
}

// policyWorkspace is a workspace with one project and no target policy.
type policyWorkspace struct {
	types.WorkspaceRepository
}

func (policyWorkspace) All() []*types.Project { return []*types.Project{{Path: "."}} }

func (policyWorkspace) Get(string) *types.Project { return nil }
