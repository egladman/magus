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

// RenderableGraph is the subset of *types.Graph used by Render. It is satisfied
// by any *types.Graph returned from (*Magus).Graph or depgraph.Build.
type RenderableGraph interface {
	Successors(path string) []string
	Predecessors(path string) []string
	Nodes() []string
	Project(path string) *types.Project
}

// RenderOption configures a Render call.
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

// Render writes a deterministic ASCII dependency tree to w.
func Render(g RenderableGraph, w io.Writer, opts ...RenderOption) error {
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

func resolveRoots(g RenderableGraph, cfg *renderConfig) []string {
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
	g RenderableGraph, cfg *renderConfig, visited map[string]bool, prefix string, depth int,
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

// FormatDur formats a duration as a human-readable string.
func FormatDur(d time.Duration) string {
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
	if _, err := fmt.Fprintln(w, "digraph magus {"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "  rankdir=LR;"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, `  node [shape=box, style=rounded];`); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, ""); err != nil {
		return err
	}

	for _, n := range out.Nodes {
		if _, err := fmt.Fprintf(w, "  %q;\n", n.Path); err != nil {
			return err
		}
	}

	hasEdges := false
	for _, n := range out.Nodes {
		if len(n.Children) > 0 {
			hasEdges = true
			break
		}
	}
	if hasEdges {
		if _, err := fmt.Fprintln(w, ""); err != nil {
			return err
		}
		for _, n := range out.Nodes {
			for _, child := range n.Children {
				if _, err := fmt.Fprintf(w, "  %q -> %q;\n", n.Path, child); err != nil {
					return err
				}
			}
		}
	}

	_, err := fmt.Fprintln(w, "}")
	return err
}

// WriteGraphMermaid emits a Mermaid flowchart with spell subgraphs, BR/duration labels,
// cross-spell edge labels, exclusive hexagons, and click-to-dir handlers.
func WriteGraphMermaid(w io.Writer, out types.GraphOutput) error {
	// Build ID map (path → safe mermaid identifier).
	ids := make(map[string]string, len(out.Nodes))
	seen := make(map[string]int)
	for _, n := range out.Nodes {
		id := mermaidID(n.Path)
		if count := seen[id]; count > 0 {
			id = fmt.Sprintf("%s_%d", id, count)
		}
		seen[id]++
		ids[n.Path] = id
	}

	// Bucket nodes by spell name, preserving per-bucket sort order.
	buckets := make(map[string][]types.Node)
	for _, n := range out.Nodes {
		key := n.SpellName
		if key == "" {
			key = "unspelled"
		}
		buckets[key] = append(buckets[key], n)
	}
	for k := range buckets {
		slices.SortFunc(buckets[k], func(a, b types.Node) int {
			return strings.Compare(a.Path, b.Path)
		})
	}
	bucketKeys := make([]string, 0, len(buckets))
	for k := range buckets {
		bucketKeys = append(bucketKeys, k)
	}
	slices.Sort(bucketKeys)

	// Spell-name lookup for edge labeling.
	spellOf := make(map[string]string, len(out.Nodes))
	for _, n := range out.Nodes {
		key := n.SpellName
		if key == "" {
			key = "unspelled"
		}
		spellOf[n.Path] = key
	}

	// Root set for class assignment.
	rootSet := make(map[string]bool, len(out.Roots))
	for _, r := range out.Roots {
		rootSet[r] = true
	}

	// Frontmatter.
	title := "magus dependency graph (" + out.Direction
	if out.SpellName != "" {
		title += ", spell=" + out.SpellName
	}
	title += ")"
	if _, err := fmt.Fprintf(w, "---\ntitle: %s\n---\n", title); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(w, "graph TD"); err != nil {
		return err
	}

	// Subgraphs.
	for _, key := range bucketKeys {
		nodes := buckets[key]
		if _, err := fmt.Fprintf(w, "  subgraph spell_%s[\"%s\"]\n", mermaidID(key), key); err != nil {
			return err
		}
		for _, n := range nodes {
			id := ids[n.Path]
			label := n.Path
			if n.BlastRadius > 0 {
				label += fmt.Sprintf("<br/>BR=%d", n.BlastRadius)
			}
			if n.DurationMs > 0 {
				label += "<br/>~" + FormatDur(time.Duration(n.DurationMs)*time.Millisecond)
			}
			var decl string
			if n.Exclusive {
				decl = fmt.Sprintf("    %s{{%q}}\n", id, label)
			} else {
				decl = fmt.Sprintf("    %s[%q]\n", id, label)
			}
			if _, err := fmt.Fprint(w, decl); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w, "  end"); err != nil {
			return err
		}
	}

	// Edges.
	hasEdges := false
	for _, n := range out.Nodes {
		if len(n.Children) > 0 {
			hasEdges = true
			break
		}
	}
	if hasEdges {
		if _, err := fmt.Fprintln(w, ""); err != nil {
			return err
		}
		for _, n := range out.Nodes {
			srcSpell := spellOf[n.Path]
			for _, child := range n.Children {
				childID, ok := ids[child]
				if !ok {
					continue
				}
				dstSpell := spellOf[child]
				var line string
				if srcSpell != dstSpell {
					line = fmt.Sprintf("  %s -->|%q| %s\n", ids[n.Path], dstSpell, childID)
				} else {
					line = fmt.Sprintf("  %s --> %s\n", ids[n.Path], childID)
				}
				if _, err := fmt.Fprint(w, line); err != nil {
					return err
				}
			}
		}
	}

	// classDef declarations.
	if _, err := fmt.Fprintln(w, ""); err != nil {
		return err
	}
	for _, key := range bucketKeys {
		fill, text := spellColor(key)
		if _, err := fmt.Fprintf(w, "  classDef spell_%s fill:%s,color:%s\n",
			mermaidID(key), fill, text); err != nil {
			return err
		}
	}
	if len(out.Roots) > 0 {
		if _, err := fmt.Fprintln(w, "  classDef root stroke-width:3px,stroke:#000"); err != nil {
			return err
		}
	}

	// class assignments — one line per spell bucket.
	if _, err := fmt.Fprintln(w, ""); err != nil {
		return err
	}
	for _, key := range bucketKeys {
		nodes := buckets[key]
		nodeIDs := make([]string, len(nodes))
		for i, n := range nodes {
			nodeIDs[i] = ids[n.Path]
		}
		slices.Sort(nodeIDs)
		if _, err := fmt.Fprintf(w, "  class %s spell_%s\n",
			strings.Join(nodeIDs, ","), mermaidID(key)); err != nil {
			return err
		}
	}
	if len(out.Roots) > 0 {
		var rootIDs []string
		for _, r := range out.Roots {
			if id, ok := ids[r]; ok {
				rootIDs = append(rootIDs, id)
			}
		}
		slices.Sort(rootIDs)
		if len(rootIDs) > 0 {
			if _, err := fmt.Fprintf(w, "  class %s root\n", strings.Join(rootIDs, ",")); err != nil {
				return err
			}
		}
	}

	// Click handlers.
	hasClicks := false
	for _, n := range out.Nodes {
		if n.Dir != "" {
			hasClicks = true
			break
		}
	}
	if hasClicks {
		if _, err := fmt.Fprintln(w, ""); err != nil {
			return err
		}
		for _, key := range bucketKeys {
			for _, n := range buckets[key] {
				if n.Dir == "" {
					continue
				}
				if _, err := fmt.Fprintf(w, "  click %s \"file://%s\" \"%s\"\n",
					ids[n.Path], n.Dir, n.Path); err != nil {
					return err
				}
			}
		}
	}

	return nil
}
