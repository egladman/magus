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

	diagramsrc "github.com/egladman/magus/libs/diagram"
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

// Figure is a rendered figure: the SVG flow drew and the boxes in it.
type Figure struct {
	Title string
	SVG   string
	Nodes []Node
}

// FindingsError is flow refusing to draw the figure, most often because it exceeds the node
// or edge budget. Findings is flow's own text, which names the fix.
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

// ids turns paths and names into flow node ids that stay unique after sanitizing.
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

// render lays g out with flow and returns the figure. The authored budgets always apply:
// a figure served to a person is one a person reads, so an oversized one is a
// FindingsError telling the reader to narrow the lens, never a generated() escape.
//
// TODO: cache the rendered figure per graph and lens; every request compiles flow anew.
func render(ctx context.Context, g graph, desc string) (Figure, error) {
	flowSrc, err := diagramsrc.Source.ReadFile("flow.buzz")
	if err != nil {
		return Figure{}, fmt.Errorf("diagram: read flow source: %w", err)
	}
	rendererSrc, err := diagramsrc.Source.ReadFile("diagram.buzz")
	if err != nil {
		return Figure{}, fmt.Errorf("diagram: read renderer source: %w", err)
	}

	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	buzzstd.RegisterWithOutput(sess, io.Discard)
	// flow.buzz imports the renderer by its workspace-root path; serve that path from the
	// binary so no checkout is needed.
	sess.SetModuleDecls("libs/diagram/diagram", string(rendererSrc))

	// The driver runs inside flow's own module: a flat import of flow from a separate
	// program cannot reach the module-private layout helpers its methods call.
	v, err := sess.Eval(ctx, string(flowSrc)+"\n"+driver(g, desc))
	if err != nil {
		return Figure{}, fmt.Errorf("diagram: evaluate %s: %w", g.id, err)
	}
	if !v.IsList() || len(v.ListItems()) != 2 {
		return Figure{}, fmt.Errorf("diagram: evaluate %s: driver returned %s", g.id, v.Kind())
	}
	items := v.ListItems()
	if findings := items[1].AsString(); findings != "" {
		return Figure{}, &FindingsError{Findings: findings}
	}
	return Figure{Title: g.title, SVG: items[0].AsString(), Nodes: g.nodes}, nil
}

// driver is the Buzz appended to flow.buzz that declares g and draws it. Every value is a
// Buzz string literal built by buzzString; nothing from the workspace reaches the source
// unquoted.
func driver(g graph, desc string) string {
	var b strings.Builder
	b.WriteString("fun serveFigure() > str !> str {\n")
	fmt.Fprintf(&b, "    final f = flow(%s).title(%s).desc(%s);\n",
		buzzString(ids{}.of(g.id)), buzzString(g.title), buzzString(desc))
	for _, n := range g.nodes {
		fmt.Fprintf(&b, "    f.node(%s, label: %s, anchor: %s);\n", buzzString(n.ID), buzzString(n.Label), buzzString(n.Anchor))
	}
	for _, e := range g.edges {
		fmt.Fprintf(&b, "    f.edge(%s, dst: %s, claim: %s);\n", buzzString(e.src), buzzString(e.dst), buzzString(g.claim))
	}
	b.WriteString("    return f.svg(cssVarPalette());\n}\n")
	b.WriteString(`var serveSvg = "";
var serveFindings = "";
try {
    serveSvg = serveFigure();
} catch (e: str) {
    serveFindings = e;
}
return [serveSvg, serveFindings];
`)
	return b.String()
}

// buzzString quotes s as a Buzz string literal. Braces are escaped because a bare one opens
// an interpolation; control bytes use Buzz's three-digit decimal escape.
func buzzString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"', '\\', '{', '}':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if c < 0x20 || c == 0x7f {
				fmt.Fprintf(&b, `\%03d`, c)
				continue
			}
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}
