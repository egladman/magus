package guard

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// ExitedHolderBindingSuite pins decision 127: a holder's own `job exit` keeps
// its binding, so the exited row still grades its writes. A wait that ends
// the row, and rm, release it.
type ExitedHolderBindingSuite struct {
	suite.Suite
	ctx   context.Context
	root  string
	at    location
	who   hookAttribution
	store *job.Store
}

func TestExitedHolderBindingSuite(t *testing.T) {
	suite.Run(t, new(ExitedHolderBindingSuite))
}

const heldJob = "held-job"

func (s *ExitedHolderBindingSuite) SetupTest() {
	held := types.Job{ID: heldJob, Criteria: "land the held package", State: types.StateRunning, WritePaths: []string{"internal/held/**"},
		Checkpoint: "rev-a", ReportedBase: "rev-a", BaseVerdict: types.BaseMatch, Registered: 1}
	next := types.Job{ID: "next-job", Criteria: "the next one", State: types.StateDeclared, WritePaths: []string{"internal/next/**"}}
	s.ctx, s.root = fleetFixture(s.T(), held, next)
	s.at = hookLocation(s.ctx, Dependencies{})
	s.who = hookAttribution{Host: "claude-code", Session: "8f2c6a1e", Agent: "a1b2c3"}
	s.store = job.NewStore(job.Location{StateBase: fixture.StateBase(s.T()), CacheDir: s.at.cacheDir, Root: s.at.workspace})
	bindCaller(s.T(), s.ctx, s.who, heldJob)
}

// judge runs command as the holder, or as its root session when byRoot.
func (s *ExitedHolderBindingSuite) judge(command string, byRoot bool) types.GuardJudgment {
	by := s.who
	if byRoot {
		by.Agent = ""
	}
	return Judge(s.ctx, Dependencies{}, Request{Input: bashCall(s.T(), by.Session, by.Agent, command), Host: by.Host})
}

// write is the holder's editor Write of path, from a session rooted at cwd.
func (s *ExitedHolderBindingSuite) write(cwd, path string) types.GuardJudgment {
	input := hookJSON(s.T(), map[string]any{"session_id": s.who.Session, "cwd": cwd, "hook_event_name": "PreToolUse",
		"agent_id": s.who.Agent, "agent_type": "general-purpose",
		"tool_name": "Write", "tool_input": map[string]any{"file_path": path, "content": "package x\n"}})
	return Judge(s.ctx, Dependencies{}, Request{Input: input, Host: s.who.Host})
}

func (s *ExitedHolderBindingSuite) moveTo(state types.JobState) {
	_, err := s.store.Update(s.ctx, heldJob, func(cur *types.Job) { cur.State = state })
	s.Require().NoError(err)
}

// exitOwnJob files the holder's result and moves the row to exited, as the
// store does once the line runs.
func (s *ExitedHolderBindingSuite) exitOwnJob() {
	v := s.judge("./magus job exit held-job --stdin <<'EOF'\n{\"schema_version\":2,\"changed_paths\":[],\"unresolved_risks\":[]}\nEOF", false)
	s.Require().NotEqual(decisionDeny, v.Decision, v.Reason)
	s.moveTo(types.StateExited)
}

func (s *ExitedHolderBindingSuite) TestOwnExitKeepsTheBinding() {
	s.exitOwnJob()
	s.Equal(job.Binding{Job: heldJob}, boundJob(s.who, s.at))
}

func (s *ExitedHolderBindingSuite) TestOwnClientExitKeepsTheBinding() {
	input := hookJSON(s.T(), map[string]any{"session_id": s.who.Session, "cwd": "/Users/dev/repo", "hook_event_name": "PreToolUse",
		"agent_id": s.who.Agent, "agent_type": "general-purpose",
		"tool_name": "mcp__magus__client", "tool_input": map[string]any{"script": `import "magus"; magus\job.exit("held-job");`}})
	v := Judge(s.ctx, Dependencies{}, Request{Input: input, Host: s.who.Host})
	s.Require().NotEqual(decisionDeny, v.Decision, v.Reason)
	s.moveTo(types.StateExited)
	s.Equal(job.Binding{Job: heldJob}, boundJob(s.who, s.at))
}

