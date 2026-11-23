package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

func TestProjectLookup(t *testing.T) {
	result := types.KnowledgeQueryOutput{
		Offset: 2, MatchCount: 5,
		Matches: []types.KnowledgeMatch{{ID: "method:magus.where"}, {ID: "project:docs"}},
		Nodes: []types.KnowledgeNode{
			{ID: "method:magus.where", Kind: types.KindMethod, Attrs: map[string]string{"buzz": "where(path: str) -> str"}},
			{ID: "module:magus", Kind: types.KindModule},
		},
	}
	page := projectLookup(result)
	require.Equal(t, []lookupMatch{{ID: "method:magus.where", Signature: "where(path: str) -> str"}, {ID: "project:docs"}}, page.Matches)
	require.Equal(t, 5, page.Total)
	require.Equal(t, 4, *page.NextOffset)

	result.MatchCount = 4
	require.Nil(t, projectLookup(result).NextOffset)
	result = types.KnowledgeQueryOutput{Answer: types.KnowledgeAnswer{Verdict: types.VerdictUnknown}}
	empty := projectLookup(result)
	require.Equal(t, types.VerdictUnknown, empty.Verdict)
	require.NotNil(t, empty.Matches)
	require.Nil(t, empty.NextOffset)
}

func TestQueryRejectsInvalidInputs(t *testing.T) {
	tool := queryTool{}
	for _, params := range []map[string]any{
		{},
		{"terms": " "},
		{"terms": "kind=project", "offset": -1.0},
		{"terms": "kind=project", "offset": 0.5},
		{"terms": "kind=project", "offset": "1"},
	} {
		_, err := tool.Invoke(t.Context(), spells.InvokeRequest{Params: params})
		require.Error(t, err)
	}
}

func TestQueryReturnsWorkspaceMatches(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), nil, 0o644))
	m, err := magus.Open(t.Context(), root, magus.WithSpawnRule(tier.Ceiling))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	tool := queryTool{workspace: m}
	response, err := tool.Invoke(t.Context(), spells.InvokeRequest{Params: map[string]any{"terms": "kind=project"}})
	require.NoError(t, err)
	page, ok := response.Data.(lookupPage)
	require.True(t, ok)
	require.NotEmpty(t, page.Matches)
	require.LessOrEqual(t, len(page.Matches), lookupPageSize)
}
