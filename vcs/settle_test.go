package vcs

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// settleRepo is a repository whose main branch changed f and whose side branch changed
// g, so a merge of side changes exactly g against main.
func settleRepo(t *testing.T) string {
	t.Helper()
	dir := testkit.NewRepo(t, map[string]string{"f": "base\n", "g": "base\n"}).Root()
	gitRun(t, dir, "checkout", "-q", "-b", "side")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "g"), []byte("side\n"), 0o644))
	gitRun(t, dir, "commit", "-q", "-am", "side")
	gitRun(t, dir, "checkout", "-q", "-")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f"), []byte("main\n"), 0o644))
	gitRun(t, dir, "commit", "-q", "-am", "main")
	return dir
}

// TestGitHookOperationBeforeTheCommit: the pre-* hooks see the merge through the index,
// and pre-commit acts only under a merge-shaped state.
func TestGitHookOperationBeforeTheCommit(t *testing.T) {
	dir := settleRepo(t)
	ctx := t.Context()

	op, ok, err := GitHookOperation(ctx, dir, HookEvent{Hook: HookPreCommit})
	require.NoError(t, err)
	assert.False(t, ok, "an ordinary commit is not settled")
	assert.Equal(t, HookOperation{}, op)

	gitRun(t, dir, "merge", "-q", "--no-commit", "--no-ff", "side")
	op, ok, err = GitHookOperation(ctx, dir, HookEvent{Hook: HookPreCommit})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, HookOperation{
		Kind: "merge", Changed: []string{"g"}, CommitPending: true, indexFile: op.indexFile, gitDir: op.gitDir, base: "HEAD",
	}, op)

	// pre-merge-commit fires before git writes MERGE_HEAD, so it takes the hook's word.
	op, ok, err = GitHookOperation(ctx, dir, HookEvent{Hook: HookPreMergeCommit})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, HookOperation{Kind: "merge", Changed: []string{"g"}, CommitPending: true, gitDir: op.gitDir, base: "HEAD"}, op)
}

// TestGitHookOperationHonorsTheExportedIndex: `git commit -a` builds the commit from a
// temporary index, and the hook reads and stages through that one.
func TestGitHookOperationHonorsTheExportedIndex(t *testing.T) {
	dir := settleRepo(t)
	ctx := t.Context()
	gitRun(t, dir, "merge", "-q", "--no-commit", "--no-ff", "side")
	// A second index, as git commit -a would hand the hook, with h staged in it alone.
	index := filepath.Join(gitDirOf(t, dir), "index.settle")
	data, err := os.ReadFile(filepath.Join(gitDirOf(t, dir), "index"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(index, data, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "h"), []byte("new\n"), 0o644))

	op, ok, err := GitHookOperation(ctx, dir, HookEvent{Hook: HookPreCommit, IndexFile: index})
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, HookStage(ctx, dir, op, []string{"h"}))

	inOther, err := gitOutput(ctx, dir, gitOpts{Env: []string{"GIT_INDEX_FILE=" + index}}, "diff", "--cached", "--name-only", "HEAD")
	require.NoError(t, err)
	assert.Equal(t, "g\nh", inOther)
	inDefault, err := gitOutput(ctx, dir, gitOpts{}, "diff", "--cached", "--name-only", "HEAD")
	require.NoError(t, err)
	assert.Equal(t, "g", inDefault, "the default index is not the one git will commit from")

	dirty, err := HookDirtyFiles(ctx, dir, op)
	require.NoError(t, err)
	assert.Empty(t, dirty, "the operation's index holds g and h as the tree has them; the default index would call h untracked")
}

