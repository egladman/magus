package knowledge

import (
	"cmp"
	"maps"
	"slices"

	"github.com/egladman/magus/types"
)

// Diffing is comparison, not analysis: given the base graph and the current graph
// (both already assembled), it reports the node/edge deltas. Nodes are keyed by ID,
// edges by (source, target, relation) - the same identities the store and merge use -
// so the diff mirrors what a rebuild would change. Deterministic: every slice is
// sorted, so the same pair of graphs always yields byte-identical output.
//
// Nodes get field-level change tracking (changedNodeFields); edges do not. Edge
// identity IS (source, target, relation), so a re-scored or re-provenanced edge that
// keeps those three is intentionally not reported - tracking edge-attribute drift
// would be dominated by reference-edge line-number churn and swamp the real topology
// signal this artifact exists to surface. The contract is stated in
// KnowledgeGraphDiffDefinition; TestDiffGraphsEdgeAttrChangeIgnored locks it in.

// DiffGraphs reports how the graph changed from base to current: nodes added,
// removed, or changed (same ID, different data), and edges added or removed.
// baseLabel names the base revision (or baseline file) and is echoed into the result.
func DiffGraphs(baseLabel string, before, after types.KnowledgeGraphOutput) types.KnowledgeGraphDiff {
	out := types.KnowledgeGraphDiff{
		Definition:    types.KnowledgeGraphDiffDefinition,
		SchemaVersion: types.KnowledgeSchemaVersion,
		Base:          baseLabel,
	}

	beforeNodes := nodesByID(before.Nodes)
	afterNodes := nodesByID(after.Nodes)
	for id, a := range afterNodes {
		b, ok := beforeNodes[id]
		if !ok {
			out.NodesAdded = append(out.NodesAdded, a)
			continue
		}
		if fields := changedNodeFields(b, a); len(fields) > 0 {
			out.NodesChanged = append(out.NodesChanged, types.KnowledgeNodeChange{ID: id, Fields: fields, Before: b, After: a})
		}
	}
	for id, b := range beforeNodes {
		if _, ok := afterNodes[id]; !ok {
			out.NodesRemoved = append(out.NodesRemoved, b)
		}
	}

	beforeEdges := edgesByKey(before.Links)
	afterEdges := edgesByKey(after.Links)
	for k, e := range afterEdges {
		if _, ok := beforeEdges[k]; !ok {
			out.EdgesAdded = append(out.EdgesAdded, e)
		}
	}
	for k, e := range beforeEdges {
		if _, ok := afterEdges[k]; !ok {
			out.EdgesRemoved = append(out.EdgesRemoved, e)
		}
	}

	sortNodes(out.NodesAdded)
	sortNodes(out.NodesRemoved)
	slices.SortFunc(out.NodesChanged, func(x, y types.KnowledgeNodeChange) int { return cmp.Compare(x.ID, y.ID) })
	sortEdges(out.EdgesAdded)
	sortEdges(out.EdgesRemoved)
	return out
}

func nodesByID(nodes []types.KnowledgeNode) map[string]types.KnowledgeNode {
	m := make(map[string]types.KnowledgeNode, len(nodes))
	for _, n := range nodes {
		m[n.ID] = n
	}
	return m
}

func edgesByKey(edges []types.KnowledgeEdge) map[edgeKey]types.KnowledgeEdge {
	m := make(map[edgeKey]types.KnowledgeEdge, len(edges))
	for _, e := range edges {
		m[edgeKey{e.Source, e.Target, e.Relation}] = e
	}
	return m
}

// changedNodeFields returns the names of the fields that differ between two nodes with
// the same ID (empty when identical). Attrs are compared as maps.
func changedNodeFields(b, a types.KnowledgeNode) []string {
	var fields []string
	if b.Kind != a.Kind {
		fields = append(fields, "kind")
	}
	if b.Label != a.Label {
		fields = append(fields, "label")
	}
	if b.Doc != a.Doc {
		fields = append(fields, "doc")
	}
	if b.Source != a.Source {
		fields = append(fields, "source")
	}
	if !maps.Equal(b.Attrs, a.Attrs) {
		fields = append(fields, "attrs")
	}
	return fields
}

func sortNodes(nodes []types.KnowledgeNode) {
	slices.SortFunc(nodes, func(a, b types.KnowledgeNode) int { return cmp.Compare(a.ID, b.ID) })
}

func sortEdges(edges []types.KnowledgeEdge) {
	slices.SortFunc(edges, func(a, b types.KnowledgeEdge) int {
		if c := cmp.Compare(a.Source, b.Source); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Target, b.Target); c != 0 {
			return c
		}
		return cmp.Compare(a.Relation, b.Relation)
	})
}
