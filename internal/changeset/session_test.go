package changeset

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
// same way the terminal viewer does: a mark is just a digest, and only the store knows which
// file it belongs to and whether that file is now complete.
func TestMarkViewedReportsTheFileItFinished(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Diff{}, "")
	s.TrackHunks("/w", []FileHunks{
		{Path: "a.go", Hunks: []Hunk{{Digest: "a1"}, {Digest: "a2"}}},
		{Path: "b.go", Hunks: []Hunk{{Digest: "b1"}}},
	}, func(p string) string { return "content-of-" + p })

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
	digest := func(p string) string { return "content-of-" + p }
	s.TrackHunks("/w", []FileHunks{{Path: "old.go", Hunks: []Hunk{{Digest: "x1"}}}}, digest)
	s.TrackHunks("/w", []FileHunks{{Path: "new.go", Hunks: []Hunk{{Digest: "y1"}}}}, digest)

	_, finished := s.MarkViewed("/w", "x1", true)
	assert.Empty(t, finished, "a digest from the superseded changeset finishes nothing")

	_, finished = s.MarkViewed("/w", "y1", true)
	assert.Equal(t, "new.go", finished)
}

// A file whose hunks are byte-identical shares ONE digest, so counting occurrences would set
// a total the marked set can never reach and that file could never be finished.
func TestTrackHunksCountsDistinctDigests(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Diff{}, "")
	s.TrackHunks("/w", []FileHunks{
		{Path: "a.go", Hunks: []Hunk{{Digest: "same"}, {Digest: "same"}}},
	}, func(p string) string { return "content" })

	_, finished := s.MarkViewed("/w", "same", true)
	assert.Equal(t, "a.go", finished, "one distinct digest is the whole file")
}

// A receipt must attest to the bytes the reader SAW. The content is fingerprinted when the
// changeset is tracked, so an agent editing the file mid-review cannot get its own edit
// stamped as read - the next report calls it stale instead.
func TestContentAtIsTakenWhenTracked(t *testing.T) {
	s := NewStore("")
	s.Attach("/w", "working", types.Diff{}, "")
	s.TrackHunks("/w", []FileHunks{{Path: "a.go", Hunks: []Hunk{{Digest: "h1"}}}},
		func(p string) string { return "as-the-reader-saw-it" })

	assert.Equal(t, "as-the-reader-saw-it", s.ContentAt("/w", "a.go"))
	assert.Empty(t, s.ContentAt("/w", "never-tracked.go"))
}

// A self-review remark is a sentence addressed to a teammate that has not been sent yet.
// Losing eight of them to a daemon restart is losing the work, which is what happened while
// comments sat with the coordination state.
func TestHumanDraftsSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	const root = "/ws"

	first := NewStore(dir)
	first.Attach(root, "main", types.Diff{}, "asof1")
	first.AddComment(root, types.DiffComment{
		Path: "a.go", Hunk: 0, Body: "this is the bit reviewers always ask about",
		Anchor: types.CommentAnchor{Digest: "d1", Quote: "\treturn nil"},
	}, types.DiffAuthorHuman)
	first.AddComment(root, types.DiffComment{
		Path: "b.go", Hunk: 2, Body: "agent noise from the pairing session",
	}, types.DiffAuthorAgent)

	// A new Store over the same state directory IS the restart.
	second := NewStore(dir)
	sess := second.Attach(root, "main", types.Diff{}, "asof2")
	require.Len(t, sess.Comments, 1, "exactly the human draft should come back")
	assert.Equal(t, "a.go", sess.Comments[0].Path)
	assert.Equal(t, "this is the bit reviewers always ask about", sess.Comments[0].Body)
	// The anchor rides along, so the surface can still say the code under it moved.
	assert.Equal(t, types.CommentAnchor{Digest: "d1", Quote: "\treturn nil"}, sess.Comments[0].Anchor)
	assert.False(t, sess.Comments[0].Published)
}

