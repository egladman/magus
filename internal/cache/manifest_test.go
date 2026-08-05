package cache

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/require"
)

// withPlatform overrides the platform a Cache stamps onto manifests it writes and
// checks manifests it reads against (see Manifest.Platform). Test-only: production
// code always reads runtime.GOOS/runtime.GOARCH (set once in Open), and a real
// build has no reason to lie about what platform it ran on. This is what lets a
// single test process drive both branches of the platform guard without two
// machines.
func withPlatform(platform string) Option {
	return func(c *Cache) { c.platform = platform }
}

// TestManifestPlatformMismatchIsMiss is the guard's whole point: a manifest
// written on one platform must not replay on another, because src: lines are
// content hashes and darwin/linux compute the identical digest for the same
// commit — nothing else in the key would catch this.
func TestManifestPlatformMismatchIsMiss(t *testing.T) {
	root := t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	writeMain(t, root, "package main")
	step := makeStep(root)
	step.Target = "build"

	c1, err := Open(t.Context(), cdir, WithMutable(true), withPlatform("linux/amd64"))
	require.NoError(t, err, "cache.Open c1")
	calls := 0
	r1, err := c1.Run(context.Background(), step, func(context.Context) error { calls++; return nil })
	require.NoError(t, err, "Run c1")
	require.False(t, r1.Hit, "first run must be a miss")
	require.Equal(t, 1, calls)

	c2, err := Open(t.Context(), cdir, WithMutable(true), withPlatform("darwin/arm64"))
	require.NoError(t, err, "cache.Open c2")
	r2, err := c2.Run(context.Background(), step, func(context.Context) error { calls++; return nil })
	require.NoError(t, err, "Run c2")
	require.False(t, r2.Hit, "a manifest written on a different platform must not replay")
	require.Equal(t, 2, calls, "fn must run again rather than replay the other platform's entry")
}

// TestManifestPlatformMatchStillReplays guards against over-blocking: the same
// platform on both ends must still hit exactly as it did before this field
// existed.
func TestManifestPlatformMatchStillReplays(t *testing.T) {
	root := t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	writeMain(t, root, "package main")
	step := makeStep(root)
	step.Target = "build"

	c1, err := Open(t.Context(), cdir, WithMutable(true), withPlatform("linux/amd64"))
	require.NoError(t, err, "cache.Open c1")
	calls := 0
	r1, err := c1.Run(context.Background(), step, func(context.Context) error { calls++; return nil })
	require.NoError(t, err, "Run c1")
	require.False(t, r1.Hit)
	require.Equal(t, 1, calls)

	c2, err := Open(t.Context(), cdir, WithMutable(true), withPlatform("linux/amd64"))
	require.NoError(t, err, "cache.Open c2")
	r2, err := c2.Run(context.Background(), step, func(context.Context) error { calls++; return nil })
	require.NoError(t, err, "Run c2")
	require.True(t, r2.Hit, "same platform must still replay")
	require.Equal(t, 1, calls, "fn must not run again on a same-platform hit")
}

// TestManifestEmptyPlatformMatchesAnyPlatform pins the chosen legacy policy: a
// manifest written before Platform existed (empty string) is treated as a match
// against every platform, the same permissive-on-absence convention readManifest
// already applies to Hash and ProjectPath above. The alternative (empty never
// matches) would invalidate every manifest already on disk today, since none of
// them carry this field yet.
func TestManifestEmptyPlatformMatchesAnyPlatform(t *testing.T) {
	root := t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	writeMain(t, root, "package main")
	step := makeStep(root)
	step.Target = "build"

	c1, err := Open(t.Context(), cdir, WithMutable(true), withPlatform("linux/amd64"))
	require.NoError(t, err, "cache.Open c1")
	r1, err := c1.Run(context.Background(), step, func(context.Context) error { return nil })
	require.NoError(t, err, "Run c1")
	require.False(t, r1.Hit)

	// Rewrite the just-written manifest to simulate one written before this field
	// existed: strip Platform back to its zero value.
	mp := c1.manifestPath(step.ProjectPath, r1.Hash)
	m, err := c1.readManifest(step.ProjectPath, r1.Hash)
	require.NoError(t, err, "readManifest")
	require.Equal(t, "linux/amd64", m.Platform, "sanity: snapshot stamped the writer's platform")
	m.Platform = ""
	data, err := json.MarshalIndent(m, "", "  ")
	require.NoError(t, err)
	require.NoError(t, writeAtomic(mp, data))

	c2, err := Open(t.Context(), cdir, WithMutable(true), withPlatform("darwin/arm64"))
	require.NoError(t, err, "cache.Open c2")
	calls := 0
	r2, err := c2.Run(context.Background(), step, func(context.Context) error { calls++; return nil })
	require.NoError(t, err, "Run c2")
	require.True(t, r2.Hit, "an empty-platform (legacy) manifest must replay on any platform")
	require.Equal(t, 0, calls)
}