// TestSettleReadsWriteNoIndex: a settle hook, the merge driver and every other magus
// read run while the person's git may hold index.lock. A status that refreshes the index
// takes that lock, so reads must leave the index exactly as git left it.
func TestSettleReadsWriteNoIndex(t *testing.T) {
	dir := settleRepo(t)
	ctx := t.Context()
	gitRun(t, dir, "merge", "-q", "--no-commit", "--no-ff", "side")
	// Same content, new stat data: a refreshing status would rewrite this entry.
	later := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "f"), later, later))
	index := filepath.Join(gitDirOf(t, dir), "index")
	before, err := os.ReadFile(index)
	require.NoError(t, err)

	op, ok, err := GitHookOperation(ctx, dir, HookEvent{Hook: HookPreCommit})
	require.NoError(t, err)
	require.True(t, ok)
	dirty, err := HookDirtyFiles(ctx, dir, op)
	require.NoError(t, err)
	assert.Empty(t, dirty)
	dirty, err = gitVCS{}.DirtyFiles(ctx, dir, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"g"}, dirty)

	after, err := os.ReadFile(index)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a magus read rewrote the index, so it held index.lock")
}

// A merge that deletes a generated file stages the deletion itself. The regeneration
// that follows writes other outputs, and staging those must not name the deleted path,
// which git add refuses once neither the index nor the tree holds it.
func TestHookStageAfterAMergeThatDeletesAFile(t *testing.T) {
	dir := settleRepo(t)
	ctx := t.Context()
	gitRun(t, dir, "checkout", "-q", "-b", "drop")
	gitRun(t, dir, "rm", "-q", "f")
	gitRun(t, dir, "commit", "-q", "-m", "drop f")
	gitRun(t, dir, "checkout", "-q", "side")
	gitRun(t, dir, "merge", "-q", "--no-commit", "--no-ff", "drop")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "g"), []byte("regenerated\n"), 0o644))

	op, ok, err := GitHookOperation(ctx, dir, HookEvent{Hook: HookPreCommit})
	require.NoError(t, err)
	require.True(t, ok)
	dirty, err := HookDirtyFiles(ctx, dir, op)
	require.NoError(t, err)
	assert.Equal(t, []string{"g"}, dirty)
	require.NoError(t, HookStage(ctx, dir, op, dirty))

	staged, err := gitOutput(ctx, dir, gitOpts{}, "diff", "--cached", "--name-status", "HEAD")
	require.NoError(t, err)
	assert.Equal(t, "D\tf\nM\tg", staged)
}

