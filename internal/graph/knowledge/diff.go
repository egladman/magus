package knowledge

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/render/md"
	"github.com/egladman/magus/types"
)

// Diffing is comparison, not analysis: given the base graph and the current graph
// (both already assembled), it reports the node/edge deltas. Nodes are keyed by ID,
// edges by (source, target, relation), the same identities the store and merge use,
// so the diff mirrors what a rebuild would change. Deterministic: every slice is
// sorted, so the same pair of graphs always yields byte-identical output.
//
// Nodes get field-level change tracking (changedNodeFields); edges do not. Edge
// identity IS (source, target, relation), so a re-scored or re-provenanced edge that
// keeps those three is intentionally not reported: tracking edge-attribute drift
// would be dominated by reference-edge line-number churn and swamp the real topology
// signal this artifact exists to surface. The contract is stated in
// KnowledgeGraphDiffDefinition; TestDiffGraphsEdgeAttrChangeIgnored locks it in.

// DiffGraphs reports how the graph changed from base to current: nodes added,
// removed, or changed (same ID, different data), and edges added or removed.
// baseLabel names the base revision (or baseline file) and is echoed into the result.
func DiffGraphs(baseLabel string, before, after types.KnowledgeGraphOutput) types.KnowledgeGraphDiff {
	out := types.KnowledgeGraphDiff{
		Definition: types.KnowledgeGraphDiffDefinition,
		APIVersion: types.KnowledgeAPIVersion,
		Base:       baseLabel,
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

// StripVCSAttrs removes the @vcs enrichment attrs (the "vcs_" namespace: last commit,
// date, count) from every node, so a graph diff reflects domain shape rather than which
// files got new commits since the base. See graphDiff for why both sides are stripped.
// It replaces (never mutates) a node's Attrs map: Graph.Output() shares the live graph's
// maps by reference, so deleting in place would corrupt the source graph.
func StripVCSAttrs(g *types.KnowledgeGraphOutput) {
	for i := range g.Nodes {
		src := g.Nodes[i].Attrs
		hasVCS := false
		for k := range src {
			if strings.HasPrefix(k, "vcs_") {
				hasVCS = true
				break
			}
		}
		if !hasVCS {
			continue // nothing to strip; leave the shared map untouched
		}
		kept := make(map[string]string, len(src))
		for k, v := range src {
			if !strings.HasPrefix(k, "vcs_") {
				kept[k] = v
			}
		}
		if len(kept) == 0 {
			kept = nil
		}
		g.Nodes[i].Attrs = kept
	}
}

// BaselineHasSymbols reports whether an exported graph contains any symbol nodes, so the
// current graph can be built to match (see graphDiff).
func BaselineHasSymbols(g types.KnowledgeGraphOutput) bool {
	for _, n := range g.Nodes {
		if n.Kind == types.KindSymbol {
			return true
		}
	}
	return false
}

// DiffNames lists the changed node IDs (added, removed, then changed), the `-o name`
// projection for piping into other tools.
func DiffNames(d types.KnowledgeGraphDiff) []string {
	names := make([]string, 0, len(d.NodesAdded)+len(d.NodesRemoved)+len(d.NodesChanged))
	for _, n := range d.NodesAdded {
		names = append(names, n.ID)
	}
	for _, n := range d.NodesRemoved {
		names = append(names, n.ID)
	}
	for _, c := range d.NodesChanged {
		names = append(names, c.ID)
	}
	return names
}

// DiffText prints a plain-text summary of a graph diff.
func DiffText(w io.Writer, d types.KnowledgeGraphDiff) error {
	fmt.Fprintf(w, "graph diff against %s\n", d.Base)
	fmt.Fprintf(w, "  nodes: +%d -%d ~%d\n", len(d.NodesAdded), len(d.NodesRemoved), len(d.NodesChanged))
	fmt.Fprintf(w, "  edges: +%d -%d\n", len(d.EdgesAdded), len(d.EdgesRemoved))
	for _, n := range d.NodesAdded {
		fmt.Fprintf(w, "  + %s\n", n.ID)
	}
	for _, n := range d.NodesRemoved {
		fmt.Fprintf(w, "  - %s\n", n.ID)
	}
	for _, c := range d.NodesChanged {
		fmt.Fprintf(w, "  ~ %s (%v)\n", c.ID, c.Fields)
	}
	return nil
}

// RenderDiffMarkdown renders a graph diff as a Markdown report for a CI comment.
func RenderDiffMarkdown(d types.KnowledgeGraphDiff) []byte {
	var b md.Builder
	b.Heading(1, "Knowledge graph diff")
	b.Paragraphf("Base: `%s`. Nodes +%d -%d ~%d; edges +%d -%d.",
		d.Base, len(d.NodesAdded), len(d.NodesRemoved), len(d.NodesChanged), len(d.EdgesAdded), len(d.EdgesRemoved))

	if len(d.NodesAdded) > 0 {
		b.Heading(2, "Nodes added")
		b.List(nodeItems(d.NodesAdded)...)
	}
	if len(d.NodesRemoved) > 0 {
		b.Heading(2, "Nodes removed")
		b.List(nodeItems(d.NodesRemoved)...)
	}
	if len(d.NodesChanged) > 0 {
		b.Heading(2, "Nodes changed")
		rows := make([][]string, len(d.NodesChanged))
		for i, c := range d.NodesChanged {
			rows[i] = []string{md.Code(c.ID), md.Codes(c.Fields), changeDetail(c)}
		}
		b.Table([]string{"Node", "Fields", "Before -> After"}, []md.Align{md.Left, md.Left, md.Left}, rows)
	}
	if len(d.EdgesAdded) > 0 {
		b.Heading(2, "Edges added")
		b.List(edgeItems(d.EdgesAdded)...)
	}
	if len(d.EdgesRemoved) > 0 {
		b.Heading(2, "Edges removed")
		b.List(edgeItems(d.EdgesRemoved)...)
	}
	return b.Bytes()
}

func nodeItems(nodes []types.KnowledgeNode) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = fmt.Sprintf("%s [%s]", md.Code(n.ID), n.Kind)
	}
	return out
}

func edgeItems(edges []types.KnowledgeEdge) []string {
	out := make([]string, len(edges))
	for i, e := range edges {
		out[i] = fmt.Sprintf("%s --%s--> %s", md.Code(e.Source), e.Relation, md.Code(e.Target))
	}
	return out
}

// changeDetail summarizes a changed node's before -> after values, one field per line,
// truncated so the Markdown table cell stays readable. Attrs are summarized as a marker
// rather than dumped (the full maps live in the json output's before/after).
func changeDetail(c types.KnowledgeNodeChange) string {
	parts := make([]string, 0, len(c.Fields))
	for _, f := range c.Fields {
		before, after := nodeField(c.Before, f), nodeField(c.After, f)
		parts = append(parts, fmt.Sprintf("%s: %s -> %s", f, md.Code(clip(before)), md.Code(clip(after))))
	}
	return strings.Join(parts, "<br>")
}

// nodeField returns a node's value for a diffable field name as a string. attrs is
// reported as a marker; the full map is in the json output.
func nodeField(n types.KnowledgeNode, field string) string {
	switch field {
	case "kind":
		return n.Kind
	case "label":
		return n.Label
	case "doc":
		return n.Doc
	case "source":
		return n.Source
	default: // attrs
		return fmt.Sprintf("%d attrs", len(n.Attrs))
	}
}

// clip shortens a value for a table cell, replacing empties with a visible marker.
func clip(s string) string {
	if s == "" {
		return "(empty)"
	}
	const max = 40
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}
