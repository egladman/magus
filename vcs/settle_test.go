package vcs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// settleRepo is a repository whose main branch changed f and whose side branch changed
// g, so a merge of side changes exactly g against main.
func settleRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"f": "base\n", "g": "base\n"})
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
	assert.Equal(t, "merge", op.Kind)
	assert.True(t, op.CommitPending)
	assert.Equal(t, []string{"g"}, op.Changed)

	// pre-merge-commit fires before git writes MERGE_HEAD, so it takes the hook's word.
	op, ok, err = GitHookOperation(ctx, dir, HookEvent{Hook: HookPreMergeCommit})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, HookOperation{Kind: "merge", Changed: []string{"g"}, CommitPending: true, gitDir: op.gitDir}, op)
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
	assert.Equal(t, []string{"g", "h"}, dirty, "what the operation's index holds against HEAD, and h is not untracked there")
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
	assert.Equal(t, "cherry-pick", op.Kind)
	assert.False(t, op.CommitPending)
	assert.Equal(t, []string{"f"}, op.Changed, "HEAD against its parent")

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
	assert.Equal(t, "rebase", op.Kind)
	assert.Equal(t, []string{"f"}, op.Changed, "onto against HEAD")
	untouch("rebase-merge")

	touch("rebase-apply/next")
	touch("rebase-apply/last")
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "rebase-apply", "next"), []byte("1\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "rebase-apply", "last"), []byte("2\n"), 0o644))
	_, ok, err = GitHookOperation(ctx, dir, HookEvent{Hook: HookPostApplypatch})
	require.NoError(t, err)
	assert.False(t, ok, "a patch before the last")

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
