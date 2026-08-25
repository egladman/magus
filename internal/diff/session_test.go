package diff

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestAttachReturnsTheSameSessionForOneRoot(t *testing.T) {
	s := NewStore("")
	a := s.Attach("/w", "working", types.Diff{Base: "working"}, "")
	b := s.Attach("/w", "working", types.Diff{Base: "working"}, "")
	assert.Equal(t, a.ID, b.ID,
		"two attaches to one workspace must share a session, or a console tab and an agent "+
			"mark progress on different objects while both believe they are paired")
}

// Attaching again is how a client delivers a recomputed changeset. It must not discard the
// conversation happening over it.
func TestAttachUpdatesTheReviewWithoutClearingTheConversation(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Diff{Files: []types.DiffFile{{Path: "a.go"}}}, "")
	s.AddComment("/w", types.DiffComment{Path: "a.go", Body: "look here"}, types.DiffAuthorAgent)
	s.MarkViewed("/w", "digest1", true)

	got := s.Attach("/w", "working", types.Diff{Files: []types.DiffFile{{Path: "b.go"}}}, "")
	require.Len(t, got.Diff.Files, 1)
	assert.Equal(t, "b.go", got.Diff.Files[0].Path, "the changeset must refresh")
	assert.Len(t, got.Comments, 1, "comments must survive a recompute")
	assert.Equal(t, []string{"digest1"}, got.Viewed, "progress must survive a recompute")
}

// The whole point of digesting content instead of naming a line: a rebase that does not touch
// the hunk leaves the mark standing.
func TestHunkDigestIsStableAcrossMovedLineNumbers(t *testing.T) {
	body := []string{" ctx", "-old", "+new"}
	assert.Equal(t, HunkDigest("a.go", body), HunkDigest("a.go", body))
}

func TestHunkDigestSeparatesIdenticalHunksInDifferentFiles(t *testing.T) {
	body := []string{" ctx", "-old", "+new"}
	assert.NotEqual(t, HunkDigest("a.go", body), HunkDigest("b.go", body),
		"the same three lines changed in two files are two different marks")
}

// A GOLDEN vector, and the console asserts the identical one in session.test.ts.
//
// The two implementations must agree byte for byte or the feature silently half-works: a hunk
// the person marked read in the browser would still look unread to an agent reading the same
// session, and neither side would report an error. Two tests over one literal is what turns
// that into a build failure instead.
func TestHunkDigestMatchesTheConsoleGoldenVector(t *testing.T) {
	assert.Equal(t, "9a0125a4f7864894",
		HunkDigest("a.go", []string{" ctx", "-old", "+new"}),
		"digest drift from console/src/console/diff/session.ts would desynchronize viewed state")
}

func TestHunkDigestChangesWhenTheBodyDoes(t *testing.T) {
	assert.NotEqual(t,
		HunkDigest("a.go", []string{"-old", "+new"}),
		HunkDigest("a.go", []string{"-old", "+newer"}))
}

func TestMarkViewedTogglesAndDeduplicates(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Diff{}, "")
	s.MarkViewed("/w", "d1", true)
	got, _ := s.MarkViewed("/w", "d1", true)
	assert.Equal(t, []string{"d1"}, got.Viewed, "marking twice must not duplicate")
	got, _ = s.MarkViewed("/w", "d1", false)
	assert.Empty(t, got.Viewed)
}

// Progress is the one piece whose value is surviving an interruption.
func TestViewedStatePersistsAcrossStores(t *testing.T) {
	dir := t.TempDir()
	s1 := NewStore(dir)
	s1.Attach("/w", "working", types.Diff{}, "")
	s1.MarkViewed("/w", "d1", true)
	s1.MarkViewed("/w", "d2", true)

	s2 := NewStore(dir)
	got := s2.Attach("/w", "working", types.Diff{}, "")
	assert.ElementsMatch(t, []string{"d1", "d2"}, got.Viewed,
		"a reader who is interrupted must not lose what they had already read")
}

func TestCorruptViewedFileIsIgnoredRatherThanFatal(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, mkdirAllWrite(filepath.Join(dir, "review", "viewed.json"), "{not json"))
	s := NewStore(dir)
	got := s.Attach("/w", "working", types.Diff{}, "")
	assert.Empty(t, got.Viewed, "a corrupt progress file must not stop a review from opening")
}

// The daemon stamps the author from the transport. A body that claims otherwise is ignored,
// which is what stops an agent posting as the person.
func TestCommentAuthorIsStampedNotClaimed(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Diff{}, "")
	got := s.AddComment("/w",
		types.DiffComment{Path: "a.go", Body: "hi", Author: types.DiffAuthorHuman},
		types.DiffAuthorAgent)
	require.Len(t, got.Comments, 1)
	assert.Equal(t, types.DiffAuthorAgent, got.Comments[0].Author,
		"a claimed author in the payload must never win over the transport's")
}

