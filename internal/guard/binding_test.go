package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

// ExitedHolderBindingSuite pins decision 127: a holder's own `job exit` keeps
// its binding, so the exited row still grades its writes. A wait that ends
// the row, and rm, release it.
type ExitedHolderBindingSuite struct {
	suite.Suite
	layers ctxLayers
	root   string
	at     location
	who    hookAttribution
	store  *job.Store
}

func TestExitedHolderBindingSuite(t *testing.T) {
	suite.Run(t, new(ExitedHolderBindingSuite))
}

const heldJob = "held-job"

func (s *ExitedHolderBindingSuite) SetupTest() {
	held := types.Job{ID: heldJob, Criteria: "land the held package", State: types.StateRunning, WritePaths: []string{"internal/held/**"},
		Checkpoint: "rev-a", ReportedBase: "rev-a", BaseVerdict: types.BaseMatch, Registered: 1}
	next := types.Job{ID: "next-job", Criteria: "the next one", State: types.StateDeclared, WritePaths: []string{"internal/next/**"}}
	s.layers, s.root = fleetLayers(s.T(), held, next)
	s.at = hookLocation(s.ctx(), Dependencies{})
	s.who = hookAttribution{Host: "claude-code", Session: "8f2c6a1e", Agent: "a1b2c3"}
	s.store = job.NewStore(job.Location{StateBase: fixture.StateBase(s.T()), CacheDir: s.at.cacheDir, Root: s.at.workspace})
	bindCaller(s.T(), s.ctx(), s.who, heldJob)
}

// judge runs command as the holder, or as its root session when byRoot.
func (s *ExitedHolderBindingSuite) judge(command string, byRoot bool) types.GuardJudgment {
	by := s.who
	if byRoot {
		by.Agent = ""
	}
	return Judge(s.ctx(), Dependencies{}, Request{Input: bashCall(s.T(), by.Session, by.Agent, command), Host: by.Host})
}

func (s *ExitedHolderBindingSuite) setState(state types.JobState) {
	_, err := s.store.Update(s.ctx(), heldJob, func(cur *types.Job) { cur.State = state })
	s.Require().NoError(err)
}

// exitOwnJob files the holder's result and moves the row to exited, as the
// store does once the line runs.
func (s *ExitedHolderBindingSuite) exitOwnJob() {
	v := s.judge("./magus job exit held-job --stdin <<'EOF'\n{\"schema_version\":2,\"changed_paths\":[],\"unresolved_risks\":[]}\nEOF", false)
	s.Require().NotEqual(decisionDeny, v.Decision, v.Reason)
	s.setState(types.StateExited)
}

func (s *ExitedHolderBindingSuite) TestOwnExitKeepsTheBinding() {
	s.exitOwnJob()
	s.Equal(job.Binding{Job: heldJob}, boundJob(s.who, s.at))
}

func (s *ExitedHolderBindingSuite) TestOwnClientExitKeepsTheBinding() {
	input := hookJSON(s.T(), map[string]any{"session_id": s.who.Session, "cwd": "/Users/dev/repo", "hook_event_name": "PreToolUse",
		"agent_id": s.who.Agent, "agent_type": "general-purpose",
		"tool_name": "mcp__magus__client", "tool_input": map[string]any{"script": `import "magus"; magus\job("held-job").exit();`}})
	v := Judge(s.ctx(), Dependencies{}, Request{Input: input, Host: s.who.Host})
	s.Require().NotEqual(decisionDeny, v.Decision, v.Reason)
	s.setState(types.StateExited)
	s.Equal(job.Binding{Job: heldJob}, boundJob(s.who, s.at))
}

func (s *ExitedHolderBindingSuite) TestPassedWaitReleasesTheBinding() {
	s.Require().NotEqual(decisionDeny, s.judge("./magus job wait held-job", true).Decision)
	s.setState(types.StatePass)
	s.NotEqual(decisionDeny, s.judge("sed -n 1,5p x", false).Decision)
	s.Equal(job.Binding{}, boundJob(s.who, s.at))
}

func (s *ExitedHolderBindingSuite) TestFailedWaitReleasesTheBinding() {
	s.Require().NotEqual(decisionDeny, s.judge("./magus job wait held-job", true).Decision)
	s.setState(types.StateFail)
	s.NotEqual(decisionDeny, s.judge("sed -n 1,5p x", false).Decision)
	s.Equal(job.Binding{}, boundJob(s.who, s.at))
}

