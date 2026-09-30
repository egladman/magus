package diagram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/libs/figure"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/egladman/magus/types"
)

// The kinds of graph a figure draws.
const (
	KindProjects = "projects"
	KindTargets  = "targets"
	KindImports  = "imports"
)

// Lens names exactly what a figure shows. Scope keeps the nodes whose anchor is one of its
// directories or sits under one; empty keeps every node. Focus, a node id or anchor, cuts
// the figure to the nodes within Depth edges of it in either direction; Depth 0 is the
// focus alone. Project names the project a KindTargets figure draws.
type Lens struct {
	Kind    string
	Project string
	Scope   []string
	Focus   string
	Depth   int
}

// Node is one box of a rendered figure. Anchor is the workspace-relative path it depicts.
type Node struct {
	ID     string `json:"id"`
	Anchor string `json:"anchor"`
	Label  string `json:"label"`
}

// Figure is a rendered figure: the SVG magus/figure drew and the boxes in it.
type Figure struct {
	Title string
	SVG   string
	Nodes []Node
}

// FindingsError is magus/figure refusing to draw the figure, most often because it exceeds
// the node or edge budget. Findings is the module's own text, which names the fix.
type FindingsError struct {
	Findings string
}

func (e *FindingsError) Error() string { return "diagram: " + e.Findings }

// LensError is a lens naming something the figure does not have.
type LensError struct {
	msg string
}

func (e *LensError) Error() string { return "diagram: " + e.msg }

// ErrNotIndexed is an import figure asked of a workspace with no symbol index: an empty
// figure would read as "nothing imports anything".
var ErrNotIndexed = errors.New("diagram: the import graph is not indexed; run magus graph build")

type edge struct {
	src, dst string
}

// graph is a figure's input before its lens cuts it. Edges name node ids.
type graph struct {
	id    string
	title string
	claim string
	nodes []Node
	edges []edge
}

// projectGraph draws the workspace's projects and their declared depends_on edges.
func projectGraph(tg types.TargetGraphOutput) graph {
	g := graph{id: KindProjects, title: "Workspace projects", claim: "flow"}
	taken := ids{}
	byPath := map[string]string{}
	for _, p := range tg.Projects {
		id := taken.of(p.Path)
		byPath[p.Path] = id
		g.nodes = append(g.nodes, Node{ID: id, Anchor: p.Path, Label: p.Label()})
	}
	for _, p := range tg.Projects {
		for _, dep := range p.DependsOn {
			if to, ok := byPath[dep]; ok {
				g.edges = append(g.edges, edge{src: byPath[p.Path], dst: to})
			}
		}
	}
	return g
}

// targetGraph draws one project's targets and the same-project dependencies between them.
// Every target is anchored at its project's directory: the target graph records no line.
func targetGraph(tg types.TargetGraphOutput, project string) (graph, bool) {
	i := slices.IndexFunc(tg.Projects, func(p types.TargetGraphProject) bool { return p.Path == project })
	if i < 0 {
		return graph{}, false
	}
	p := tg.Projects[i]
	g := graph{id: KindTargets + ":" + p.Path, title: "Targets in " + p.Label(), claim: "flow"}
	taken := ids{}
	byName := map[string]string{}
	for _, n := range p.Nodes {
		id := taken.of(n.Name)
		byName[n.Name] = id
		g.nodes = append(g.nodes, Node{ID: id, Anchor: p.Path, Label: n.Name})
	}
	for _, n := range p.Nodes {
		for _, dep := range n.Dependencies {
			if to, ok := byName[dep]; ok {
				g.edges = append(g.edges, edge{src: byName[n.Name], dst: to})
			}
		}
	}
	return g, true
}

