package review

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadMissingStoreIsEmptyNotAnError(t *testing.T) {
	s, err := Load(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, s)
}

func TestRecordMerges(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	require.NoError(t, Record(dir, []Receipt{{Path: "a.go", Digest: "aaa", At: now}}))
	require.NoError(t, Record(dir, []Receipt{{Path: "b.go", Digest: "bbb", At: now}}))

	s, err := Load(dir)
	require.NoError(t, err)
	// Acknowledging b must not withdraw a: a reviewer working through a large change in
	// several sittings is the normal case.
	assert.True(t, s.Covers("a.go", "aaa"))
	assert.True(t, s.Covers("b.go", "bbb"))
}

func TestRecordReplacesTheSamePath(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	require.NoError(t, Record(dir, []Receipt{{Path: "a.go", Digest: "old", At: now}}))
	require.NoError(t, Record(dir, []Receipt{{Path: "a.go", Digest: "new", At: now}}))

	s, err := Load(dir)
	require.NoError(t, err)
	require.Len(t, s, 1)
	assert.True(t, s.Covers("a.go", "new"))
	assert.False(t, s.Covers("a.go", "old"))
}

// The receipt is a claim about CONTENT, not about a path. Without this the first
// acknowledgement of a file would silently cover every later edit to it, which is the one
// way this feature could actively mislead a reader.
func TestCoversVoidsWhenTheContentMoves(t *testing.T) {
	s := Store{"a.go": {Path: "a.go", Digest: "aaa"}}

	assert.True(t, s.Covers("a.go", "aaa"))
	assert.False(t, s.Covers("a.go", "bbb"))
	assert.False(t, s.Covers("never-read.go", "aaa"))
}

// An unreadable file digests to "", and "" must never satisfy a receipt: otherwise a
// deleted file would read as acknowledged forever.
func TestCoversRejectsAnEmptyDigest(t *testing.T) {
	s := Store{"a.go": {Path: "a.go", Digest: ""}}
	assert.False(t, s.Covers("a.go", ""))
}

func TestDigestFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("package a\n\nfunc F() {}\n"), 0o644))

	first := DigestFile(path)
	assert.NotEmpty(t, first)

	// Reindenting is free, the same trade the notes store makes: a fingerprint that fires
	// on reformatting produces false staleness, and false staleness gets ignored.
	require.NoError(t, os.WriteFile(path, []byte("package a\n\n\tfunc   F() {}\n"), 0o644))
	assert.Equal(t, first, DigestFile(path))

	require.NoError(t, os.WriteFile(path, []byte("package a\n\nfunc G() {}\n"), 0o644))
	assert.NotEqual(t, first, DigestFile(path))

	assert.Empty(t, DigestFile(filepath.Join(dir, "gone.go")))
}
