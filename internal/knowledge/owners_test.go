package knowledge

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodeownersMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          bool
	}{
		{"*", "pkg/a", true},
		{"/pkg/a/", "pkg/a", true},
		{"/pkg/a/", "pkg/a/sub", true},
		{"pkg/a", "pkg/a", true},
		{"pkg/a", "pkg/a/sub", true},
		{"pkg/a", "pkg/apple", false}, // segment-aware prefix, not string prefix
		{"pkg/a", "pkg/b", false},
		{"*.go", "pkg/a/main.go", true},
		{"*.go", "pkg/a/main.rs", false},
		{"/docs/", "pkg/a", false},
		{"docs/", "docs", true},
		// An anchored glob matches only at its depth; an unanchored one at any depth.
		{"/*.md", "readme.md", true},
		{"/*.md", "pkg/a/readme.md", false},
		{"*.md", "pkg/a/readme.md", true},
		{"/pkg/*/main.go", "pkg/a/main.go", true},
		{"/pkg/*/main.go", "x/pkg/a/main.go", false},
		// bare "*" matches all depths; anchored "/*" only root-level entries.
		{"*", "pkg/a", true},
		{"/*", "website", true},
		{"/*", "pkg/a", false},
	} {
		assert.Equalf(t, tc.want, codeownersMatch(tc.pattern, tc.path), "match(%q, %q)", tc.pattern, tc.path)
	}
}

func TestLastMatchWins(t *testing.T) {
	rules := []codeownersRule{
		{pattern: "*", owners: []string{"@root"}, line: 1},
		{pattern: "pkg/a", owners: []string{"@team-a"}, line: 2},
		{pattern: "pkg/a/secret", owners: nil, line: 3}, // ownership unset: shadows earlier
	}
	r, ok := lastMatch(rules, "pkg/b")
	require.True(t, ok)
	assert.Equal(t, []string{"@root"}, r.owners)

	r, ok = lastMatch(rules, "pkg/a")
	require.True(t, ok)
	assert.Equal(t, []string{"@team-a"}, r.owners, "the more specific later rule wins")

	_, ok = lastMatch(rules, "pkg/a/secret")
	assert.False(t, ok, "an owner-less rule unsets ownership, so no edge")
}

func TestAssembleOwnersEdgesAndNodes(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "CODEOWNERS", "# owners\n* @root\n/pkg/a/ @team-a @alice\n")

	nodes := []ownedNode{
		{ID: "project:pkg/a", Path: "pkg/a"},
		{ID: "project:pkg/b", Path: "pkg/b"},
		{ID: "file:pkg/a/main.buzz", Path: "pkg/a/main.buzz"},
	}
	out := mergeAll([]Shard{assembleOwners(root, nodes)}).Output()

	// pkg/a and its file are owned by team-a + alice (the later, more specific rule).
	assert.True(t, hasEdge(out, "owner:@team-a", "project:pkg/a", types.RelationOwns))
	assert.True(t, hasEdge(out, "owner:@alice", "project:pkg/a", types.RelationOwns))
	assert.True(t, hasEdge(out, "owner:@team-a", "file:pkg/a/main.buzz", types.RelationOwns))
	// pkg/b falls to the catch-all owner.
	assert.True(t, hasEdge(out, "owner:@root", "project:pkg/b", types.RelationOwns))
	assert.False(t, hasEdge(out, "owner:@team-a", "project:pkg/b", types.RelationOwns))

	// Owner nodes exist and carry the owner kind; the edge is extracted with provenance.
	n, ok := nodeByID(out, "owner:@team-a")
	require.True(t, ok)
	assert.Equal(t, types.KindOwner, n.Kind)
	e, _ := findEdge(out, "owner:@team-a", "project:pkg/a", types.RelationOwns)
	assert.Equal(t, types.ConfidenceExtracted, e.Confidence)
	assert.Contains(t, e.Provenance, "CODEOWNERS:")
}

func TestAssembleOwnersNoFile(t *testing.T) {
	root := t.TempDir()
	s := assembleOwners(root, []ownedNode{{ID: "project:pkg/a", Path: "pkg/a"}})
	assert.Empty(t, s.Edges, "no CODEOWNERS, no owner edges")
	assert.Empty(t, s.Nodes)
}

// TestOwnersOnlyMatchExistingNodes: a rule covering a path not in the graph adds no
// dangling edge (owners are matched against existing nodes only).
func TestOwnersOnlyMatchExistingNodes(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "CODEOWNERS", "/nonexistent/ @ghost\n")
	s := assembleOwners(root, []ownedNode{{ID: "project:pkg/a", Path: "pkg/a"}})
	assert.Empty(t, s.Edges)
}