// TestGitHookOperationAfterTheCommit: post-commit settles a single pick and the last of
// several, never one with picks to come, never a rebase's pick; post-rewrite settles a
// rebase from the onto its state dir names, and not an amend.
func TestGitHookOperationAfterTheCommit(t *testing.T) {
	dir := settleRepo(t)
	ctx := t.Context()
	gitDir := gitDirOf(t, dir)
	touch := func(name string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(gitDir, name)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(gitDir, name), []byte("x\n"), 0o644))
	}
	untouch := func(name string) { require.NoError(t, os.RemoveAll(filepath.Join(gitDir, name))) }

	_, ok, err := GitHookOperation(ctx, dir, HookEvent{Hook: HookPostCommit})
	require.NoError(t, err)
	assert.False(t, ok, "an ordinary commit")

	touch("CHERRY_PICK_HEAD")
	op, ok, err := GitHookOperation(ctx, dir, HookEvent{Hook: HookPostCommit})
	require.NoError(t, err)
	require.True(t, ok)
	// Changed is HEAD against its parent.
	assert.Equal(t, HookOperation{Kind: "cherry-pick", Changed: []string{"f"}, indexFile: op.indexFile, gitDir: op.gitDir, base: "HEAD~1"}, op)

	touch("sequencer/todo")
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "sequencer", "todo"), []byte("pick 1111111 one\npick 2222222 two\n"), 0o644))
	_, ok, err = GitHookOperation(ctx, dir, HookEvent{Hook: HookPostCommit})
	require.NoError(t, err)
	assert.False(t, ok, "a pick with another to come")
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "sequencer", "todo"), []byte("pick 2222222 two\n"), 0o644))
	_, ok, err = GitHookOperation(ctx, dir, HookEvent{Hook: HookPostCommit})
	require.NoError(t, err)
	assert.True(t, ok, "the last pick")
	untouch("sequencer")

	touch("rebase-merge/onto")
	_, ok, err = GitHookOperation(ctx, dir, HookEvent{Hook: HookPostCommit})
	require.NoError(t, err)
	assert.False(t, ok, "a rebase's pick waits for post-rewrite")
	untouch("CHERRY_PICK_HEAD")

	_, ok, err = GitHookOperation(ctx, dir, HookEvent{Hook: HookPostRewrite, Args: []string{"amend"}})
	require.NoError(t, err)
	assert.False(t, ok, "an amend is not a merge")

	onto, err := gitOutput(ctx, dir, gitOpts{}, "rev-parse", "HEAD~1")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "rebase-merge", "onto"), []byte(onto+"\n"), 0o644))
	op, ok, err = GitHookOperation(ctx, dir, HookEvent{Hook: HookPostRewrite, Args: []string{"rebase"}})
	require.NoError(t, err)
	require.True(t, ok)
	// Changed is onto against HEAD.
	assert.Equal(t, HookOperation{Kind: "rebase", Changed: []string{"f"}, indexFile: op.indexFile, gitDir: op.gitDir, base: onto}, op)
	untouch("rebase-merge")

	touch("rebase-apply/next")
	touch("rebase-apply/last")
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "rebase-apply", "next"), []byte("1\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "rebase-apply", "last"), []byte("2\n"), 0o644))
	_, ok, err = GitHookOperation(ctx, dir, HookEvent{Hook: HookPostApplypatch})
	require.NoError(t, err)
	assert.False(t, ok, "a patch before the last")
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "rebase-apply", "next"), []byte("2\n"), 0o644))
	touch("rebase-apply/rebasing")
	_, ok, err = GitHookOperation(ctx, dir, HookEvent{Hook: HookPostApplypatch})
	require.NoError(t, err)
	assert.False(t, ok, "the last patch of `git rebase --apply`, which post-rewrite settles")
	untouch("rebase-apply/rebasing")
	gitRun(t, dir, "update-ref", "ORIG_HEAD", "HEAD~1")
	_, ok, err = GitHookOperation(ctx, dir, HookEvent{Hook: HookPostApplypatch})
	require.NoError(t, err)
	assert.True(t, ok, "the last patch of an am series")

	_, _, err = GitHookOperation(ctx, dir, HookEvent{Hook: "post-checkout"})
	require.ErrorContains(t, err, "not a hook magus settles a merge from")
}

// TestHookSettledTreeAnswersOneCommit: the record matches the tree it was written for,
// once, and a different tree is a different commit.
func TestHookSettledTreeAnswersOneCommit(t *testing.T) {
	dir := settleRepo(t)
	ctx := t.Context()
	gitRun(t, dir, "merge", "-q", "--no-commit", "--no-ff", "side")
	op, ok, err := GitHookOperation(ctx, dir, HookEvent{Hook: HookPreMergeCommit})
	require.NoError(t, err)
	require.True(t, ok)

	settled, err := HookTreeSettled(ctx, dir, op)
	require.NoError(t, err)
	assert.False(t, settled, "nothing recorded")

	require.NoError(t, HookRecordSettledTree(ctx, dir, op))
	settled, err = HookTreeSettled(ctx, dir, op)
	require.NoError(t, err)
	assert.True(t, settled)
	settled, err = HookTreeSettled(ctx, dir, op)
	require.NoError(t, err)
	assert.False(t, settled, "the record answered the commit before")

	require.NoError(t, HookRecordSettledTree(ctx, dir, op))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f"), []byte("edited after the settle\n"), 0o644))
	gitRun(t, dir, "add", "f")
	settled, err = HookTreeSettled(ctx, dir, op)
	require.NoError(t, err)
	assert.False(t, settled, "the index moved on")
}