// importGraph draws package directories and the imports between them.
func importGraph(ig types.ImportGraph) graph {
	g := graph{id: KindImports, title: "Package imports", claim: "imports"}
	var dirs []string
	for from, tos := range ig.Packages {
		dirs = append(dirs, from)
		dirs = append(dirs, tos...)
	}
	slices.Sort(dirs)
	dirs = slices.Compact(dirs)
	taken := ids{}
	byDir := map[string]string{}
	for _, d := range dirs {
		byDir[d] = taken.of(d)
		g.nodes = append(g.nodes, Node{ID: byDir[d], Anchor: d, Label: d})
	}
	for _, from := range slices.Sorted(maps.Keys(ig.Packages)) {
		for _, to := range ig.Packages[from] {
			g.edges = append(g.edges, edge{src: byDir[from], dst: byDir[to]})
		}
	}
	return g
}

// ids turns paths and names into figure ids that stay unique after sanitizing.
type ids map[string]bool

func (s ids) of(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	base := strings.Trim(b.String(), "-")
	if base == "" {
		base = "root"
	}
	id := base
	for n := 2; s[id]; n++ {
		id = base + "-" + strconv.Itoa(n)
	}
	s[id] = true
	return id
}

// cut applies the lens's scope, then its focus and depth, keeping only edges whose ends
// both survive. It never adds an edge the graph did not have.
func (g graph) cut(l Lens) (graph, error) {
	if l.Depth < 0 {
		return graph{}, &LensError{msg: "depth must be 0 or more"}
	}
	if l.Focus == "" && l.Depth > 0 {
		return graph{}, &LensError{msg: "depth needs a focus"}
	}
	keep := map[string]bool{}
	for _, n := range g.nodes {
		if inScope(n.Anchor, l.Scope) {
			keep[n.ID] = true
		}
	}
	if l.Focus != "" {
		i := slices.IndexFunc(g.nodes, func(n Node) bool {
			return keep[n.ID] && (n.ID == l.Focus || n.Anchor == l.Focus)
		})
		if i < 0 {
			return graph{}, &LensError{msg: fmt.Sprintf("focus %q is not in the figure", l.Focus)}
		}
		keep = g.neighbourhood(g.nodes[i].ID, l.Depth, keep)
	}
	out := graph{id: g.id, title: g.title, claim: g.claim}
	for _, n := range g.nodes {
		if keep[n.ID] {
			out.nodes = append(out.nodes, n)
		}
	}
	for _, e := range g.edges {
		if keep[e.src] && keep[e.dst] {
			out.edges = append(out.edges, e)
		}
	}
	return out, nil
}

// neighbourhood is every node within depth undirected hops of focus, walking only nodes in
// allowed.
func (g graph) neighbourhood(focus string, depth int, allowed map[string]bool) map[string]bool {
	adj := map[string][]string{}
	for _, e := range g.edges {
		if allowed[e.src] && allowed[e.dst] {
			adj[e.src] = append(adj[e.src], e.dst)
			adj[e.dst] = append(adj[e.dst], e.src)
		}
	}
	seen := map[string]bool{focus: true}
	frontier := []string{focus}
	for range depth {
		var next []string
		for _, id := range frontier {
			for _, o := range adj[id] {
				if !seen[o] {
					seen[o] = true
					next = append(next, o)
				}
			}
		}
		frontier = next
	}
	return seen
}

func inScope(anchor string, scope []string) bool {
	if len(scope) == 0 {
		return true
	}
	for _, s := range scope {
		s = path.Clean(strings.TrimSuffix(s, "/"))
		if s == "." || anchor == s || strings.HasPrefix(anchor, s+"/") {
			return true
		}
	}
	return false
}

// describe is the lens in words, shown under the figure's title.
func (l Lens) describe() string {
	var parts []string
	if len(l.Scope) > 0 {
		parts = append(parts, "scope "+strings.Join(l.Scope, ", "))
	}
	if l.Focus != "" {
		parts = append(parts, fmt.Sprintf("focus %s, depth %d", l.Focus, l.Depth))
	}
	return strings.Join(parts, "; ")
}

