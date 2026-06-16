// The immutable DAG (CSR adjacency, lazy bitset closure) underlying depgraph's project queries.
package depgraph

import (
	"errors"
	"fmt"
	"iter"
	"slices"
	"sync"
	"time"
)

// ID identifies a node; stable for the lifetime of a Graph, assigned in lexicographic order.
// Do not use a Builder ID to index a Graph (reassigned at Build).
type ID int32

// NoID is the zero value returned when a lookup fails.
const NoID ID = -1

// Direction is the traversal direction for graph walks.
type Direction int

const (
	Downstream Direction = iota // forward edges: node → dependencies
	Upstream                    // reverse edges: node → dependents
)

// ErrCycle is returned by Builder.Build when the edge set forms a cycle.
var ErrCycle = errors.New("graph: dependency cycle")

// closureThreshold: below → BFS; at or above → build lazy bitset closure.
const closureThreshold = 256

// Builder accumulates nodes and edges; not safe for concurrent use.
type Builder struct {
	pathToBuilderID map[string]int32
	builderPaths    []string
	rawEdges        [][2]int32 // [from, to] in builder-ID space
	edgeSeen        map[[2]int32]struct{}
}

// New returns an empty Builder.
func New() *Builder {
	return &Builder{
		pathToBuilderID: map[string]int32{},
		edgeSeen:        map[[2]int32]struct{}{},
	}
}

// AddNode registers path and returns its stable builder ID. Idempotent.
func (b *Builder) AddNode(path string) ID {
	if id, ok := b.pathToBuilderID[path]; ok {
		return ID(id)
	}
	id := int32(len(b.builderPaths))
	b.pathToBuilderID[path] = id
	b.builderPaths = append(b.builderPaths, path)
	return ID(id)
}

// AddEdge records a directed edge from→to (silently deduplicates); cycle detection is in Build.
func (b *Builder) AddEdge(from, to ID) error {
	if from == to {
		path := ""
		if int(from) < len(b.builderPaths) {
			path = b.builderPaths[from]
		}
		return fmt.Errorf("graph: self-loop on %q", path)
	}
	if int(from) >= len(b.builderPaths) || from < 0 {
		return fmt.Errorf("graph: from id %d out of range", from)
	}
	if int(to) >= len(b.builderPaths) || to < 0 {
		return fmt.Errorf("graph: to id %d out of range", to)
	}
	key := [2]int32{int32(from), int32(to)}
	if _, dup := b.edgeSeen[key]; dup {
		return nil
	}
	b.edgeSeen[key] = struct{}{}
	b.rawEdges = append(b.rawEdges, key)
	return nil
}

// BuildOption configures a Build call.
type BuildOption func(*buildCfg)

type buildCfg struct{ obs Observer }

// WithObserver attaches obs to the resulting Graph (query → OnQuery; build → OnBuild).
func WithObserver(o Observer) BuildOption {
	return func(c *buildCfg) { c.obs = o }
}

// Build detects cycles (Kahn's algorithm) and returns an immutable Graph. Builder must not be reused.
func (b *Builder) Build(opts ...BuildOption) (*Graph, error) {
	cfg := &buildCfg{obs: NoopObserver{}}
	for _, o := range opts {
		o(cfg)
	}
	start := time.Now()
	n := int32(len(b.builderPaths))
	if n == 0 {
		cfg.obs.OnBuild(BuildStats{Duration: time.Since(start)})
		return &Graph{obs: cfg.obs}, nil
	}

	sorted := make([]string, n)
	copy(sorted, b.builderPaths)
	slices.Sort(sorted)

	idByPath := make(map[string]ID, n)
	for lexIdx, p := range sorted {
		idByPath[p] = ID(lexIdx)
	}

	type edge struct{ from, to ID }
	edges := make([]edge, 0, len(b.rawEdges))
	for _, re := range b.rawEdges {
		from := idByPath[b.builderPaths[re[0]]]
		to := idByPath[b.builderPaths[re[1]]]
		edges = append(edges, edge{from, to})
	}

	fwdMap := make([][]ID, n)
	edgeCount := 0
	for _, e := range edges {
		fwdMap[e.from] = append(fwdMap[e.from], e.to)
		edgeCount++
	}
	for i := range fwdMap {
		if len(fwdMap[i]) > 1 {
			slices.Sort(fwdMap[i])
			fwdMap[i] = slices.Compact(fwdMap[i])
		}
	}

	revMap := make([][]ID, n)
	for v := ID(0); int(v) < int(n); v++ {
		for _, w := range fwdMap[v] {
			revMap[w] = append(revMap[w], v)
		}
	}
	for i := range revMap {
		if len(revMap[i]) > 1 {
			slices.Sort(revMap[i])
		}
	}

	inDeg := make([]int32, n)
	for v := ID(0); int(v) < int(n); v++ {
		for _, w := range fwdMap[v] {
			inDeg[w]++
		}
	}
	queue := make([]ID, 0, n)
	for v := ID(0); int(v) < int(n); v++ {
		if inDeg[v] == 0 {
			queue = append(queue, v)
		}
	}
	topo := make([]ID, 0, n)
	for head := 0; head < len(queue); head++ {
		v := queue[head]
		topo = append(topo, v)
		for _, w := range fwdMap[v] {
			inDeg[w]--
			if inDeg[w] == 0 {
				queue = append(queue, w)
			}
		}
	}
	if int32(len(topo)) != n {
		cfg.obs.OnError(ErrCycle)
		return nil, ErrCycle
	}

	fwdOff, fwdT := buildCSR(fwdMap, n)
	revOff, revT := buildCSR(revMap, n)

	g := &Graph{
		n:        n,
		idByPath: idByPath,
		pathByID: sorted,
		fwdOff:   fwdOff,
		fwdT:     fwdT,
		revOff:   revOff,
		revT:     revT,
		topo:     topo,
		obs:      cfg.obs,
	}
	cfg.obs.OnBuild(BuildStats{
		Nodes:    int(n),
		Edges:    edgeCount,
		Duration: time.Since(start),
	})
	return g, nil
}

