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
// Git-derived data goes to History, out of the structural deltas, since a --rev base
// has none. Neither input is modified.
func DiffGraphs(baseLabel string, before, after types.KnowledgeGraphOutput) types.KnowledgeGraphDiff {
	beforeShape, beforeHistory := splitHistory(before)
	afterShape, afterHistory := splitHistory(after)

	out := diffSides(beforeShape, afterShape)
	out.Definition = types.KnowledgeGraphDiffDefinition
	out.APIVersion = types.KnowledgeAPIVersion
	out.Base = baseLabel
	out.History = diffHistory(beforeHistory, afterHistory)
	return out
}

// historySide is one graph's history projection. carriers counts the nodes holding
// history and edges is how many history edges it has; both zero means none at all.
type historySide struct {
	graph    types.KnowledgeGraphOutput
	carriers int
	edges    int
}

func (s historySide) present() bool { return s.carriers+s.edges > 0 }

// diffHistory returns nil when the projections agree. When only one side has history,
// listing it would only restate that side, so the note counts it instead.
func diffHistory(before, after historySide) *types.KnowledgeGraphHistoryDiff {
	switch {
	case !before.present() && after.present():
		return &types.KnowledgeGraphHistoryDiff{Note: fmt.Sprintf(
			"not compared: the base carries no version-control history, and the current side has it on %d nodes and %d edges",
			after.carriers, after.edges)}
	case before.present() && !after.present():
		return &types.KnowledgeGraphHistoryDiff{Note: fmt.Sprintf(
			"not compared: the current side carries no version-control history, and the base has it on %d nodes and %d edges",
			before.carriers, before.edges)}
	}
	// A node the tree added or removed is already a structural change, so only author
	// nodes are added or removed here.
	d := diffSides(before.graph, after.graph)
	notAuthor := func(n types.KnowledgeNode) bool { return n.Kind != types.KindAuthor }
	h := types.KnowledgeGraphHistoryDiff{
		NodesAdded:   nilIfNoNodes(slices.DeleteFunc(d.NodesAdded, notAuthor)),
		NodesRemoved: nilIfNoNodes(slices.DeleteFunc(d.NodesRemoved, notAuthor)),
		NodesChanged: d.NodesChanged,
		EdgesAdded:   d.EdgesAdded,
		EdgesRemoved: d.EdgesRemoved,
	}
	if len(h.NodesAdded)+len(h.NodesRemoved)+len(h.NodesChanged)+len(h.EdgesAdded)+len(h.EdgesRemoved) == 0 {
		return nil
	}
	return &h
}

func nilIfNoNodes(nodes []types.KnowledgeNode) []types.KnowledgeNode {
	if len(nodes) == 0 {
		return nil
	}
	return nodes
}

// splitHistory partitions a graph into its structure and its history projection.
// Graph.Output shares attr maps with the live graph, so a split node gets new maps.
func splitHistory(g types.KnowledgeGraphOutput) (types.KnowledgeGraphOutput, historySide) {
	var shape types.KnowledgeGraphOutput
	var hist historySide
	for _, n := range g.Nodes {
		if n.Kind == types.KindAuthor {
			hist.graph.Nodes = append(hist.graph.Nodes, n)
			hist.carriers++
			continue
		}
		structural, history := splitAttrs(n.Attrs)
		if history != nil {
			hist.carriers++
		}
		hist.graph.Nodes = append(hist.graph.Nodes, types.KnowledgeNode{ID: n.ID, Kind: n.Kind, Attrs: history})
		n.Attrs = structural
		shape.Nodes = append(shape.Nodes, n)
	}
	for _, e := range g.Links {
		if e.Relation == types.RelationAuthored || e.Relation == types.RelationCoChanges {
			hist.graph.Links = append(hist.graph.Links, e)
			hist.edges++
			continue
		}
		shape.Links = append(shape.Links, e)
	}
	return shape, hist
}