func (s *ExitedHolderBindingSuite) TestRejectedWaitKeepsTheBinding() {
	s.Require().NotEqual(decisionDeny, s.judge("./magus job wait held-job", true).Decision)
	s.setState(types.StateExited)
	s.NotEqual(decisionDeny, s.judge("sed -n 1,5p x", false).Decision)
	s.Equal(job.Binding{Job: heldJob}, boundJob(s.who, s.at))
}

func (s *ExitedHolderBindingSuite) TestRmReleasesTheBinding() {
	s.Require().NotEqual(decisionDeny, s.judge("magus job rm held-job --force", false).Decision)
	_, err := s.store.Delete(s.ctx(), heldJob, true)
	s.Require().NoError(err)
	s.Equal(job.Binding{}, boundJob(s.who, s.at))
}

// The 10-29 trail lost the lease at a `job exit --schema` read, which only
// prints the schema.
func (s *ExitedHolderBindingSuite) TestExitSchemaReadKeepsTheBinding() {
	s.Require().NotEqual(decisionDeny, s.judge("./magus job exit held-job --schema", false).Decision)
	s.Equal(job.Binding{Job: heldJob}, boundJob(s.who, s.at))
}

func (s *ExitedHolderBindingSuite) TestBareExitSchemaReadKeepsTheBinding() {
	releaseOnEnd(s.at, s.who, "./magus job exit --schema")
	s.Equal(job.Binding{Job: heldJob}, boundJob(s.who, s.at))
}

func (s *ExitedHolderBindingSuite) TestWaitHelpKeepsTheBinding() {
	releaseOnEnd(s.at, s.who, "./magus job wait held-job --help")
	s.Equal(job.Binding{Job: heldJob}, boundJob(s.who, s.at))
}

func (s *ExitedHolderBindingSuite) TestRmDryRunKeepsTheBinding() {
	releaseOnEnd(s.at, s.who, "./magus job rm held-job --force --dry-run")
	s.Equal(job.Binding{Job: heldJob}, boundJob(s.who, s.at))
}

func (s *ExitedHolderBindingSuite) TestGlobalDryRunWaitKeepsTheBinding() {
	releaseOnEnd(s.at, s.who, "./magus --dry-run job wait held-job")
	s.Equal(job.Binding{Job: heldJob}, boundJob(s.who, s.at))
}

func (s *ExitedHolderBindingSuite) TestExecOfANewJobAfterOwnExitRebinds() {
	s.exitOwnJob()
	v := s.judge("./magus job exec next-job", false)
	s.Require().NotEqual(decisionDeny, v.Decision, v.Reason)
	s.Equal(job.Binding{Job: "next-job"}, boundJob(s.who, s.at))
}

func (s *ExitedHolderBindingSuite) TestRootSessionsOwnExitReleasesTheBinding() {
	root := hookAttribution{Host: s.who.Host, Session: s.who.Session}
	bindCaller(s.T(), s.ctx(), root, heldJob)
	releaseOnEnd(s.at, root, "./magus job exit held-job --stdin")
	s.Equal(job.Binding{}, boundJob(root, s.at))
}

func (s *ExitedHolderBindingSuite) TestRootSessionsExitSchemaKeepsTheBinding() {
	root := hookAttribution{Host: s.who.Host, Session: s.who.Session}
	bindCaller(s.T(), s.ctx(), root, heldJob)
	releaseOnEnd(s.at, root, "./magus job exit held-job --schema")
	s.Equal(job.Binding{Job: heldJob}, boundJob(root, s.at))
}

// BindOnExecSuite pins that a binding follows an exec the store would take.
type BindOnExecSuite struct {
	suite.Suite
	layers ctxLayers
	at     location
	who    hookAttribution
	store  *job.Store
	here   string
}

func TestBindOnExecSuite(t *testing.T) {
	suite.Run(t, new(BindOnExecSuite))
}

