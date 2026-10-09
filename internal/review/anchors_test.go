package review

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/notes"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChangedSymbolIDsAreWhatASymbolAnchorNames pins the second half of the anchors query. A note
// anchors a symbol by its index id, so passing labels or paths would match nothing and the
// section would report a clean tree it never checked.
func TestChangedSymbolIDsAreWhatASymbolAnchorNames(t *testing.T) {
	rev := types.Diff{Files: []types.DiffFile{
		{Path: "a.go", Symbols: []types.DiffSymbol{{ID: "m types/Diff#", Label: "Diff"}, {ID: "", Label: "unindexed"}}},
		{Path: "b.go", Symbols: []types.DiffSymbol{{ID: "m types/Diff#", Label: "Diff"}}},
	}}
	assert.Equal(t, []string{"a.go", "b.go"}, ChangedPaths(rev))
	assert.Equal(t, []string{"m types/Diff#"}, changedSymbolIDs(rev), "deduplicated, and an unindexed symbol is not an id")
}

// TestSymbolAnchorJoinsAgainstTheGraphsNodeID holds the two ends of the anchor join together.
//
// A note anchors a bare SCIP key, the diff reports its changed symbols as knowledge-graph node
// ids, and this is the only layer where both spellings are in scope. It shipped comparing them
// directly, so symbol anchors (the form the store's own template tells authors to prefer)
// never matched, and the impact report said no note anchored what you had changed.
func TestSymbolAnchorJoinsAgainstTheGraphsNodeID(t *testing.T) {
	const key = "m internal/cache/Store#Put()."
	changed := knowledge.AnchorNodeID("symbol", key, string(notes.ScopeShared))
	require.NotEqual(t, key, changed,
		"if the two spellings agreed, the join could not have been broken")

	res := stampAnchorNodeIDs([]notes.ResolvedAnchor{{
		Note: "put-is-not-idempotent", Pos: 0,
		Anchor: notes.Anchor{Kind: notes.AnchorSymbol, Target: key},
	}}, string(notes.ScopeShared))

	hits := notes.AnchorHits(res, nil, []string{changed})

	// Target stays the one the author wrote, not the graph's spelling.
	assert.Equal(t, []notes.AnchorHit{{
		Note:    "put-is-not-idempotent",
		Pos:     0,
		Kind:    notes.AnchorSymbol,
		Target:  key,
		Matched: changed,
		Match:   notes.MatchSymbol,
	}}, hits)
}

// TestNoteStoresSkipsWhatIsNotDeclared. A workspace declaring neither store is the default,
// and it must read as "no anchors" rather than as an error the report prints around.
func TestNoteStoresSkipsWhatIsNotDeclared(t *testing.T) {
	root := t.TempDir()

	none, err := NoteStores(root, NoteDirs{})
	require.NoError(t, err)
	assert.Empty(t, none)

	shared, err := NoteStores(root, NoteDirs{Shared: "notes"})
	require.NoError(t, err)
	assert.Equal(t, []NoteStore{{Dir: filepath.Join(root, "notes"), Scope: notes.ScopeShared}}, shared)
}

// A misdeclared store is an error to the caller. Returning no anchors instead would print a
// section that reads as a clean tree nobody checked.
func TestChangesetAnchorsReturnsAMisdeclaredStoreAsAnError(t *testing.T) {
	root := t.TempDir()
	load := func(context.Context) (*knowledge.Graph, error) {
		t.Fatal("the graph is only loaded once a store is declared and valid")
		return nil, nil
	}

	hits, err := ChangesetAnchors(t.Context(), root, NoteDirs{Shared: "../outside"}, load, types.Diff{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "note anchors")
	assert.Nil(t, hits)

	hits, err = ChangesetAnchors(t.Context(), root, NoteDirs{}, load, types.Diff{})
	require.NoError(t, err, "declaring no store is the default, not a fault")
	assert.Nil(t, hits)
}

// TestJoinAnchorsReadsAFileAnchorWithoutAGraph. Grading needs the graph and naming does not: a
// graph that would not load leaves the hit in place and marks it ungraded, never absent.
func TestJoinAnchorsReadsAFileAnchorWithoutAGraph(t *testing.T) {
	root := t.TempDir()
	stores, err := NoteStores(root, NoteDirs{Shared: "notes"})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(stores[0].Dir, 0o755))
	require.NoError(t, notes.Save(stores[0].Dir, notes.Note{
		Name:    "cache-pairs",
		Title:   "Cache pairs",
		Anchors: []notes.Anchor{{Kind: notes.AnchorFile, Target: "internal/cache/cache.go"}},
		Body:    "Put and Get change together.\n",
	}))

	hits := joinAnchors(t.Context(), root, stores, nil, []string{"internal/cache/cache.go"}, nil)

	assert.Equal(t, []AnchorHit{{
		Note:    "cache-pairs",
		Title:   "Cache pairs",
		Pos:     0,
		Kind:    notes.AnchorFile,
		Target:  "internal/cache/cache.go",
		Matched: "internal/cache/cache.go",
		Match:   string(notes.MatchFile),
		Drift:   string(notes.StatusUngraded),
	}}, hits)
}
