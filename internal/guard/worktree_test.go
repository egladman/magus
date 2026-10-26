package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// worktreeRepo is a clone of a one-commit origin, so origin/main is a real remote-tracking
// ref, with linked worktrees added beside it.
type worktreeRepo struct {
	t      *testing.T
	origin *testkit.Repo
	main   string
	parent string
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
	return r.judgeFrom(r.main, command, rows...)
}

func (r worktreeRepo) judgeFrom(callDir, command string, rows ...types.Job) ShellVerdict {
	r.t.Helper()
	return r.judgeWith(Dependencies{}, callDir, command, rows...)
}

func (r worktreeRepo) judgeWith(deps Dependencies, callDir, command string, rows ...types.Job) ShellVerdict {
	r.t.Helper()
	ctx := job.WithSnapshot(r.t.Context(), job.Snapshot{Rows: rows})
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
	wt := r.add("worker")
	r.git(r.main, "worktree", "lock", "--reason", "claude agent agent-a1b2c3 (pid 68539)", wt)
	return wt, types.Job{ID: "w1", State: state, CheckoutRoot: wt}
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
	assertRemovalDenied(t, r.judgeFrom(filepath.Join(held), "git worktree remove "+held), "this session runs in it")
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

	filed := r.judge("git worktree remove "+wt, types.Job{ID: "w", State: types.StateExited, CheckoutRoot: wt, Result: &types.JobResult{}})
	assertRemovalDenied(t, filed, "job w (exited)")
	require.Len(t, filed.Next, 1)
	assert.Equal(t, hint.JobWait.Argv("w"), filed.Next[0].Argv, "an exited job with a filed result waits for its grade")
}

// Your session's finished worker's worktree is locked by its host for as long as the host
// runs, so the lock is the only condition -f -f reaches past, and a plain removal is
// served the -f -f line.
func TestWorktreeRemovePassesYourFinishedWorkersLockedWorktree(t *testing.T) {
	for _, state := range []types.JobState{types.StatePass, types.StateFail} {
		t.Run(string(state), func(t *testing.T) {
			r := newWorktreeRepository(t)
			wt, row := r.workerWorktree(state)
			deps := rootSession(t, spawnedW1)
			for _, command := range []string{
				"git worktree remove -f -f " + wt,
				"git worktree remove --force --force " + wt,
				"git worktree remove -ff " + wt,
			} {
				assert.Equal(t, ShellVerdict{}, r.judgeWith(deps, r.main, command, row), "%q", command)
			}
		})
	}

	r := newWorktreeRepository(t)
	wt, row := r.workerWorktree(types.StatePass)
	deps := rootSession(t, spawnedW1)
	want := []string{"git", "worktree", "remove", "-f", "-f", wt}
	for _, command := range []string{"git worktree remove " + wt, "git worktree remove -f " + wt} {
		v := r.judgeWith(deps, r.main, command, row)
		assertRemovalDenied(t, v, "locked over the worker your session spawned for job w1")
		require.Len(t, v.Next, 1, "%q", command)
		assert.Equal(t, want, v.Next[0].Argv)
		assert.Equal(t, "git worktree remove -f -f "+wt, v.Next[0].Run)
	}
}

