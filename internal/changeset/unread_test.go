package changeset

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/types"
)

func TestUnreadHunksListsWhatNoMarkCoversInPatchOrder(t *testing.T) {
	t.Parallel()

	files := []FileHunks{
		{Path: "a.go", Hunks: []Hunk{{Index: 0, Digest: "a0"}, {Index: 1, Digest: "a1"}}},
		{Path: "b.go", Hunks: []Hunk{{Index: 0, Digest: "b0"}}},
	}

	assert.Equal(t, []types.DiffHunkRef{
		{Path: "a.go", Index: 1, Digest: "a1"},
		{Path: "b.go", Index: 0, Digest: "b0"},
	}, UnreadHunks(files, []string{"a0", "elsewhere"}))
	assert.Empty(t, UnreadHunks(files, []string{"a0", "a1", "b0"}))
	assert.Len(t, UnreadHunks(files, nil), 3)
}

func TestUnreadLoadViewedReadsMarksWithoutASession(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "review"), 0o755))
	marks := `{"digest":"a0","viewed":true}` + "\n" + `{"digest":"b0","viewed":true}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "review", "viewed.jsonl"), []byte(marks), 0o644))

	got, err := NewStore(dir).LoadViewed()

	require.NoError(t, err)
	assert.Equal(t, []string{"a0", "b0"}, got)
}

func TestUnreadLoadViewedOfAStoreNobodyWroteIsEmptyNotAnError(t *testing.T) {
	t.Parallel()

	got, err := NewStore(t.TempDir()).LoadViewed()

	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestUnreadLoadViewedSaysWhenTheMarksCannotBeRead(t *testing.T) {
	t.Parallel()

	legacy := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(legacy, "review"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "review", "viewed.json"), []byte(`["a0"]`), 0o644))
	unreadable := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(unreadable, "review", "viewed.jsonl"), 0o755), "a directory where the file belongs")
	garbled := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(garbled, "review"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(garbled, "review", "viewed.jsonl"), []byte("{not json\n"), 0o644))

	tests := []struct {
		name  string
		store *Store
	}{
		{"an older magus's JSON", NewStore(legacy)},
		{"unreadable", NewStore(unreadable)},
		{"undecodable", NewStore(garbled)},
		{"no state directory", NewStore("")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.store.LoadViewed()
			require.Error(t, err)
			assert.Nil(t, got)
		})
	}
}

// An old JSON file is neither read nor skipped: the error names it and says to preserve it.
func TestUnreadLoadViewedNamesAnOldJSONFileAndHowToPreserveIt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	old := filepath.Join(dir, "review", "viewed.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(old), 0o755))
	require.NoError(t, os.WriteFile(old, []byte(`["a0"]`), 0o644))

	_, err := NewStore(dir).LoadViewed()

	require.ErrorIs(t, err, file.ErrLegacyStore)
	require.ErrorContains(t, err, "preserve it")
	require.ErrorContains(t, err, old)
}

func TestUnreadLoadViewedWithNoStateDirectoryIsTheSentinel(t *testing.T) {
	t.Parallel()

	_, err := NewStore("").LoadViewed()

	assert.ErrorIs(t, err, ErrNoStateDir)
}

func TestNewRangeNamesTheLinesAHunkLeavesBehind(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "a.go:3-6", NewRange("a.go", 3, 4))
	assert.Equal(t, "a.go:3", NewRange("a.go", 3, 1))
	assert.Equal(t, "a.go:3 (deleted)", NewRange("a.go", 3, 0))
}
