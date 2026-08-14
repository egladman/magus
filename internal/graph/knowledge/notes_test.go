package knowledge

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssembleNotes(t *testing.T) {
	known := map[string]bool{
		"symbol:m internal/cache/Store#Put().": true,
		"file:internal/cache/cache.go":         true,
		"project:.":                            true,
	}
	in := []types.KnowledgeNote{{
		Name:  "cache-invalidation-pairing",
		Title: "The two caches invalidate together",
		Path:  "notes/cache-invalidation-pairing.md",
		Tags:  []string{"cache", "gotcha"},
		Anchors: []string{
			"symbol:m internal/cache/Store#Put().",
			"file:internal/cache/cache.go",
		},
	}}

	out := mergeAll([]Shard{assembleNotes(in, known, SharedNotesShardName, ScopeShared)}).Output()

	n, ok := nodeByID(out, "note:cache-invalidation-pairing")
	require.True(t, ok)
	assert.Equal(t, types.KindNote, n.Kind)
	assert.Equal(t, "The two caches invalidate together", n.Label)
	assert.Equal(t, "notes/cache-invalidation-pairing.md", n.Source,
		"Source is the file path, which is what lets @vcs attribute the note to its author")
	assert.Equal(t, "cache,gotcha", n.Attrs[AttrTags])

	// A note anchored to several entities is the case no single comment could express.
	assert.True(t, hasEdge(out, "note:cache-invalidation-pairing", "symbol:m internal/cache/Store#Put().", types.RelationAnnotates))
	assert.True(t, hasEdge(out, "note:cache-invalidation-pairing", "file:internal/cache/cache.go", types.RelationAnnotates))
}

// TestAssembleNotes_UnresolvedAnchorEmitsNoEdge is the guard against putting a phantom in
// the graph. An anchor that names nothing is a real condition - a renamed symbol, a deleted
// file - and the honest report is `magus notes verify`, not an edge every consumer then has
// to defend against.
func TestAssembleNotes_UnresolvedAnchorEmitsNoEdge(t *testing.T) {
	known := map[string]bool{"project:.": true}
	in := []types.KnowledgeNote{{
		Name:    "points-at-a-ghost",
		Title:   "Anchored to something that no longer exists",
		Path:    "notes/points-at-a-ghost.md",
		Anchors: []string{"symbol:m gone/Removed#", "project:."},
	}}

	out := mergeAll([]Shard{assembleNotes(in, known, SharedNotesShardName, ScopeShared)}).Output()

	_, ok := nodeByID(out, "note:points-at-a-ghost")
	assert.True(t, ok, "the note itself is still a node")
	_, ok = findEdge(out, "note:points-at-a-ghost", "symbol:m gone/Removed#", types.RelationAnnotates)
	assert.False(t, ok, "no dangling edge to a node that was never minted")
	assert.True(t, hasEdge(out, "note:points-at-a-ghost", "project:.", types.RelationAnnotates),
		"the anchors that do resolve are unaffected")
}

// TestSharedNotesShardIsExportable pins the difference from @memory and from the private
// store: shared notes are committed content everyone who clones already has, so
// withholding them from the remote cache would hide team knowledge for no benefit.
func TestSharedNotesShardIsExportable(t *testing.T) {
	assert.False(t, IsLocalShard(SharedNotesShardName))
	assert.Equal(t, "@notes/shared", SharedNotesShardName)
	assert.Equal(t, "@notes/private", PrivateNotesShardName)
}

func TestAssembleNotes_SkipsUnnamed(t *testing.T) {
	out := mergeAll([]Shard{assembleNotes([]types.KnowledgeNote{{Title: "no name"}}, nil, SharedNotesShardName, ScopeShared)}).Output()
	assert.Empty(t, out.Nodes)
}

// TestPrivateNotesShardIsNeverExported is the one property that makes a notes location
// outside the repository safe to support at all. @notes is exportable because its content
// is already committed to the repo everyone clones; a personal note is on one machine and
// in nobody's repo, so pushing that shard would leak private content into a shared cache -
// the same hazard @memory's exclusion exists to prevent.
func TestPrivateNotesShardIsNeverExported(t *testing.T) {
	assert.True(t, IsLocalShard(PrivateNotesShardName), "personal notes must never reach the remote cache")
	assert.True(t, IsLocalShard(RuntimeShardName))
	assert.True(t, IsLocalShard(CoverageShardName))

	assert.False(t, IsLocalShard(SharedNotesShardName),
		"the workspace's own notes ARE shared - they are committed, and withholding them would hide team knowledge for no benefit")
	assert.False(t, IsLocalShard(RegistryShardName))
	assert.False(t, IsLocalShard(DocsShardName))
}

