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
	next := 4
	want := lookupPage{
		Matches: []lookupMatch{{ID: "method:magus.where", Signature: "where(path: str) -> str"}, {ID: "project:docs"}},
		Total: 5, NextOffset: &next,
	}
	require.Equal(t, want, projectLookup(result))

	result.MatchCount = 4
	want.Total, want.NextOffset = 4, nil
	require.Equal(t, want, projectLookup(result))
	result = types.KnowledgeQueryOutput{Answer: types.KnowledgeAnswer{Verdict: types.VerdictUnknown}}
	require.Equal(t, lookupPage{Matches: []lookupMatch{}, Verdict: types.VerdictUnknown}, projectLookup(result))
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
