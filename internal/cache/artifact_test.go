package cache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeEntry plants one manifest plus its blob directly in the store, so a history
// test states the versions it is about instead of driving whole runs to produce them.
func writeEntry(t *testing.T, c *Cache, project, target, path, body string, at time.Time) string {
	t.Helper()
	blob := writeBlob(t, c, body)
	man := Manifest{
		ProjectPath: project,
		// The cache key must be unique PER ENTRY, not per content: three runs that
		// produced identical bytes are three entries. Deriving it from the blob alone
		// made them overwrite one another, so the fixture collapsed the history before
		// ListArtifacts could be asked to.
		Hash:      fmt.Sprintf("%s-%s-%d", blob[:16], target, at.UnixNano()),
		Target:    target,
		CreatedAt: at,
		Outputs:   []OutputRecord{{Path: path, Blob: blob, Mode: 0o644, Size: int64(len(body))}},
	}
	data, err := json.Marshal(man)
	require.NoError(t, err)
	mp := c.manifestPath(project, man.Hash)
	require.NoError(t, os.MkdirAll(filepath.Dir(mp), 0o755))
	require.NoError(t, os.WriteFile(mp, data, 0o644))
	return blob
}

func writeBlob(t *testing.T, c *Cache, body string) string {
	t.Helper()
	sum := sha256Hex([]byte(body))
	bp := c.blobPath(sum)
	require.NoError(t, os.MkdirAll(filepath.Dir(bp), 0o755))
	require.NoError(t, os.WriteFile(bp, []byte(body), 0o644))
	return sum
}

func testCacheDir(t *testing.T) *Cache {
	t.Helper()
	c, err := Open(filepath.Join(t.TempDir(), "cache"))
	require.NoError(t, err)
	return c
}

