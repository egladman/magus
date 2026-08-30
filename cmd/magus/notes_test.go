package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/memory"
	store "github.com/egladman/magus/internal/notes"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A record already points into the graph, so promotion reads its anchors rather than asking
// the promoter to restate them - that restatement is the friction promotion exists to remove.
func TestAnchorsComeFromTheRecordsNodeRefs(t *testing.T) {
	got, err := anchorsFromRefs(memory.Record{
		Name: "pairing",
		Refs: []memory.Ref{
			{Kind: memory.RefKindQuery, Target: "kind:target"},
			{Kind: memory.RefKindNode, Target: "symbol:m cache/Put()."},
			{Kind: memory.RefKindCommand, Target: "magus run test"},
			{Kind: memory.RefKindNode, Target: "file:internal/cache/cache.go"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []store.Anchor{
		{Kind: store.AnchorSymbol, Target: "m cache/Put()."},
		{Kind: store.AnchorFile, Target: "internal/cache/cache.go"},
	}, got, "node refs become anchors; re-runnable refs never do")
}

// Refused rather than given an anchor of convenience. A wrong anchor passes verify forever
// while pointing at something the note is not about, which is worse than no note at all.
func TestARecordWithNoNodeRefCannotBePromoted(t *testing.T) {
	_, err := anchorsFromRefs(memory.Record{
		Name: "loose",
		Refs: []memory.Ref{{Kind: memory.RefKindQuery, Target: "kind:target"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unanchored")
	assert.Contains(t, err.Error(), "magus memory put loose", "the error names the fix")
}

// A node id whose prefix is not an anchor kind (a package, a dir) is skipped, not fatal: the
// record may carry several refs and only some of them name anchorable entities.
func TestUnanchorableNodeRefsAreSkipped(t *testing.T) {
	got, err := anchorsFromRefs(memory.Record{
		Name: "mixed",
		Refs: []memory.Ref{
			{Kind: memory.RefKindNode, Target: "package:gomod github.com/x/y"},
			{Kind: memory.RefKindNode, Target: "project:."},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []store.Anchor{{Kind: store.AnchorProject, Target: "."}}, got)
}

func TestPromoteBodyCarriesTheProseAndTheEvidence(t *testing.T) {
	body := promoteBody(memory.Record{
		Name: "pairing",
		Body: "Both caches must be cleared together.",
		Refs: []memory.Ref{
			{Kind: memory.RefKindNode, Target: "symbol:m cache/Put()."},
			{Kind: memory.RefKindQuery, Target: "kind:target"},
		},
	})
	assert.Contains(t, body, "Both caches must be cleared together.")
	assert.Contains(t, body, "query: kind:target", "the re-runnable ref is what makes the claim checkable")
	assert.NotContains(t, body, "symbol:m cache/Put().", "a node ref is already an anchor, not evidence prose")
	assert.Contains(t, body, "Drafted by an agent as memory record pairing")
}

// A pointer record carries no prose, and a blank file is the thing most likely to be
// abandoned. It gets a prompt instead.
func TestPromoteBodyPromptsWhenTheRecordHadNoProse(t *testing.T) {
	body := promoteBody(memory.Record{Name: "bare", Refs: []memory.Ref{{Kind: memory.RefKindNode, Target: "project:."}}})
	assert.Contains(t, body, "Say what is true")
	assert.NotEmpty(t, strings.TrimSpace(body))
}

// The scope decides the shape of the path, not just who can read the note, and the two cases
// are asserted together because getting them backwards is silent: an absolute path for a
// shared note still opens on the machine that produced the listing, and only stops working
// once someone else reads it.
func TestNotePathIsRelativeOnlyForSharedNotes(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "ws")
	shared := notesStore{dir: filepath.Join(root, "notes"), scope: store.ScopeShared}
	private := notesStore{dir: filepath.Join(string(filepath.Separator), "home", "me", "vault"), scope: store.ScopePrivate}

	sharedNote := store.Note{Name: "why-buzz", Path: filepath.Join(root, "notes", "why-buzz.md")}
	if got, want := notePath(root, shared, sharedNote), "notes/why-buzz.md"; got != want {
		t.Errorf("shared note path = %q, want %q", got, want)
	}
	// A private store lives outside the workspace, so there is nothing for a relative path
	// to be relative TO; it stays absolute rather than becoming a ../.. walk.
	want := filepath.Join(string(filepath.Separator), "home", "me", "vault", "why-buzz.md")
	if got := notePath(root, private, store.Note{Name: "why-buzz", Path: want}); got != want {
		t.Errorf("private note path = %q, want %q", got, want)
	}
}

// The case the old derivation got wrong. A note that declares an id is identified by that id
// and NOT by its filename, so rebuilding the path from the name pointed the reader at a file
// that does not exist. The listing reports where the note actually is.
func TestNotePathReportsTheFileNotTheId(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "ws")
	shared := notesStore{dir: filepath.Join(root, "notes"), scope: store.ScopeShared}
	n := store.Note{
		Name: "cache-pairing",
		ID:   "cache-pairing",
		Path: filepath.Join(root, "notes", "Archive", "Some Note.md"),
	}
	if got, want := notePath(root, shared, n), "notes/Archive/Some Note.md"; got != want {
		t.Errorf("renamed note path = %q, want %q", got, want)
	}
}

// A capture holds BOTH halves of the conversation. A transcript of only your own remarks is
// not a transcript of the review: what was decided is nearly always in what somebody said
// back, and that half lives on the forge rather than in the session.
func TestCaptureKeepsWhatColleaguesSaid(t *testing.T) {
	sess := &types.DiffSession{
		ID:   "rev-1",
		AsOf: "patch-1",
		Comments: []types.DiffComment{
			{Path: "a.go", Hunk: 1, Author: types.DiffAuthorHuman, Body: "mine"},
		},
	}
	threads := []types.ReviewThread{
		{ID: "t1", Path: "a.go", Line: 12, Author: "priya", Body: "theirs"},
		{ID: "t2", Path: "z.go", Line: 3, Author: "marcus", Body: "elsewhere"},
	}

	c := captureFromSession(sess, threads, "review", nil)
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
	sess := &types.DiffSession{
		ID: "rev-1",
		Comments: []types.DiffComment{
			{Path: "a.go", Author: types.DiffAuthorHuman, Body: "mine"},
		},
	}
	n, err := captureFromSession(sess, nil, "review", nil).Note("cap")
	require.NoError(t, err)
	assert.Contains(t, n.Body, "mine")
}

// The daemon accelerates and never gates. A person who wrote remarks in `magus diff` with no
// daemon running has them on disk, and capture refusing because no daemon is up would lose the
// one artifact nothing can recreate.
func TestCaptureReadsTheStoreWhenNoDaemonIsRunning(t *testing.T) {
	cache := t.TempDir()
	root := filepath.Join(cache, "ws")
	written := changeset.NewStore(cache)
	written.Attach(root, "main", types.Diff{Base: "main"}, "asof")
	written.AddComment(root, types.DiffComment{Path: "a.go", Line: 4, Body: "mine"}, types.DiffAuthorHuman)

	sess := storedDiffSession(cache, "diff --git a/a.go b/a.go\n")
	require.Len(t, sess.Comments, 1, "the drafts the store persisted are the transcript")
	assert.Equal(t, "mine", sess.Comments[0].Body)
	// Digested from the patch exactly as attachDiffSession digests it, so a capture taken with
	// no daemon names the note a daemon-attached one would have named.
	assert.Equal(t, changeset.PatchDigest("diff --git a/a.go b/a.go\n"), sess.AsOf)
	assert.Equal(t, "review-"+sess.AsOf[:12], captureName(sess))
}

// An unreadable patch must not name every such capture the same note: the second one would
// collide with the first and be refused, which is the transcript lost.
func TestAStoredSessionWithNoPatchHasNoSnapshotId(t *testing.T) {
	sess := storedDiffSession(t.TempDir(), "")
	assert.Empty(t, sess.AsOf)
	assert.Equal(t, "review-thread", captureName(sess))
}

// A colleague's remark is a fact about the review, not about whether a background process is
// up. Without a daemon the forge is asked directly rather than the reader being shown a review
// with nobody else in it.
func TestReviewThreadsReachTheForgeWithNoDaemon(t *testing.T) {
	withFakeReviewProvider(t, []any{
		map[string]any{"id": "t1", "path": "a.go", "line": 11, "author": "priya", "body": "theirs"},
	})
	cache := t.TempDir()

	threads, reason := localReviewThreads(t.Context(), types.ReviewOrigin{Branch: "feat/x"}, cache)
	require.Len(t, threads, 1)
	assert.Equal(t, "theirs", threads[0].Body)
	assert.Empty(t, reason)
	// Nothing has been on screen here, so the whole conversation is new - the mark the daemon
	// would have applied, taken from the watermark the store persists rather than from a session.
	assert.True(t, threads[0].New)
}

// The watermark outlives the daemon that normally reads it, so a thread already seen does not
// come back marked new the moment the daemon is stopped.
func TestASeenThreadIsNotNewWithoutADaemon(t *testing.T) {
	withFakeReviewProvider(t, []any{
		map[string]any{"id": "t1", "path": "a.go", "line": 11, "author": "priya", "body": "theirs"},
	})
	cache := t.TempDir()
	root := filepath.Join(cache, "ws")
	sessions := changeset.NewStore(cache)
	sessions.Attach(root, "main", types.Diff{Base: "main"}, "asof")
	sessions.MarkThreadsSeen(root, []string{"t1"})

	threads, _ := localReviewThreads(t.Context(), types.ReviewOrigin{Branch: "feat/x"}, cache)
	require.Len(t, threads, 1)
	assert.False(t, threads[0].New)
}

// Both read paths compute the mark, so a capture can say which half of the conversation the
// reader had not weighed yet. It is told to the person TAKING the capture and never written
// into the note: New belongs to this reader's history with the review, and in a transcript a
// colleague reads next year it would describe somebody else's morning.
func TestCaptureSaysWhatWasNewToThisReader(t *testing.T) {
	threads := []types.ReviewThread{
		{ID: "t1", Author: "priya", Body: "you weighed this already"},
		{ID: "t2", Author: "marcus", Body: "arrived since you looked", New: true},
	}

	assert.Contains(t, newRemarkLine(threads), "1 remark on the review had not been in front of you before")
	assert.Empty(t, newRemarkLine(threads[:1]), "a conversation the reader has already had says nothing")
	assert.Contains(t, newRemarkLine(append(threads, types.ReviewThread{ID: "t3", New: true})), "2 remarks")
}

// A malformed remark is reported rather than dropped: the threads that decoded still travel,
// and the caller says what it could not read.
func TestALocalReadReportsWhatItCouldNotDecode(t *testing.T) {
	withFakeReviewProvider(t, []any{
		map[string]any{"id": "t1", "path": "a.go", "line": 11, "author": "priya", "body": "theirs"},
		"not a thread at all",
	})

	threads, reason := localReviewThreads(t.Context(), types.ReviewOrigin{Branch: "feat/x"}, t.TempDir())
	assert.Len(t, threads, 1)
	assert.NotEmpty(t, reason, "a transcript silently missing a remark is worse than no transcript")
}

// withFakeReviewProvider wires a review provider for this test, so the daemon-free read path
// can be exercised against threads instead of only against an unwired workspace.
func withFakeReviewProvider(t *testing.T, threads []any) {
	t.Helper()
	name := "fake-notes-review-" + t.Name()
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(name,
		spells.WithInvoker(func(_ context.Context, req spells.InvokeRequest) (any, error) {
			switch req.Target {
			case spells.FindReviewContract:
				return map[string]any{"id": "482", "repo": "acme/acme"}, nil
			case spells.ReviewThreadsContract:
				return threads, nil
			default:
				return map[string]any{}, nil
			}
		})))
	prev := bindings.ReviewProvider()
	bindings.SetReviewProvider(name)
	t.Cleanup(func() { bindings.SetReviewProvider(prev) })
}

func TestFirstNonEmptyTreatsBlankAsEmpty(t *testing.T) {
	assert.Equal(t, "a", firstNonEmpty("a", "b"))
	assert.Equal(t, "b", firstNonEmpty("", "b"))
	assert.Equal(t, "b", firstNonEmpty("   \n", "b"))
	assert.Equal(t, "", firstNonEmpty("", ""))
}

func TestRelativeToRootKeepsAnOutsidePathAbsolute(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repos", "magus")

	assert.Equal(t, "cmd/magus/diff.go", relativeToRoot(root, filepath.Join(root, "cmd", "magus", "diff.go")))
	assert.Equal(t, ".", relativeToRoot(root, root))

	outside := filepath.Join(string(filepath.Separator), "repos", "other", "x.go")
	assert.Equal(t, outside, relativeToRoot(root, outside))
}
