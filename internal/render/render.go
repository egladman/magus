// Package render contains graph presentation helpers: ASCII tree and DOT
// formatters. These were moved out of the public magus package so the
// public API stays free of formatting details.
package render

import (
	"fmt"
	"io"
	"slices"

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

// WriteGraphDOT emits a deterministic Graphviz DOT digraph to w (rankdir=LR, paths quoted).
func WriteGraphDOT(w io.Writer, out types.GraphOutput) error {
	return writeDOT(w, projectGraphIR(out))
}

// projectGraphIR maps the project dependency graph onto a dotGraph. Nodes are grouped
// by spell and sorted by path within a group, which keeps the output stable.
func projectGraphIR(out types.GraphOutput) dotGraph {
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

	g := dotGraph{Name: "magus"}
	for _, key := range bucketKeys {
		var bucket []string
		for _, n := range out.Nodes {
			if spellOf[n.Path] == key {
				bucket = append(bucket, n.Path)
			}
		}
		slices.Sort(bucket)
		g.Nodes = append(g.Nodes, bucket...)
	}
	for _, n := range out.Nodes {
		for _, child := range n.Children {
			g.Edges = append(g.Edges, [2]string{n.Path, child})
		}
	}
	return g
}