// TestListArtifactsNewestFirst pins the order, which is the whole readability of
// the output: a reader asking what changed wants the most recent change first.
func TestListArtifactsNewestFirst(t *testing.T) {
	c := testCacheDir(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	writeEntry(t, c, "pkg/a", "build", "dist/app", "v1", base)
	writeEntry(t, c, "pkg/a", "build", "dist/app", "v2", base.Add(time.Hour))
	writeEntry(t, c, "pkg/a", "build", "dist/app", "v3", base.Add(2*time.Hour))

	got, err := c.ListArtifacts(context.Background(), "pkg/a", "dist/app")
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.True(t, got[0].CreatedAt.After(got[1].CreatedAt), "newest first")
	assert.True(t, got[1].CreatedAt.After(got[2].CreatedAt))
	assert.Equal(t, int64(2), got[0].Output.Size)
	assert.Equal(t, "build", got[0].Target)
}

// TestListArtifactsCollapsesIdenticalContent is the behaviour that makes the output
// worth reading. A target that ran twenty times and produced the same bytes changed
// the artifact once; listing twenty rows buries the one change the reader came for.
//
// The surviving row is the OLDEST of each identical run, because the question is
// when content first appeared, not when it was last re-confirmed.
func TestListArtifactsCollapsesIdenticalContent(t *testing.T) {
	c := testCacheDir(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	writeEntry(t, c, "pkg/a", "build", "dist/app", "v1", base)
	writeEntry(t, c, "pkg/a", "build", "dist/app", "v1", base.Add(time.Hour))
	writeEntry(t, c, "pkg/a", "build", "dist/app", "v1", base.Add(2*time.Hour))
	writeEntry(t, c, "pkg/a", "build", "dist/app", "v2", base.Add(3*time.Hour))

	got, err := c.ListArtifacts(context.Background(), "pkg/a", "dist/app")
	require.NoError(t, err)
	require.Len(t, got, 2, "three identical v1 entries collapse to one")
	assert.Equal(t, int64(2), got[0].Output.Size)
	assert.Equal(t, base, got[1].CreatedAt, "the kept v1 row is the earliest, not the latest")
}

// TestListArtifactsScopesToPathAndProject: a manifest lists every output of a run,
// and a store holds every project. Both filters have to hold or history would report
// a sibling artifact's changes as this one's.
func TestListArtifactsScopesToPathAndProject(t *testing.T) {
	c := testCacheDir(t)
	now := time.Now().UTC()
	writeEntry(t, c, "pkg/a", "build", "dist/app", "a1", now)
	writeEntry(t, c, "pkg/a", "build", "dist/other", "o1", now)
	writeEntry(t, c, "pkg/b", "build", "dist/app", "b1", now)

	got, err := c.ListArtifacts(context.Background(), "pkg/a", "dist/app")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, int64(2), got[0].Output.Size)

	_, err = c.ListArtifacts(context.Background(), "pkg/zzz", "dist/app")
	require.Error(t, err, "an unknown project is not an empty history")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// TestListArtifactsEmptyForUnknownPath separates "this project has no such
// artifact" from "this project was never built", which the caller renders
// differently.
func TestListArtifactsEmptyForUnknownPath(t *testing.T) {
	c := testCacheDir(t)
	writeEntry(t, c, "pkg/a", "build", "dist/app", "v1", time.Now().UTC())

	got, err := c.ListArtifacts(context.Background(), "pkg/a", "dist/nope")
	require.NoError(t, err, "a built project with no such output is not an error")
	assert.Empty(t, got)
}

// TestMaterializeArtifactWritesBytesAndMode covers the happy path including the
// permission bits: an exported binary that loses +x is useless, and mode is recorded
// per output precisely so a materialized copy keeps it.
func TestMaterializeArtifactWritesBytesAndMode(t *testing.T) {
	c := testCacheDir(t)
	writeEntry(t, c, "pkg/a", "build", "dist/app", "hello", time.Now().UTC())
	versions, err := c.ListArtifacts(context.Background(), "pkg/a", "dist/app")
	require.NoError(t, err)
	require.Len(t, versions, 1)

	v := versions[0]
	v.Output.Mode = 0o755
	dst := filepath.Join(t.TempDir(), "nested", "app")
	require.NoError(t, c.GetArtifact(context.Background(), v, dst))

	body, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(body))
	fi, err := os.Stat(dst)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), fi.Mode().Perm(), "mode is restored, so an executable stays executable")
}

// TestMaterializeArtifactReportsEviction is the failure that must never be quiet.
// The store evicts LRU blobs, so a version can be listed from a surviving manifest
// with no bytes behind it. Reported as success with an empty file, a diff would show
// no differences and the reader would conclude the artifact was unchanged - the most
// misleading wrong answer available.
func TestMaterializeArtifactReportsEviction(t *testing.T) {
	c := testCacheDir(t)
	writeEntry(t, c, "pkg/a", "build", "dist/app", "hello", time.Now().UTC())
	versions, err := c.ListArtifacts(context.Background(), "pkg/a", "dist/app")
	require.NoError(t, err)
	require.Len(t, versions, 1)

	// Evict the blob but keep the manifest, which is exactly what GC leaves behind.
	require.NoError(t, os.Remove(c.blobPath(versions[0].Output.Blob)))

	dst := filepath.Join(t.TempDir(), "app")
	err = c.GetArtifact(context.Background(), versions[0], dst)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrArtifactMissing)
	assert.Contains(t, err.Error(), "--no-cache", "the error names the way out")
	assert.NoFileExists(t, dst, "a failed materialize leaves nothing behind to be mistaken for the artifact")
}

// TestArtifactVersionShort keeps the display hash short enough to scan in a column
// and long enough to identify a blob.
func TestArtifactVersionShort(t *testing.T) {
	assert.Equal(t, "0123456789ab", ArtifactVersion{Output: OutputRecord{Blob: "0123456789abcdef"}}.ShortBlob())
	assert.Equal(t, "abc", ArtifactVersion{Output: OutputRecord{Blob: "abc"}}.ShortBlob(), "a short hash is returned whole")
}