// TestSettleHooksStartMagusOnlyToSettle runs the installed hooks under real multi-commit
// operations, with a stand-in magus that logs each start: every operation starts it once,
// from the hook that settles it, and every pick, replayed commit or patch before that
// exits in the hook's own shell. Under magus.settle=defer nothing starts, and the record
// names where the first deferred operation began.
func TestSettleHooksStartMagusOnlyToSettle(t *testing.T) {
	dir := testkit.NewRepo(t, map[string]string{"f": "base\n"}).Root()
	ctx := t.Context()
	scratch := t.TempDir()
	started := filepath.Join(scratch, "started")
	bin := filepath.Join(scratch, "magus")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\necho \"$*\" >>"+started+"\n"), 0o755))
	_, err := gitVCS{}.InstallRegenHook(ctx, dir, bin+" vcs resolve --hook")
	require.NoError(t, err)
	starts := func() []string {
		t.Helper()
		data, err := os.ReadFile(started)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		require.NoError(t, err)
		require.NoError(t, os.Remove(started))
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}
	write := func(name, content string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	stopped := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = gitEnv()
		require.Error(t, cmd.Run(), "git %s was meant to stop on a conflict", strings.Join(args, " "))
	}

	gitRun(t, dir, "checkout", "-q", "-b", "side")
	for _, name := range []string{"s1", "s2", "s3", "s4", "s5", "s6"} {
		write(name, name+"\n")
		gitRun(t, dir, "add", name)
		gitRun(t, dir, "commit", "-q", "-m", name)
	}
	gitRun(t, dir, "checkout", "-q", "-b", "trunk", "side~6")
	write("m", "main\n")
	gitRun(t, dir, "add", "m")
	gitRun(t, dir, "commit", "-q", "-m", "m")
	main := gitRun2(t, dir, "rev-parse", "HEAD")
	gitRun(t, dir, "format-patch", "-q", "-o", filepath.Join(scratch, "patches"), "side~6..side")
	assert.Empty(t, starts(), "ordinary commits")

	gitRun(t, dir, "checkout", "-q", "-b", "pick", main)
	gitRun(t, dir, "cherry-pick", "side~6..side")
	assert.Equal(t, []string{"vcs resolve --hook post-commit"}, starts(), "six clean picks")

	gitRun(t, dir, "checkout", "-q", "-b", "stopped-mid", main)
	write("s3", "clash\n")
	gitRun(t, dir, "add", "s3")
	gitRun(t, dir, "commit", "-q", "-m", "clash")
	starts()
	stopped("cherry-pick", "side~6..side")
	write("s3", "resolved\n")
	gitRun(t, dir, "add", "s3")
	gitRun(t, dir, "-c", "core.editor=true", "cherry-pick", "--continue")
	assert.Equal(t, []string{"vcs resolve --hook post-commit"}, starts(), "stopped on the third, continued")

	gitRun(t, dir, "checkout", "-q", "-b", "stopped-last", main)
	write("s6", "clash\n")
	gitRun(t, dir, "add", "s6")
	gitRun(t, dir, "commit", "-q", "-m", "clash")
	starts()
	sequenceStart := gitRun2(t, dir, "rev-parse", "HEAD")
	stopped("cherry-pick", "side~6..side")
	write("s6", "resolved\n")
	gitRun(t, dir, "add", "s6")
	gitRun(t, dir, "commit", "-q", "--no-edit")
	// git hands this commit's hook the same environment whether `git commit` or
	// `cherry-pick --continue` runs it, and a regeneration staged into it stops
	// --continue half done, so the settle is deferred to `magus vcs resolve`.
	assert.Empty(t, starts(), "stopped on the last, concluded by git commit")
	op, ok, err := DeferredSettle(ctx, dir)
	require.NoError(t, err)
	require.True(t, ok, "the stopped last pick leaves its settle deferred")
	assert.Equal(t, sequenceStart, op.base, "from where the sequence started")
	require.NoError(t, ClearDeferredSettle(ctx, dir))

	for _, backend := range []string{"--merge", "--apply"} {
		gitRun(t, dir, "checkout", "-q", "-b", "rebased"+backend, "side")
		gitRun(t, dir, "rebase", "-q", backend, main)
		assert.Equal(t, []string{"vcs resolve --hook post-rewrite rebase"}, starts(), "rebase %s", backend)
	}

	gitRun(t, dir, "checkout", "-q", "-b", "applied", main)
	patches, err := filepath.Glob(filepath.Join(scratch, "patches", "*.patch"))
	require.NoError(t, err)
	gitRun(t, dir, append([]string{"am", "-q"}, patches...)...)
	assert.Equal(t, []string{"vcs resolve --hook post-applypatch"}, starts(), "an am series")

	gitRun(t, dir, "checkout", "-q", "-b", "deferred", main)
	gitRun(t, dir, "-c", "magus.settle=defer", "cherry-pick", "side~6..side~3")
	gitRun(t, dir, "-c", "magus.settle=defer", "cherry-pick", "side~3..side")
	assert.Empty(t, starts(), "deferred")
	op, ok, err = DeferredSettle(ctx, dir)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, main, op.base, "where the first batch started")
	assert.Equal(t, []string{"s1", "s2", "s3", "s4", "s5", "s6"}, op.Changed, "both batches")
	require.NoError(t, ClearDeferredSettle(ctx, dir))
	_, ok, err = DeferredSettle(ctx, dir)
	require.NoError(t, err)
	assert.False(t, ok)
}

