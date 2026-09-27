package guard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// worktreeRepo is a clone of a one-commit origin, so origin/main is a real remote-tracking
// ref, with linked worktrees added beside it.
type worktreeRepo struct {
	t      *testing.T
	main   string
	parent string
}

func newWorktreeRepo(t *testing.T) worktreeRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := realPath(t.TempDir())
	origin := filepath.Join(root, "origin")
	require.NoError(t, os.MkdirAll(origin, 0o755))
	r := worktreeRepo{t: t, main: filepath.Join(root, "main"), parent: root}
	r.git(origin, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(origin, "a.txt"), []byte("a\n"), 0o644))
	r.git(origin, "add", "a.txt")
	r.git(origin, "commit", "-q", "-m", "init")
	r.git(root, "clone", "-q", origin, r.main)
	for _, kv := range [][2]string{{"maintenance.auto", "false"}, {"gc.auto", "0"}, {"user.name", "test"}, {"user.email", "test@example.com"}} {
		r.git(r.main, "config", kv[0], kv[1])
	}
	return r
}

func (r worktreeRepo) git(dir string, args ...string) {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=test", "-c", "user.email=test@example.com"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	out, err := cmd.CombinedOutput()
	require.NoError(r.t, err, "git %v: %s", args, out)
}

// add makes a clean linked worktree on a new branch at origin/main.
func (r worktreeRepo) add(name string) string {
	r.t.Helper()
	wt := filepath.Join(r.parent, name)
	r.git(r.main, "worktree", "add", "-q", "-b", name, wt, "origin/main")
	return wt
}

// commit records an empty commit in dir that no remote carries.
func (r worktreeRepo) commit(dir string) {
	r.t.Helper()
	r.git(dir, "commit", "-q", "--allow-empty", "-m", "local")
}

// judge runs the rule for command from the main checkout, against a job store holding rows.
func (r worktreeRepo) judge(command string, rows ...types.Job) ShellVerdict {
	r.t.Helper()
	return r.judgeFrom(r.main, command, rows...)
}

func (r worktreeRepo) judgeFrom(callDir, command string, rows ...types.Job) ShellVerdict {
	r.t.Helper()
	ctx := job.WithSnapshot(r.t.Context(), job.Snapshot{Rows: rows})
	at := location{cacheDir: r.t.TempDir(), workspace: r.main, dir: callDir}
	return denyWorktreeRemove(ctx, Dependencies{}, at, callDir, command, DialectBash)
}

func assertRemovalDenied(t *testing.T, v ShellVerdict, says ...string) {
	t.Helper()
	assert.Equal(t, denyRule{Name: denyRuleWorktreeRemove}, v.Rule)
	for _, s := range says {
		assert.Contains(t, v.Deny, s)
	}
}

// A clean worktree whose commits are all on a remote-tracking ref loses nothing, however
// the removal is spelled.
func TestWorktreeRemovePassesWhenNothingCanBeLost(t *testing.T) {
	r := newWorktreeRepo(t)
	wt := r.add("done")
	for _, command := range []string{
		"git worktree remove " + wt,
		"git worktree remove --force " + wt,
		"git -C " + r.main + " --no-pager worktree remove -f -- " + wt,
		"cd " + r.main + " && git worktree remove ../done",
		"git worktree remove ../done",
	} {
		assert.Equal(t, ShellVerdict{}, r.judge(command), "%q", command)
	}
}

// Each failing condition refuses with its own reason and the command that inspects it.
func TestWorktreeRemoveDeniesEachUnsafeCondition(t *testing.T) {
	r := newWorktreeRepo(t)

	dirty := r.add("dirty")
	require.NoError(t, os.WriteFile(filepath.Join(dirty, "a.txt"), []byte("changed\n"), 0o644))
	assertRemovalDenied(t, r.judge("git worktree remove "+dirty), "1 modified, staged or untracked path(s), first a.txt", "git -C "+dirty+" status")

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
	assertRemovalDenied(t, r.judge("git worktree remove "+filepath.Join(r.parent, "origin")), "not a worktree of this repository")
	assertRemovalDenied(t, r.judge("cd "+r.parent+"/origin && git worktree remove "+held), "another repository")
}

