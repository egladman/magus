package magus

import (
	"context"
	"io/fs"
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
	primary, worktrees := linkedWorktrees(t, "worker")
	worktree := worktrees[0]

	primaryCache, err := ResolveCacheDir(primary)
	require.NoError(t, err)
	ref, _ := trail.WriteBlob(context.Background(), primaryCache, "grd", []byte("the full verdict"))
	require.NotEmpty(t, ref)

	opened, err := Inspect(context.Background(), worktree)
	require.NoError(t, err)
	m := opened.(*Magus)
	got, err := m.PayloadByRef(context.Background(), ref)
	require.NoError(t, err)
	assert.Equal(t, "the full verdict", string(got))

	missing := "grd" + strings.Repeat("0", 16)
	_, err = m.PayloadByRef(context.Background(), missing)
	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.Contains(t, err.Error(), m.CacheDir())
	assert.Contains(t, err.Error(), primaryCache)

	opened, err = Inspect(context.Background(), primary)
	require.NoError(t, err)
	_, err = opened.(*Magus).PayloadByRef(context.Background(), missing)
	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.NotContains(t, err.Error(), m.CacheDir(), "the primary checkout reads its own store alone")
}
