package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// worktreeRepo is a clone of a one-commit origin, so origin/main is a real remote-tracking
// ref, with linked worktrees added beside it.
type worktreeRepo struct {
	t      *testing.T
	origin *testkit.Repo
	main   string
	parent string
	// tries are the attempts the judged store holds.
	tries []types.Attempt
}

func newWorktreeRepository(t *testing.T) worktreeRepo {
	t.Helper()
	origin := testkit.NewRepository(t, map[string]string{"a.txt": "a\n"})
	parent := testkit.NewWorkspace(t)
	r := worktreeRepo{t: t, origin: origin, main: parent.Path("main"), parent: parent.Root()}
	r.git(parent.Root(), "clone", "-q", origin.Root(), r.main)
	for _, kv := range [][2]string{{"maintenance.auto", "false"}, {"gc.auto", "0"}, {"user.name", "test"}, {"user.email", "test@example.com"}} {
		r.git(r.main, "config", kv[0], kv[1])
	}
	return r
}

// git runs in dir through the origin's runner, which owns the fixture environment.
func (r worktreeRepo) git(dir string, args ...string) {
	r.t.Helper()
	r.origin.Git(append([]string{"-C", dir}, args...)...)
}

// in is r judged under t. A suite builds its repository under the suite's
// test, and each judgment reports to the test making it.
func (r worktreeRepo) in(t *testing.T) worktreeRepo {
	r.t = t
	return r
}

// state reads what a judgment must leave alone. That is every ref, each
// worktree's HEAD, branch and lock, and each worktree's status.
func (r worktreeRepo) state() string {
	r.t.Helper()
	list := r.origin.Git("-C", r.main, "worktree", "list", "--porcelain")
	read := []string{r.origin.Git("-C", r.main, "for-each-ref"), list}
	for _, line := range strings.Split(list, "\n") {
		if dir, ok := strings.CutPrefix(line, "worktree "); ok {
			read = append(read, dir, r.origin.Git("-C", dir, "status", "--porcelain", "--untracked-files=all"))
		}
	}
	return strings.Join(read, "\n")
}

// assertUnchanged fails t when the repository no longer reads as before.
// A suite sharing one repository checks it after its last test. A test that
// changed it would make its siblings pass or fail by their order.
func (r worktreeRepo) assertUnchanged(t *testing.T, before string) {
	t.Helper()
	assert.Equal(t, before, r.state(), "a test changed the repository its suite shares")
}

// add makes a clean linked worktree on a new branch at origin/main.
func (r worktreeRepo) add(name string) string {
	r.t.Helper()
	wt := filepath.Join(r.parent, name)
	r.git(r.main, "worktree", "add", "-q", "-b", name, wt, "origin/main")
	return wt
}

// commit records a commit in dir that no remote carries, adding a file named after dir.
func (r worktreeRepo) commit(dir string) string {
	r.t.Helper()
	return r.commitFiles(dir, map[string]string{filepath.Base(dir) + ".txt": "local\n"})
}