// --force never reaches past a condition, and every failed one is listed together.
func TestWorktreeRemoveForceStillDeniesAndListsEveryFailure(t *testing.T) {
	r := newWorktreeRepo(t)
	wt := r.add("busy")
	r.commit(wt)
	require.NoError(t, os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("wip\n"), 0o644))
	r.git(r.main, "worktree", "lock", wt)

	v := r.judge("git worktree remove -f -f " + wt)
	assertRemovalDenied(t, v, "first wip.txt", "it is locked, so", "1 commit(s) on it", "--force changes nothing")
}

// A finished job filed what its worktree held, so unpublished commits there are not lost;
// a job that never returned filed nothing, and a live one is still working.
func TestWorktreeRemoveTrustsAFinishedJobsWorktree(t *testing.T) {
	r := newWorktreeRepo(t)
	wt := r.add("worker")
	r.commit(wt)
	for _, state := range []types.JobState{types.StatePass, types.StateFail} {
		assert.Equal(t, ShellVerdict{}, r.judge("git worktree remove "+wt, types.Job{ID: "w", State: state, CheckoutRoot: wt}), "%s", state)
	}
	assertRemovalDenied(t, r.judge("git worktree remove "+wt, types.Job{ID: "w", State: types.StateNoReturn, CheckoutRoot: wt}), "1 commit(s) on it")
	assertRemovalDenied(t, r.judge("git worktree remove "+wt, types.Job{ID: "w", State: types.StateExited, CheckoutRoot: wt}), "job w (exited)")
}

// Every fact the rule cannot read refuses, since the removal cannot be undone.
func TestWorktreeRemoveDeniesWhatItCannotRead(t *testing.T) {
	r := newWorktreeRepo(t)
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
		"git worktree remove $WT":                          "not literal",
		"git worktree remove":                              "exactly one worktree",
		"env git worktree remove " + wt:                    "through a wrapper",
		"if true; then git worktree remove " + wt + "; fi": "conditional",
		"git worktree remove " + wt + " && (":              "does not parse",
		"git --git-dir=x worktree remove " + wt:            "not knowable",
	} {
		assertRemovalDenied(t, r.judge(command), says)
	}
	assertRemovalDenied(t, r.judgeFrom("", "git worktree remove "+wt), "which checkout this session runs in is unknown")
}

// Prune clears only records of directories already gone, and help prints usage: neither
// removes anything, so neither is judged.
func TestWorktreeRemoveLeavesPruneAndHelpAlone(t *testing.T) {
	r := newWorktreeRepo(t)
	for _, command := range []string{"git worktree prune", "git worktree list", "git worktree remove --help", "git worktree remove -h", "git help worktree remove"} {
		assert.Equal(t, ShellVerdict{}, r.judge(command), "%q", command)
		assert.Empty(t, Evaluate(testDependencies(), command).Deny, "%q", command)
	}
	wt := r.add("gone")
	require.NoError(t, os.RemoveAll(wt))
	assertRemovalDenied(t, r.judge("git worktree remove "+wt), "`git worktree prune` clears the record")
	assertRemovalDenied(t, r.judge("git worktree remove "+wt+" --help"))
}

// The rule is wired into the hook's command arm, ranked beside the other rules that read
// the filesystem.
func TestJudgeWorktreeRemove(t *testing.T) {
	r := newWorktreeRepo(t)
	testkit.Isolate(t)
	ctx := WithLocation(context.Background(), t.TempDir(), r.main, r.main)
	clean, dirty := r.add("clean"), r.add("wip")
	require.NoError(t, os.WriteFile(filepath.Join(dirty, "wip.txt"), nil, 0o644))

	assert.NotEqual(t, "deny", Judge(ctx, Dependencies{}, Request{Input: "git worktree remove " + clean}).Decision)
	v := Judge(ctx, Dependencies{}, Request{Input: "git worktree remove " + dirty})
	assert.Equal(t, "deny", v.Decision)
	assert.Equal(t, string(denyRuleWorktreeRemove), v.Rule)
}
