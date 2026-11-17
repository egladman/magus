package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types/diff"
)

// A capture holds BOTH halves of the conversation. A transcript of only your own remarks is
// not a transcript of the review: what was decided is nearly always in what somebody said
// back, and that half lives on the forge rather than in the session.
func TestCaptureKeepsWhatColleaguesSaid(t *testing.T) {
	sess := &diff.Review{
		ID:   "rev-1",
		AsOf: "patch-1",
		Comments: []diff.Comment{
			{Path: "a.go", Hunk: 1, Author: diff.AuthorUnattributed, Body: "mine"},
		},
	}
	threads := []diff.ReviewComment{
		{ID: "t1", Path: "a.go", Line: 12, Author: "priya", Body: "theirs"},
		{ID: "t2", Path: "z.go", Line: 3, Author: "marcus", Body: "elsewhere"},
	}

	c := CaptureFromSession(sess, threads, "review", nil)
	n, err := c.Note("cap")
	require.NoError(t, err)

	assert.Contains(t, n.Body, "theirs")
	assert.Contains(t, n.Body, "priya")
	// The colleague's remark precedes the reply it provoked. A transcript that opened with the
	// answer reads backwards.
	assert.Less(t, strings.Index(n.Body, "theirs"), strings.Index(n.Body, "mine"))
	// A thread on a file the session never commented on still anchors that file, which is what
	// makes the capture turn up when somebody later asks what is known about z.go.
	assert.Contains(t, n.Body, "elsewhere")
	assert.Len(t, n.Anchors, 2)
}

// A review with no forge behind it is the ordinary case, and the local conversation is worth
// keeping on its own.
func TestCaptureWithNoThreadsIsStillATranscript(t *testing.T) {
	sess := &diff.Review{
		ID: "rev-1",
		Comments: []diff.Comment{
			{Path: "a.go", Author: diff.AuthorUnattributed, Body: "mine"},
		},
	}
	n, err := CaptureFromSession(sess, nil, "review", nil).Note("cap")
	require.NoError(t, err)
	assert.Contains(t, n.Body, "mine")
}

// The server accelerates and never gates. A person who wrote remarks in `magus diff` with no
// server running has them on disk, and capture refusing because no server is up would lose the
// one artifact nothing can recreate.
func TestCaptureReadsTheStoreWhenNoServerIsRunning(t *testing.T) {
	cache := t.TempDir()
	root := filepath.Join(cache, "ws")
	written := changeset.NewStore(cache)
	written.Attach(root, "main", diff.Diff{Base: "main"}, "asof")
	written.AddComment(root, diff.Comment{Path: "a.go", Line: 4, Body: "mine"}, diff.AuthorUnattributed)

	sess, err := storedDiffReview(cache, "diff --git a/a.go b/a.go\n")
	require.NoError(t, err)
	require.Len(t, sess.Comments, 1, "the drafts the store persisted are the transcript")
	assert.Equal(t, "mine", sess.Comments[0].Body)
	// Digested from the patch exactly as attachDiffReview digests it, so a capture taken with
	// no server names the note a server-attached one would have named.
	assert.Equal(t, changeset.PatchDigest("diff --git a/a.go b/a.go\n"), sess.AsOf)
	assert.Equal(t, "review-"+sess.AsOf[:12], CaptureName(sess))
}

// An unreadable patch must not name every such capture the same note: the second one would
// collide with the first and be refused, which is the transcript lost.
func TestAStoredSessionWithNoPatchHasNoSnapshotId(t *testing.T) {
	sess, err := storedDiffReview(t.TempDir(), "")
	require.NoError(t, err)
	assert.Empty(t, sess.AsOf)
	assert.Equal(t, "review-thread", CaptureName(sess))
}

// A colleague's remark is a fact about the review, not about whether a background process is
// up. Without a server the forge is asked directly rather than the reader being shown a review
// with nobody else in it.
func TestReviewCommentsReachTheForgeWithNoServer(t *testing.T) {
	withCannedThreads(t, []diff.ReviewComment{{ID: "t1", Path: "a.go", Line: 11, Author: "priya", Body: "theirs"}})
	cache := t.TempDir()

	threads, reason := localReviewComments(t.Context(), diff.ReviewOrigin{Branch: "feat/x"}, cache)
	require.Len(t, threads, 1)
	assert.Equal(t, "theirs", threads[0].Body)
	assert.Empty(t, reason)
	// Nothing has been on screen here, so the whole conversation is new: the mark the server
	// would have applied, taken from the watermark the store persists rather than from a session.
	assert.True(t, threads[0].New)
}

