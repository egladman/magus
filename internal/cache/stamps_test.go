package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStampsGateTheReplay drives the install lifecycle: the run that writes the stamp
// is recorded with it, an unchanged stamp replays, and a deleted or rewritten stamp
// runs the tool again, since the tree it vouched for may be gone.
func TestStampsGateTheReplay(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	stamp := filepath.Join(root, "test", "pkg", "node_modules", ".pnpm", "lock.yaml")

	step := makeStep(root)
	step.Stamps = []string{"test/pkg/node_modules/.pnpm/lock.yaml"}
	calls := 0
	fn := func(_ context.Context) error {
		calls++
		require.NoError(t, os.MkdirAll(filepath.Dir(stamp), 0o755))
		return os.WriteFile(stamp, []byte("lock v1"), 0o644)
	}
	run := func() Result {
		t.Helper()
		r, err := c.Run(context.Background(), step, fn)
		require.NoError(t, err)
		return r
	}

	assert.False(t, run().Hit, "no stamp yet: the tool must run")
	assert.True(t, run().Hit, "the stamp the run left is recorded, so an unchanged tree replays")
	assert.Equal(t, 1, calls)

	require.NoError(t, os.RemoveAll(filepath.Dir(filepath.Dir(stamp))))
	assert.False(t, run().Hit, "a deleted tree must reinstall")
	assert.Equal(t, 2, calls)

	require.NoError(t, os.WriteFile(stamp, []byte("lock v0"), 0o644))
	assert.False(t, run().Hit, "a stamp another tool rewrote no longer vouches for the entry")
	assert.Equal(t, 3, calls)
}

// A stamp the tool never writes can never vouch for a tree, so the entry never replays.
func TestAnAbsentStampNeverReplays(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	step := makeStep(root)
	step.Stamps = []string{"test/pkg/never-written"}
	calls := 0
	fn := func(_ context.Context) error { calls++; return nil }
	for range 2 {
		r, err := c.Run(context.Background(), step, fn)
		require.NoError(t, err)
		assert.False(t, r.Hit)
	}
	assert.Equal(t, 2, calls)
}
