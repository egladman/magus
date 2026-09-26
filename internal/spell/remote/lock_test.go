package remote

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLockMarshalIsStable(t *testing.T) {
	t.Parallel()
	lint := digest.FromBytes([]byte("lint"))
	gofmt := digest.FromBytes([]byte("fmt"))
	l := Lock{Version: lockVersion, Spells: map[string]LockEntry{
		"ghcr.io/team/spells/lint": {Tag: "1.4", Digest: lint},
		"ghcr.io/team/spells/fmt":  {Tag: "2", Digest: gofmt},
	}}
	raw, err := l.Marshal()
	require.NoError(t, err)
	again, err := l.Marshal()
	require.NoError(t, err)
	assert.Equal(t, raw, again, "the same pins write the same bytes")

	text := string(raw)
	assert.True(t, strings.HasPrefix(text, lockHeader), text)
	assert.Less(t, strings.Index(text, "ghcr.io/team/spells/fmt:"), strings.Index(text, "ghcr.io/team/spells/lint:"), "keys are sorted")
	assert.Contains(t, text, `tag: "1.4"`, "a numeric-looking tag stays a string")

	back, err := parseLock(raw)
	require.NoError(t, err)
	assert.Equal(t, l, back)

	empty, err := Lock{Version: lockVersion}.Marshal()
	require.NoError(t, err)
	assert.Equal(t, lockHeader+"version: 1\n", string(empty))
}

func TestReadLock(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	got, err := ReadLock(root)
	require.NoError(t, err)
	assert.Equal(t, Lock{Version: lockVersion}, got, "no lock reads as an empty one")

	d := digest.FromBytes([]byte("m"))
	for name, tc := range map[string]struct {
		body, wantErr string
	}{
		"other version": {body: "version: 2\n", wantErr: "schema version 2, this magus reads version 1"},
		"empty":         {body: "", wantErr: "schema version 0"},
		"unknown field": {body: "version: 1\nspells:\n  ghcr.io/team/lint:\n    tag: v1\n    digest: " + d.String() + "\n    size: 3\n", wantErr: "field size not found"},
		"workspace key": {body: "version: 1\nspells:\n  spells/lint:\n    tag: v1\n    digest: " + d.String() + "\n", wantErr: `"spells/lint" is not a registry path`},
		"bad digest":    {body: "version: 1\nspells:\n  ghcr.io/team/lint:\n    tag: v1\n    digest: sha256:abc\n", wantErr: "ghcr.io/team/lint"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, LockFile), []byte(tc.body), 0o644))
			_, err := ReadLock(dir)
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// An update reads the lock it replaces under the file lock, so the callback sees what
// is on disk, and the result lands whole.
func TestUpdateLock(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d1, d2 := digest.FromBytes([]byte("one")), digest.FromBytes([]byte("two"))
	first := Lock{Version: lockVersion, Spells: map[string]LockEntry{"ghcr.io/team/lint": {Tag: "v1", Digest: d1}}}
	require.NoError(t, UpdateLock(t.Context(), root, func(cur Lock) (Lock, error) {
		assert.Equal(t, Lock{Version: lockVersion}, cur)
		return first, nil
	}))
	second := Lock{Version: lockVersion, Spells: map[string]LockEntry{"ghcr.io/team/lint": {Tag: "v2", Digest: d2}}}
	require.NoError(t, UpdateLock(t.Context(), root, func(cur Lock) (Lock, error) {
		assert.Equal(t, first, cur)
		return second, nil
	}))
	got, err := ReadLock(root)
	require.NoError(t, err)
	assert.Equal(t, second, got)
	assert.NoFileExists(t, filepath.Join(root, LockFile+".flock"), "the flock never sits beside the committed lock")
}

// A lock that pins nothing is never written, and an update that unpins the last spell
// removes the file rather than leaving a header and a version behind.
func TestUpdateLockWritesNoEmptyLock(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lockPath := filepath.Join(root, LockFile)
	empty := func(Lock) (Lock, error) { return Lock{Version: lockVersion}, nil }

	require.NoError(t, UpdateLock(t.Context(), root, empty))
	assert.NoFileExists(t, lockPath, "no remote spell, no lock")

	pinned := Lock{Version: lockVersion, Spells: map[string]LockEntry{
		"ghcr.io/team/lint": {Tag: "v1", Digest: digest.FromBytes([]byte("one"))},
	}}
	require.NoError(t, UpdateLock(t.Context(), root, func(Lock) (Lock, error) { return pinned, nil }))
	require.FileExists(t, lockPath)

	require.NoError(t, UpdateLock(t.Context(), root, empty))
	assert.NoFileExists(t, lockPath, "unpinning the last spell removes the lock")
	got, err := ReadLock(root)
	require.NoError(t, err)
	assert.Equal(t, Lock{Version: lockVersion}, got)
}