// splitAttrs returns attrs without its history keys, then only those keys. An empty half
// is nil: an empty map would read as a change against a side that never had the keys.
func splitAttrs(attrs map[string]string) (structural, history map[string]string) {
	for k, v := range attrs {
		if !IsHistoryAttr(k) {
			continue
		}
		if history == nil {
			history = map[string]string{}
		}
		history[k] = v
	}
	if history == nil {
		return attrs, nil
	}
	for k, v := range attrs {
		if IsHistoryAttr(k) {
			continue
		}
		if structural == nil {
			structural = map[string]string{}
		}
		structural[k] = v
	}
	return structural, history
}

// diffSides computes the sorted node and edge deltas between two graphs.
func diffSides(before, after types.KnowledgeGraphOutput) types.KnowledgeGraphDiff {
	var out types.KnowledgeGraphDiff
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

// DiffText prints a plain-text summary of a graph diff. It counts instead of listing
// two kinds of line that follow from one already printed. One is a node that only moved
// within its file; the other is an edge of a removed node. -o json lists both.
func DiffText(w io.Writer, d types.KnowledgeGraphDiff) error {
	fmt.Fprintf(w, "graph diff against %s\n", d.Base)
	fmt.Fprintf(w, "  nodes: +%d -%d ~%d\n", len(d.NodesAdded), len(d.NodesRemoved), len(d.NodesChanged))
	fmt.Fprintf(w, "  edges: +%d -%d\n", len(d.EdgesAdded), len(d.EdgesRemoved))
	for _, n := range d.NodesAdded {
		fmt.Fprintf(w, "  + %s\n", n.ID)
	}
	removed := make(map[string]bool, len(d.NodesRemoved))
	for _, n := range d.NodesRemoved {
		removed[n.ID] = true
		fmt.Fprintf(w, "  - %s\n", n.ID)
	}
	moved := 0
	for _, c := range d.NodesChanged {
		if movedOnly(c) {
			moved++
			continue
		}
		fmt.Fprintf(w, "  ~ %s (%v)\n", c.ID, c.Fields)
	}
	if moved > 0 {
		fmt.Fprintf(w, "  ~ %d more nodes moved within their file (source line only)\n", moved)
	}
	for _, e := range d.EdgesAdded {
		fmt.Fprintf(w, "  + %s --%s--> %s\n", e.Source, e.Relation, e.Target)
	}
	orphaned := 0
	for _, e := range d.EdgesRemoved {
		if removed[e.Source] || removed[e.Target] {
			orphaned++
			continue
		}
		fmt.Fprintf(w, "  - %s --%s--> %s\n", e.Source, e.Relation, e.Target)
	}
	if orphaned > 0 {
		fmt.Fprintf(w, "  - %d more edges of the removed nodes\n", orphaned)
	}
	if d.History != nil {
		fmt.Fprintf(w, "  %s\n", historySummary(d.History))
	}
	return nil
}

// movedOnly reports a change whose one difference is the line in a source "path:line".
func movedOnly(c types.KnowledgeNodeChange) bool {
	if !slices.Equal(c.Fields, []string{"source"}) {
		return false
	}
	b, a := strings.LastIndexByte(c.Before.Source, ':'), strings.LastIndexByte(c.After.Source, ':')
	return b > 0 && a > 0 && c.Before.Source[:b] == c.After.Source[:a]
}

// historySummary is the one line text and Markdown give the history section; -o json
// lists it.
func historySummary(h *types.KnowledgeGraphHistoryDiff) string {
	if h.Note != "" {
		return fmt.Sprintf("History, %s.", h.Note)
	}
	return fmt.Sprintf("History, not counted above: nodes +%d -%d ~%d; edges +%d -%d.",
		len(h.NodesAdded), len(h.NodesRemoved), len(h.NodesChanged), len(h.EdgesAdded), len(h.EdgesRemoved))
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
	if d.History != nil {
		b.Heading(2, "History")
		b.Paragraph(historySummary(d.History))
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