// A published comment lives on the host, where a teammate may already have replied. Restoring
// it as a draft would offer to send it a second time.
func TestAPublishedCommentIsNotRestoredAsADraft(t *testing.T) {
	dir := t.TempDir()
	const root = "/ws"

	first := NewStore(dir)
	first.Attach(root, "main", types.Diff{}, "asof1")
	first.AddComment(root, types.DiffComment{Path: "a.go", Body: "sent"}, types.DiffAuthorHuman)
	first.AddComment(root, types.DiffComment{Path: "b.go", Body: "still mine"}, types.DiffAuthorHuman)
	first.MarkPublished(root, "c1")

	second := NewStore(dir)
	sess := second.Attach(root, "main", types.Diff{}, "asof2")
	bodies := make([]string, 0, len(sess.Comments))
	for _, c := range sess.Comments {
		bodies = append(bodies, c.Body)
	}
	assert.Equal(t, []string{"still mine"}, bodies)
}

// Persistence is off with no state directory, which is what a test and a workspace-less daemon
// get. It must degrade to memory rather than to a panic or a write into the working directory.
func TestDraftsAreMemoryOnlyWithoutAStateDir(t *testing.T) {
	s := NewStore("")
	s.Attach("/ws", "main", types.Diff{}, "a")
	s.AddComment("/ws", types.DiffComment{Path: "a.go", Body: "x"}, types.DiffAuthorHuman)
	assert.Len(t, NewStore("").Attach("/ws", "main", types.Diff{}, "a").Comments, 0)
}

// Ids come from the highest existing number, not the count, because a restored set has GAPS:
// publishing a draft takes it out of the file, so a count-based id reuses a number a live
// comment already answers to. Resolving one would then resolve the other.
func TestACommentIDIsNeverReusedAfterAGap(t *testing.T) {
	dir := t.TempDir()
	const root = "/ws"

	first := NewStore(dir)
	first.Attach(root, "main", types.Diff{}, "a")
	for _, body := range []string{"one", "two", "three"} {
		first.AddComment(root, types.DiffComment{Path: "a.go", Body: body}, types.DiffAuthorHuman)
	}
	// The middle one leaves the file, which is what makes the set sparse.
	first.MarkPublished(root, "c2")

	second := NewStore(dir)
	second.Attach(root, "main", types.Diff{}, "b")
	sess := second.AddComment(root, types.DiffComment{Path: "b.go", Body: "four"}, types.DiffAuthorHuman)

	seen := map[string]string{}
	for _, c := range sess.Comments {
		_, dup := seen[c.ID]
		assert.False(t, dup, "id %s is held by both %q and %q", c.ID, seen[c.ID], c.Body)
		seen[c.ID] = c.Body
	}
	assert.Equal(t, "c4", sess.Comments[len(sess.Comments)-1].ID, "the next id clears the gap")
}

// The watermark is what decides whether a remark is NEW, so it has to be the reader's and it has
// to survive being asked twice. Ids, never a count: a deleted remark plus a new one nets zero.
func TestMarkThreadsSeenIsAdditiveAndDecidesWhatIsUnseen(t *testing.T) {
	store := NewStore(t.TempDir())
	root := t.TempDir()
	require.NotNil(t, store.Attach(root, "", types.Diff{}, ""))

	threads := []types.ReviewThread{{ID: "t1"}, {ID: "t2"}}
	require.Equal(t, []string{"t1", "t2"}, store.Get(root).UnseenThreads(threads))

	store.MarkThreadsSeen(root, []string{"t1"})
	assert.Equal(t, []string{"t2"}, store.Get(root).UnseenThreads(threads))

	// Idempotent: seeing the same conversation again neither re-marks nor grows the set.
	store.MarkThreadsSeen(root, []string{"t1"})
	assert.Equal(t, []string{"t1"}, store.Get(root).SeenThreads)

	// A thread deleted on the host and another added nets zero on a count and must not hide the
	// new one.
	store.MarkThreadsSeen(root, []string{"t2"})
	assert.Equal(t, []string{"t3"}, store.Get(root).UnseenThreads([]types.ReviewThread{{ID: "t1"}, {ID: "t3"}}))
}

