package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openMutableCache(t *testing.T) *Cache {
	t.Helper()
	c, err := Open(t.TempDir(), WithMutable(true))
	require.NoError(t, err)
	return c
}

func TestPrune_EmptyCacheIsNoop(t *testing.T) {
	c := openMutableCache(t)
	n, freed, err := c.Prune(context.Background(), time.Now(), false)
	require.NoError(t, err)
	assert.Zero(t, n, "Prune on empty cache: n should be 0")
	assert.Zero(t, freed, "Prune on empty cache: freed should be 0")
}

func TestPrune_DryRun_NothingDeleted(t *testing.T) {
	c := openMutableCache(t)

	// Populate one entry with a no-op function.
	spec := Spec{
		WorkspaceRoot: t.TempDir(),
		ProjectPath:   "api/",
		Target:        "build",
	}
	_, err := c.Run(context.Background(), spec, func(ctx context.Context) error { return nil })
	require.NoError(t, err)

	n, freed, err := c.Prune(context.Background(), time.Now().Add(time.Hour), true)
	require.NoError(t, err)
	assert.NotZero(t, n, "dry-run Prune: expected to count at least one entry")
	_ = freed // non-zero because at least manifest exists

	// Real prune after dry-run: should also remove entries.
	n2, _, err := c.Prune(context.Background(), time.Now().Add(time.Hour), false)
	require.NoError(t, err)
	assert.NotZero(t, n2, "real Prune: expected to remove at least one entry")
}
