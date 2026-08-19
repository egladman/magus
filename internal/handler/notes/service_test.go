package notes

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	store "github.com/egladman/magus/internal/notes"
	notesv1 "github.com/egladman/magus/proto/gen/go/magus/notes/v1"
)

// coldWorkspace is a workspace whose knowledge graph will not load - the ordinary state on a
// fresh clone, before the first build, and any time the symbol index is cold.
type coldWorkspace struct{ root string }

func (w coldWorkspace) Root() string { return w.root }

func (w coldWorkspace) KnowledgeGraphWithSymbols(context.Context) (*knowledge.Graph, error) {
	return nil, assert.AnError
}

// writeNote lays down one note in dir through the store's own writer, so the fixture cannot
// drift from the shape the reader expects.
func writeNote(t *testing.T, dir, name, title string, anchors ...store.Anchor) {
	t.Helper()
	require.NoError(t, store.Save(dir, store.Note{
		Name:    name,
		Title:   title,
		Anchors: anchors,
		Body:    "why this is true",
	}))
}

func sharedOnly(root string) *Service {
	cfg := config.Config{}
	cfg.Knowledge.Notes.Shared = "notes"
	return NewService(coldWorkspace{root: root}, cfg)
}

// TestListNotesReportsBothStoresEvenWhenUndeclared: an empty list says "you have no notes"
// when the truth may be "this workspace has nowhere to put one", and those call for
// completely different next actions.
func TestListNotesReportsBothStoresEvenWhenUndeclared(t *testing.T) {
	root := t.TempDir()
	writeNote(t, filepath.Join(root, "notes"), "auth", "how auth works",
		store.Anchor{Kind: store.AnchorProject, Target: "."})

	resp, err := sharedOnly(root).ListNotes(t.Context(), connect.NewRequest(&notesv1.ListNotesRequest{}))
	require.NoError(t, err)

	require.Len(t, resp.Msg.GetStores(), 2, "both scopes are reported whether or not either yielded a note")
	shared, private := resp.Msg.GetStores()[0], resp.Msg.GetStores()[1]
	assert.Equal(t, notesv1.Scope_SCOPE_SHARED, shared.GetScope())
	assert.True(t, shared.GetDeclared())
	assert.Equal(t, int32(1), shared.GetNoteCount())
	assert.Equal(t, notesv1.Scope_SCOPE_PRIVATE, private.GetScope())
	assert.False(t, private.GetDeclared(), "an undeclared store is reported as undeclared, not omitted")
	assert.Empty(t, private.GetPath())

	require.Len(t, resp.Msg.GetNotes(), 1)
	n := resp.Msg.GetNotes()[0]
	assert.Equal(t, "auth", n.GetName())
	assert.Equal(t, notesv1.Scope_SCOPE_SHARED, n.GetScope())
	assert.Equal(t, "notes/auth.md", n.GetPath(), "a shared note's path is workspace-relative")
	assert.Empty(t, n.GetBody(), "a listing carries no prose; GetNote fills it")
}

// The console renders this path as where to open the note, so it has to be the file. A note
// that declares an id is identified by that id and not by its filename - renaming the file in
// a vault is the normal case ids exist for - and the path used to be rebuilt from the name,
// which named a file that stopped existing the moment the two diverged.
func TestASharedNotePathIsTheFileNotTheId(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "notes")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Some Note.md"),
		[]byte("---\nmagus:\n  id: cache-pairing\n  title: Two caches\n  anchors:\n    - kind: project\n      target: .\n---\n\nProse.\n"), 0o644))

	resp, err := sharedOnly(root).ListNotes(t.Context(), connect.NewRequest(&notesv1.ListNotesRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetNotes(), 1)
	n := resp.Msg.GetNotes()[0]
	assert.Equal(t, "cache-pairing", n.GetName(), "the id is the identity")
	assert.Equal(t, "notes/Some Note.md", n.GetPath(), "and the path is still the file")
}

// TestAColdGraphNeverReportsAnAnchorAsResolving is the safety property this surface turns on.
// When nothing could be checked, saying so is the honest report; rendering it as RESOLVES
// would tell a reader their notes were verified when no verification ran at all.
func TestAColdGraphNeverReportsAnAnchorAsResolving(t *testing.T) {
	root := t.TempDir()
	writeNote(t, filepath.Join(root, "notes"), "auth", "how auth works",
		store.Anchor{Kind: store.AnchorProject, Target: "."},
		store.Anchor{Kind: store.AnchorFile, Target: "internal/auth/auth.go"})

	resp, err := sharedOnly(root).ListNotes(t.Context(), connect.NewRequest(&notesv1.ListNotesRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetNotes(), 1)

	anchors := resp.Msg.GetNotes()[0].GetAnchors()
	require.Len(t, anchors, 2)
	for _, a := range anchors {
		assert.Equal(t, notesv1.AnchorStatus_ANCHOR_STATUS_UNVERIFIED, a.GetStatus())
		assert.NotEmpty(t, a.GetDetail(), "an unverified anchor says why it was not checked")
		assert.Empty(t, a.GetNodeId(), "nothing resolved, so there is no node to link to")
	}
}

// TestStalenessIsUnmeasuredNotCurrentWithoutAGraph: an unmeasured note is not a fresh one,
// and collapsing the two would show a reader a green light nobody earned.
func TestStalenessIsUnmeasuredNotCurrentWithoutAGraph(t *testing.T) {
	root := t.TempDir()
	writeNote(t, filepath.Join(root, "notes"), "auth", "how auth works",
		store.Anchor{Kind: store.AnchorProject, Target: "."})

	resp, err := sharedOnly(root).ListNotes(t.Context(), connect.NewRequest(&notesv1.ListNotesRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetNotes(), 1)

	assert.Equal(t, notesv1.Staleness_STALENESS_UNMEASURED, resp.Msg.GetNotes()[0].GetStaleness())
	assert.Zero(t, resp.Msg.GetNotes()[0].GetOutrunDays())
}

// TestGetNoteRefusesToGuessTheStore pins the one thing that must not happen: a name can exist
// in both stores, and they mean different things about who can read the note.
func TestGetNoteRefusesToGuessTheStore(t *testing.T) {
	root := t.TempDir()
	writeNote(t, filepath.Join(root, "notes"), "auth", "how auth works",
		store.Anchor{Kind: store.AnchorProject, Target: "."})
	svc := sharedOnly(root)

	_, err := svc.GetNote(t.Context(), connect.NewRequest(&notesv1.GetNoteRequest{Name: "auth"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	got, err := svc.GetNote(t.Context(), connect.NewRequest(&notesv1.GetNoteRequest{
		Name: "auth", Scope: notesv1.Scope_SCOPE_SHARED,
	}))
	require.NoError(t, err)
	assert.Equal(t, "why this is true", got.Msg.GetNote().GetBody(), "GetNote is where the prose arrives")
}

// TestGetNoteFromAnUndeclaredStoreIsNotFound: asking the private store for a note when this
// workspace has no private store is a NotFound about the STORE, not a silent fallback to the
// shared one - which would hand back a team note in answer to a question about a private one.
func TestGetNoteFromAnUndeclaredStoreIsNotFound(t *testing.T) {
	root := t.TempDir()
	writeNote(t, filepath.Join(root, "notes"), "auth", "how auth works",
		store.Anchor{Kind: store.AnchorProject, Target: "."})

	_, err := sharedOnly(root).GetNote(t.Context(), connect.NewRequest(&notesv1.GetNoteRequest{
		Name: "auth", Scope: notesv1.Scope_SCOPE_PRIVATE,
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}
