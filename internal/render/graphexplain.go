package render

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// This file renders `magus explain` and `magus path` as compact, human-readable
// text - the DEFAULT output for both the CLI and the MCP tools. It replaces the
// old adjacency notation (`<--uses-- op:go:go-build [op]`), which forced the reader
// to mentally invert the arrow. Here the edge direction is folded into a natural
// verb (active when the focus node is the source, passive when it is the target),
// edges are grouped by that verb with a count, and the FULL node IDs are listed
// (the exact token to pass to the next explain), so one rendering serves humans,
// agents that read, and the man pages. `-o json` remains the structured opt-in.

// relationPhrase maps an edge relation to its natural-language rendering in each
// direction: [active, passive]. Active is used when the focus node is the edge
// source (an out edge); passive when it is the target (an in edge). So an
// `op --uses--> tool` edge reads "uses" from the op and "used by" from the tool.
var relationPhrase = map[string][2]string{
	types.RelationUses:         {"uses", "used by"},
	types.RelationDependsOn:    {"depends on", "required by"},
	types.RelationContains:     {"contains", "part of"},
	types.RelationReferences:   {"references", "referenced by"},
	types.RelationDocuments:    {"documents", "documented by"},
	types.RelationCalls:        {"calls", "called by"},
	types.RelationImports:      {"imports", "imported by"},
	types.RelationEmits:        {"emits", "emitted by"},
	types.RelationOwns:         {"owns", "owned by"},
	types.RelationDefines:      {"defines", "defined by"},
	types.RelationRationaleFor: {"explains", "explained by"},
	types.RelationProduces:     {"produces", "produced by"},
	types.RelationConsumes:     {"consumes", "consumed by"},
	types.RelationAuthored:     {"authored", "authored by"},
}

// phraseFor returns the natural-language verb for a relation in the given
// direction, falling back to the raw relation name (with an "-> "/"<- " marker for
// an unknown relation so the direction is never lost).
func phraseFor(relation string, active bool) string {
	if p, ok := relationPhrase[relation]; ok {
		if active {
			return p[0]
		}
		return p[1]
	}
	if active {
		return relation + " ->"
	}
	return "<- " + relation
}

// wrapCol is the target line width for wrapped ID lists.
const wrapCol = 80

// ExplainText renders one node's context card: its identity and attrs, then its
// relationships grouped by natural-language verb, each group listing the full IDs.
func ExplainText(out types.KnowledgeExplainOutput) string {
	var b strings.Builder
	n := out.Node
	fmt.Fprintf(&b, "%s   %s\n", n.ID, n.Kind)
	if n.Doc != "" {
		fmt.Fprintf(&b, "%s\n", n.Doc)
	}
	if n.Source != "" {
		fmt.Fprintf(&b, "source: %s\n", n.Source)
	}
	for _, k := range slices.Sorted(maps.Keys(n.Attrs)) {
		fmt.Fprintf(&b, "%s: %s\n", k, n.Attrs[k])
	}
	if out.BlastRadius > 0 {
		reach := "nodes reach"
		if out.BlastRadius == 1 {
			reach = "node reaches"
		}
		fmt.Fprintf(&b, "%d %s this\n", out.BlastRadius, reach)
	}

	groups := relationGroups(out)
	if len(groups) > 0 {
		b.WriteByte('\n')
		w := 0
		for _, g := range groups {
			if len(g.header) > w {
				w = len(g.header)
			}
		}
		if w > 18 { // keep a runaway phrase from blowing out the column
			w = 18
		}
		for _, g := range groups {
			writeGroup(&b, g.header, g.ids, w)
		}
	}
	return b.String()
}

type relationGroup struct {
	header string
	ids    []string
}

// relationGroups buckets a node's edges by (direction, relation) in first-seen
// order - out edges (active voice) first, then in edges (passive) - and labels
// each with its verb plus a count when more than one, so a reader never miscounts
// a long list. The bucket order within out/in follows the deterministic edge order
// Explain already produces.
func relationGroups(out types.KnowledgeExplainOutput) []relationGroup {
	build := func(edges []types.KnowledgeEdgeRef, active bool) []relationGroup {
		var order []string
		byRel := map[string][]string{}
		for _, e := range edges {
			if _, seen := byRel[e.Relation]; !seen {
				order = append(order, e.Relation)
			}
			byRel[e.Relation] = append(byRel[e.Relation], e.Other)
		}
		groups := make([]relationGroup, 0, len(order))
		for _, rel := range order {
			ids := byRel[rel]
			header := phraseFor(rel, active)
			if len(ids) > 1 {
				header = fmt.Sprintf("%s (%d)", header, len(ids))
			}
			groups = append(groups, relationGroup{header: header, ids: ids})
		}
		return groups
	}
	return append(build(out.Out, true), build(out.In, false)...)
}

// writeGroup prints "<header>  <id>, <id>, ...", wrapping the ID list at wrapCol
// with a hanging indent aligned under the first ID, so a group reads as one
// labelled row however long its list. The trailing comma on a wrapped line signals
// the list continues.
func writeGroup(b *strings.Builder, header string, ids []string, w int) {
	indent := w + 2
	line := fmt.Sprintf("%-*s  ", w, header)
	col := indent
	for i, id := range ids {
		sep := ""
		if i > 0 {
			sep = ", "
		}
		if i > 0 && col+len(sep)+len(id) > wrapCol {
			line += ","
			b.WriteString(line)
			b.WriteByte('\n')
			line = strings.Repeat(" ", indent)
			col = indent
			sep = ""
		}
		line += sep + id
		col += len(sep) + len(id)
	}
	b.WriteString(line)
	b.WriteByte('\n')
}

// PathText renders a shortest path as the chain of natural-language steps from the
// source to the target, one labelled step per hop (direction folded into the verb).
func PathText(out types.KnowledgePathOutput) string {
	var b strings.Builder
	steps := "no path"
	if out.Found {
		steps = fmt.Sprintf("%d step", len(out.Steps))
		if len(out.Steps) != 1 {
			steps += "s"
		}
	}
	fmt.Fprintf(&b, "%s -> %s  (%s)\n", out.From, out.To, steps)
	if !out.Found {
		fmt.Fprintf(&b, "\nno path connects these nodes\n")
		return b.String()
	}
	w := 0
	for _, s := range out.Steps {
		if p := phraseFor(s.Relation, s.Forward); len(p) > w {
			w = len(p)
		}
	}
	fmt.Fprintf(&b, "\n%s\n", out.From)
	for _, s := range out.Steps {
		fmt.Fprintf(&b, "  %-*s  %s\n", w, phraseFor(s.Relation, s.Forward), s.To)
	}
	return b.String()
}
