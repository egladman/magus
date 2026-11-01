package guard

import (
	"fmt"
	"context"
	"os"
	"path/filepath"
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

const (
	renameDryRun    = "./magus refs ActingLease --rename ActingLeaseProbe --dry-run"
	claimForLeaseA  = "export BAGGAGE=magus.lease=lease-a; "
	buzzWritesAFile = "import \"fs\";\nfun main(args: [str]) > void !> any { fs\\writeFile(\"cmd/magus/main.go\", content: \"x\"); }\n"
)

// judgeBound judges command from a worker bound to lease-a by session alone.
func (s *PersonLeaseSuite) judgeBound(command string) types.GuardJudgment {
	who := hookAttribution{Host: "claude-code", Session: "8f2c6a1e", Agent: "a1b2c3"}
	bindCaller(s.T(), s.ctx, who, "lease-a")
	return s.judgeAs(who, command)
}

func (s *PersonLeaseSuite) judgeAs(who hookAttribution, command string) types.GuardJudgment {
	input := hookJSON(s.T(), map[string]any{"session_id": who.Session, "agent_id": who.Agent, "agent_type": "general-purpose", "cwd": s.root,
		"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": command}})
	return Judge(s.ctx, Dependencies{}, Request{Input: input, Host: who.Host})
}

func (s *PersonLeaseSuite) script(rel, body string) {
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, rel), []byte(body), 0o644))
}

func (s *PersonLeaseSuite) TestASessionBoundRenameDryRunIsRefusedUntilItNamesItsLease() {
	v := s.judgeBound(renameDryRun)
	s.Equal(string(denyRuleEditUngraded), v.Rule, v.Reason)
	s.Equal(decisionDeny, v.Decision)
	s.Equal("lease-a", v.Lease)
	s.Contains(v.Reason, "start the line with `export BAGGAGE=magus.lease=lease-a;`")
}

func (s *PersonLeaseSuite) TestTheClaimedRenameGradesPathsOutsideTheRow() {
	claimed := s.judgeBound(claimForLeaseA + renameDryRun)
	s.NotEqual(decisionDeny, claimed.Decision, claimed.Reason)

	s.T().Setenv(trail.EnvBaggage, trail.BaggageLease+"=lease-a")
	s.Equal(graded{true, "lease-a", types.LeaseSourceEnv}, gradedAs(s.edit("cmd/magus/main.go")), "outside the row")
	s.Equal(graded{false, "lease-a", types.LeaseSourceEnv}, gradedAs(s.edit("internal/ledger/store.go")), "inside the row")
}

func (s *PersonLeaseSuite) TestARewriteClaimingAnotherLeaseIsRefused() {
	v := s.judgeBound("export BAGGAGE=magus.lease=lease-b; " + renameDryRun)
	s.Equal(string(denyRuleEditUngraded), v.Rule, v.Reason)
}

func (s *PersonLeaseSuite) TestTheSessionsOwnClaimReachesTheProcess() {
	s.T().Setenv(trail.EnvBaggage, trail.BaggageLease+"=lease-a")
	s.NotEqual(string(denyRuleEditUngraded), s.judgeBound(renameDryRun).Rule)
}

func (s *PersonLeaseSuite) TestABuzzScriptThatWritesNeedsTheClaim() {
	s.script("writes.buzz", buzzWritesAFile)
	s.Equal(string(denyRuleEditUngraded), s.judgeBound("./magus buzz writes.buzz").Rule)
	s.NotEqual(string(denyRuleEditUngraded), s.judgeBound(claimForLeaseA+"./magus buzz writes.buzz").Rule)
}

func (s *PersonLeaseSuite) TestABuzzScriptAfterDashCNeedsTheClaim() {
	s.script("writes.buzz", buzzWritesAFile)
	s.Equal(string(denyRuleEditUngraded), s.judgeBound(fmt.Sprintf("./magus -C %s buzz writes.buzz", s.root)).Rule)
}

func (s *PersonLeaseSuite) TestAnInlineBuzzWriteNeedsTheClaim() {
	v := s.judgeBound(`./magus buzz -e 'import "fs"; fs\appendFile("docs/a.md", content: "x");'`)
	s.Equal(string(denyRuleEditUngraded), v.Rule, v.Reason)
}

func (s *PersonLeaseSuite) TestABuzzHeredocWriteNeedsTheClaim() {
	v := s.judgeBound("./magus buzz - <<'EOF'\n" + buzzWritesAFile + "EOF")
	s.Equal(string(denyRuleEditUngraded), v.Rule, v.Reason)
}

func (s *PersonLeaseSuite) TestABuzzScriptThatOnlyReadsPasses() {
	s.script("reads.buzz", "import \"fs\";\nfun main(args: [str]) > void !> any { fs\\readFile(\"go.mod\"); }\n")
	s.NotEqual(string(denyRuleEditUngraded), s.judgeBound("./magus buzz reads.buzz").Rule)
}

func (s *PersonLeaseSuite) TestABuzzCheckOfAWritingScriptPasses() {
	s.script("writes.buzz", buzzWritesAFile)
	s.NotEqual(string(denyRuleEditUngraded), s.judgeBound("./magus buzz --check writes.buzz").Rule)
}

func (s *PersonLeaseSuite) TestRenameHelpPasses() {
	s.NotEqual(string(denyRuleEditUngraded), s.judgeBound("./magus refs --rename --help").Rule)
}

func (s *PersonLeaseSuite) TestAnUnboundCallerIsNotAskedForAClaim() {
	s.holdHere("lease-c", "web/**")
	v := s.judgeAs(hookAttribution{Host: "claude-code", Session: "unbound", Agent: "a1b2c3"}, renameDryRun)
	s.NotEqual(string(denyRuleEditUngraded), v.Rule, v.Reason)
}

func (s *PersonLeaseSuite) TestACheckoutRecordGradesWithNoClaim() {
	bindCaller(s.T(), s.ctx, hookAttribution{}, "lease-a")
	s.NotEqual(string(denyRuleEditUngraded), s.judgeBound(renameDryRun).Rule)
}

func (s *PersonLeaseSuite) TestTheBootstrapScriptsTakeNoClaim() {
	for _, script := range []string{placementScript, job.CheckoutBootstrapScript} {
		s.Require().NoError(os.MkdirAll(filepath.Dir(filepath.Join(s.root, script)), 0o755))
		s.script(script, buzzWritesAFile)
		s.NotEqual(string(denyRuleEditUngraded), s.judgeBound("./magus buzz "+script).Rule, script)
	}
}

// A workspace opened at a bare directory has no marker for the search to find.
func (s *PersonLeaseSuite) TestAnOpenedWorkspaceIsNeverJudgedByTheProcessWorkingDir() {
	s.T().Chdir(s.root)
	own := s.T().TempDir()
	var opened []string
	deps := Dependencies{
		Workspace: own,
		CacheDir:  func(string) (string, error) { return filepath.Join(own, ".magus"), nil },
		Inspect: func(_ context.Context, root string) (types.WorkspaceRepository, error) {
			opened = append(opened, root)
			return nil, errNoDependency
		},
	}
	s.Equal(own, hookLocation(context.Background(), deps).workspace)
	Judge(context.Background(), deps, Request{Input: "magus run build svc", Dir: own})
	judgeEdit(context.Background(), deps, own, "a.txt", []byte("a\n"), []byte("b\n"), false)
	s.NotContains(opened, s.root)
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
