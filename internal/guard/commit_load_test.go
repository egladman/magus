package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
)

// UnloadableCommitSuite judges commits while the working tree's
// magusfile does not load and the approved one still does. The checkout
// holds a ./magus, so a failure is the tree's own unless a test changes it.
type UnloadableCommitSuite struct {
	suite.Suite
	layers   ctxLayers
	cacheDir string
	root     string
	deps     Dependencies
}

func TestUnloadableCommitSuite(t *testing.T) {
	suite.Run(t, new(UnloadableCommitSuite))
}

func (s *UnloadableCommitSuite) SetupTest() {
	s.layers, s.cacheDir = spawnLayers(s.T())
	approved := (&commandRuleProbe{answer: verdict.Verdict{Decision: verdict.Defer}}).rule()
	s.deps = Dependencies{
		LoadFailure:         errMissingMember,
		ApprovedCommandRule: func(context.Context) (workspace.CommandRule, error) { return approved, nil },
	}
	s.root = hookLocation(s.testContext(), Dependencies{}).workspace
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "magus"), []byte("#!/bin/sh\n"), 0o755))
}

func (s *UnloadableCommitSuite) judge(line string) verdict.Judgment {
	return Judge(s.testContext(), s.deps, Request{Input: line, Host: "claude-code", Session: line})
}

// commitSay is the verdict a commit spelled verb opens with.
func commitSay(verb string) string {
	return fmt.Sprintf("`%s` waits until this checkout's magusfile loads; regenerate what the change's sources produce with `%s`, then commit the outputs with the source.",
		verb, hint.Affected.With("generate:rw"))
}

func (s *UnloadableCommitSuite) TestAGitCommitWaitsForTheLoad() {
	stored := shortDeny(s.T(), s.cacheDir, s.judge("git commit -m x"), denyRulePolicyUnloaded, commitSay("git commit"))
	s.Contains(stored, "The working tree's magusfile does not load:\n  "+errMissingMember.Error())
}

func (s *UnloadableCommitSuite) TestAnHgCommitWaitsForTheLoad() {
	shortDeny(s.T(), s.cacheDir, s.judge("hg commit -m x"), denyRulePolicyUnloaded, commitSay("hg commit"))
}

func (s *UnloadableCommitSuite) TestAJJCommitWaitsForTheLoad() {
	shortDeny(s.T(), s.cacheDir, s.judge("jj commit -m x"), denyRulePolicyUnloaded, commitSay("jj commit"))
}

func (s *UnloadableCommitSuite) TestACherryPickContinueWaitsForTheLoad() {
	shortDeny(s.T(), s.cacheDir, s.judge("git cherry-pick --continue"), denyRulePolicyUnloaded, commitSay("git cherry-pick --continue"))
}

func (s *UnloadableCommitSuite) TestAMergeContinueWaitsForTheLoad() {
	shortDeny(s.T(), s.cacheDir, s.judge("git merge --continue"), denyRulePolicyUnloaded, commitSay("git merge --continue"))
}

func (s *UnloadableCommitSuite) TestARebaseContinueWaitsForTheLoad() {
	shortDeny(s.T(), s.cacheDir, s.judge("git rebase --continue"), denyRulePolicyUnloaded, commitSay("git rebase --continue"))
}

func (s *UnloadableCommitSuite) TestARevertContinueWaitsForTheLoad() {
	shortDeny(s.T(), s.cacheDir, s.judge("git revert --continue"), denyRulePolicyUnloaded, commitSay("git revert --continue"))
}

func (s *UnloadableCommitSuite) TestAnHgRebaseContinueWaitsForTheLoad() {
	shortDeny(s.T(), s.cacheDir, s.judge("hg rebase --continue"), denyRulePolicyUnloaded, commitSay("hg rebase --continue"))
}

func (s *UnloadableCommitSuite) TestASaplingGraftContinueWaitsForTheLoad() {
	shortDeny(s.T(), s.cacheDir, s.judge("sl graft --continue"), denyRulePolicyUnloaded, commitSay("sl graft --continue"))
}

func (s *UnloadableCommitSuite) TestAnHgHisteditAbortPasses() {
	v := s.judge("hg histedit --abort")
	s.NotEqual(verdict.Deny, v.Decision, v.Reason)
}

func (s *UnloadableCommitSuite) TestAnAbortPasses() {
	v := s.judge("git cherry-pick --abort")
	s.NotEqual(verdict.Deny, v.Decision, v.Reason)
}

func (s *UnloadableCommitSuite) TestAnEditStandsOnTheApprovedRule() {
	v := s.judge("touch notes.txt")
	s.NotEqual(verdict.Deny, v.Decision, v.Reason)
}

func (s *UnloadableCommitSuite) TestACommitPassesOnceTheTreeLoads() {
	s.deps.LoadFailure = nil
	v := s.judge("git commit -m x")
	s.NotEqual(verdict.Deny, v.Decision, v.Reason)
}

func (s *UnloadableCommitSuite) TestACommitInAnotherCheckoutPasses() {
	v := s.judge(fmt.Sprintf("git -C %s commit -m x", shellQuoted(s.T().TempDir())))
	s.NotEqual(verdict.Deny, v.Decision, v.Reason)
}

func (s *UnloadableCommitSuite) TestACommitWithNoBinaryStandsDown() {
	s.Require().NoError(os.Remove(filepath.Join(s.root, "magus")))
	s.deps.Binary = "/usr/local/bin/magus"
	v := s.judge("git commit -m x")
	s.NotEqual(verdict.Deny, v.Decision, v.Reason)
	s.Contains(v.Context, "this checkout has no ./magus.")
}

func (s *UnloadableCommitSuite) TestARebaseContinueOnAStaleBinaryNamesTheRelink() {
	carryContract(s.T(), s.root, "config another.key\n")
	s.deps.Binary = filepath.Join(s.root, "magus")
	v := s.judge("git rebase --continue")
	s.NotEqual(verdict.Deny, v.Decision, v.Reason)
	s.Contains(v.Context, fmt.Sprintf("Fix: %s.", binaryRemedy(ownSourceRoot(s.root), s.root, "", false).say))
}

func (s *UnloadableCommitSuite) TestAnHgCommitOnAStaleBinaryStandsDown() {
	carryContract(s.T(), s.root, "config another.key\n")
	v := s.judge("hg commit -m x")
	s.NotEqual(verdict.Deny, v.Decision, v.Reason)
}

func (s *UnloadableCommitSuite) testContext() context.Context { return s.layers.of(s.T()) }
