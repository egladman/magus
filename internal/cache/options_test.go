package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two checkouts at the same content, each with its own cache dir and one shared store:
// the second hits the entry the first stored and gets its outputs, while its own dir
// stays free of entries. The first's live log is published to the store whole.
func TestCheckoutsSharingAStoreHitOneEntry(t *testing.T) {
	store := t.TempDir()
	open := func() (string, *Cache) {
		root := t.TempDir()
		writeMain(t, root, "package main")
		touchOut(t, root)
		c, err := Open(t.Context(), filepath.Join(root, ".magus"), WithLocalWrite(true), WithStoreDir(store))
		require.NoError(t, err)
		return root, c
	}
	rootA, a := open()
	rootB, b := open()

	calls := 0
	run := func(c *Cache, root string) Result {
		step := makeStep(root)
		step.Outputs = types.MustParseGlobs("test/pkg/out.txt")
		r, err := c.Run(t.Context(), step, func(context.Context) error {
			calls++
			return os.WriteFile(filepath.Join(root, "test", "pkg", "out.txt"), []byte("built"), 0o644)
		})
		require.NoError(t, err)
		return r
	}
	first := run(a, rootA)
	require.False(t, first.Hit)
	second := run(b, rootB)
	assert.True(t, second.Hit, "a key names no checkout, so the sibling's entry replays")
	assert.Equal(t, 1, calls)
	assert.Equal(t, first.Hash, second.Hash)

	got, err := os.ReadFile(filepath.Join(rootB, "test", "pkg", "out.txt"))
	require.NoError(t, err)
	assert.Equal(t, "built", string(got), "the hit restores the first checkout's output")
	assert.FileExists(t, logPathIn(store, "test/pkg", first.Hash), "the entry's log is in the store")
	assert.NoDirExists(t, filepath.Join(rootB, ".magus", "manifests"), "entries stay out of the checkout")
	assert.NoDirExists(t, filepath.Join(rootB, ".magus", "cas"))
}
