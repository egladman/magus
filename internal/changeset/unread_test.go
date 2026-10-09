package changeset

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestUnreadLoadViewedWithNoStateDirectoryIsTheSentinel(t *testing.T) {
	t.Parallel()

	_, err := NewStore("").LoadViewed()

	assert.ErrorIs(t, err, ErrNoStateDir)
}

const unreadTestPatch = "diff --git a/core.go b/core.go\n" +
	"@@ -3 +3 @@\n" +
	"+func F() {}\n" +
	"@@ -9 +9,2 @@\n" +
	"+func G() { F() }\n" +
	"+func H() {}\n" +
	"diff --git a/other.go b/other.go\n" +
	"@@ -20 +20 @@\n" +
	"+var _ = F\n" +
	"diff --git a/gen/out.json b/gen/out.json\n" +
	"@@ -1 +1 @@\n" +
	"-{}\n" +
	"+{\"a\":1}\n"

func TestUnreadReportListsHunksNoMarkCovers(t *testing.T) {
	t.Parallel()

	parsed := ParseHunks(unreadTestPatch)
	read := parsed[0].Hunks[0].Digest

	rep := BuildUnreadReport("the range a...b", unreadTestPatch, []string{read, "digest-of-a-hunk-elsewhere"}, nil)

	assert.Equal(t, UnreadKnown, rep.State)
	assert.Equal(t, 4, rep.Hunks)
	require.Len(t, rep.Unread, 3)
	var keys []string
	for _, r := range rep.Unread {
		keys = append(keys, r.Key())
	}
	assert.Equal(t, []string{"core.go#1", "other.go#0", "gen/out.json#0"}, keys)

	var buf strings.Builder
	require.NoError(t, WriteUnread(&buf, rep, unreadTestPatch, "magus diff --rev a...b"))
	assert.Equal(t, `3 of 4 hunks in the range a...b are not marked read
  core.go:9-10
  other.go:20
  gen/out.json:1
mark them read in the viewer: magus diff --rev a...b
`, buf.String())
}

func TestUnreadReportSaysUnknownWhenTheMarksCannotBeRead(t *testing.T) {
	t.Parallel()

	rep := BuildUnreadReport("the working tree", unreadTestPatch, nil, errors.New("read marks: permission denied"))

	assert.Equal(t, UnreadUnknown, rep.State)
	assert.Empty(t, rep.Unread, "an unreadable store says nothing about what is unread")
	assert.Equal(t, 4, rep.Hunks)
	var buf strings.Builder
	require.NoError(t, WriteUnread(&buf, rep, unreadTestPatch, ""))
	assert.Equal(t, `read state unknown for the working tree: the read marks could not be read (read marks: permission denied)
4 hunks in the range; none is called unread
`, buf.String())
}

func TestUnreadReportOfAFullyReadChangesetSaysSo(t *testing.T) {
	t.Parallel()

	var all []string
	for _, f := range ParseHunks(unreadTestPatch) {
		for _, h := range f.Hunks {
			all = append(all, h.Digest)
		}
	}

	rep := BuildUnreadReport("the working tree", unreadTestPatch, all, nil)

	assert.Empty(t, rep.Unread)
	var buf strings.Builder
	require.NoError(t, WriteUnread(&buf, rep, unreadTestPatch, ""))
	assert.Equal(t, "every hunk of the working tree is marked read (4 hunks)\n", buf.String())
}

func TestNewRangeNamesTheLinesAHunkLeavesBehind(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "a.go:3-6", NewRange("a.go", 3, 4))
	assert.Equal(t, "a.go:3", NewRange("a.go", 3, 1))
	assert.Equal(t, "a.go:3 (deleted)", NewRange("a.go", 3, 0))
}
