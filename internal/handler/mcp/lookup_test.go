package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"

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