func (s *ExitedHolderBindingSuite) TestWriteOutsidePathsAfterOwnExitIsDenied() {
	s.exitOwnJob()
	v := s.write(s.root, filepath.Join(s.root, "cmd", "magus", "main.go"))
	s.Equal(decisionDeny, v.Decision)
	s.Equal(string(denyRuleLeaseWrite), v.Rule)
	s.Equal(heldJob, v.Lease, "the exited row still grades the write")
}

func (s *ExitedHolderBindingSuite) TestWriteInsidePathsAfterOwnExitPasses() {
	s.exitOwnJob()
	v := s.write(s.root, filepath.Join(s.root, "internal", "held", "a.go"))
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
}

func (s *ExitedHolderBindingSuite) TestDenyAfterExitNamesTheExitedStateAndRemedy() {
	s.exitOwnJob()
	v := s.write(s.root, filepath.Join(s.root, "cmd", "magus", "main.go"))
	s.Contains(v.Reason, "job held-job is exited")
	s.Contains(v.Reason, "job exec held-job")
	s.Contains(v.Reason, "ask your orchestrator to widen it")
}

// The 10-29 case: the holder's Edit, from the root checkout, into the
// checkout its job's fork made, after its own exit.
func (s *ExitedHolderBindingSuite) TestEditIntoTheJobCheckoutAfterOwnExitIsDenied() {
	own := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(own, "hack"), 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(own, "magusfile.buzz"), nil, 0o644))
	s.Require().NoError(os.WriteFile(filepath.Join(own, "hack", "README.md"), []byte("# hack\n"), 0o644))
	_, err := s.store.Update(s.ctx, heldJob, func(cur *types.Job) { cur.CheckoutRoot = own })
	s.Require().NoError(err)
	_, err = s.store.RecordWorktree(s.ctx, heldJob, types.JobWorktree{Root: own, Source: s.root})
	s.Require().NoError(err)
	s.exitOwnJob()

	input := hookJSON(s.T(), map[string]any{"session_id": s.who.Session, "cwd": s.root, "hook_event_name": "PreToolUse",
		"agent_id": s.who.Agent, "agent_type": "general-purpose", "tool_name": "Edit",
		"tool_input": map[string]any{"file_path": filepath.Join(own, "hack", "README.md"), "old_string": "# hack", "new_string": "# hack scripts"}})
	v := Judge(s.ctx, Dependencies{}, Request{Input: input, Host: s.who.Host})
	s.Equal(decisionDeny, v.Decision)
	s.Contains(v.Reason, "job held-job is exited")
}

func (s *ExitedHolderBindingSuite) TestPassedWaitReleasesTheBinding() {
	s.Require().NotEqual(decisionDeny, s.judge("./magus job wait held-job", true).Decision)
	s.moveTo(types.StatePass)
	s.NotEqual(decisionDeny, s.judge("sed -n 1,5p x", false).Decision)
	s.Equal(job.Binding{}, boundJob(s.who, s.at))
}

func (s *ExitedHolderBindingSuite) TestFailedWaitReleasesTheBinding() {
	s.Require().NotEqual(decisionDeny, s.judge("./magus job wait held-job", true).Decision)
	s.moveTo(types.StateFail)
	s.NotEqual(decisionDeny, s.judge("sed -n 1,5p x", false).Decision)
	s.Equal(job.Binding{}, boundJob(s.who, s.at))
}

func (s *ExitedHolderBindingSuite) TestRejectedWaitKeepsTheBinding() {
	s.Require().NotEqual(decisionDeny, s.judge("./magus job wait held-job", true).Decision)
	s.moveTo(types.StateExited)
	s.NotEqual(decisionDeny, s.judge("sed -n 1,5p x", false).Decision)
	s.Equal(job.Binding{Job: heldJob}, boundJob(s.who, s.at))
}

func (s *ExitedHolderBindingSuite) TestRmReleasesTheBinding() {
	s.Require().NotEqual(decisionDeny, s.judge("magus job rm held-job --force", false).Decision)
	_, err := s.store.Delete(s.ctx, heldJob, true)
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

// The cost decision 127 accepted: still bound, the exited holder's fork
// outside its row's tree is refused until it takes another job.
func (s *ExitedHolderBindingSuite) TestForkOutsideTheTreeAfterOwnExitIsRefused() {
	s.exitOwnJob()
	v := s.judge("magus job fork other-job --parent other --read-only", false)
	s.Equal(decisionDeny, v.Decision)
}
