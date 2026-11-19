package diagram

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/libs/diagram"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/egladman/magus/types"
)

// The kinds of graph a diagram draws.
const (
	KindProjects = "projects"
	KindTargets  = "targets"
	KindImports  = "imports"
)

// Lens names exactly what a diagram shows. Scope keeps the nodes whose anchor is one of its
// directories or sits under one; empty keeps every node. Focus, a node id or anchor, cuts
// the diagram to the nodes within Depth edges of it in either direction; Depth 0 is the
// focus alone. Project names the project a KindTargets diagram draws.
type Lens struct {
	Kind    string
	Project string
	Scope   []string
	Focus   string
	Depth   int
}

// Node is one box of a rendered diagram. Anchor is the workspace-relative path it depicts.
type Node struct {
	ID     string `json:"id"`
	Anchor string `json:"anchor"`
	Label  string `json:"label"`
}

// Diagram is a rendered diagram: the SVG magus/diagram drew and the boxes in it.
type Diagram struct {
	Title string
	SVG   string
	Nodes []Node
}

// FindingsError is magus/diagram refusing to draw the diagram, most often because it exceeds
// the node or edge budget. Findings is the module's own text, which names the fix.
type FindingsError struct {
	Findings string
}

func (e *FindingsError) Error() string { return "diagram: " + e.Findings }

// LensError is a lens naming something the diagram does not have.
type LensError struct {
	msg string
}

func (e *LensError) Error() string { return "diagram: " + e.msg }

// ErrNotIndexed is an import diagram asked of a workspace with no symbol index: an empty
// diagram would read as "nothing imports anything".
var ErrNotIndexed = errors.New("diagram: the import graph is not indexed, run magus graph build")

type edge struct {
	src, dst string
}

// graph is a diagram's input before its lens cuts it. Edges name node ids.
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
		id := taken.unique(p.Path)
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
	g := graph{id: fmt.Sprintf("%s:%s", KindTargets, p.Path), title: fmt.Sprintf("Targets in %s", p.Label()), claim: "flow"}
	taken := ids{}
	byName := map[string]string{}
	for _, n := range p.Nodes {
		id := taken.unique(n.Name)
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
		byDir[d] = taken.unique(d)
		g.nodes = append(g.nodes, Node{ID: byDir[d], Anchor: d, Label: d})
	}
	for _, from := range slices.Sorted(maps.Keys(ig.Packages)) {
		for _, to := range ig.Packages[from] {
			g.edges = append(g.edges, edge{src: byDir[from], dst: byDir[to]})
		}
	}
	return g
}

