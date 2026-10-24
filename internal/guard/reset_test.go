package guard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// `git reset --hard <rev>` in a clean checkout no live job was taken in is judged as `git
// switch` to that revision: neither discards anything there. A dirty tree, a live job, a
// reset with no revision or one relocated with -C stays whole-tree's.
func TestHardResetOnACleanUnboundTreeIsJudgedAsASwitch(t *testing.T) {
	r := newWorktreeRepo(t)
	judge := func(command string, rows ...types.Job) ShellVerdict {
		ctx := job.WithSnapshot(t.Context(), job.Snapshot{Rows: rows})
		at := location{cacheDir: t.TempDir(), workspace: r.main, dir: r.main}
		return judgeShellLine(ctx, Dependencies{}, at, r.main, command, DialectBash)
	}
	wholeTree := denyRule{Name: denyRuleWholeTree, Arg: "git reset --hard"}

	assert.Empty(t, judge("git reset --hard origin/main").Deny)
	assert.Empty(t, judge("git fetch -q && git reset --hard origin/main").Deny)
	assert.Equal(t, judge("git switch --detach origin/main"), judge("git reset --hard origin/main"))

	assert.Equal(t, wholeTree, judge("git reset --hard").Rule, "no revision")
	assert.Equal(t, wholeTree, judge("git -C "+r.main+" reset --hard origin/main").Rule, "relocated")
	assert.Equal(t, wholeTree, judge("git reset --hard origin/main", types.Job{ID: "w1", State: types.StateRunning, CheckoutRoot: r.main}).Rule, "a live job")
	assert.Empty(t, judge("git reset --hard origin/main", types.Job{ID: "w1", State: types.StatePass, CheckoutRoot: r.main}).Deny, "an ended job")

	require.NoError(t, os.WriteFile(filepath.Join(r.main, "a.txt"), []byte("edited\n"), 0o644))
	assert.Equal(t, wholeTree, judge("git reset --hard origin/main").Rule, "a dirty tree")
}
