// Package render contains graph presentation helpers: ASCII tree, DOT, and
// Mermaid formatters. These were moved out of the public magus package so the
// public surface stays free of formatting details.
package render

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/types"
)

// RenderOption configures a WriteTree call.
type RenderOption func(*renderConfig)

type renderConfig struct {
	roots    []string
	dir      types.Direction
	maxDepth int
	spell    string
}

// WithRoots restricts the tree to subtrees rooted at the named projects.
func WithRoots(paths ...string) RenderOption {
	return func(c *renderConfig) { c.roots = paths }
}

// WithDirection sets the traversal direction (Downstream or Upstream).
func WithDirection(d types.Direction) RenderOption {
	return func(c *renderConfig) { c.dir = d }
}

// WithMaxDepth caps the depth of the rendered tree. 0 means unlimited.
func WithMaxDepth(depth int) RenderOption {
	return func(c *renderConfig) { c.maxDepth = depth }
}

// WithSpell filters the tree to projects of the given spell name
// ("go", "rust", "typescript"). Projects of other spells are skipped
// from display, and their subtrees are not traversed.
func WithSpell(name string) RenderOption {
	return func(c *renderConfig) { c.spell = name }
}

// WriteTree writes a deterministic ASCII dependency tree to w. Writer-first, like
// its WriteGraph*/WriteTargetGraph* siblings.
func WriteTree(w io.Writer, g *types.Graph, opts ...RenderOption) error {
	cfg := &renderConfig{}
	for _, o := range opts {
		o(cfg)
	}

	adjFn := func(path string) []string {
		if cfg.dir == types.Downstream {
			return g.Successors(path)
		}
		return g.Predecessors(path)
	}

	roots := cfg.roots
	if len(roots) == 0 {
		roots = resolveRoots(g, cfg)
	}
	slices.Sort(roots)

	for _, root := range roots {
		p := g.Project(root)
		if p == nil {
			continue
		}
		if cfg.spell != "" && !hasSpellName(p, cfg.spell) {
			continue
		}
		visited := map[string]bool{}
		if err := renderStringNode(w, root, adjFn, g, cfg, visited, "", 0); err != nil {
			return err
		}
	}
	return nil
}

func resolveRoots(g *types.Graph, cfg *renderConfig) []string {
	if cfg.spell != "" {
		var roots []string
		for _, path := range g.Nodes() {
			p := g.Project(path)
			if p == nil || !hasSpellName(p, cfg.spell) {
				continue
			}
			hasSameSpellPred := false
			for _, predPath := range g.Predecessors(path) {
				pp := g.Project(predPath)
				if pp != nil && hasSpellName(pp, cfg.spell) {
					hasSameSpellPred = true
					break
				}
			}
			if !hasSameSpellPred {
				roots = append(roots, path)
			}
		}
		if len(roots) == 0 {
			for _, path := range g.Nodes() {
				if p := g.Project(path); p != nil && hasSpellName(p, cfg.spell) {
					roots = append(roots, path)
				}
			}
		}
		return roots
	}

	var roots []string
	if cfg.dir == types.Downstream {
		for _, path := range g.Nodes() {
			if len(g.Predecessors(path)) == 0 {
				roots = append(roots, path)
			}
		}
	} else {
		for _, path := range g.Nodes() {
			if len(g.Successors(path)) == 0 {
				roots = append(roots, path)
			}
		}
	}
	if len(roots) == 0 {
		roots = append(roots, g.Nodes()...)
	}
	return roots
}

func hasSpellName(p *types.Project, name string) bool {
	return p.Spell == name || slices.Contains(p.Spells, name)
}

