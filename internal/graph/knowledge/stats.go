package knowledge

import (
	"cmp"
	"slices"

	"github.com/egladman/magus/types"
)

const (
	maxGods         = 15
	maxUndocumented = 25
	maxOrphans      = 40 // cap the orphan SAMPLE; IsolatedCount reports the true total
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
	orphans, isolated := g.orphanNodes(kind)
	components, largest := g.connectivity()
	return types.KnowledgeStats{
		Definition:       types.KnowledgeStatsDefinition,
		NodeCount:        len(g.nodes),
		EdgeCount:        len(g.edges),
		Gods:             g.godNodes(kind),
		Orphans:          orphans,
		Coverage:         g.docCoverage(kind),
		IsolatedCount:    isolated,
		Components:       components,
		LargestComponent: largest,
	}
}

// connectivity measures how fragmented the graph is: the number of weakly-connected components (edge
// direction ignored) and the size of the largest. A well-linked graph is one big component; many
// components means the builder minted nodes it never connected. Union-find over the undirected edge set,
// so it is O(V + E a(V)) and deterministic. Every node starts in its own set, so an isolated node counts
// as its own component.
func (g *Graph) connectivity() (components, largest int) {
	parent := make(map[string]string, len(g.nodes))
	for id := range g.nodes {
		parent[id] = id
	}
	var find func(string) string
	find = func(x string) string {
		for parent[x] != x {
			parent[x] = parent[parent[x]] // path halving
			x = parent[x]
		}
		return x
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}
	for _, e := range g.edges {
		// An edge can name a node outside g.nodes only in a malformed graph; guard so find never seeds a
		// stray root. Both endpoints exist for every builder-produced edge.
		if _, ok := parent[e.Source]; !ok {
			continue
		}
		if _, ok := parent[e.Target]; !ok {
			continue
		}
		union(e.Source, e.Target)
	}
	sizes := make(map[string]int, len(g.nodes))
	for id := range g.nodes {
		root := find(id)
		sizes[root]++
		if sizes[root] > largest {
			largest = sizes[root]
		}
	}
	return len(sizes), largest
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

// orphanNodes returns neglected nodes and the TRUE count of fully isolated ones. An isolated node (any
// kind, in+out == 0) is the core data-quality signal - the builder minted it but linked nothing to it, so
// it is undiscoverable in the graph. The returned slice is a SAMPLE capped at maxOrphans (a graph can hold
// hundreds of isolated diagnostic codes; listing them all would drown the report), sorted by ID; isolated
// is the full total so the caller can say "showing N of M". The spell case is a SEMANTIC orphan (it has
// edges - it contains ops - but nothing uses them), so it is reported regardless of the cap's isolated
// sampling and does not count toward isolated.
func (g *Graph) orphanNodes(kind string) (sample []types.KnowledgeOrphan, isolated int) {
	var all []types.KnowledgeOrphan
	for id, n := range g.nodes {
		if kind != "" && n.Kind != kind {
			continue
		}
		if len(g.in[id])+len(g.out[id]) == 0 {
			// Spells are edge-light by design: a structural spell (provides no ops) and an unused builtin
			// are EXPECTED to be unlinked, not data-quality gaps - the spell case below governs the one
			// spell orphan that matters (a declared op-provider nothing runs, which has edges). So a
			// 0-degree spell is skipped rather than counted as isolated.
			if n.Kind == types.KindSpell {
				continue
			}
			isolated++
			reason := "isolated: nothing links to it and it links nothing"
			if n.Kind == types.KindDoc {
				reason = "no doc links to it and it documents nothing"
			}
			all = append(all, types.KnowledgeOrphan{ID: id, Kind: n.Kind, Label: n.Label, Reason: reason})
			continue
		}
		// A declared, op-providing spell that nothing runs is genuinely dead even though it has edges.
		if n.Kind == types.KindSpell && n.Attrs[AttrDeclared] == "true" && g.spellProvidesOps(id) && !g.spellUsed(id) {
			all = append(all, types.KnowledgeOrphan{ID: id, Kind: n.Kind, Label: n.Label, Reason: "declared but no target uses it"})
		}
	}
	slices.SortFunc(all, func(a, b types.KnowledgeOrphan) int { return cmp.Compare(a.ID, b.ID) })
	if len(all) > maxOrphans {
		all = all[:maxOrphans]
	}
	return all, isolated
}

// spellProvidesOps reports whether the spell contributes any op node. A spell with none
// (a structural dispatch spell like the magusfile spell, which turns magusfile functions
// into targets) provides nothing a target could invoke, so it is never an orphan.
func (g *Graph) spellProvidesOps(spellID string) bool {
	for _, e := range g.out[spellID] {
		if e.Relation == types.RelationContains {
			return true
		}
	}
	return false
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
