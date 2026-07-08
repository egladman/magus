package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	magus "github.com/egladman/magus"
	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/knowledge"
	"github.com/egladman/magus/internal/render/md"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// graphDiff reports how the knowledge graph changed relative to a baseline: the nodes
// and edges added, removed, or changed. It is the PR-review blast-radius artifact - emit
// it as json or markdown for a CI comment. The baseline is either an export file
// (`magus graph export -o json`, e.g. from the base branch) or, with --rev, a git
// revision whose tracked files are built into a base graph on the fly.
func graphDiff(ctx context.Context, root string, args []string) error {
	var refresh, globalScope bool
	var rev string
	pos, err := cmdParse("graph diff", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&refresh, "refresh", false, "force a full graph rebuild of the current graph before diffing")
		fs.BoolVar(&globalScope, "global", false, "diff the global (all-workspaces) graph; match this to how the baseline was exported")
		fs.StringVar(&rev, "rev", "", "diff against a git revision (e.g. HEAD~1, main) instead of an export file")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus graph diff <baseline.json> [flags]")
			fmt.Fprintln(os.Stderr, "       magus graph diff --rev <revision> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.KnowledgeGraphDiffDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The positional argument is a whole-graph export produced earlier with")
			fmt.Fprintln(os.Stderr, "`magus graph export -o json`; the current working-tree graph is diffed against it.")
			fmt.Fprintln(os.Stderr, "Symbol shards in the baseline are matched automatically; pass --global if it was global.")
			fmt.Fprintln(os.Stderr, "With --rev, the base graph is built from that revision's tracked files (domain-only,")
			fmt.Fprintln(os.Stderr, "using the current config); no export file is needed. --rev and the positional are exclusive.")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	gotRev, gotFile := rev != "", len(pos) > 0
	if gotRev == gotFile { // neither or both
		fmt.Fprintln(os.Stderr, "magus graph diff: give exactly one of a baseline export file or --rev <revision>")
		return errSilent{exitCode: 2}
	}
	if gotFile && len(pos) > 1 {
		fmt.Fprintln(os.Stderr, "magus graph diff: takes a single baseline export file")
		return errSilent{exitCode: 2}
	}
	if gotRev && globalScope {
		// A --rev base is always a single-workspace domain-only build, while --global
		// qualifies the current side's node IDs by workspace; diffing the two would
		// report every node added/removed. Reject rather than emit a garbage diff.
		fmt.Fprintln(os.Stderr, "magus graph diff: --rev cannot be combined with --global (the base is built single-workspace)")
		return errSilent{exitCode: 2}
	}

	outOpts, err := ResolveOutput(global.output, outputMarkdown)
	if err != nil {
		return err
	}

	baseline, baseLabel, err := diffBaseline(ctx, root, rev, pos)
	if err != nil {
		return err
	}

	// Build the current graph to match the baseline's shape: if the baseline carries
	// symbol nodes (a symbol-seeded export), include symbols here too, else every
	// baseline symbol would surface as a spurious removal. A --rev baseline is always
	// domain-only (an archive has no built .scip indexes), so the current side is too.
	g, err := loadKnowledgeGraph(ctx, root, refresh, globalScope, baselineHasSymbols(baseline))
	if err != nil {
		return err
	}
	current := g.Output()
	// @vcs enrichment (opt-in) stamps commit-varying attrs on file nodes. They are not
	// domain shape, and the base side never has them (a --rev export tree has no history,
	// and an exported baseline predates the current commit), so keeping them would report
	// nearly every file node as changed. Strip them from both sides for a structural diff.
	stripVCSAttrs(&baseline)
	stripVCSAttrs(&current)
	diff := knowledge.DiffGraphs(baseLabel, baseline, current)

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

// diffBaseline resolves the base graph to diff against: either a git revision (--rev,
// built from that revision's tracked files) or a positional export file. It returns the
// base graph and a human label for it (the revision or the file path).
func diffBaseline(ctx context.Context, root, rev string, pos []string) (types.KnowledgeGraphOutput, string, error) {
	if rev != "" {
		g, err := baseGraphFromRev(ctx, root, rev)
		if err != nil {
			return types.KnowledgeGraphOutput{}, "", err
		}
		return g, rev, nil
	}
	baselinePath := pos[0]
	raw, err := os.ReadFile(baselinePath)
	if err != nil {
		return types.KnowledgeGraphOutput{}, "", fmt.Errorf("graph diff: read baseline %q: %w", baselinePath, err)
	}
	var baseline types.KnowledgeGraphOutput
	if err := codec.Unmarshal(raw, &baseline); err != nil {
		return types.KnowledgeGraphOutput{}, "", fmt.Errorf("graph diff: decode baseline %q (expected `magus graph export -o json` output): %w", baselinePath, err)
	}
	return baseline, baselinePath, nil
}

// stripVCSAttrs removes the @vcs enrichment attrs (the "vcs_" namespace: last commit,
// date, count) from every node, so a graph diff reflects domain shape rather than which
// files got new commits since the base. See graphDiff for why both sides are stripped.
// It replaces (never mutates) a node's Attrs map: Graph.Output() shares the live graph's
// maps by reference, so deleting in place would corrupt the source graph.
func stripVCSAttrs(g *types.KnowledgeGraphOutput) {
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

// baseGraphFromRev builds a base knowledge graph from a revision's tracked files. It
// materializes the revision into a throwaway temp tree via the VCS abstraction (any
// backend implementing RevisionExporter; git does, and one that does not gives a clear
// error), then runs the ordinary extraction pipeline there via a direct Inspect (NOT the
// memoized inspectWorkspace, which panics on a second root). The result is domain-only
// and reflects the CURRENT config applied to the revision's files - a historical-config
// diff would need the rev's own config threaded through, deliberately out of scope here.
//
// The base build is pinned to an isolated, immutable cache under the temp tree: without
// this, an absolute cache.dir / MAGUS_CACHE_DIR would make resolveCacheDir ignore the
// temp root, and the base build would read the CURRENT workspace's shards as its starting
// point (corrupting the base) and prune live shards on write. Isolated + immutable means
// it assembles in memory and touches nothing outside the temp tree.
func baseGraphFromRev(ctx context.Context, root, rev string) (types.KnowledgeGraphOutput, error) {
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.Source == types.VCSSourceDisabled || res.VCS == nil {
		return types.KnowledgeGraphOutput{}, fmt.Errorf("graph diff: --rev needs version control, but none resolved for this workspace")
	}
	exporter, ok := res.VCS.(types.RevisionExporter)
	if !ok {
		return types.KnowledgeGraphOutput{}, fmt.Errorf("graph diff: --rev is not supported by %s; export a baseline with `graph export -o json` instead", res.Name)
	}

	tmp, err := os.MkdirTemp("", "magus-graph-diff-")
	if err != nil {
		return types.KnowledgeGraphOutput{}, fmt.Errorf("graph diff: create temp tree: %w", err)
	}
	defer os.RemoveAll(tmp)

	if err := exporter.ExportRevision(ctx, root, rev, tmp); err != nil {
		return types.KnowledgeGraphOutput{}, fmt.Errorf("graph diff: export revision %q: %w", rev, err)
	}

	cfg := globalCfg
	cfg.Cache.Dir = filepath.Join(tmp, ".magus-base-cache") // absolute -> wins over any env cache dir
	cfg.Cache.Immutable = true
	// The exported tree has no VCS metadata, so history enrichment can't run against it;
	// disable it so the base is not asymmetrically missing @vcs attrs the current side has.
	cfg.Knowledge.VCS.Enabled = false

	ws, err := magus.Inspect(ctx, tmp, magus.WithLoadedConfig(cfg))
	if err != nil {
		return types.KnowledgeGraphOutput{}, fmt.Errorf("graph diff: inspect revision %q tree: %w", rev, err)
	}
	g, err := magus.BuildKnowledgeGraph(ctx, ws, ws.Root(), cfg, false, slog.Default())
	if err != nil {
		return types.KnowledgeGraphOutput{}, fmt.Errorf("graph diff: build graph for revision %q: %w", rev, err)
	}
	return g.Output(), nil
}