func renderStringNode(w io.Writer, path string, adjFn func(string) []string,
	g *types.Graph, cfg *renderConfig, visited map[string]bool, prefix string, depth int,
) error {
	if visited[path] {
		_, err := fmt.Fprintf(w, "%s (visited)\n", path)
		return err
	}
	visited[path] = true
	if _, err := fmt.Fprintln(w, path); err != nil {
		return err
	}

	if cfg.maxDepth > 0 && depth >= cfg.maxDepth {
		return nil
	}

	children := adjFn(path)
	if cfg.spell != "" {
		var filtered []string
		for _, c := range children {
			if cp := g.Project(c); cp != nil && hasSpellName(cp, cfg.spell) {
				filtered = append(filtered, c)
			}
		}
		children = filtered
	}

	for i, child := range children {
		isLast := i == len(children)-1
		connector, extension := "├── ", "│   "
		if isLast {
			connector, extension = "└── ", "    "
		}
		if _, err := fmt.Fprintf(w, "%s%s", prefix, connector); err != nil {
			return err
		}
		if err := renderStringNode(w, child, adjFn, g, cfg, visited, prefix+extension, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// spellPalette maps spell names to fill colors. Unknown spells fall back
// to the unspelled color.
var spellPalette = map[string]struct{ fill, text string }{
	"go":         {"#00ADD8", "#fff"},
	"rust":       {"#DEA584", "#000"},
	"typescript": {"#3178C6", "#fff"},
	"docker":     {"#2496ED", "#fff"},
	"bash":       {"#4EAA25", "#000"},
	"teal":       {"#5d4d7a", "#fff"},
	"unspelled":  {"#888888", "#fff"},
}

func spellColor(name string) (fill, text string) {
	if c, ok := spellPalette[name]; ok {
		return c.fill, c.text
	}
	return spellPalette["unspelled"].fill, spellPalette["unspelled"].text
}

// mermaidID converts a project path into a valid Mermaid node identifier.
func mermaidID(path string) string {
	var b strings.Builder
	for _, r := range path {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

// mermaidIDs assigns each path a unique Mermaid-safe node id, de-duplicated with a
// numeric suffix on collision. It sorts first so the path that keeps the bare id on
// a sanitised-name collision is deterministic regardless of input order (the output
// feeds the MAGUS.md drift gate).
func mermaidIDs(paths []string) map[string]string {
	sorted := slices.Clone(paths)
	slices.Sort(sorted)
	ids := make(map[string]string, len(sorted))
	seen := make(map[string]int)
	for _, p := range sorted {
		id := mermaidID(p)
		if count := seen[id]; count > 0 {
			id = fmt.Sprintf("%s_%d", id, count)
		}
		seen[id]++
		ids[p] = id
	}
	return ids
}

// FormatDuration formats a duration as a human-readable string.
func FormatDuration(d time.Duration) string {
	ms := d.Milliseconds()
	switch {
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms < 60000:
		s := float64(ms) / 1000
		return fmt.Sprintf("%.4gs", s)
	default:
		sec := ms / 1000
		m := sec / 60
		s := sec % 60
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm%ds", m, s)
	}
}

// WriteGraphDOT emits a deterministic Graphviz DOT digraph to w (rankdir=LR, paths quoted).
func WriteGraphDOT(w io.Writer, out types.GraphOutput) error {
	return writeDOT(w, projectGraphIR(out))
}

// WriteGraphMermaid emits a Mermaid flowchart with spell subgraphs, BR/duration labels,
// cross-spell edge labels, exclusive hexagons, and click-to-dir handlers.
func WriteGraphMermaid(w io.Writer, out types.GraphOutput) error {
	return writeMermaid(w, projectGraphIR(out))
}

// projectGraphIR maps the project dependency graph onto the shared renderGraph:
// spell buckets become subgraphs (and classes), blast-radius/duration ride in the
// node label, exclusive nodes become hexagons, a cross-spell edge carries the
// dependency's spell as its label, and a project dir becomes a click handler.
func projectGraphIR(out types.GraphOutput) renderGraph {
	paths := make([]string, len(out.Nodes))
	for i, n := range out.Nodes {
		paths[i] = n.Path
	}
	ids := mermaidIDs(paths)

	spellOf := make(map[string]string, len(out.Nodes))
	bucketSet := map[string]bool{}
	for _, n := range out.Nodes {
		key := n.SpellName
		if key == "" {
			key = "unspelled"
		}
		spellOf[n.Path] = key
		bucketSet[key] = true
	}
	bucketKeys := make([]string, 0, len(bucketSet))
	for k := range bucketSet {
		bucketKeys = append(bucketKeys, k)
	}
	slices.Sort(bucketKeys)

	rootSet := make(map[string]bool, len(out.Roots))
	for _, r := range out.Roots {
		rootSet[r] = true
	}

	title := "magus dependency graph (" + out.Direction
	if out.SpellName != "" {
		title += ", spell=" + out.SpellName
	}
	title += ")"
	g := renderGraph{Title: title, DOTName: "magus"}

	for _, key := range bucketKeys {
		g.Groups = append(g.Groups, renderGroup{ID: "spell_" + mermaidID(key), Label: key})
	}
	// Nodes, bucket by bucket and sorted within, for deterministic output.
	for _, key := range bucketKeys {
		group := "spell_" + mermaidID(key)
		var bucket []types.Node
		for _, n := range out.Nodes {
			if spellOf[n.Path] == key {
				bucket = append(bucket, n)
			}
		}
		slices.SortFunc(bucket, func(a, b types.Node) int { return strings.Compare(a.Path, b.Path) })
		for _, n := range bucket {
			label := n.Path
			if n.BlastRadius > 0 {
				label += fmt.Sprintf("<br/>BR=%d", n.BlastRadius)
			}
			if n.DurationMs > 0 {
				label += "<br/>~" + FormatDuration(time.Duration(n.DurationMs)*time.Millisecond)
			}
			shape := shapeBox
			if n.Exclusive {
				shape = shapeHexagon
			}
			classes := []string{group}
			if rootSet[n.Path] {
				classes = append(classes, "root")
			}
			rn := renderNode{ID: ids[n.Path], DOTID: n.Path, Label: label, Shape: shape, Classes: classes, Group: group}
			if n.Dir != "" {
				rn.ClickURL = "file://" + n.Dir
				rn.ClickTip = n.Path
			}
			g.Nodes = append(g.Nodes, rn)
		}
	}
	// Edges: skip dangling children (not declared as nodes), matching prior behavior.
	for _, n := range out.Nodes {
		for _, child := range n.Children {
			cid, ok := ids[child]
			if !ok {
				continue
			}
			label := ""
			if spellOf[n.Path] != spellOf[child] {
				label = spellOf[child]
			}
			g.Edges = append(g.Edges, renderEdge{From: ids[n.Path], To: cid, Label: label})
		}
	}
	for _, key := range bucketKeys {
		fill, text := spellColor(key)
		g.Classes = append(g.Classes, renderClass{Name: "spell_" + mermaidID(key), Style: fmt.Sprintf("fill:%s,color:%s", fill, text)})
	}
	if len(out.Roots) > 0 {
		g.Classes = append(g.Classes, renderClass{Name: "root", Style: "stroke-width:3px,stroke:#000"})
	}
	return g
}