// A thread with no id cannot be tracked, and calling it new forever would mark the conversation
// unread on every render.
func TestUnseenThreadsIgnoresAnUnidentifiedThread(t *testing.T) {
	var sess types.DiffSession
	assert.Empty(t, sess.UnseenThreads([]types.ReviewThread{{ID: ""}}))
}

// TestTrackHunksRelocatesADraftWhoseCodeMoved is the end-to-end shape of the anchor: a remark
// written against a line, the file edited above it, and the remark still on the right code.
//
// Before this the anchor field was written by nobody and read by nothing, while the store's own
// comment claimed it "carries what is needed to say the code under it moved". It did not.
func TestTrackHunksRelocatesADraftWhoseCodeMoved(t *testing.T) {
	root := t.TempDir()
	s := NewStore("")
	s.Attach(root, "main", types.Diff{}, "asof1")

	before := []FileHunks{{Path: "a.go", Hunks: []Hunk{
		{Lines: []string{" func F() {", " \tx := 1", " \treturn x", " }"}, Digest: "d1", NewStart: 10, NewCount: 4},
	}}}
	s.TrackHunks(root, before, nil)

	sess := s.AddComment(root, types.DiffComment{
		Path: "a.go", Hunk: 0, Line: 12, Body: "why is this not a pointer",
		Anchor: s.AnchorFor(root, "a.go", 12),
	}, types.DiffAuthorHuman)
	require.Len(t, sess.Comments, 1)
	require.Equal(t, "\treturn x", sess.Comments[0].Anchor.Quote, "the server captured what the reader saw")

	// The same code, twenty lines further down.
	after := []FileHunks{{Path: "a.go", Hunks: []Hunk{
		{Lines: []string{" func F() {", " \tx := 1", " \treturn x", " }"}, Digest: "d2", NewStart: 30, NewCount: 4},
	}}}
	s.TrackHunks(root, after, nil)

	got := s.Get(root)
	require.Len(t, got.Comments, 1)
	assert.Equal(t, 32, got.Comments[0].Line, "the remark follows its code")
	assert.Equal(t, types.AnchorMoved, got.Comments[0].Rung, "and says it moved rather than claiming it never did")
}

// A published remark is not re-placed: a colleague may already have replied to it where it sits,
// and moving our copy would make the two surfaces disagree about what was said where.
func TestTrackHunksLeavesAPublishedRemarkWhereItWasSent(t *testing.T) {
	root := t.TempDir()
	s := NewStore("")
	s.Attach(root, "main", types.Diff{}, "asof1")
	tracked := []FileHunks{{Path: "a.go", Hunks: []Hunk{
		{Lines: []string{" a", " b"}, Digest: "d1", NewStart: 1, NewCount: 2},
	}}}
	s.TrackHunks(root, tracked, nil)
	s.AddComment(root, types.DiffComment{
		Path: "a.go", Line: 1, Body: "sent already", Published: true,
		Anchor: types.CommentAnchor{Quote: "a"},
	}, types.DiffAuthorHuman)

	s.TrackHunks(root, []FileHunks{{Path: "a.go", Hunks: []Hunk{
		{Lines: []string{" z", " a", " b"}, Digest: "d2", NewStart: 1, NewCount: 3},
	}}}, nil)

	got := s.Get(root)
	require.Len(t, got.Comments, 1)
	assert.Equal(t, 1, got.Comments[0].Line, "a published remark stays where the colleague saw it")
	assert.Equal(t, types.AnchorUnknown, got.Comments[0].Rung)
}
