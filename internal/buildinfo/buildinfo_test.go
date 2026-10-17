package buildinfo

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// File is never empty (an empty identity would disable the version gate) and maps the
// same executable file to the same value, so a process and the children it forks from
// that file keep adopting each other.
func TestFileStableAndNonEmpty(t *testing.T) {
	assert.NotEmpty(t, File())
	if File() != Unverifiable() {
		assert.Contains(t, File(), "dev-bin-")
		assert.Equal(t, File(), computeFile(), "the same executable file must map to the same identity")
	}
}

// Unverifiable must be a per-process-unique token so two unprovable builds never adopt
// each other. Regenerating it must not reproduce the same value.
func TestUnverifiableIsUnique(t *testing.T) {
	assert.NotEqual(t, "dev-unverifiable-"+randomToken(), Unverifiable(), "the unverifiable token must be freshly random per process")
	assert.NotEqual(t, randomToken(), randomToken(), "randomToken must not repeat")
}

func TestBuildIsStable(t *testing.T) {
	build := Build()
	assert.Len(t, build, 16, "the test binary carries a Go build ID")
	assert.Equal(t, build, Build())
}

// With no readable build ID the key still gets a never-empty identity, while a per-build
// store has no name and the digest says so.
func TestBuildFallsBackToTheFileIdentityWithoutABuildID(t *testing.T) {
	old := digest
	digest = func() string { return "" }
	t.Cleanup(func() { digest = old })

	assert.Empty(t, Digest())
	assert.Equal(t, File(), Build())
}

// CI builds a fresh binary on every run: same bytes, new file, new mtime. It is the same
// build, so its cache entries and compiled chunks are the ones the last run wrote.
func TestBuildIsTheBuildsContentNotItsFile(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	body, err := os.ReadFile(exe)
	require.NoError(t, err)
	fresh := filepath.Join(t.TempDir(), "magus")
	require.NoError(t, os.WriteFile(fresh, body, 0o755))
	long := time.Now().Add(-48 * time.Hour)
	require.NoError(t, os.Chtimes(fresh, long, long))

	d, err := buildIDDigest(exe)
	require.NoError(t, err)
	require.NotEmpty(t, d)
	assert.Equal(t, d, Build())
	freshDigest, err := buildIDDigest(fresh)
	require.NoError(t, err)
	assert.Equal(t, d, freshDigest)

	id, err := readBuildID(exe)
	require.NoError(t, err)
	require.NotEmpty(t, id, "the test binary carries a Go build ID")
	replacement := []byte(id)
	replacement[0] ^= 1
	other := bytes.ReplaceAll(body, []byte(id), replacement)
	require.False(t, bytes.Equal(body, other), "the test must change the stored build ID")
	rebuilt := filepath.Join(t.TempDir(), "magus")
	require.NoError(t, os.WriteFile(rebuilt, other, 0o755))
	rebuiltID, err := readBuildID(rebuilt)
	require.NoError(t, err)
	assert.Equal(t, string(replacement), rebuiltID)
	rebuiltDigest, err := buildIDDigest(rebuilt)
	require.NoError(t, err)
	assert.NotEqual(t, d, rebuiltDigest, "another build is another identity")
}

// A file that carries no build ID is not an unreadable one: only the second is an error.
func TestReadBuildIDTellsAbsentFromUnreadable(t *testing.T) {
	script := filepath.Join(t.TempDir(), "script")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho hi\n"), 0o755))
	id, err := readBuildID(script)
	require.NoError(t, err)
	assert.Empty(t, id)

	_, err = readBuildID(filepath.Join(t.TempDir(), "missing"))
	require.ErrorIs(t, err, os.ErrNotExist)
}