// TestNotesCarryTheirScope: the two stores hold the same shape of node, so without this a
// reader could not tell "the team committed this" from "someone wrote this in their vault".
func TestNotesCarryTheirScope(t *testing.T) {
	in := []types.KnowledgeNote{{Name: "n", Title: "T", Path: "notes/n.md"}}

	ws := mergeAll([]Shard{assembleNotes(in, nil, SharedNotesShardName, ScopeShared)}).Output()
	n, ok := nodeByID(ws, "note:n")
	require.True(t, ok)
	assert.Equal(t, ScopeShared, n.Attrs[AttrScope])

	priv := assembleNotes(in, nil, PrivateNotesShardName, ScopePrivate)
	assert.Equal(t, PrivateNotesShardName, priv.Name)
	p, ok := nodeByID(mergeAll([]Shard{priv}).Output(), "note:private/n")
	require.True(t, ok)
	assert.Equal(t, ScopePrivate, p.Attrs[AttrScope])
}

// TestSharedAndPrivateNotesDoNotCollide: the two stores share a name space on disk, and
// node merging is first-writer-wins, so an unqualified ID would let a private note vanish
// into the shared note of the same name and donate its edges to it.
func TestSharedAndPrivateNotesDoNotCollide(t *testing.T) {
	known := map[string]bool{"project:.": true, "file:a.go": true}
	shared := []types.KnowledgeNote{{Name: "auth", Title: "the team's", Path: "notes/auth.md", Anchors: []string{"project:."}}}
	private := []types.KnowledgeNote{{Name: "auth", Title: "mine", Path: "/vault/auth.md", Anchors: []string{"file:a.go"}}}

	out := mergeAll([]Shard{
		assembleNotes(shared, known, SharedNotesShardName, ScopeShared),
		assembleNotes(private, known, PrivateNotesShardName, ScopePrivate),
	}).Output()

	s, ok := nodeByID(out, "note:auth")
	require.True(t, ok, "the shared note keeps the unqualified id")
	assert.Equal(t, "the team's", s.Label)

	p, ok := nodeByID(out, "note:private/auth")
	require.True(t, ok, "the private note survives alongside it")
	assert.Equal(t, "mine", p.Label)

	assert.True(t, hasEdge(out, "note:auth", "project:.", types.RelationAnnotates))
	assert.True(t, hasEdge(out, "note:private/auth", "file:a.go", types.RelationAnnotates))
	assert.False(t, hasEdge(out, "note:auth", "file:a.go", types.RelationAnnotates),
		"the private note's edges must not be attributed to the team's")
}

// TestAnchorNodeIDNamespacesANoteAnchorByScope pins the case two hand-kept copies of this
// mapping had already diverged on: a note-kind anchor inside a PRIVATE note names a note in
// the same store, so it must carry the private namespace.
//
// The scope-blind spelling is not a cosmetic difference. Assembly minted the edge under
// note:private/<name> while the resolver looked the anchor up as note:<name>, so every
// private note-to-note anchor verified as dangling and its author was told to re-anchor a
// note that was never broken.
func TestAnchorNodeIDNamespacesANoteAnchorByScope(t *testing.T) {
	assert.Equal(t, "note:private/auth", AnchorNodeID("note", "auth", ScopePrivate))
	assert.Equal(t, "note:auth", AnchorNodeID("note", "auth", ScopeShared))

	// The mapping agrees with the ID the assembler actually mints, which is the property
	// that makes a resolved anchor and an emitted edge name the same node.
	assert.Equal(t, noteID(ScopePrivate, "auth"), AnchorNodeID("note", "auth", ScopePrivate))
	assert.Equal(t, noteID(ScopeShared, "auth"), AnchorNodeID("note", "auth", ScopeShared))
}

// TestAnchorNodeIDIgnoresScopeOffANote guards the other direction: scope namespaces ONLY a
// note-to-note anchor. A private note anchored to a file names the same file node the team
// sees, and namespacing that would dangle every private note anchored to real code.
func TestAnchorNodeIDIgnoresScopeOffANote(t *testing.T) {
	for _, scope := range []string{ScopeShared, ScopePrivate} {
		assert.Equal(t, "symbol:m internal/cache/Store#Put().", AnchorNodeID("symbol", "m internal/cache/Store#Put().", scope))
		assert.Equal(t, "file:internal/cache/cache.go", AnchorNodeID("file", "internal/cache/cache.go", scope))
		assert.Equal(t, "project:.", AnchorNodeID("project", ".", scope))
		assert.Equal(t, "target:.:build", AnchorNodeID("target", "target:.:build", scope), "a target anchor is already a full id")
		assert.Empty(t, AnchorNodeID("nonsense", "x", scope))
	}
}
