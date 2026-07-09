package knowledge

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssembleVCS(t *testing.T) {
	entries := []types.KnowledgeVCS{
		{Path: "b.buzz", LastCommit: "beef", LastUnix: 1_700_000_000, Commits: 3},
		{Path: "a.buzz", LastCommit: "cafe", LastUnix: 1_600_000_000, Commits: 1},
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
