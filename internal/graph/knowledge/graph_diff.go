package knowledge

import (
	"fmt"
	"io"
	"strings"

	"github.com/egladman/magus/internal/render/md"
	"github.com/egladman/magus/types"
)

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