func buildCSR(adj [][]ID, n int32) (off []int32, tgt []int32) {
	off = make([]int32, n+1)
	total := int32(0)
	for i := int32(0); i < n; i++ {
		total += int32(len(adj[i]))
		off[i+1] = total
	}
	tgt = make([]int32, total)
	for i := int32(0); i < n; i++ {
		base := off[i]
		for j, w := range adj[i] {
			tgt[base+int32(j)] = int32(w)
		}
	}
	return off, tgt
}

// Graph is an immutable DAG; all methods are safe for concurrent use.
type Graph struct {
	n        int32
	idByPath map[string]ID
	pathByID []string
	fwdOff   []int32 // CSR forward adjacency
	fwdT     []int32
	revOff   []int32 // CSR reverse adjacency
	revT     []int32
	topo     []ID // Kahn order (deps before dependents)

	cl          *bitClosure // lazy transitive closure
	closureOnce sync.Once

	obs Observer
}

// Len returns the number of nodes.
func (g *Graph) Len() int { return int(g.n) }

// ID returns the Graph ID for path, or NoID if not found.
func (g *Graph) ID(path string) (ID, bool) {
	id, ok := g.idByPath[path]
	return id, ok
}

// Path returns the path for id, or ("", false) if out of range.
func (g *Graph) Path(id ID) (string, bool) {
	if id < 0 || int(id) >= int(g.n) {
		return "", false
	}
	return g.pathByID[id], true
}

// Nodes iterates over all IDs in lexicographic order.
func (g *Graph) Nodes() iter.Seq[ID] {
	return func(yield func(ID) bool) {
		for i := ID(0); int(i) < int(g.n); i++ {
			if !yield(i) {
				return
			}
		}
	}
}

// Successors iterates over direct dependencies of id.
func (g *Graph) Successors(id ID) iter.Seq[ID] {
	return func(yield func(ID) bool) {
		if id < 0 || int(id) >= int(g.n) {
			return
		}
		lo, hi := g.fwdOff[id], g.fwdOff[id+1]
		for i := lo; i < hi; i++ {
			if !yield(ID(g.fwdT[i])) {
				return
			}
		}
	}
}

// Predecessors iterates over direct dependents of id.
func (g *Graph) Predecessors(id ID) iter.Seq[ID] {
	return func(yield func(ID) bool) {
		if id < 0 || int(id) >= int(g.n) {
			return
		}
		lo, hi := g.revOff[id], g.revOff[id+1]
		for i := lo; i < hi; i++ {
			if !yield(ID(g.revT[i])) {
				return
			}
		}
	}
}

// TopoOrder returns a copy of the Kahn order (deps before dependents).
func (g *Graph) TopoOrder() []ID {
	return slices.Clone(g.topo)
}

func (g *Graph) closure() *bitClosure {
	g.closureOnce.Do(func() {
		start := time.Now()
		g.cl = buildClosure(g)
		g.obs.OnQuery(QueryEvent{
			Op:       "closure_build",
			Nodes:    int(g.n),
			Strategy: "bitset",
			Duration: time.Since(start),
		})
	})
	return g.cl
}
