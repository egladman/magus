package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/memory"
	store "github.com/egladman/magus/internal/notes"
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
