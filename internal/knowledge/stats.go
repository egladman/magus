package knowledge

import (
	"cmp"
	"slices"

	"github.com/egladman/magus/types"
)

const (
	maxGods         = 15
	maxUndocumented = 25
)

// documentableKinds are the kinds whose doc coverage graph stats reports:
// entities magus generates docs for (diagnostic pages, spell pages, module pages).
var documentableKinds = []string{types.KindDiagnostic, types.KindSpell, types.KindModule}

// Stats computes the knowledge-graph analytics behind `magus graph stats`: god
// nodes (highest degree - where risk concentrates), orphans (isolated docs,
// unused spells), and doc coverage per documentable kind. kind, when non-empty,
// scopes every section to that node kind. Deterministic and LLM-free.
func (g *Graph) Stats(kind string) types.KnowledgeStats {
	g.ensureAdj()
	return types.KnowledgeStats{
		Definition: types.KnowledgeStatsDefinition,
		NodeCount:  len(g.nodes),
		EdgeCount:  len(g.edges),
		Gods:       g.godNodes(kind),
		Orphans:    g.orphanNodes(kind),
		Coverage:   g.docCoverage(kind),
	}
}

// godNodes returns the highest-degree nodes (concentration), top maxGods, sorted
// by degree then ID for determinism.
func (g *Graph) godNodes(kind string) []types.KnowledgeGodNode {
	var gods []types.KnowledgeGodNode
	for id, n := range g.nodes {
		if kind != "" && n.Kind != kind {
			continue
		}
		in, out := len(g.in[id]), len(g.out[id])
		if in+out == 0 {
			continue
		}
		gods = append(gods, types.KnowledgeGodNode{ID: id, Kind: n.Kind, Label: n.Label, Degree: in + out, In: in, Out: out})
	}
	slices.SortFunc(gods, func(a, b types.KnowledgeGodNode) int {
		if a.Degree != b.Degree {
			return cmp.Compare(b.Degree, a.Degree)
		}
		return cmp.Compare(a.ID, b.ID)
	})
	if len(gods) > maxGods {
		gods = gods[:maxGods]
	}
	return gods
}

// orphanNodes returns neglected nodes: a doc with no edges at all (nothing links
// to it and it documents nothing) and a spell no target uses. Sorted by ID.
func (g *Graph) orphanNodes(kind string) []types.KnowledgeOrphan {
	var orphans []types.KnowledgeOrphan
	for id, n := range g.nodes {
		if kind != "" && n.Kind != kind {
			continue
		}
		switch n.Kind {
		case types.KindDoc:
			if len(g.in[id])+len(g.out[id]) == 0 {
				orphans = append(orphans, types.KnowledgeOrphan{ID: id, Kind: n.Kind, Label: n.Label, Reason: "no doc links to it and it documents nothing"})
			}
		case types.KindSpell:
			if !g.spellUsed(id) {
				orphans = append(orphans, types.KnowledgeOrphan{ID: id, Kind: n.Kind, Label: n.Label, Reason: "no target uses it"})
			}
		}
	}
	slices.SortFunc(orphans, func(a, b types.KnowledgeOrphan) int { return cmp.Compare(a.ID, b.ID) })
	return orphans
}

// spellUsed reports whether any target uses one of the spell's ops
// (spell -contains-> op <-uses- target).
func (g *Graph) spellUsed(spellID string) bool {
	for _, e := range g.out[spellID] {
		if e.Relation == types.RelationContains && g.hasInRel(e.Target, types.RelationUses) {
			return true
		}
	}
	return false
}

// docCoverage reports, per documentable kind, how many nodes have a doc pointing
// at them (an incoming documents edge), listing the undocumented ones.
func (g *Graph) docCoverage(kind string) []types.KnowledgeDocCoverage {
	kinds := documentableKinds
	if kind != "" {
		if !slices.Contains(documentableKinds, kind) {
			return nil
		}
		kinds = []string{kind}
	}
	byKind := map[string][]types.KnowledgeNode{}
	for _, n := range g.nodes {
		if slices.Contains(kinds, n.Kind) {
			byKind[n.Kind] = append(byKind[n.Kind], n)
		}
	}
	var out []types.KnowledgeDocCoverage
	for _, k := range kinds {
		nodes := byKind[k]
		if len(nodes) == 0 {
			continue
		}
		documented := 0
		var undoc []string
		for _, n := range nodes {
			if g.hasInRel(n.ID, types.RelationDocuments) {
				documented++
			} else {
				undoc = append(undoc, n.Label)
			}
		}
		slices.Sort(undoc)
		if len(undoc) > maxUndocumented {
			undoc = undoc[:maxUndocumented]
		}
		out = append(out, types.KnowledgeDocCoverage{
			Kind: k, Total: len(nodes), Documented: documented,
			Percent: documented * 100 / len(nodes), Undocumented: undoc,
		})
	}
	return out
}

func (g *Graph) hasInRel(id, rel string) bool {
	for _, e := range g.in[id] {
		if e.Relation == rel {
			return true
		}
	}
	return false
}
