package cache

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
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

// rewriteManifest applies mutate to the manifest stored for project/hash and writes it
// back, standing in for a manifest that reached this cache from somewhere hostile:
// importArtifact writes a remote manifest's BODY verbatim once its identity and
// signature gates pass, so nothing before readManifest inspects the records inside.
func rewriteManifest(t *testing.T, c *Cache, project, hash string, m Manifest, mutate func(*Manifest)) {
	t.Helper()
	m.Outputs = slices.Clone(m.Outputs) // mutate the copy, never the caller's records
	mutate(&m)
	data, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, writeAtomic(c.manifestPath(project, hash), data))
}

// TestManifestOutOfTreeOutputPathIsRefused: replay joins OutputRecord.Path onto the
// workspace root, so a manifest naming "../../victim" writes outside the workspace on
// the next hit. The entry must read as a miss and the file outside must be untouched.
func TestManifestOutOfTreeOutputPathIsRefused(t *testing.T) {
	root, cdir, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}

	r1, err := c.Run(context.Background(), step, func(context.Context) error {
		return os.WriteFile(out, []byte("built"), 0o644)
	})
	require.NoError(t, err, "first run")
	require.False(t, r1.Hit)
	stored, err := c.readManifest(step.ProjectPath, r1.Hash)
	require.NoError(t, err, "readManifest")

	victim := filepath.Join(t.TempDir(), "victim.txt")
	require.NoError(t, os.WriteFile(victim, []byte("mine"), 0o644))
	escape, err := filepath.Rel(root, victim)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(escape, ".."), "sanity: %q must climb out of the workspace", escape)
	require.Equal(t, victim, filepath.Clean(filepath.Join(root, escape)),
		"the record must name a file that exists outside the workspace, so only the refusal protects it")
	rewriteManifest(t, c, step.ProjectPath, r1.Hash, *stored, func(m *Manifest) {
		m.Outputs[0].Path = filepath.ToSlash(escape)
	})

	_, err = c.readManifest(step.ProjectPath, r1.Hash)
	require.Error(t, err, "an out-of-tree output path must not read back as a valid entry")

	c2, err := Open(t.Context(), cdir, WithMutable(true))
	require.NoError(t, err, "cache.Open c2")
	calls := 0
	r2, err := c2.Run(context.Background(), step, func(context.Context) error {
		calls++
		return os.WriteFile(out, []byte("built"), 0o644)
	})
	require.NoError(t, err, "second run")
	assert.False(t, r2.Hit, "a manifest that escapes the workspace must be a miss")
	assert.Equal(t, 1, calls, "the target must run instead of replaying the hostile entry")
	body, err := os.ReadFile(victim)
	require.NoError(t, err, "the file outside the workspace must still exist")
	assert.Equal(t, "mine", string(body), "replay must not write outside the workspace root")
}

// TestManifestMalformedBlobRefIsRefused: blobPath shards on the first two characters and
// joins the rest, so a blob ref that is not a plain sha256 digest resolves outside cas/ -
// an arbitrary read, and with a hostile output path an arbitrary write.
func TestManifestMalformedBlobRefIsRefused(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}

	r1, err := c.Run(context.Background(), step, func(context.Context) error {
		return os.WriteFile(out, []byte("built"), 0o644)
	})
	require.NoError(t, err, "first run")

	good, err := c.readManifest(step.ProjectPath, r1.Hash)
	require.NoError(t, err)
	require.Len(t, good.Outputs[0].Blob, 64, "sanity: a real blob ref is a full sha256 digest")

	for _, blob := range []string{
		"../../../../etc/passwd",
		"..",
		"ab",
		strings.Repeat("f", 63),
		strings.Repeat("F", 64),
		strings.Repeat("g", 64),
	} {
		t.Run(blob, func(t *testing.T) {
			rewriteManifest(t, c, step.ProjectPath, r1.Hash, *good, func(m *Manifest) { m.Outputs[0].Blob = blob })
			_, err := c.readManifest(step.ProjectPath, r1.Hash)
			require.Error(t, err, "blob ref %q must be refused", blob)
			assert.Contains(t, err.Error(), "malformed blob ref")
		})
	}
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