// fingerprint identifies g's content, so two graphs that draw alike share a key and any
// change to a node, an edge or their order does not.
func (g graph) fingerprint() string {
	h := sha256.New()
	field := func(parts ...string) {
		for _, p := range parts {
			fmt.Fprintf(h, "%d:%s", len(p), p)
		}
		_, _ = h.Write([]byte{'\n'})
	}
	field(g.id, g.title, g.claim)
	for _, n := range g.nodes {
		field("n", n.ID, n.Anchor, n.Label)
	}
	for _, e := range g.edges {
		field("e", e.src, e.dst)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Bounds on renderCache. A lens is client-chosen, so keys are unbounded; a diagram's node
// and edge budgets bound each entry, and these bound the sum.
const (
	renderCacheEntries = 64
	renderCacheBytes   = 32 << 20
)

// renderCache holds rendered diagrams for a long-running server, keyed on the graph's
// fingerprint, the lens and the anchor template. It is safe for concurrent use. Only a
// success is stored: a findings refusal is recomputed, and so is anything computed under a
// context that ended, since that result reflects one client leaving, not the graph.
type renderCache struct {
	mu       sync.Mutex
	entries  map[string]*renderEntry
	clock    uint64
	bytes    int
	inflight map[string]*renderFlight
}

// renderEntry is a cached diagram. used is the cache clock at its last hit, so the entry
// with the smallest used is the least recently used. Eviction scans for it: the entry
// bound keeps that scan short, and a typed map needs no list of untyped elements.
type renderEntry struct {
	fig  Diagram
	used uint64
}

// renderFlight is a render in progress that concurrent requests for the same key wait on.
// abandoned stays true unless the leader finished under a live context.
type renderFlight struct {
	done      chan struct{}
	fig       Diagram
	err       error
	abandoned bool
}

func newRenderCache() *renderCache {
	return &renderCache{
		entries:  map[string]*renderEntry{},
		inflight: map[string]*renderFlight{},
	}
}

func renderKey(g graph, desc, anchorHref string) string {
	return fmt.Sprintf("%s\x00%s\x00%s", g.fingerprint(), desc, anchorHref)
}

// get returns the diagram for key, calling compute at most once across concurrent callers of
// the same key. A caller waiting on another's render returns when its own ctx ends. If the
// computing caller's ctx ended, waiters compute for themselves rather than inherit its
// cancellation.
func (c *renderCache) get(ctx context.Context, key string, compute func(context.Context) (Diagram, error)) (Diagram, error) {
	for {
		c.mu.Lock()
		if e, ok := c.entries[key]; ok {
			c.clock++
			e.used = c.clock
			fig := e.fig
			c.mu.Unlock()
			return cloneDiagram(fig), nil
		}
		if fl, ok := c.inflight[key]; ok {
			c.mu.Unlock()
			select {
			case <-fl.done:
			case <-ctx.Done():
				return Diagram{}, ctx.Err()
			}
			if fl.abandoned {
				continue
			}
			return cloneDiagram(fl.fig), fl.err
		}
		fl := &renderFlight{done: make(chan struct{}), abandoned: true}
		c.inflight[key] = fl
		c.mu.Unlock()

		return c.lead(ctx, key, fl, compute)
	}
}

// lead runs compute for fl and publishes the outcome. The deferred release also runs on a
// panic, so waiters are never left blocked on a flight that will not finish.
func (c *renderCache) lead(ctx context.Context, key string, fl *renderFlight, compute func(context.Context) (Diagram, error)) (Diagram, error) {
	defer func() {
		c.mu.Lock()
		delete(c.inflight, key)
		c.mu.Unlock()
		close(fl.done)
	}()
	fig, err := compute(ctx)
	if err != nil && ctx.Err() != nil {
		return Diagram{}, err
	}
	fl.fig, fl.err, fl.abandoned = fig, err, false
	if err == nil {
		c.store(key, fig)
	}
	return cloneDiagram(fig), err
}

func (c *renderCache) store(key string, fig Diagram) {
	size := len(fig.SVG)
	if size > renderCacheBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok {
		c.bytes -= len(e.fig.SVG)
	}
	c.clock++
	c.entries[key] = &renderEntry{fig: cloneDiagram(fig), used: c.clock}
	c.bytes += size
	for len(c.entries) > renderCacheEntries || c.bytes > renderCacheBytes {
		c.evictOldest()
	}
}

// evictOldest drops the least recently used entry. Callers hold mu.
func (c *renderCache) evictOldest() {
	var oldest string
	used := uint64(math.MaxUint64)
	for k, e := range c.entries {
		if e.used < used {
			oldest, used = k, e.used
		}
	}
	c.bytes -= len(c.entries[oldest].fig.SVG)
	delete(c.entries, oldest)
}

// cloneDiagram copies the slice a caller could otherwise share with the cache.
func cloneDiagram(f Diagram) Diagram {
	f.Nodes = slices.Clone(f.Nodes)
	return f
}

// ids turns paths and names into diagram ids that stay unique after sanitizing.
type ids map[string]bool

func (s ids) unique(name string) string {
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
		id = fmt.Sprintf("%s-%s", base, strconv.Itoa(n))
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
			return graph{}, &LensError{msg: fmt.Sprintf("focus %q is not in the diagram", l.Focus)}
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

// describe is the lens in words, shown under the diagram's title.
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

// render lays g out with magus/diagram. The diagram budgets always apply: an oversized
// diagram is a FindingsError telling the reader to narrow the lens. Every call compiles
// diagram anew; the server reaches it through renderCache.
func render(ctx context.Context, g graph, desc, anchorHref string) (Diagram, error) {
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	buzzstd.RegisterWithOutput(sess, io.Discard)
	if err := bindings.RegisterMagusRecordTypes(sess); err != nil {
		return Diagram{}, err
	}
	diagram.DeclareModules(sess)

	svg, err := diagram.Draw(ctx, sess, buildDiagram(g, desc, anchorHref), anchorHref)
	var refused *diagram.Findings
	if errors.As(err, &refused) {
		return Diagram{}, &FindingsError{Findings: refused.Text}
	}
	if err != nil {
		return Diagram{}, fmt.Errorf("diagram: evaluate %s: %w", g.id, err)
	}
	// A node's ID is the one diagram draws as data-node, so a client matches rows to the SVG:
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
	return Diagram{Title: g.title, SVG: svg, Nodes: nodes}, nil
}

// buildDiagram is g as a diagram\Diagram record.
//
// An imports diagram draws each package as a Dir box and derives edges from its imports.
// Projects and targets are not directories, so they are actors joined by explicit flows
// in an unscoped diagram. A non-empty anchorHref links every actor to its anchor.
func buildDiagram(g graph, desc, anchorHref string) diagram.Diagram {
	f := diagram.Diagram{ID: ids{}.unique(g.id), Title: g.title, Desc: desc}
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
			f.Boxes = append(f.Boxes, diagram.Box{Label: n.Label, Dir: &diagram.Dir{
				Path: n.Anchor, ID: "dir:" + n.Anchor, Language: "go",
				Imports: imports[n.ID], ImportsIndexed: true, Files: 1,
			}})
		}
		f.GraphEdges = true
		return f
	}
	// A served diagram is a lens over the graph the server already holds, so nothing in the
	// tree is left for a scope to check.
	f.UnscopedWhy = "served from the workspace graph: " + desc
	plain := diagram.Look("plain")
	names := actorNames(g.nodes)
	actors := make(map[string]*diagram.Actor, len(g.nodes))
	for _, n := range g.nodes {
		a := &diagram.Actor{Name: names[n.ID], Link: anchorLink(anchorHref, n.Anchor), Look: &plain}
		actors[n.ID] = a
		f.Boxes = append(f.Boxes, diagram.Box{Actor: a})
	}
	for _, e := range g.edges {
		f.Flows = append(f.Flows, diagram.Flow{
			Src: diagram.End{Actor: actors[e.src]},
			Dst: diagram.End{Actor: actors[e.dst]},
		})
	}
	return f
}

// actorNames names each node's actor. diagram keys an actor by its name, so a label two
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
			out[n.ID] = fmt.Sprintf("%s (%s)", n.Label, n.Anchor)
		}
	}
	return out
}

// anchorLink fills anchorHref for an actor, which diagram paints as a plain link.
func anchorLink(anchorHref, anchor string) string {
	if anchorHref == "" {
		return ""
	}
	return strings.NewReplacer("{path}", anchor, "{line}", "").Replace(anchorHref)
}
