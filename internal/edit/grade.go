package edit

import (
	"fmt"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// RenameCandidates is the retrieval matches for ref plus ref itself when it is an exact
// symbol id: retrieval ranks by text, so it can omit the id it was handed.
func RenameCandidates(g *knowledge.Graph, ref string) []types.KnowledgeMatch {
	matches := g.Resolve(ref, 0)
	if refs, ok := g.Refs(ref); ok && refs.Symbol == ref {
		matches = append(matches, types.KnowledgeMatch{ID: ref, Kind: types.KindSymbol, Label: refs.Label})
	}
	return matches
}

// RenameBlindSpots refuses a rename of id, a symbol of language, for every index that could
// hold its sites and is missing or older than its sources: such an index can hold call
// sites the occurrence list never names, and no per-site check sees a site that is not
// listed. An index of another language holds none of them, so it refuses nothing.
func RenameBlindSpots(id, language string, cov knowledge.Coverage) []types.EditRefusal {
	answer := knowledge.Answer(id, true, cov.Narrow(language))
	blind := make([]types.EditRefusal, 0, len(answer.Gaps)+len(answer.StaleIndexDetails))
	for _, gap := range answer.Gaps {
		blind = append(blind, types.EditRefusal{Reason: fmt.Sprintf("project %s: symbol index %s; build it with `%s`", gap.Project.Path, gap.Describe(), hint.GraphBuild)})
	}
	for _, stale := range answer.StaleIndexDetails {
		index := "index"
		if stale.Language != "" {
			index = stale.Language + " index"
		}
		blind = append(blind, types.EditRefusal{Reason: fmt.Sprintf("project %s changed after its %s was built; refresh with `%s`", stale.Project, index, hint.GraphBuild)})
	}
	return blind
}
