package magus

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/trail"
)

// A hook the host ran from the primary checkout stored its verdict there, while the worker
// it was shown to sits in a linked worktree: the ref still resolves from the worktree, and
// a miss names both stores it searched.
func TestPayloadByRefReadsThePrimaryCheckoutFromALinkedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	primary, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", primary}, args...)...).CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, out)
	}
	git("init", "-q")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "test")
	require.NoError(t, os.WriteFile(filepath.Join(primary, "magusfile.buzz"), nil, 0o644))
	git("add", "-A")
	git("commit", "-q", "-m", "init")
	worktree := filepath.Join(t.TempDir(), "worker")
	git("worktree", "add", "-q", worktree)

	primaryCache, err := ResolveCacheDir(primary)
	require.NoError(t, err)
	ref, _ := trail.WriteBlob(context.Background(), primaryCache, "grd", []byte("the full verdict"))
	require.NotEmpty(t, ref)

	opened, err := Inspect(context.Background(), worktree)
	require.NoError(t, err)
	m := opened.(*Magus)
	got, err := m.PayloadByRef(ref)
	require.NoError(t, err)
	assert.Equal(t, "the full verdict", string(got))

	missing := "grd" + strings.Repeat("0", 16)
	_, err = m.PayloadByRef(missing)
	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.Contains(t, err.Error(), m.CacheDir())
	assert.Contains(t, err.Error(), primaryCache)

	opened, err = Inspect(context.Background(), primary)
	require.NoError(t, err)
	_, err = opened.(*Magus).PayloadByRef(missing)
	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.NotContains(t, err.Error(), m.CacheDir(), "the primary checkout reads its own store alone")
}