func (s *BindOnExecSuite) SetupTest() {
	elsewhere := s.T().TempDir()
	s.here = s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(s.here, "magus.yaml"), []byte("{}\n"), 0o644))
	var err error
	s.here, err = workspace.FindRoot(s.here)
	s.Require().NoError(err)
	commitCheckout(s.T(), s.here)
	ended := types.EndReasonSessionEnded
	rows := []types.Job{
		{ID: "owned-elsewhere", State: types.StateDeclared, Worktree: &types.JobWorktree{Root: elsewhere, Made: true}},
		{ID: "held-elsewhere", State: types.StateRunning, CheckoutRoot: elsewhere, Registered: 1},
		{ID: "passed", State: types.StatePass, CheckoutRoot: s.here, Registered: 1},
		{ID: "free", State: types.StateDeclared},
		{ID: "paused", State: types.StateDeclared, Paused: 1},
		{ID: "stopped-here", State: types.StateRunning, CheckoutRoot: s.here, Registered: 1, HolderStopped: 1},
		{ID: "ended-here", State: types.StateNoReturn, EndReason: ended, CheckoutRoot: s.here, Registered: 1},
		{ID: "ended-elsewhere", State: types.StateNoReturn, EndReason: ended, CheckoutRoot: elsewhere, Registered: 1},
	}
	s.layers, _ = fleetLayers(s.T())
	s.layers = append(s.layers, snapshotLayer(job.Snapshot{Rows: rows}))
	s.at = hookLocation(s.ctx(), Dependencies{})
	s.who = hookAttribution{Host: "claude-code", Session: "8f2c6a1e"}
	s.store = job.NewStore(job.Location{StateBase: fixture.StateBase(s.T()), CacheDir: s.at.cacheDir, Root: s.at.workspace})
}

func (s *BindOnExecSuite) bind(id string) job.Binding {
	bindOnExec(s.ctx(), s.at, s.who, fmt.Sprintf("cd %s && ./magus job exec %s", s.here, id))
	return s.store.Binding(s.who.caller())
}

func (s *BindOnExecSuite) TestARowOwnedElsewhereDoesNotBind() {
	s.Equal(job.Binding{}, s.bind("owned-elsewhere"))
}

func (s *BindOnExecSuite) TestARowHeldElsewhereDoesNotBind() {
	s.Equal(job.Binding{}, s.bind("held-elsewhere"))
}

func (s *BindOnExecSuite) TestAPassedRowDoesNotBind() {
	s.Equal(job.Binding{}, s.bind("passed"))
}

func (s *BindOnExecSuite) TestAPausedRowDoesNotBind() {
	s.Equal(job.Binding{}, s.bind("paused"))
}

func (s *BindOnExecSuite) TestARowEndedWithItsSessionElsewhereDoesNotBind() {
	s.Equal(job.Binding{}, s.bind("ended-elsewhere"))
}

func (s *BindOnExecSuite) TestARowInACheckoutWithNoRevisionDoesNotBind() {
	bare := s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(bare, "magus.yaml"), []byte("{}\n"), 0o644))
	bindOnExec(s.ctx(), s.at, s.who, fmt.Sprintf("cd %s && ./magus job exec free", bare))
	s.Equal(job.Binding{}, s.store.Binding(s.who.caller()))
}

func (s *BindOnExecSuite) TestAFreeRowBinds() {
	s.Equal(job.Binding{Job: "free"}, s.bind("free"))
}

// Decision 250: a stop keeps the row running, so its holder retakes it.
func (s *BindOnExecSuite) TestAStoppedRowRetakenHereBinds() {
	s.Equal(job.Binding{Job: "stopped-here"}, s.bind("stopped-here"))
}

// Exec takes the row back, though it reads terminal until the line runs.
func (s *BindOnExecSuite) TestARowEndedWithItsSessionHereBinds() {
	s.Equal(job.Binding{Job: "ended-here"}, s.bind("ended-here"))
}

// The cost decision 127 accepted: still bound, the exited holder's fork
// outside its row's tree is refused until it takes another job.
func (s *ExitedHolderBindingSuite) TestForkOutsideTheTreeAfterOwnExitIsRefused() {
	s.exitOwnJob()
	v := s.judge("magus job fork other-job --parent other --read-only", false)
	s.Equal(decisionDeny, v.Decision)
}

func (s *ExitedHolderBindingSuite) ctx() context.Context { return s.layers.of(s.T()) }

func (s *BindOnExecSuite) ctx() context.Context { return s.layers.of(s.T()) }
