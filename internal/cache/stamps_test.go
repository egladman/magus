package cache

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/egladman/magus/types"
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

// TestVerifiedOutputsGateTheReplay pins the cached drift gate: proto's generate runs
// without rw as a check, and a hit used to restore fresh outputs over stale committed
// ones and pass without comparing anything. With VerifyOutputs a hit replays only while
// the tree's outputs read as the entry recorded them, mode included, so a tree that
// still matches keeps skipping the generator.
func TestVerifiedOutputsGateTheReplay(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	gen := filepath.Join(root, "test", "pkg", "gen")
	out := filepath.Join(gen, "api.pb.go")

	step := makeStep(root)
	step.Outputs = types.MustParseGlobs("test/pkg/gen/**")
	step.OutputsDeclared = true
	step.VerifyOutputs = true
	calls := 0
	fn := func(_ context.Context) error {
		calls++
		require.NoError(t, os.MkdirAll(gen, 0o755))
		return os.WriteFile(out, []byte("generated v2"), 0o644)
	}
	run := func() Result {
		t.Helper()
		r, err := c.Run(context.Background(), step, fn)
		require.NoError(t, err)
		return r
	}
	cached := func() bool {
		t.Helper()
		hit, err := c.IsCached(context.Background(), step)
		require.NoError(t, err)
		return hit
	}

	assert.False(t, run().Hit)
	assert.True(t, run().Hit, "outputs as the entry recorded them: the generator is skipped")
	assert.True(t, cached(), "the probe answers as the lookup does")

	require.NoError(t, os.WriteFile(out, []byte("stale"), 0o644))
	assert.False(t, cached())
	assert.False(t, run().Hit, "a stale committed output must reach the check, not be papered over")
	assert.Equal(t, 2, calls)

	orphan := filepath.Join(gen, "orphan.pb.go")
	require.NoError(t, os.WriteFile(orphan, []byte("left behind"), 0o644))
	assert.False(t, run().Hit, "a file the outputs match that the entry never recorded is drift too")
	require.NoError(t, os.Remove(orphan))
	assert.False(t, run().Hit, "the last entry recorded the orphan, so its removal moves the tree")
	require.NoError(t, os.Remove(out))
	assert.False(t, run().Hit, "a deleted output must regenerate through the check")
	assert.Equal(t, 5, calls)

	if runtime.GOOS != "windows" {
		require.NoError(t, os.Chmod(out, 0o755))
		assert.False(t, run().Hit, "an output whose mode moved is drift too")
		assert.True(t, run().Hit, "the entry now records the new mode")
		assert.Equal(t, 6, calls)
	}

	step.VerifyOutputs = false
	before := calls
	require.NoError(t, os.WriteFile(out, []byte("stale"), 0o644))
	assert.True(t, run().Hit, "an unverified step restores its outputs, as a build's hit always has")
	assert.Equal(t, before, calls)
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

// An entry filed before its step declared outputs records none, so it runs again and files
// an entry whose hit restores them.
func TestAnEntryWithoutTheDeclaredOutputsNeverReplays(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := filepath.Join(root, "test", "pkg", "index.scip")
	step := makeStep(root)
	calls := 0
	fn := func(_ context.Context) error {
		calls++
		return os.WriteFile(out, []byte("index"), 0o644)
	}
	run := func() Result {
		t.Helper()
		r, err := c.Run(context.Background(), step, fn)
		require.NoError(t, err)
		return r
	}

	assert.False(t, run().Hit)
	require.NoError(t, os.Remove(out))
	step.Outputs = types.MustParseGlobs("test/pkg/index.scip")
	step.OutputsDeclared = true
	assert.False(t, run().Hit, "the entry recorded no outputs, so it cannot restore the index")
	assert.Equal(t, 2, calls)

	require.NoError(t, os.Remove(out))
	assert.True(t, run().Hit, "the new entry records the index")
	assert.FileExists(t, out, "and its hit restores it")
	assert.Equal(t, 2, calls)
}