// Only the root session that spawned the finished worker owns its lock; every other lock,
// caller and condition still refuses.
func TestWorktreeRemoveRefusesALockItCannotProveIsYourFinishedWorkers(t *testing.T) {
	removeFF := func(wt string) string { return "git worktree remove -f -f " + wt }

	t.Run("a subagent caller", func(t *testing.T) {
		r := newWorktreeRepository(t)
		wt, row := r.workerWorktree(types.StatePass)
		deps := rootSession(t, spawnedW1)
		deps.caller.Agent = "agent-other"
		assertRemovalDenied(t, r.judgeWith(deps, r.main, removeFF(wt), row), "it is locked (claude agent")
	})
	t.Run("an identity-less caller", func(t *testing.T) {
		r := newWorktreeRepository(t)
		wt, row := r.workerWorktree(types.StatePass)
		deps := rootSession(t, spawnedW1)
		deps.caller = job.Caller{}
		assertRemovalDenied(t, r.judgeWith(deps, r.main, removeFF(wt), row), "it is locked (claude agent")
	})
	t.Run("a caller acting under a forked lease", func(t *testing.T) {
		r := newWorktreeRepository(t)
		wt, row := r.workerWorktree(types.StatePass)
		deps := rootSession(t, spawnedW1)
		deps.lease = "root/child"
		child := types.Job{ID: "root/child", Parent: "root", State: types.StateRunning}
		assertRemovalDenied(t, r.judgeWith(deps, r.main, removeFF(wt), row, child), "it is locked (claude agent")
	})
	t.Run("another session's spawn record", func(t *testing.T) {
		r := newWorktreeRepository(t)
		wt, row := r.workerWorktree(types.StatePass)
		deps := rootSession(t, nil)
		writeSpawnedAgent(hint.NewGate(deps.facts.CacheDir(), FactsKey("claude", "s2")), "agent-a1b2c3", spawnedW1["agent-a1b2c3"])
		assertRemovalDenied(t, r.judgeWith(deps, r.main, removeFF(wt), row), "it is locked (claude agent")
	})
	t.Run("an untrusted spawn record", func(t *testing.T) {
		r := newWorktreeRepository(t)
		wt, row := r.workerWorktree(types.StatePass)
		deps := rootSession(t, map[string]spawnedAgent{"agent-a1b2c3": {Description: "root/worker w1", UntrustedJob: "w1"}})
		assertRemovalDenied(t, r.judgeWith(deps, r.main, removeFF(wt), row), "it is locked (claude agent")
	})
	t.Run("a job that never returned", func(t *testing.T) {
		r := newWorktreeRepository(t)
		wt, row := r.workerWorktree(types.StateNoReturn)
		assertRemovalDenied(t, r.judgeWith(rootSession(t, spawnedW1), r.main, removeFF(wt), row), "it is locked (claude agent")
	})
	t.Run("an exited job", func(t *testing.T) {
		r := newWorktreeRepository(t)
		wt, row := r.workerWorktree(types.StateExited)
		v := r.judgeWith(rootSession(t, spawnedW1), r.main, removeFF(wt), row)
		assertRemovalDenied(t, v, "it is locked (claude agent")
		assert.Contains(t, v.Lead, "job w1 exited without filing a result")
		require.Len(t, v.Next, 1)
		assert.Equal(t, hint.JobExit.Argv("w1"), v.Next[0].Argv)
	})
	t.Run("an exited job with a filed result", func(t *testing.T) {
		r := newWorktreeRepository(t)
		wt, row := r.workerWorktree(types.StateExited)
		row.Result = &types.JobResult{}
		v := r.judgeWith(rootSession(t, spawnedW1), r.main, removeFF(wt), row)
		assertRemovalDenied(t, v, "job w1 (exited) was taken in it", "it is locked (claude agent")
		require.Len(t, v.Next, 1)
		assert.Equal(t, hint.JobWait.Argv("w1"), v.Next[0].Argv)
	})
	t.Run("a magus checkout lock", func(t *testing.T) {
		r := newWorktreeRepository(t)
		wt := r.add("worker")
		r.git(r.main, "worktree", "lock", "--reason", vcs.CheckoutLockReason, wt)
		row := types.Job{ID: "w1", State: types.StatePass, CheckoutRoot: wt}
		assertRemovalDenied(t, r.judgeWith(rootSession(t, spawnedW1), r.main, removeFF(wt), row), "it is locked (magus checkout)")
	})
	t.Run("a dirty worktree", func(t *testing.T) {
		r := newWorktreeRepository(t)
		wt, row := r.workerWorktree(types.StatePass)
		require.NoError(t, os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("wip\n"), 0o644))
		deps := rootSession(t, spawnedW1)
		assertRemovalDenied(t, r.judgeWith(deps, r.main, removeFF(wt), row), "first wip.txt")
		v := r.judgeWith(deps, r.main, "git worktree remove "+wt, row)
		assertRemovalDenied(t, v, "first wip.txt", "locked over the worker")
		assert.Empty(t, v.Next, "-f -f answers the lock alone")
	})
	t.Run("commits your checkout does not hold", func(t *testing.T) {
		r := newWorktreeRepository(t)
		wt, row := r.workerWorktree(types.StatePass)
		r.commit(wt)
		assertRemovalDenied(t, r.judgeWith(rootSession(t, spawnedW1), r.main, removeFF(wt), row), "1 commit(s) on it", "first worker.txt")
	})
	t.Run("a path your checkout changed after the pick", func(t *testing.T) {
		r := newWorktreeRepository(t)
		wt, row := r.workerWorktree(types.StatePass)
		picked := r.commitFiles(wt, map[string]string{"a.txt": "worker\n"})
		r.git(r.main, "cherry-pick", "--no-commit", picked)
		r.commitFiles(r.main, map[string]string{"extra.txt": "root\n"})
		r.commitFiles(r.main, map[string]string{"a.txt": "root\n"})
		assertRemovalDenied(t, r.judgeWith(rootSession(t, spawnedW1), r.main, removeFF(wt), row), "read differently in your checkout's HEAD, first a.txt")
	})
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
	assertRemovalDenied(t, r.judgeFrom("", "git worktree remove "+wt), "which checkout this session runs in is unknown")
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
