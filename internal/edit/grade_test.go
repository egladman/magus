package edit

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// Retrieval ranks by text, so an exact symbol id can fall outside g.Resolve's matches;
// `refs <id> --rename` was refused for an id `refs <id>` listed.
func TestRenameCandidatesIncludeAnExactSymbolID(t *testing.T) {
	const id = "symbol:gomod x `x/internal/edit`/checkUnchanged()."
	g := knowledge.NewGraph()
	g.Merge([]types.KnowledgeNode{
		{ID: id, Kind: types.KindSymbol, Label: "checkUnchanged"},
		{ID: "file:edit.go", Kind: types.KindFile, Label: "edit.go"},
	}, []types.KnowledgeEdge{{Source: "file:edit.go", Target: id, Relation: types.RelationDefines, Confidence: types.ConfidenceExtracted, Score: 1}})

	ids := []string{}
	for _, m := range RenameCandidates(g, id) {
		ids = append(ids, m.ID)
	}
	assert.Contains(t, ids, id)
}

// A project with a Go and a Buzz index judges them apart, and a rename is refused only for
// an index that could hold the symbol's sites. `refs probeTimeoutFrom --rename` was refused
// for "project . changed after its index was built" when only the Buzz index was behind.
func TestRenameBlindSpotsCountOnlyTheSymbolsLanguage(t *testing.T) {
	const goID, buzzID, bareID = "symbol:gomod x `x`/probeTimeoutFrom().", "symbol:buzz x spell/probe().", "symbol:x bare()."
	defines := func(file, id string) types.KnowledgeEdge {
		return types.KnowledgeEdge{Source: "file:" + file, Target: id, Relation: types.RelationDefines, Confidence: types.ConfidenceExtracted, Score: 1}
	}
	g := knowledge.NewGraph()
	g.Merge([]types.KnowledgeNode{
		{ID: goID, Kind: types.KindSymbol, Label: "probeTimeoutFrom", Attrs: map[string]string{types.AttrLanguage: "go"}},
		// No language on the node: the defining file's is the symbol's.
		{ID: buzzID, Kind: types.KindSymbol, Label: "probe"},
		{ID: "file:spell.buzz", Kind: types.KindFile, Label: "spell.buzz", Attrs: map[string]string{types.AttrLanguage: "buzz"}},
		{ID: bareID, Kind: types.KindSymbol, Label: "bare"},
	}, []types.KnowledgeEdge{defines("probe.go", goID), defines("spell.buzz", buzzID), defines("bare.txt", bareID)})

	staleGo := types.KnowledgeStaleIndex{Project: ".", Language: "go", Op: "scip"}
	staleBuzz := types.KnowledgeStaleIndex{Project: ".", Language: "buzz", Op: "scip-buzz"}
	missingBuzz := types.KnowledgeSymbolGap{Project: types.NewProjectRef(".", ""), Language: "buzz", State: types.SymbolIndexNotBuilt}
	refused := func(index string) types.EditRefusal {
		return types.EditRefusal{Reason: fmt.Sprintf("project . changed after its %s was built; refresh with `%s`", index, hint.GraphBuild)}
	}

	for _, tc := range []struct {
		name  string
		id    string
		stale []types.KnowledgeStaleIndex
		gaps  []types.KnowledgeSymbolGap
		want  []types.EditRefusal
	}{
		{name: "go symbol beside a stale buzz index", id: goID, stale: []types.KnowledgeStaleIndex{staleBuzz}},
		{name: "go symbol beside a missing buzz index", id: goID, gaps: []types.KnowledgeSymbolGap{missingBuzz}},
		{name: "go symbol beside a stale go index", id: goID, stale: []types.KnowledgeStaleIndex{staleBuzz, staleGo}, want: []types.EditRefusal{refused("go index")}},
		{name: "buzz symbol beside a stale go index", id: buzzID, stale: []types.KnowledgeStaleIndex{staleGo}},
		{name: "buzz symbol beside a stale buzz index", id: buzzID, stale: []types.KnowledgeStaleIndex{staleGo, staleBuzz}, want: []types.EditRefusal{refused("buzz index")}},
		{name: "a symbol of no known language counts every index", id: bareID, stale: []types.KnowledgeStaleIndex{staleBuzz}, want: []types.EditRefusal{refused("buzz index")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cov := knowledge.Coverage{Seeded: true, Probed: true, Stale: tc.stale, Gaps: tc.gaps}
			got := RenameBlindSpots(tc.id, g.SymbolLanguage(tc.id), cov)
			if tc.want == nil {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}