// TestGitHookOperationSettlesTheWholeSequence: the last pick of a sequence settles what
// every pick changed, from the HEAD the sequencer recorded at its start, and a pick git
// stopped on with more to come is left to that last one.
func TestGitHookOperationSettlesTheWholeSequence(t *testing.T) {
	dir := settleRepo(t)
	ctx := t.Context()
	gitDir := gitDirOf(t, dir)
	start := gitRun2(t, dir, "rev-parse", "HEAD")
	for _, name := range []string{"p1", "p2"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644))
		gitRun(t, dir, "add", name)
		gitRun(t, dir, "commit", "-q", "-m", name)
	}
	require.NoError(t, os.MkdirAll(filepath.Join(gitDir, "sequencer"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "sequencer", "head"), []byte(start+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "CHERRY_PICK_HEAD"), []byte(start+"\n"), 0o644))
	todo := filepath.Join(gitDir, "sequencer", "todo")

	require.NoError(t, os.WriteFile(todo, []byte("pick 1111111 p3\n"), 0o644))
	op, ok, err := GitHookOperation(ctx, dir, HookEvent{Hook: HookPostCommit})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, []string{"p1", "p2"}, op.Changed, "both picks, not the last alone")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "p3"), []byte("p3\n"), 0o644))
	gitRun(t, dir, "add", "p3")
	_, ok, err = GitHookOperation(ctx, dir, HookEvent{Hook: HookPreCommit})
	require.NoError(t, err)
	assert.False(t, ok, "a stopped last pick: --continue refuses a commit its hook changed, so the hook's shell defers it")

	require.NoError(t, os.Remove(todo))
	op, ok, err = GitHookOperation(ctx, dir, HookEvent{Hook: HookPreCommit})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, []string{"p1", "p2", "p3"}, op.Changed, "the pick being committed and the ones before it")

	require.NoError(t, os.WriteFile(todo, []byte("pick 1111111 p3\npick 2222222 p4\n"), 0o644))
	_, ok, err = GitHookOperation(ctx, dir, HookEvent{Hook: HookPreCommit})
	require.NoError(t, err)
	assert.False(t, ok, "a stopped pick with another to come")
}
