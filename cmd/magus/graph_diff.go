package main

import (
	"archive/tar"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	magus "github.com/egladman/magus"
	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/knowledge"
	"github.com/egladman/magus/internal/render/md"
	"github.com/egladman/magus/types"
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
	diff := knowledge.DiffGraphs(baseLabel, baseline, g.Output())

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

// baseGraphFromRev builds a base knowledge graph from a git revision's tracked files.
// It streams `git archive <rev>` into a throwaway temp tree, then runs the ordinary
// extraction pipeline there via a direct Inspect (NOT the memoized inspectWorkspace,
// which panics on a second root). The result is domain-only and reflects the CURRENT
// config applied to the revision's files - a historical-config diff would need the rev's
// own config threaded through, which is deliberately out of scope here.
//
// The base build is pinned to an isolated, immutable cache under the temp tree: without
// this, an absolute cache.dir / MAGUS_CACHE_DIR would make resolveCacheDir ignore the
// temp root, and the base build would read the CURRENT workspace's shards as its starting
// point (corrupting the base) and prune live shards on write. Isolated + immutable means
// it assembles in memory and touches nothing outside the temp tree.
func baseGraphFromRev(ctx context.Context, root, rev string) (types.KnowledgeGraphOutput, error) {
	tmp, err := os.MkdirTemp("", "magus-graph-diff-")
	if err != nil {
		return types.KnowledgeGraphOutput{}, fmt.Errorf("graph diff: create temp tree: %w", err)
	}
	defer os.RemoveAll(tmp)

	if err := gitArchiveTo(ctx, root, rev, tmp); err != nil {
		return types.KnowledgeGraphOutput{}, err
	}

	cfg := globalCfg
	cfg.Cache.Dir = filepath.Join(tmp, ".magus-base-cache") // absolute -> wins over any env cache dir
	cfg.Cache.Immutable = true

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

// gitArchiveTo extracts the tracked files at rev into dstDir, re-rooted at the magus
// root. `git -C root archive <rev> -- .` limits the archive to root's own subtree and
// emits paths relative to it, so the temp tree lines up with a plain Inspect(dstDir)
// whether root is the git top-level or a nested subdir. It pipes through the stdlib tar
// reader so no `tar` binary is required, and rejects any entry whose path escapes dstDir.
func gitArchiveTo(ctx context.Context, root, rev, dstDir string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "archive", "--format=tar", rev, "--", ".")
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("graph diff: git archive pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("graph diff: start git archive: %w", err)
	}

	extractErr := extractTar(stdout, dstDir)
	// Drain any unread archive bytes unconditionally (even after an extract error) so git
	// never blocks writing to a full pipe; only then is it safe to Wait (Wait closes the
	// pipe). This ordering is load-bearing.
	_, _ = io.Copy(io.Discard, stdout)
	if waitErr := cmd.Wait(); waitErr != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return fmt.Errorf("graph diff: git archive %q failed: %s (check the revision exists and has tracked files here)", rev, msg)
	}
	if extractErr != nil {
		return fmt.Errorf("graph diff: extract revision %q archive: %w", rev, extractErr)
	}
	return nil
}

// extractTar writes a tar stream into dstDir, creating parent directories as needed and
// refusing any entry whose path would escape dstDir (git archive never emits such an
// entry; the guard is defense in depth against a crafted path). Symlinks, hardlinks, and
// other special entries are skipped with a debug log: the extraction feeds a read-only
// graph build, so an escaping or dangling link has no value there.
func extractTar(r io.Reader, dstDir string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if !filepath.IsLocal(hdr.Name) {
			return fmt.Errorf("archive entry %q escapes the destination", hdr.Name)
		}
		target := filepath.Join(dstDir, filepath.FromSlash(hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeTarEntry(tr, target, hdr); err != nil {
				return err
			}
		default:
			slog.Default().Debug("graph diff: skipping non-regular archive entry", "name", hdr.Name, "typeflag", hdr.Typeflag)
		}
	}
}

// writeTarEntry writes one regular-file entry to target.
func writeTarEntry(tr *tar.Reader, target string, hdr *tar.Header) error {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, tr); err != nil {
		f.Close()
		return fmt.Errorf("write %q: %w", hdr.Name, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %q: %w", hdr.Name, err)
	}
	return nil
}