// The watermark outlives the server that normally reads it, so a thread already seen does not
// come back marked new the moment the server is stopped.
func TestASeenThreadIsNotNewWithoutAServer(t *testing.T) {
	withCannedThreads(t, []diff.ReviewComment{{ID: "t1", Path: "a.go", Line: 11, Author: "priya", Body: "theirs"}})
	cache := t.TempDir()
	root := filepath.Join(cache, "ws")
	sessions := changeset.NewStore(cache)
	sessions.Attach(root, "main", diff.Diff{Base: "main"}, "asof")
	sessions.MarkCommentsSeen(root, []string{"t1"})

	threads, _ := localReviewComments(t.Context(), diff.ReviewOrigin{Branch: "feat/x"}, cache)
	require.Len(t, threads, 1)
	assert.False(t, threads[0].New)
}

// Both read paths compute the mark, so a capture can say which half of the conversation the
// reader had not weighed yet. It is told to the person TAKING the capture and never written
// into the note: New belongs to this reader's history with the review, and in a transcript a
// colleague reads next year it would describe somebody else's morning.
func TestCaptureSaysWhatWasNewToThisReader(t *testing.T) {
	threads := []diff.ReviewComment{
		{ID: "t1", Author: "priya", Body: "you weighed this already"},
		{ID: "t2", Author: "marcus", Body: "arrived since you looked", New: true},
	}

	assert.Contains(t, NewRemarkLine(threads), "1 remark on the review had not been in front of you before")
	assert.Empty(t, NewRemarkLine(threads[:1]), "a conversation the reader has already had says nothing")
	assert.Contains(t, NewRemarkLine(append(threads, diff.ReviewComment{ID: "t3", New: true})), "2 remarks")
}

// A malformed remark fails the read loudly: the threads decode strictly, so the caller says
// what it could not read rather than handing over a conversation with a remark missing.
func TestALocalReadReportsWhatItCouldNotDecode(t *testing.T) {
	name := register.Spell(t, "malformed-notes-review", spells.WithFuncSource(`import "magus/spell";
export fun find_review(target: Target, req: FindReviewRequest) > ReviewTarget {
    return ReviewTarget{ id = "482", repo = "acme/acme" };
}
export fun review_threads(target: Target, req: ReviewRef) > any {
    return [{"id": "t1", "path": "a.go", "line": 11, "author": "priya", "body": "theirs"}, "not a thread at all"];
}`))
	prev := bindings.ReviewProvider()
	bindings.SetReviewProvider(name)
	t.Cleanup(func() { bindings.SetReviewProvider(prev) })

	threads, reason := localReviewComments(t.Context(), diff.ReviewOrigin{Branch: "feat/x"}, t.TempDir())
	assert.Empty(t, threads)
	assert.NotEmpty(t, reason, "a transcript silently missing a remark is worse than no transcript")
}

// withCannedThreads wires a review provider for this test, so the server-free read path
// can be exercised against threads instead of only against an unwired workspace.
func withCannedThreads(t *testing.T, threads []diff.ReviewComment) {
	t.Helper()
	name := register.ReviewSpell(t, diff.ReviewTarget{ID: "482", Repo: "acme/acme"}, threads)
	prev := bindings.ReviewProvider()
	bindings.SetReviewProvider(name)
	t.Cleanup(func() { bindings.SetReviewProvider(prev) })
}

// unreadableNoteStores declares a shared and a private store and plants a name in the
// shared one that EXISTS and cannot be read: a directory where the .md should be, so
// the failure is EISDIR rather than a permission bit root would ignore.
func unreadableNoteStores(t *testing.T, name string) (root string, dirs NoteDirs) {
	t.Helper()
	root = t.TempDir()
	privateDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "notes", name+".md"), 0o755))
	return root, NoteDirs{Shared: "notes", Private: privateDir}
}

// FindNote reported an unreadable note as a bare fmt.Errorf, which no caller could
// match. `notes edit` switches on the error, and with nothing to match on this fell
// past every case: the name read as free, and the command scaffolded a SECOND copy in
// the other store on top of the broken one.
func TestFindNoteMarksAnUnreadableNote(t *testing.T) {
	root, dirs := unreadableNoteStores(t, "broken")

	stores, err := ResolveNoteStores(root, dirs, "")
	require.NoError(t, err)
	require.Len(t, stores, 2, "both stores must be declared for the duplicate-scaffold case to exist")

	_, _, err = FindNote(stores, "broken")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnreadableNote, "an existing unreadable note needs a sentinel a caller can match")
	assert.NotErrorIs(t, err, ErrAmbiguousNote, "unreadable is not ambiguous")
	assert.NotErrorIs(t, err, os.ErrNotExist, "reporting it absent sends the reader hunting for a missing file")
	assert.Contains(t, err.Error(), "shared", "the message must name which store holds it")
}
