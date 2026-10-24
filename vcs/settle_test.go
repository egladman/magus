package vcs

import (
	"os"
	"path/filepath"
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

// pendingMergeOp is the operation a hook sees while git has a merge staged and not yet
// committed, reading and staging through indexFile ("" for the default index).
func pendingMergeOp(t *testing.T, dir, indexFile string) HookOperation {
	t.Helper()
	op := HookOperation{Kind: "merge", CommitPending: true, indexFile: indexFile, gitDir: absoluteGitDir(t, dir), base: "HEAD"}
	changed, err := hookChangedFiles(t.Context(), dir, op)
	require.NoError(t, err)
	op.Changed = changed
	return op
}

// TestHookStageHonorsTheOperationsIndex: `git commit -a` builds the commit from a
// temporary index, and the hook reads and stages through that one.
func TestHookStageHonorsTheOperationsIndex(t *testing.T) {
	dir := settleRepo(t)
	ctx := t.Context()
	gitRun(t, dir, "merge", "-q", "--no-commit", "--no-ff", "side")
	// A second index, as git commit -a would hand the hook, with h staged in it alone.
	index := filepath.Join(absoluteGitDir(t, dir), "index.settle")
	data, err := os.ReadFile(filepath.Join(absoluteGitDir(t, dir), "index"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(index, data, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "h"), []byte("new\n"), 0o644))

	op := pendingMergeOp(t, dir, index)
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
	index := filepath.Join(absoluteGitDir(t, dir), "index")
	before, err := os.ReadFile(index)
	require.NoError(t, err)

	op := pendingMergeOp(t, dir, "")
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

	op := pendingMergeOp(t, dir, "")
	dirty, err := HookDirtyFiles(ctx, dir, op)
	require.NoError(t, err)
	assert.Equal(t, []string{"g"}, dirty)
	require.NoError(t, HookStage(ctx, dir, op, dirty))

	staged, err := gitOutput(ctx, dir, gitOpts{}, "diff", "--cached", "--name-status", "HEAD")
	require.NoError(t, err)
	assert.Equal(t, "D\tf\nM\tg", staged)
}
