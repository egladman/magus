package knowledge

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

func TestExplainResolutionNote(t *testing.T) {
	t.Parallel()
	card := func(how types.KnowledgeResolution) types.KnowledgeExplainOutput {
		return types.KnowledgeExplainOutput{Node: types.KnowledgeNode{ID: "dir:internal/httpx"}, Resolution: how}
	}
	assert.Empty(t, ResolutionNote("dir:internal/httpx", card(types.ResolvedID)))
	assert.Equal(t, "\"internal/httpx\" resolved by path to dir:internal/httpx\n",
		ResolutionNote("internal/httpx", card(types.ResolvedPath)))
	assert.Equal(t, "\"httpx\" matched no node exactly; showing the closest, dir:internal/httpx (explain it by ID to be sure)\n",
		ResolutionNote("httpx", card(types.ResolvedFuzzy)))
}
