package knowledge

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAssembleVCSAuthors: an author gets a node with an `authored` edge to each node-backed
// file they touched; a file with no graph node contributes no author or edge; and an author
// over the fan-out cap gets a files_authored COUNT with NO edges (the god-node guard).
func TestAssembleVCSAuthors(t *testing.T) {
	fileNodePaths := map[string]bool{"a.buzz": true, "b.buzz": true}
	entries := []types.KnowledgeVCS{
		{Path: "a.buzz", LastCommit: "c1", Authors: []string{"Ada", "Bob"}, Commits: 2},
		{Path: "b.buzz", LastCommit: "c2", Authors: []string{"Ada"}, Commits: 1},
		{Path: "ghost.buzz", LastCommit: "c3", Authors: []string{"Cy"}, Commits: 1}, // no file node
	}
	for i := 0; i < maxAuthorFanout+3; i++ { // push "Prolific" past the cap
		p := fmt.Sprintf("p%03d.buzz", i)
		fileNodePaths[p] = true
		entries = append(entries, types.KnowledgeVCS{Path: p, LastCommit: "x", Authors: []string{"Prolific"}, Commits: 1})
	}
	out := mergeAll([]Shard{assembleVCS(entries, fileNodePaths)}).Output()

	// Under-cap authors get a per-file `authored` edge.
	assert.True(t, hasEdge(out, "author:Ada", "file:a.buzz", types.RelationAuthored))
	assert.True(t, hasEdge(out, "author:Ada", "file:b.buzz", types.RelationAuthored))
	assert.True(t, hasEdge(out, "author:Bob", "file:a.buzz", types.RelationAuthored))

	// An author of only non-node files (ghost.buzz has no node) contributes nothing.
	_, ok := nodeByID(out, "author:Cy")
	assert.False(t, ok, "no node for an author who touched no node-backed file")

	// Over-cap author: a files_authored count, and NOT one edge.
	prolific, ok := nodeByID(out, "author:Prolific")
	require.True(t, ok)
	assert.Equal(t, strconv.Itoa(maxAuthorFanout+3), prolific.Attrs[AttrFilesAuthored], "count instead of edges")
	for _, e := range out.Links {
		assert.NotEqual(t, "author:Prolific", e.Source, "an over-cap author emits no authored edges")
	}
}

func TestAssembleVCS(t *testing.T) {
	entries := []types.KnowledgeVCS{
		{Path: "b.buzz", LastCommit: "beef", LastUnix: 1_700_000_000, Commits: 3},
		{Path: "a.buzz", LastCommit: "cafe", LastUnix: 1_600_000_000, LastAuthor: "Ada", Commits: 1},
		{Path: "gone.buzz", LastCommit: "dead", LastUnix: 1, Commits: 9}, // no file node -> dropped
	}
	known := map[string]bool{"a.buzz": true, "b.buzz": true}

	s := assembleVCS(entries, known)
	require.Len(t, s.Nodes, 2, "gone.buzz has no file node, so it is dropped")
	// Sorted by ID: file:a.buzz before file:b.buzz.
	assert.Equal(t, fileID("a.buzz"), s.Nodes[0].ID)
	assert.Equal(t, types.KindFile, s.Nodes[0].Kind)
	assert.Equal(t, map[string]string{
		"vcs_last_commit":   "cafe",
		"vcs_last_modified": "2020-09-13",
		"vcs_last_author":   "Ada",
		"vcs_commits":       "1",
	}, s.Nodes[0].Attrs)
	assert.Equal(t, fileID("b.buzz"), s.Nodes[1].ID)
	assert.Equal(t, "3", s.Nodes[1].Attrs["vcs_commits"])
	assert.Empty(t, s.Edges, "@vcs only folds attrs, it adds no edges")
}

// TestAssembleVCSFoldsAsPartialNode confirms the @vcs node is a partial (ID + attrs) that
// merges onto a file node the buzz shard defines, rather than a standalone node.
func TestAssembleVCSFoldsAsPartialNode(t *testing.T) {
	g := NewGraph()
	// The buzz shard's fuller file node.
	g.Merge([]types.KnowledgeNode{{ID: fileID("x.buzz"), Kind: types.KindFile, Label: "x.buzz", Source: "x.buzz"}}, nil)
	// The @vcs partial folds its attrs on.
	s := assembleVCS([]types.KnowledgeVCS{{Path: "x.buzz", LastCommit: "abc123", Commits: 2}}, map[string]bool{"x.buzz": true})
	g.Merge(s.Nodes, s.Edges)

	out := g.Output()
	require.Len(t, out.Nodes, 1, "the partial merged onto the existing node, not a second node")
	n := out.Nodes[0]
	assert.Equal(t, "x.buzz", n.Source, "buzz shard's Source is preserved")
	assert.Equal(t, "abc123", n.Attrs["vcs_last_commit"], "vcs attrs are folded in")
}

func TestAssembleVCSEmpty(t *testing.T) {
	assert.Empty(t, assembleVCS(nil, map[string]bool{"a.buzz": true}).Nodes)
	// An entry with no history yields no attrs, so no node.
	s := assembleVCS([]types.KnowledgeVCS{{Path: "a.buzz"}}, map[string]bool{"a.buzz": true})
	assert.Empty(t, s.Nodes)
}