// The load-bearing rule of the whole paired design.
func TestSuggestDoesNotMoveTheCursor(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Diff{}, "")
	s.SetCursor("/w", types.DiffCursor{Path: "where/i/was.go", Hunk: 2})
	got := s.Suggest("/w", types.DiffSuggestion{Path: "elsewhere.go", Hunk: 0, Reason: "3 callers"})

	assert.Equal(t, "where/i/was.go", got.Cursor.Path,
		"an agent suggesting must never move the reader's viewport")
	assert.Equal(t, 2, got.Cursor.Hunk)
	require.Len(t, got.Suggestions, 1)
	assert.False(t, got.Suggestions[0].Accepted)
}

func TestAcceptingASuggestionIsWhatMovesTheCursor(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Diff{}, "")
	s.SetCursor("/w", types.DiffCursor{Path: "a.go", Hunk: 0})
	s.Suggest("/w", types.DiffSuggestion{Path: "b.go", Hunk: 3, Reason: "look"})

	got := s.AnswerSuggestion("/w", "s1", true)
	assert.Equal(t, "b.go", got.Cursor.Path, "the human accepting is the only path to the viewport")
	assert.Equal(t, 3, got.Cursor.Hunk)
	assert.True(t, got.Suggestions[0].Accepted)
}

// Declining is recorded, not discarded, so an agent can stop repeating itself.
func TestDecliningIsRecordedAndLeavesTheCursorAlone(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Diff{}, "")
	s.SetCursor("/w", types.DiffCursor{Path: "a.go", Hunk: 0})
	s.Suggest("/w", types.DiffSuggestion{Path: "b.go", Hunk: 3, Reason: "look"})

	got := s.AnswerSuggestion("/w", "s1", false)
	assert.Equal(t, "a.go", got.Cursor.Path)
	assert.True(t, got.Suggestions[0].Declined)
	assert.False(t, got.Suggestions[0].Accepted)
}

func TestResolveComment(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Diff{}, "")
	s.AddComment("/w", types.DiffComment{Path: "a.go", Body: "x"}, types.DiffAuthorAgent)
	got := s.ResolveComment("/w", "c1", true)
	assert.True(t, got.Comments[0].Resolved)
}

// A caller must be able to tell "no session" from "an empty session".
func TestMutatingAnUnattachedRootYieldsNil(t *testing.T) {
	s := NewStore("")
	assert.Nil(t, s.Get("/nope"))
	assert.Nil(t, s.SetCursor("/nope", types.DiffCursor{}))
	unattached, _ := s.MarkViewed("/nope", "d", true)
	assert.Nil(t, unattached)
	assert.Nil(t, s.Suggest("/nope", types.DiffSuggestion{}))
}

// A handed-out session must not be a window into live state.
func TestReturnedSessionsAreCopies(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Diff{}, "")
	got, _ := s.MarkViewed("/w", "d1", true)
	got.Viewed[0] = "tampered"
	fresh := s.Get("/w")
	assert.Equal(t, []string{"d1"}, fresh.Viewed)
}

func mkdirAllWrite(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

// TestMarkViewedReportsTheFileItFinished is what lets the console earn a read receipt the
// same way `magus diff --tui` does: a mark is just a digest, and only the store knows which
// file it belongs to and whether that file is now complete.
func TestMarkViewedReportsTheFileItFinished(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Diff{}, "")
	s.TrackHunks("/w", []FileHunks{
		{Path: "a.go", Hunks: []Hunk{{Digest: "a1"}, {Digest: "a2"}}},
		{Path: "b.go", Hunks: []Hunk{{Digest: "b1"}}},
	})

	// A file with more hunks left is not finished.
	_, finished := s.MarkViewed("/w", "a1", true)
	assert.Empty(t, finished, "a.go still has an unread hunk")

	_, finished = s.MarkViewed("/w", "a2", true)
	assert.Equal(t, "a.go", finished, "the last hunk finishes the file")

	// A one-hunk file finishes on its only mark.
	_, finished = s.MarkViewed("/w", "b1", true)
	assert.Equal(t, "b.go", finished)

	// Unmarking finishes nothing: it is the reader taking a claim BACK.
	_, finished = s.MarkViewed("/w", "b1", false)
	assert.Empty(t, finished)
}

// A digest the store cannot place completes nothing. It means the patch moved under the
// session, and guessing there would mint a receipt for a file the reader never finished.
func TestMarkViewedFinishesNothingForAnUntrackedDigest(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Diff{}, "")

	_, finished := s.MarkViewed("/w", "stranger", true)
	assert.Empty(t, finished, "no hunk mapping was tracked, so nothing can be complete")
}

// Re-attaching replaces the mapping rather than merging into it: the changeset was just
// recomputed, so a digest from the previous one names nothing a reader can still mark.
func TestTrackHunksReplacesThePreviousMapping(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Diff{}, "")
	s.TrackHunks("/w", []FileHunks{{Path: "old.go", Hunks: []Hunk{{Digest: "x1"}}}})
	s.TrackHunks("/w", []FileHunks{{Path: "new.go", Hunks: []Hunk{{Digest: "y1"}}}})

	_, finished := s.MarkViewed("/w", "x1", true)
	assert.Empty(t, finished, "a digest from the superseded changeset finishes nothing")

	_, finished = s.MarkViewed("/w", "y1", true)
	assert.Equal(t, "new.go", finished)
}
