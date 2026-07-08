package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/knowledge"
	"github.com/egladman/magus/internal/render/md"
	"github.com/egladman/magus/types"
)

// graphDiff reports how the knowledge graph changed relative to a baseline export
// (`magus graph export -o json`): the nodes and edges added, removed, or changed. It
// is the PR-review blast-radius artifact - emit it as json or markdown for a CI
// comment. The baseline is a file today; a `[rev]` form that assembles the base graph
// from `git show`-ed content is a natural follow-up (the diff engine here is the reusable half).
func graphDiff(ctx context.Context, root string, args []string) error {
	var refresh, globalScope bool
	pos, err := cmdParse("graph diff", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&refresh, "refresh", false, "force a full graph rebuild of the current graph before diffing")
		fs.BoolVar(&globalScope, "global", false, "diff the global (all-workspaces) graph; match this to how the baseline was exported")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus graph diff <baseline.json> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.KnowledgeGraphDiffDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The argument is a whole-graph export produced earlier with `magus graph export -o json`")
			fmt.Fprintln(os.Stderr, "(e.g. on the base branch); the current working-tree graph is diffed against it. Symbol")
			fmt.Fprintln(os.Stderr, "shards in the baseline are matched automatically; pass --global if the baseline was global.")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		fmt.Fprintln(os.Stderr, "magus graph diff: requires a baseline graph export (from `magus graph export -o json`)")
		return errSilent{exitCode: 2}
	}
	baselinePath := pos[0]

	outOpts, err := ResolveOutput(global.output, outputMarkdown)
	if err != nil {
		return err
	}

	raw, err := os.ReadFile(baselinePath)
	if err != nil {
		return fmt.Errorf("graph diff: read baseline %q: %w", baselinePath, err)
	}
	var baseline types.KnowledgeGraphOutput
	if err := codec.Unmarshal(raw, &baseline); err != nil {
		return fmt.Errorf("graph diff: decode baseline %q (expected `magus graph export -o json` output): %w", baselinePath, err)
	}

	// Build the current graph to match the baseline's shape: if the baseline carries
	// symbol nodes (a symbol-seeded export), include symbols here too, else every
	// baseline symbol would surface as a spurious removal.
	g, err := loadKnowledgeGraph(ctx, root, refresh, globalScope, baselineHasSymbols(baseline))
	if err != nil {
		return err
	}
	diff := knowledge.DiffGraphs(baselinePath, baseline, g.Output())

	switch outOpts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(outOpts, diff)
	case outputMarkdown:
		_, err := os.Stdout.Write(renderDiffMarkdown(diff))
		return err
	case outputName:
		return diffNames(diff)
	}
	return diffText(diff)
}

// baselineHasSymbols reports whether an exported graph contains any symbol nodes, so the
// current graph can be built to match (see graphDiff).
func baselineHasSymbols(g types.KnowledgeGraphOutput) bool {
	for _, n := range g.Nodes {
		if n.Kind == types.KindSymbol {
			return true
		}
	}
	return false
}

// diffNames prints the changed node IDs one per line (added, removed, then changed),
// the `-o name` projection for piping into other tools.
func diffNames(d types.KnowledgeGraphDiff) error {
	for _, n := range d.NodesAdded {
		fmt.Println(n.ID)
	}
	for _, n := range d.NodesRemoved {
		fmt.Println(n.ID)
	}
	for _, c := range d.NodesChanged {
		fmt.Println(c.ID)
	}
	return nil
}

// diffText prints a plain-text summary of a graph diff.
func diffText(d types.KnowledgeGraphDiff) error {
	fmt.Printf("graph diff against %s\n", d.Base)
	fmt.Printf("  nodes: +%d -%d ~%d\n", len(d.NodesAdded), len(d.NodesRemoved), len(d.NodesChanged))
	fmt.Printf("  edges: +%d -%d\n", len(d.EdgesAdded), len(d.EdgesRemoved))
	for _, n := range d.NodesAdded {
		fmt.Printf("  + %s\n", n.ID)
	}
	for _, n := range d.NodesRemoved {
		fmt.Printf("  - %s\n", n.ID)
	}
	for _, c := range d.NodesChanged {
		fmt.Printf("  ~ %s (%v)\n", c.ID, c.Fields)
	}
	return nil
}

// renderDiffMarkdown renders a graph diff as a Markdown report for a CI comment.
func renderDiffMarkdown(d types.KnowledgeGraphDiff) []byte {
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