// commitFiles writes files in dir and commits them, returning the new HEAD.
func (r worktreeRepo) commitFiles(dir string, files map[string]string) string {
	r.t.Helper()
	for name, body := range files {
		require.NoError(r.t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	r.git(dir, "add", "-A")
	r.git(dir, "commit", "-q", "-m", "local")
	return r.origin.Git("-C", dir, "rev-parse", "HEAD")
}

// judge runs the rule for command from the main checkout, against a job store holding rows.
func (r worktreeRepo) judge(command string, rows ...types.Job) ShellVerdict {
	r.t.Helper()
	return r.judgeIn(r.main, command, rows...)
}

func (r worktreeRepo) judgeIn(callDir, command string, rows ...types.Job) ShellVerdict {
	r.t.Helper()
	return r.judgeWith(Dependencies{}, callDir, command, rows...)
}

func (r worktreeRepo) judgeWith(deps Dependencies, callDir, command string, rows ...types.Job) ShellVerdict {
	r.t.Helper()
	ctx := job.WithSnapshot(r.t.Context(), job.Snapshot{Rows: rows, Attempts: r.tries})
	at := location{cacheDir: r.t.TempDir(), workspace: r.main, dir: callDir}
	return denyWorktreeRemove(ctx, deps, at, callDir, command, DialectBash)
}

// rootSession is an identified root caller whose session facts hold one spawn record per
// entry of spawns, keyed by agent id.
func rootSession(t *testing.T, spawns map[string]spawnedAgent) Dependencies {
	t.Helper()
	deps := Dependencies{caller: job.Caller{Host: "claude", Session: "s1"}, facts: hint.NewGate(t.TempDir(), FactsKey("claude", "s1"))}
	for agentID, rec := range spawns {
		writeSpawnedAgent(deps.facts, agentID, rec)
	}
	return deps
}

// workerWorktree is a worktree locked the way Claude Code locks an isolated worker's,
// with the job w1 it was taken for.
func (r worktreeRepo) workerWorktree(state types.JobState) (string, types.Job) {
	r.t.Helper()
	wt := r.addLocked("worker")
	return wt, workerRow(state, wt)
}

// addLocked adds a worktree locked the way Claude Code locks the one it
// made for the agent spawnedW1 records.
func (r worktreeRepo) addLocked(name string) string {
	r.t.Helper()
	wt := r.add(name)
	r.git(r.main, "worktree", "lock", "--reason", "claude agent agent-a1b2c3 (pid 68539)", wt)
	return wt
}

// workerRow is job w1 taken in wt.
func workerRow(state types.JobState, wt string) types.Job {
	return types.Job{ID: "w1", State: state, CheckoutRoot: wt}
}

// spawnedW1 is the record the root's spawn of job w1's worker files.
var spawnedW1 = map[string]spawnedAgent{"agent-a1b2c3": {Description: "root/worker w1"}}

func assertRemovalDenied(t *testing.T, v ShellVerdict, says ...string) {
	t.Helper()
	assert.Equal(t, denyRule{Name: denyRuleWorktreeRemove}, v.Rule)
	assert.Contains(t, v.Deny, " may lose work: ", "the verdict names the first condition it fails")
	for _, s := range says {
		assert.Contains(t, denial{Say: v.Deny, Why: v.Why}.full(), s)
	}
}

// A clean worktree whose commits are all on a remote-tracking ref loses nothing, however
// the removal is spelled.
func TestWorktreeRemovePassesWhenNothingCanBeLost(t *testing.T) {
	r := newWorktreeRepository(t)
	wt := r.add("done")
	for _, command := range []string{
		"git worktree remove " + wt,
		"git worktree remove --force " + wt,
		fmt.Sprintf("git -C %s --no-pager worktree remove -f -- %s", r.main, wt),
		fmt.Sprintf("cd %s && git worktree remove ../done", r.main),
		"git worktree remove ../done",
	} {
		assert.Equal(t, ShellVerdict{}, r.judge(command), "%q", command)
	}
}

// Each failing condition refuses with its own reason and the command that inspects it.
// An exited row that filed no result has no grade to wait for, so a clean landed tree
// passes; a dirty or unlanded one still denies, pointing at job exit.
func TestWorktreeRemovePassesAnUngradedRowOnlyWhenNothingCanBeLost(t *testing.T) {
	r := newWorktreeRepository(t)
	clean := r.add("clean")
	row := func(wt string) types.Job { return types.Job{ID: "u", State: types.StateExited, CheckoutRoot: wt} }
	assert.Equal(t, ShellVerdict{}, r.judge("git worktree remove "+clean, row(clean)))

	unlanded := r.add("unlanded")
	r.commit(unlanded)
	dirty := r.add("dirty")
	require.NoError(t, os.WriteFile(filepath.Join(dirty, "scratch.txt"), []byte("x\n"), 0o644))
	for _, wt := range []string{unlanded, dirty} {
		v := r.judge("git worktree remove "+wt, row(wt))
		assertRemovalDenied(t, v)
		require.Len(t, v.Next, 1)
		assert.Equal(t, hint.JobExit.Argv("u"), v.Next[0].Argv)
	}
}

func TestWorktreeRemoveDeniesEachUnsafeCondition(t *testing.T) {
	r := newWorktreeRepository(t)

	dirty := r.add("dirty")
	require.NoError(t, os.WriteFile(filepath.Join(dirty, "a.txt"), []byte("changed\n"), 0o644))
	assertRemovalDenied(t, r.judge("git worktree remove "+dirty), "1 modified, staged or untracked path(s), first a.txt", fmt.Sprintf("git -C %s status", dirty))

	untracked := r.add("untracked")
	require.NoError(t, os.WriteFile(filepath.Join(untracked, "new.txt"), []byte("new\n"), 0o644))
	assertRemovalDenied(t, r.judge("git worktree remove "+untracked), "first new.txt")

	ahead := r.add("ahead")
	r.commit(ahead)
	assertRemovalDenied(t, r.judge("git worktree remove "+ahead), "1 commit(s) on it", "--not --remotes origin/main")

	held := r.add("held")
	live := types.Job{ID: "w1", State: types.StateRunning, CheckoutRoot: held}
	assertRemovalDenied(t, r.judge("git worktree remove "+held, live), "job w1 (running) was taken in it", "describe job w1")

	locked := r.add("locked")
	r.git(r.main, "worktree", "lock", "--reason", "in use", locked)
	assertRemovalDenied(t, r.judge("git worktree remove "+locked), "it is locked (in use)")

	assertRemovalDenied(t, r.judge("git worktree remove "+r.main), "it is the main worktree")
	assertRemovalDenied(t, r.judgeIn(filepath.Join(held), "git worktree remove "+held), "this session runs in it")
	assertRemovalDenied(t, r.judge("git worktree remove "+r.origin.Root()), "not a worktree of this repository")
	assertRemovalDenied(t, r.judge(fmt.Sprintf("cd %s && git worktree remove %s", r.origin.Root(), held)), "another repository")
}

// --force never reaches past a condition, and every failed one is listed together.
func TestWorktreeRemoveForceStillDeniesAndListsEveryFailure(t *testing.T) {
	r := newWorktreeRepository(t)
	wt := r.add("busy")
	r.commit(wt)
	require.NoError(t, os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("wip\n"), 0o644))
	r.git(r.main, "worktree", "lock", wt)

	v := r.judge("git worktree remove -f -f " + wt)
	assertRemovalDenied(t, v, "first wip.txt", "it is locked, so", "1 commit(s) on it", "--force changes nothing")
}

// A finished job's result does not hold its commits, so they must be published or landed
// in the caller's HEAD whatever the job's state; an exited job is still being graded.
func TestWorktreeRemoveRequiresLandedCommitsWhateverTheJobState(t *testing.T) {
	r := newWorktreeRepository(t)
	wt := r.add("worker")
	r.commit(wt)
	for _, state := range []types.JobState{types.StatePass, types.StateFail, types.StateNoReturn} {
		assertRemovalDenied(t, r.judge("git worktree remove "+wt, types.Job{ID: "w", State: state, CheckoutRoot: wt}),
			"1 commit(s) on it", "1 path(s) they changed read differently in your checkout's HEAD, first worker.txt")
	}

	v := r.judge("git worktree remove "+wt, types.Job{ID: "w", State: types.StateExited, CheckoutRoot: wt})
	assertRemovalDenied(t, v, "1 commit(s) on it")
	assert.Contains(t, v.Lead, "job w exited without filing a result")
	require.Len(t, v.Next, 1)
	assert.Equal(t, hint.JobExit.Argv("w"), v.Next[0].Argv, "an exited job with no result is told to file one")

	r.tries = []types.Attempt{filedTry("w")}
	filed := r.judge("git worktree remove "+wt, types.Job{ID: "w", State: types.StateExited, CheckoutRoot: wt})
	assertRemovalDenied(t, filed, "job w (exited)")
	require.Len(t, filed.Next, 1)
	assert.Equal(t, hint.JobWait.Argv("w"), filed.Next[0].Argv, "an exited job with a filed result waits for its grade")
}

// FinishedWorkerWorktreeSuite removes your session's finished worker's
// worktree. Its host locks it for as long as the host runs, so the lock is the
// only condition -f -f reaches past, and a plain removal is served that line.
type FinishedWorkerWorktreeSuite struct {
	suite.Suite
	repo   worktreeRepo
	before string
	wt     string
}

func TestFinishedWorkerWorktreeSuite(t *testing.T) {
	suite.Run(t, new(FinishedWorkerWorktreeSuite))
}

func (s *FinishedWorkerWorktreeSuite) SetupSuite() {
	s.repo = newWorktreeRepository(s.T())
	s.wt = s.repo.addLocked("worker")
	s.before = s.repo.state()
}

func (s *FinishedWorkerWorktreeSuite) TearDownSuite() {
	s.repo.assertUnchanged(s.T(), s.before)
}

func (s *FinishedWorkerWorktreeSuite) passesForcedTwice(state types.JobState) {
	r := s.repo.in(s.T())
	deps := rootSession(s.T(), spawnedW1)
	for _, command := range []string{
		"git worktree remove -f -f " + s.wt,
		"git worktree remove --force --force " + s.wt,
		"git worktree remove -ff " + s.wt,
	} {
		s.Equal(ShellVerdict{}, r.judgeWith(deps, r.main, command, workerRow(state, s.wt)), "%q", command)
	}
}

func (s *FinishedWorkerWorktreeSuite) TestAPassedJobsPassesForcedTwice() {
	s.passesForcedTwice(types.StatePass)
}

func (s *FinishedWorkerWorktreeSuite) TestAFailedJobsPassesForcedTwice() {
	s.passesForcedTwice(types.StateFail)
}

func (s *FinishedWorkerWorktreeSuite) TestAPlainRemovalIsServedTheForcedTwiceLine() {
	r := s.repo.in(s.T())
	deps := rootSession(s.T(), spawnedW1)
	want := []string{"git", "worktree", "remove", "-f", "-f", s.wt}
	for _, command := range []string{"git worktree remove " + s.wt, "git worktree remove -f " + s.wt} {
		v := r.judgeWith(deps, r.main, command, workerRow(types.StatePass, s.wt))
		assertRemovalDenied(s.T(), v, "locked over the worker your session spawned for job w1")
		s.Require().Len(v.Next, 1, "%q", command)
		got := v.Next[0]
		s.Equal(types.NextStep{ID: got.ID, Run: "git worktree remove -f -f " + s.wt, Argv: want, Why: got.Why}, got)
	}
}

// UnprovenWorkerLockSuite holds that only the root session that spawned the
// finished worker owns its lock. Every other lock, caller and condition still
// refuses. Each condition has a worktree of its own.
type UnprovenWorkerLockSuite struct {
	suite.Suite
	repo   worktreeRepo
	before string
	// worker is clean; checkout, dirty and ahead each fail one more condition.
	worker, checkout, dirty, ahead string
}

func TestWorktreeRemoveUnprovenWorkerLockSuite(t *testing.T) {
	suite.Run(t, new(UnprovenWorkerLockSuite))
}

func (s *UnprovenWorkerLockSuite) SetupSuite() {
	s.repo = newWorktreeRepository(s.T())
	s.worker = s.repo.addLocked("worker")
	s.checkout = s.repo.add("checkout")
	s.repo.git(s.repo.main, "worktree", "lock", "--reason", vcs.CheckoutLockReason, s.checkout)
	s.dirty = s.repo.addLocked("dirty")
	s.Require().NoError(os.WriteFile(filepath.Join(s.dirty, "wip.txt"), []byte("wip\n"), 0o644))
	s.ahead = s.repo.addLocked("ahead")
	s.repo.commit(s.ahead)
	s.before = s.repo.state()
}

func (s *UnprovenWorkerLockSuite) TearDownSuite() {
	s.repo.assertUnchanged(s.T(), s.before)
}

func (s *UnprovenWorkerLockSuite) removeForcedTwice(deps Dependencies, wt string, rows ...types.Job) ShellVerdict {
	r := s.repo.in(s.T())
	return r.judgeWith(deps, r.main, "git worktree remove -f -f "+wt, rows...)
}

// refusedOverTheLock judges the clean worker's -f -f, its row in state.
func (s *UnprovenWorkerLockSuite) refusedOverTheLock(deps Dependencies, state types.JobState, rows ...types.Job) ShellVerdict {
	v := s.removeForcedTwice(deps, s.worker, append([]types.Job{workerRow(state, s.worker)}, rows...)...)
	assertRemovalDenied(s.T(), v, "it is locked (claude agent")
	return v
}

func (s *UnprovenWorkerLockSuite) TestASubagentCallerIsRefused() {
	deps := rootSession(s.T(), spawnedW1)
	deps.caller.Agent = "agent-other"
	s.refusedOverTheLock(deps, types.StatePass)
}

func (s *UnprovenWorkerLockSuite) TestAnIdentityLessCallerIsRefused() {
	deps := rootSession(s.T(), spawnedW1)
	deps.caller = job.Caller{}
	s.refusedOverTheLock(deps, types.StatePass)
}

func (s *UnprovenWorkerLockSuite) TestACallerActingUnderAForkedLeaseIsRefused() {
	deps := rootSession(s.T(), spawnedW1)
	deps.lease = "root/child"
	child := types.Job{ID: "root/child", Parent: "root", State: types.StateRunning}
	s.refusedOverTheLock(deps, types.StatePass, child)
}

func (s *UnprovenWorkerLockSuite) TestAnotherSessionsSpawnRecordIsRefused() {
	deps := rootSession(s.T(), nil)
	writeSpawnedAgent(hint.NewGate(deps.facts.CacheDir(), FactsKey("claude", "s2")), "agent-a1b2c3", spawnedW1["agent-a1b2c3"])
	s.refusedOverTheLock(deps, types.StatePass)
}

func (s *UnprovenWorkerLockSuite) TestAnUntrustedSpawnRecordIsRefused() {
	deps := rootSession(s.T(), map[string]spawnedAgent{"agent-a1b2c3": {Description: "root/worker w1", UntrustedJob: "w1"}})
	s.refusedOverTheLock(deps, types.StatePass)
}

func (s *UnprovenWorkerLockSuite) TestAJobThatNeverReturnedIsRefused() {
	s.refusedOverTheLock(rootSession(s.T(), spawnedW1), types.StateNoReturn)
}

func (s *UnprovenWorkerLockSuite) TestAnExitedJobIsSentToFileItsResult() {
	v := s.refusedOverTheLock(rootSession(s.T(), spawnedW1), types.StateExited)
	s.Contains(v.Lead, "job w1 exited without filing a result")
	s.Require().Len(v.Next, 1)
	s.Equal(hint.JobExit.Argv("w1"), v.Next[0].Argv)
}

func (s *UnprovenWorkerLockSuite) TestAnExitedJobWithAFiledResultWaitsForItsGrade() {
	r := s.repo.in(s.T())
	r.tries = []types.Attempt{filedTry("w1")}
	v := r.judgeWith(rootSession(s.T(), spawnedW1), r.main, "git worktree remove -f -f "+s.worker, workerRow(types.StateExited, s.worker))
	assertRemovalDenied(s.T(), v, "job w1 (exited) was taken in it", "it is locked (claude agent")
	s.Require().Len(v.Next, 1)
	s.Equal(hint.JobWait.Argv("w1"), v.Next[0].Argv)
}

func (s *UnprovenWorkerLockSuite) TestAMagusCheckoutLockIsRefused() {
	v := s.removeForcedTwice(rootSession(s.T(), spawnedW1), s.checkout, workerRow(types.StatePass, s.checkout))
	assertRemovalDenied(s.T(), v, "it is locked (magus checkout)")
}

func (s *UnprovenWorkerLockSuite) TestADirtyWorktreeIsRefused() {
	r := s.repo.in(s.T())
	deps := rootSession(s.T(), spawnedW1)
	row := workerRow(types.StatePass, s.dirty)
	assertRemovalDenied(s.T(), s.removeForcedTwice(deps, s.dirty, row), "first wip.txt")
	v := r.judgeWith(deps, r.main, "git worktree remove "+s.dirty, row)
	assertRemovalDenied(s.T(), v, "first wip.txt", "locked over the worker")
	s.Empty(v.Next, "-f -f answers the lock alone")
}

func (s *UnprovenWorkerLockSuite) TestCommitsYourCheckoutDoesNotHoldAreRefused() {
	v := s.removeForcedTwice(rootSession(s.T(), spawnedW1), s.ahead, workerRow(types.StatePass, s.ahead))
	assertRemovalDenied(s.T(), v, "1 commit(s) on it", "first ahead.txt")
}

// The pick commits in the main checkout, so this case builds a repository of
// its own.
func TestWorktreeRemoveRefusesAPathYourCheckoutChangedAfterThePick(t *testing.T) {
	r := newWorktreeRepository(t)
	wt, row := r.workerWorktree(types.StatePass)
	picked := r.commitFiles(wt, map[string]string{"a.txt": "worker\n"})
	r.git(r.main, "cherry-pick", "--no-commit", picked)
	r.commitFiles(r.main, map[string]string{"extra.txt": "root\n"})
	r.commitFiles(r.main, map[string]string{"a.txt": "root\n"})
	removal := r.judgeWith(rootSession(t, spawnedW1), r.main, "git worktree remove -f -f "+wt, row)
	assertRemovalDenied(t, removal, "read differently in your checkout's HEAD, first a.txt")
}

// A cherry-pick lands a worker's commits under new ids, amended and with more beside
// them; what it changed reads the same in the caller's HEAD, so nothing is lost.
// A checkout a job's fork made is that job's: git removes its lock only with -f -f, which
// would discard work too, so its owner is sent to magus, and a caller under another job is
// refused.
func TestWorktreeRemoveHoldsAJobsCheckoutToItsRoot(t *testing.T) {
	r := newWorktreeRepository(t)
	wt := r.add("owned")
	r.git(r.main, "worktree", "lock", "--reason", vcs.CheckoutLockReason, wt)
	row := types.Job{ID: "w1", State: types.StatePass, CheckoutRoot: wt, Worktree: &types.JobWorktree{Root: wt, Source: r.main}}
	remove := "git worktree remove -f -f " + wt

	root := Dependencies{caller: job.Caller{Host: "claude", Session: "s1"}}
	for _, line := range []string{remove, "git worktree remove " + wt, "git worktree remove -f " + wt} {
		v := r.judgeWith(root, r.main, line, row)
		assertRemovalDenied(t, v, "job w1's fork made and locked")
		require.Len(t, v.Next, 1, "%q", line)
		assert.Equal(t, hint.JobWait.Argv("w1"), v.Next[0].Argv, "an ended job's checkout goes through its waiter: %q", line)
	}
	declared := row
	declared.State, declared.CheckoutRoot = types.StateDeclared, ""
	v := r.judgeWith(root, r.main, remove, declared)
	require.Len(t, v.Next, 1)
	assert.Equal(t, hint.JobExit.Argv("w1"), v.Next[0].Argv, "a job not ended yet is ended through exit")

	foreign := Dependencies{caller: job.Caller{Host: "claude", Session: "s1", Agent: "agent-x"}, lease: "other"}
	other := types.Job{ID: "other", State: types.StateRunning}
	assertRemovalDenied(t, r.judgeWith(foreign, r.main, remove, row, other), "job w1's fork made it")
}

func TestOwnsCheckoutIsTheHoldersAndTheRoots(t *testing.T) {
	r := newWorktreeRepository(t)
	wt := r.add("owned")
	row := types.Job{ID: "w1", State: types.StateRunning, CheckoutRoot: wt, Worktree: &types.JobWorktree{Root: wt, Source: r.main}}
	other := types.Job{ID: "other", State: types.StateRunning}
	ctx := job.WithSnapshot(t.Context(), job.Snapshot{Rows: []types.Job{row, other}})
	at := location{cacheDir: t.TempDir(), workspace: r.main}
	caller := job.Caller{Host: "claude", Session: "s1", Agent: "agent-a1b2c3"}

	assert.True(t, ownsCheckout(ctx, Dependencies{caller: caller, lease: "w1"}, at, wt), "the bound worker")
	assert.True(t, ownsCheckout(ctx, Dependencies{caller: job.Caller{Host: "claude", Session: "s1"}}, at, wt), "the root")
	assert.False(t, ownsCheckout(ctx, Dependencies{caller: caller, lease: "other"}, at, wt), "another job's worker")

	spawned := rootSession(t, spawnedW1)
	spawned.caller = caller
	assert.True(t, ownsCheckout(ctx, spawned, at, wt), "a worker the root spawned for the job, before its binding is read")
	unspawned := rootSession(t, nil)
	unspawned.caller = caller
	assert.False(t, ownsCheckout(ctx, unspawned, at, wt), "an agent no spawn record attributes to the job")
}

func TestWorktreeRemovePassesCommitsYourCheckoutHoldsUnderNewHashes(t *testing.T) {
	r := newWorktreeRepository(t)
	wt, row := r.workerWorktree(types.StatePass)
	picked := r.commitFiles(wt, map[string]string{"a.txt": "worker\n", "x.txt": "x\n"})
	r.git(r.main, "cherry-pick", "--no-commit", picked)
	r.commitFiles(r.main, map[string]string{"extra.txt": "root\n"})

	assert.Equal(t, ShellVerdict{}, r.judgeWith(rootSession(t, spawnedW1), r.main, "git worktree remove -f -f "+wt, row))

	plain := r.add("plain")
	picked = r.commitFiles(plain, map[string]string{"p.txt": "p\n"})
	r.git(r.main, "cherry-pick", picked)
	assert.Equal(t, ShellVerdict{}, r.judge("git worktree remove "+plain), "landed commits pass with no job at all")
}

// An unlock leaves a removal nothing to stop it, so it is judged by every condition but
// the lock itself.
func TestWorktreeUnlockIsJudgedLikeRemove(t *testing.T) {
	r := newWorktreeRepository(t)
	lock := func(name string) string {
		wt := r.add(name)
		r.git(r.main, "worktree", "lock", "--reason", "in use", wt)
		return wt
	}

	clean := lock("clean")
	assert.Equal(t, ShellVerdict{}, r.judge("git worktree unlock "+clean))

	dirty := lock("dirty")
	require.NoError(t, os.WriteFile(filepath.Join(dirty, "wip.txt"), []byte("wip\n"), 0o644))
	v := r.judge("git worktree unlock " + dirty)
	assertRemovalDenied(t, v, "first wip.txt")
	assert.Contains(t, v.Deny, "unlocking "+dirty)
	assert.NotContains(t, v.Why, "it is locked")

	held := lock("held")
	assertRemovalDenied(t, r.judge("git worktree unlock "+held, types.Job{ID: "w1", State: types.StateRunning, CheckoutRoot: held}), "job w1 (running)")

	ahead := lock("ahead")
	r.commit(ahead)
	assertRemovalDenied(t, r.judge(fmt.Sprintf("git -C %s worktree unlock %s", r.main, ahead)), "1 commit(s) on it")
	assertRemovalDenied(t, r.judge("git worktree unlock "+r.main), "it is the main worktree")
}

// Every fact the rule cannot read refuses, since the removal cannot be undone.
func TestWorktreeRemoveDeniesWhatItCannotRead(t *testing.T) {
	r := newWorktreeRepository(t)
	wt := r.add("unread")

	noStore := denyWorktreeRemove(t.Context(), Dependencies{}, location{workspace: r.main}, r.main, "git worktree remove "+wt, DialectBash)
	assertRemovalDenied(t, noStore, "job store cannot be read")

	ctx := job.WithSnapshot(t.Context(), job.Snapshot{Err: os.ErrPermission})
	broken := denyWorktreeRemove(ctx, Dependencies{}, location{cacheDir: t.TempDir()}, r.main, "git worktree remove "+wt, DialectBash)
	assertRemovalDenied(t, broken, "job store cannot be read")

	disabled := false
	off := denyWorktreeRemove(t.Context(), Dependencies{VCS: types.VCSOptions{Enabled: &disabled}}, location{cacheDir: t.TempDir()}, r.main, "git worktree remove "+wt, DialectBash)
	assertRemovalDenied(t, off, "version control is disabled")

	for command, says := range map[string]string{
		"git worktree remove $WT":                                   "not literal",
		"git worktree remove":                                       "exactly one worktree",
		"env git worktree remove " + wt:                             "through a wrapper",
		fmt.Sprintf("if true; then git worktree remove %s; fi", wt): "conditional",
		fmt.Sprintf("git worktree remove %s && (", wt):              "does not parse",
		"git --git-dir=x worktree remove " + wt:                     "not knowable",
	} {
		assertRemovalDenied(t, r.judge(command), says)
	}
	assertRemovalDenied(t, r.judgeIn("", "git worktree remove "+wt), "which checkout this session runs in is unknown")
}

// A for loop over literal words is judged one removal per word, as if written out; a list
// only the shell can expand stays refused.
func TestWorktreeRemoveFollowsALiteralLoop(t *testing.T) {
	r := newWorktreeRepository(t)
	r.add("one")
	r.add("two")
	loop := func(words string) string {
		return fmt.Sprintf("for b in %s; do git worktree remove %s/$b; done", words, r.parent)
	}

	for _, command := range []string{
		loop("one two"),
		`for b in one "two"; do git worktree remove ../"${b}" --force; done`,
		fmt.Sprintf("cd %s && for b in one two; do git -C main worktree remove ../$b; done", r.parent),
	} {
		assert.Equal(t, ShellVerdict{}, r.judge(command), "%q", command)
	}

	dirty := r.add("three")
	require.NoError(t, os.WriteFile(filepath.Join(dirty, "a.txt"), []byte("changed\n"), 0o644))
	assertRemovalDenied(t, r.judge(loop("one three two")), fmt.Sprintf("Removing %s is refused", dirty), "first a.txt")

	for _, words := range []string{"$(ls)", "*", "one $W", "{one,two}", "~/one", `one\ two`, `"one two"`} {
		assertRemovalDenied(t, r.judge(loop(words)), "a loop whose words are not all literal")
	}
	for _, command := range []string{
		fmt.Sprintf("for b in one two; do b=main; git worktree remove %s/$b; done", r.parent),
		fmt.Sprintf("for b in one two; do git worktree remove %s/$b; break; done", r.parent),
		"f() { :; }; " + loop("one two"),
		fmt.Sprintf("for b; do git worktree remove %s/$b; done", r.parent),
		fmt.Sprintf("while true; do git worktree remove %s/one; done", r.parent),
	} {
		assertRemovalDenied(t, r.judge(command), "a loop whose words are not all literal")
	}
	assertRemovalDenied(t, r.judge(fmt.Sprintf("for b in one; do git worktree remove %s/${b%%/}; done", r.parent)), "not literal")
}

// Prune clears only records of directories already gone, and help prints usage: neither
// removes anything, so neither is judged.
func TestWorktreeRemoveLeavesPruneAndHelpAlone(t *testing.T) {
	r := newWorktreeRepository(t)
	for _, command := range []string{"git worktree prune", "git worktree list", "git worktree remove --help", "git worktree remove -h", "git help worktree remove"} {
		assert.Equal(t, ShellVerdict{}, r.judge(command), "%q", command)
		assert.Empty(t, Evaluate(testDependencies(), command).Deny, "%q", command)
	}
	wt := r.add("gone")
	require.NoError(t, os.RemoveAll(wt))
	assertRemovalDenied(t, r.judge("git worktree remove "+wt), "`git worktree prune` clears the record")
	assertRemovalDenied(t, r.judge(fmt.Sprintf("git worktree remove %s --help", wt)))
}

// The rule is wired into the hook's command arm, ranked beside the other rules that read
// the filesystem.
func TestJudgeWorktreeRemove(t *testing.T) {
	r := newWorktreeRepository(t)
	testkit.Isolate(t)
	ctx := WithLocation(context.Background(), t.TempDir(), r.main, r.main)
	clean, dirty := r.add("clean"), r.add("wip")
	require.NoError(t, os.WriteFile(filepath.Join(dirty, "wip.txt"), nil, 0o644))

	assert.NotEqual(t, "deny", Judge(ctx, Dependencies{}, Request{Input: "git worktree remove " + clean}).Decision)
	v := Judge(ctx, Dependencies{}, Request{Input: "git worktree remove " + dirty})
	assert.Equal(t, verdictWithRule("deny", string(denyRuleWorktreeRemove)), unworded(v))
}

// WorktreePruneSuite judges `git worktree prune`. A gone record is the only
// pointer to its branch, so prune must not clear one with unreached commits.
type WorktreePruneSuite struct {
	suite.Suite
	repo worktreeRepo
}

func TestWorktreePruneSuite(t *testing.T) {
	suite.Run(t, new(WorktreePruneSuite))
}

func (s *WorktreePruneSuite) SetupTest() {
	s.repo = newWorktreeRepository(s.T())
}

func (s *WorktreePruneSuite) TestPassesWhenNoCheckoutIsGone() {
	s.repo.add("kept")
	s.Equal(ShellVerdict{}, s.repo.judge("git worktree prune"))
}

func (s *WorktreePruneSuite) TestDeniesAGoneCheckoutWhoseCommitNoRemoteReaches() {
	wt := s.repo.add("lost")
	s.repo.commit(wt)
	s.Require().NoError(os.RemoveAll(wt))

	v := s.repo.judge("git worktree prune")
	s.Equal(denyRule{Name: denyRuleWorktreeRemove}, v.Rule)
	s.Contains(v.Deny, "pruning the worktree records may lose work")
	full := denial{Say: v.Deny, Why: v.Why}.full()
	s.Contains(full, "is lost: its directory is deleted")
	s.Contains(full, fmt.Sprintf("restore with `git worktree add %s lost`", wt))
}

func (s *WorktreePruneSuite) TestPassesWhenTheGoneCommitIsOnARemoteRef() {
	wt := s.repo.add("published")
	head := s.repo.commit(wt)
	s.repo.git(s.repo.main, "update-ref", "refs/remotes/origin/published", head)
	s.Require().NoError(os.RemoveAll(wt))

	s.Equal(ShellVerdict{}, s.repo.judge("git worktree prune"))
}

// RmOfAWorktreeSuite judges a recursive rm that reaches a worktree. Each
// condition has a worktree of its own: dirty holds a change, ahead a commit no
// remote carries, done nothing.
type RmOfAWorktreeSuite struct {
	suite.Suite
	r                  worktreeRepo
	before             string
	dirty, ahead, done string
}

func TestRmOfAWorktreeSuite(t *testing.T) { suite.Run(t, new(RmOfAWorktreeSuite)) }

func (s *RmOfAWorktreeSuite) SetupSuite() {
	s.r = newWorktreeRepository(s.T())
	s.dirty = s.r.add("dirty")
	s.Require().NoError(os.WriteFile(filepath.Join(s.dirty, "a.txt"), []byte("changed\n"), 0o644))
	s.ahead = s.r.add("ahead")
	s.r.commit(s.ahead)
	s.Require().NoError(os.MkdirAll(filepath.Join(s.ahead, "build"), 0o755))
	s.done = s.r.add("done")
	s.before = s.r.state()
}

func (s *RmOfAWorktreeSuite) TearDownSuite() {
	s.r.assertUnchanged(s.T(), s.before)
}

func (s *RmOfAWorktreeSuite) TestIsJudgedLikeRemove() {
	r := s.r.in(s.T())
	assertRemovalDenied(s.T(), r.judge("rm -rf "+s.dirty), "first a.txt")
	assertRemovalDenied(s.T(), r.judge("rm -r "+s.ahead), "1 commit(s) on it")
	assertRemovalDenied(s.T(), r.judge(fmt.Sprintf("cd %s && rm -fR ahead", r.parent)), "1 commit(s) on it")
	assertRemovalDenied(s.T(), r.judge("rm --recursive -- "+s.ahead), "1 commit(s) on it")
	assertRemovalDenied(s.T(), r.judge(fmt.Sprintf(`magus buzz -e 'fs\removeAll("%s");'`, s.ahead)), "1 commit(s) on it")
}

func (s *RmOfAWorktreeSuite) TestDirectoryHoldingAWorktreeIsJudged() {
	r := s.r.in(s.T())
	assertRemovalDenied(s.T(), r.judge("rm -rf "+r.parent), "it is the main worktree")
	assertRemovalDenied(s.T(), r.judge("rm -rf "+filepath.Join(r.parent, "a*")), "1 commit(s) on it")
}

func (s *RmOfAWorktreeSuite) TestCleanLandedWorktreePasses() {
	s.Equal(ShellVerdict{}, s.r.in(s.T()).judge("rm -rf "+s.done))
}

func (s *RmOfAWorktreeSuite) TestElsewhereIsNotJudged() {
	r := s.r.in(s.T())
	for _, command := range []string{
		"rm -rf " + filepath.Join(s.ahead, "build"),
		"rm " + filepath.Join(s.ahead, "ahead.txt"),
		`rm -rf "$tmp"`,
		"rm -rf " + s.T().TempDir(),
	} {
		s.Equal(ShellVerdict{}, r.judge(command), "%q", command)
	}
}