// render lays g out with magus/figure and returns the figure. The authored budgets always
// apply: a figure served to a person is one a person reads, so an oversized one is a
// FindingsError telling the reader to narrow the lens, never a generated() escape.
//
// TODO: cache the rendered figure per graph and lens; every request compiles figure anew.
func render(ctx context.Context, g graph, desc, anchorHref string) (Figure, error) {
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	buzzstd.RegisterWithOutput(sess, io.Discard)
	// figure names magus\Dir and its kin, which only the magus mirrors declare.
	bindings.DeclareMagusTypes(sess)
	sess.SetModuleDecls("magus/figure", figure.Source)

	svg, err := figure.Draw(ctx, sess, figureOf(g, desc, anchorHref), anchorHref)
	var refused *figure.Findings
	if errors.As(err, &refused) {
		return Figure{}, &FindingsError{Findings: refused.Text}
	}
	if err != nil {
		return Figure{}, fmt.Errorf("diagram: evaluate %s: %w", g.id, err)
	}
	// A node's ID is the one figure draws as data-node, so a client matches rows to the SVG:
	// a box is its directory path, an actor is external:<name>.
	nodes := make([]Node, len(g.nodes))
	names := actorNames(g.nodes)
	for i, n := range g.nodes {
		n.ID = "external:" + names[n.ID]
		if g.claim == "imports" {
			n.ID = n.Anchor
		}
		nodes[i] = n
	}
	return Figure{Title: g.title, SVG: svg, Nodes: nodes}, nil
}

// figureOf is g as a figure\Figure record.
//
// An imports figure draws each package as a box from a Dir carrying the imports g holds,
// and the graph edges follow from those, as a docs figure's do. Projects and targets are
// no directories, so they are actors joined by hand edges, and the figure says why it is
// unscoped. A non-empty anchorHref links every actor to its anchor.
func figureOf(g graph, desc, anchorHref string) figure.Figure {
	f := figure.Figure{ID: ids{}.of(g.id), Title: g.title, Desc: desc}
	if g.claim == KindImports {
		anchor := make(map[string]string, len(g.nodes))
		for _, n := range g.nodes {
			anchor[n.ID] = n.Anchor
		}
		imports := map[string][]string{}
		for _, e := range g.edges {
			imports[e.src] = append(imports[e.src], anchor[e.dst])
		}
		for _, n := range g.nodes {
			f.Boxes = append(f.Boxes, figure.Box{Label: n.Label, Dir: &figure.Dir{
				Path: n.Anchor, ID: "dir:" + n.Anchor, Language: "go",
				Imports: imports[n.ID], ImportsIndexed: true, Files: 1,
			}})
		}
		f.GraphEdges = true
		return f
	}
	// A served figure is a lens over the graph the server already holds, so nothing in the
	// tree is left for a scope to check.
	f.UnscopedWhy = "served from the workspace graph: " + desc
	plain := figure.Look("plain")
	names := actorNames(g.nodes)
	actors := make(map[string]*figure.Actor, len(g.nodes))
	for _, n := range g.nodes {
		a := &figure.Actor{Name: names[n.ID], Link: linkTo(anchorHref, n.Anchor), Look: &plain}
		actors[n.ID] = a
		f.Boxes = append(f.Boxes, figure.Box{Actor: a})
	}
	for _, e := range g.edges {
		f.Flows = append(f.Flows, figure.Flow{
			Src: figure.End{Actor: actors[e.src]},
			Dst: figure.End{Actor: actors[e.dst]},
		})
	}
	return f
}

// actorNames names each node's actor. figure keys an actor by its name, so a label two
// nodes share takes the node's anchor to stay apart.
func actorNames(nodes []Node) map[string]string {
	count := map[string]int{}
	for _, n := range nodes {
		count[n.Label]++
	}
	out := make(map[string]string, len(nodes))
	for _, n := range nodes {
		out[n.ID] = n.Label
		if count[n.Label] > 1 {
			out[n.ID] = n.Label + " (" + n.Anchor + ")"
		}
	}
	return out
}

// linkTo fills anchorHref for an actor, which figure paints as a plain link.
func linkTo(anchorHref, anchor string) string {
	if anchorHref == "" {
		return ""
	}
	return strings.NewReplacer("{path}", anchor, "{line}", "").Replace(anchorHref)
}
