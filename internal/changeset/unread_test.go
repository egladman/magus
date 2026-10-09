package changeset

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	require.NoError(t, os.WriteFile(filepath.Join(dir, "review", "viewed.json"), []byte(`["a0","b0"]`), 0o644))

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

	corrupt := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(corrupt, "review"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(corrupt, "review", "viewed.json"), []byte(`{not json`), 0o644))
	unreadable := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(unreadable, "review", "viewed.json"), 0o755), "a directory where the file belongs")

	tests := []struct {
		name  string
		store *Store
	}{
		{"undecodable", NewStore(corrupt)},
		{"unreadable", NewStore(unreadable)},
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
