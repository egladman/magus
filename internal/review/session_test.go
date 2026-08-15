package review

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
	a := s.Attach("/w", "working", types.Review{Base: "working"})
	b := s.Attach("/w", "working", types.Review{Base: "working"})
	assert.Equal(t, a.ID, b.ID,
		"two attaches to one workspace must share a session, or a console tab and an agent "+
			"mark progress on different objects while both believe they are paired")
}

// Attaching again is how a client delivers a recomputed changeset. It must not discard the
// conversation happening over it.
func TestAttachUpdatesTheReviewWithoutClearingTheConversation(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Review{Files: []types.ReviewFile{{Path: "a.go"}}})
	s.AddComment("/w", types.ReviewComment{Path: "a.go", Body: "look here"}, types.ReviewAuthorAgent)
	s.MarkViewed("/w", "digest1", true)

	got := s.Attach("/w", "working", types.Review{Files: []types.ReviewFile{{Path: "b.go"}}})
	require.Len(t, got.Review.Files, 1)
	assert.Equal(t, "b.go", got.Review.Files[0].Path, "the changeset must refresh")
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

func TestHunkDigestChangesWhenTheBodyDoes(t *testing.T) {
	assert.NotEqual(t,
		HunkDigest("a.go", []string{"-old", "+new"}),
		HunkDigest("a.go", []string{"-old", "+newer"}))
}

func TestMarkViewedTogglesAndDeduplicates(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Review{})
	s.MarkViewed("/w", "d1", true)
	got := s.MarkViewed("/w", "d1", true)
	assert.Equal(t, []string{"d1"}, got.Viewed, "marking twice must not duplicate")
	got = s.MarkViewed("/w", "d1", false)
	assert.Empty(t, got.Viewed)
}

// Progress is the one piece whose value is surviving an interruption.
func TestViewedStatePersistsAcrossStores(t *testing.T) {
	dir := t.TempDir()
	s1 := NewStore(dir)
	s1.Attach("/w", "working", types.Review{})
	s1.MarkViewed("/w", "d1", true)
	s1.MarkViewed("/w", "d2", true)

	s2 := NewStore(dir)
	got := s2.Attach("/w", "working", types.Review{})
	assert.ElementsMatch(t, []string{"d1", "d2"}, got.Viewed,
		"a reader who is interrupted must not lose what they had already read")
}

func TestCorruptViewedFileIsIgnoredRatherThanFatal(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, mkdirAllWrite(filepath.Join(dir, "review", "viewed.json"), "{not json"))
	s := NewStore(dir)
	got := s.Attach("/w", "working", types.Review{})
	assert.Empty(t, got.Viewed, "a corrupt progress file must not stop a review from opening")
}

// The daemon stamps the author from the transport. A body that claims otherwise is ignored,
// which is what stops an agent posting as the person.
func TestCommentAuthorIsStampedNotClaimed(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Review{})
	got := s.AddComment("/w",
		types.ReviewComment{Path: "a.go", Body: "hi", Author: types.ReviewAuthorHuman},
		types.ReviewAuthorAgent)
	require.Len(t, got.Comments, 1)
	assert.Equal(t, types.ReviewAuthorAgent, got.Comments[0].Author,
		"a claimed author in the payload must never win over the transport's")
}

// The load-bearing rule of the whole paired design.
func TestSuggestDoesNotMoveTheCursor(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Review{})
	s.SetCursor("/w", types.ReviewCursor{Path: "where/i/was.go", Hunk: 2})
	got := s.Suggest("/w", types.ReviewSuggestion{Path: "elsewhere.go", Hunk: 0, Reason: "3 callers"})

	assert.Equal(t, "where/i/was.go", got.Cursor.Path,
		"an agent suggesting must never move the reader's viewport")
	assert.Equal(t, 2, got.Cursor.Hunk)
	require.Len(t, got.Suggestions, 1)
	assert.False(t, got.Suggestions[0].Accepted)
}

func TestAcceptingASuggestionIsWhatMovesTheCursor(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Review{})
	s.SetCursor("/w", types.ReviewCursor{Path: "a.go", Hunk: 0})
	s.Suggest("/w", types.ReviewSuggestion{Path: "b.go", Hunk: 3, Reason: "look"})

	got := s.AnswerSuggestion("/w", "s1", true)
	assert.Equal(t, "b.go", got.Cursor.Path, "the human accepting is the only path to the viewport")
	assert.Equal(t, 3, got.Cursor.Hunk)
	assert.True(t, got.Suggestions[0].Accepted)
}

// Declining is recorded, not discarded, so an agent can stop repeating itself.
func TestDecliningIsRecordedAndLeavesTheCursorAlone(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Review{})
	s.SetCursor("/w", types.ReviewCursor{Path: "a.go", Hunk: 0})
	s.Suggest("/w", types.ReviewSuggestion{Path: "b.go", Hunk: 3, Reason: "look"})

	got := s.AnswerSuggestion("/w", "s1", false)
	assert.Equal(t, "a.go", got.Cursor.Path)
	assert.True(t, got.Suggestions[0].Declined)
	assert.False(t, got.Suggestions[0].Accepted)
}

func TestResolveComment(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Review{})
	s.AddComment("/w", types.ReviewComment{Path: "a.go", Body: "x"}, types.ReviewAuthorAgent)
	got := s.ResolveComment("/w", "c1", true)
	assert.True(t, got.Comments[0].Resolved)
}

// A caller must be able to tell "no session" from "an empty session".
func TestMutatingAnUnattachedRootYieldsNil(t *testing.T) {
	s := NewStore("")
	assert.Nil(t, s.Get("/nope"))
	assert.Nil(t, s.SetCursor("/nope", types.ReviewCursor{}))
	assert.Nil(t, s.MarkViewed("/nope", "d", true))
	assert.Nil(t, s.Suggest("/nope", types.ReviewSuggestion{}))
}

// A handed-out session must not be a window into live state.
func TestReturnedSessionsAreCopies(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Review{})
	got := s.MarkViewed("/w", "d1", true)
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
