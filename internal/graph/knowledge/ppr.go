package knowledge

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"slices"

	"github.com/egladman/magus/internal/textrank"
	"github.com/egladman/magus/types"
)

// PPRScore is one node's personalized PageRank. Seeds names the at most three seeds that
// contributed most to Score, by contribution then id.
type PPRScore struct {
	ID    string
	Score float64
	Seeds []string
}

// PPRResult is one personalized PageRank run. Scores holds every walked node with a positive
// score, seeds included, highest first and ties by ID. Unplaced lists the seeds no node
// carries. Walked counts the subgraph's nodes; Truncated reports that MaxNodes stopped the
// walk with reachable nodes left out. Skipped lists the weighted relations no edge in the
// walked subgraph carries, so a caller can tell an unwalked relation from an empty one.
type PPRResult struct {
	Scores    []PPRScore
	Unplaced  []string
	Walked    int
	Truncated bool
	Skipped   []string
}

const pprSeedsPerScore = 3

// PersonalizedPageRank ranks the nodes near seeds, a node ID to its weight. It walks
// breadth-first from the seeds over the relations w.Edges weights, in both directions of
// every edge, up to w.MaxNodes nodes, then runs at most w.Iterations rounds of power
// iteration that restart to the normalized seed weights with probability w.Restart,
// stopping once a round moves less than w.Tolerance in L1. Every set is visited in sorted
// order, so the result is bit-for-bit reproducible.
//
// A bad weight is an error: Restart outside (0,1), Iterations or MaxNodes under 1, a
// negative Tolerance, no relation or an unknown one in Edges, a negative or infinite edge
// or seed weight, and seeds of which none is a node or none that is placed weighs anything.
func (g *Graph) PersonalizedPageRank(seeds map[string]float64, w textrank.PPRWeights) (PPRResult, error) {
	if err := checkPPR(seeds, w); err != nil {
		return PPRResult{}, err
	}
	var res PPRResult
	var placed []string
	for _, id := range slices.Sorted(maps.Keys(seeds)) {
		if _, ok := g.node(id); ok {
			placed = append(placed, id)
		} else {
			res.Unplaced = append(res.Unplaced, id)
		}
	}
	if len(placed) == 0 {
		return PPRResult{}, fmt.Errorf("ppr seeds: none of the %d seeds is a node in the graph", len(seeds))
	}

	walk := map[types.RelationID]float64{}
	for rel, weight := range w.Edges {
		if weight > 0 {
			walk[types.RelationID(rel)] = weight
		}
	}
	sub := g.pprSubgraph(placed, walk, w.MaxNodes)
	res.Walked, res.Truncated = len(sub.ids), sub.truncated
	for _, rel := range slices.Sorted(maps.Keys(w.Edges)) {
		if !sub.carried[types.RelationID(rel)] {
			res.Skipped = append(res.Skipped, rel)
		}
	}

	cols, err := pprColumns(seeds, sub)
	if err != nil {
		return PPRResult{}, err
	}
	p := sub.iterate(cols, w)
	res.Scores = sub.scores(cols, p)
	return res, nil
}

func checkPPR(seeds map[string]float64, w textrank.PPRWeights) error {
	switch {
	case !(w.Restart > 0 && w.Restart < 1):
		return fmt.Errorf("ppr.restart: must be between 0 and 1 exclusive, got %v", w.Restart)
	case w.Iterations < 1:
		return fmt.Errorf("ppr.iterations: must be at least 1, got %d", w.Iterations)
	case !(w.Tolerance >= 0):
		return fmt.Errorf("ppr.tolerance: must not be negative, got %v", w.Tolerance)
	case w.MaxNodes < 1:
		return fmt.Errorf("ppr.max_nodes: must be at least 1, got %d", w.MaxNodes)
	case len(w.Edges) == 0:
		return fmt.Errorf("ppr.edges: names no relation, so nothing can be walked")
	case len(seeds) == 0:
		return fmt.Errorf("ppr seeds: none given")
	}
	for _, rel := range slices.Sorted(maps.Keys(w.Edges)) {
		if _, ok := types.KnowledgeRelation(types.RelationID(rel)); !ok {
			return fmt.Errorf("ppr.edges.%s: no such relation", rel)
		}
		if weight := w.Edges[rel]; !(weight >= 0) || math.IsInf(weight, 1) {
			return fmt.Errorf("ppr.edges.%s: must be a finite weight of at least 0, got %v", rel, weight)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(seeds)) {
		if weight := seeds[id]; !(weight >= 0) || math.IsInf(weight, 1) {
			return fmt.Errorf("ppr seed %s: must be a finite weight of at least 0, got %v", id, weight)
		}
	}
	return nil
}

type pprArc struct {
	to     int
	weight float64
}

// pprGraph is the walked subgraph, indexed by position in ids (sorted). arcs[i] is sorted
// by neighbor and holds one arc per neighbor, its relations' weights summed; the arcs are
// symmetric, so degree[i] is both what i sends and what reaches it.
type pprGraph struct {
	ids       []string
	index     map[string]int
	arcs      [][]pprArc
	degree    []float64
	truncated bool
	carried   map[types.RelationID]bool
}

// pprSubgraph admits the placed seeds, then their neighbors breadth-first, each frontier in
// sorted order, until maxNodes are in. The seeds count against maxNodes too.
func (g *Graph) pprSubgraph(placed []string, walk map[types.RelationID]float64, maxNodes int) pprGraph {
	g.ensureAdj()
	var sub pprGraph
	admitted := map[string]bool{}
	var queue []string
	admit := func(id string) bool {
		if admitted[id] {
			return true
		}
		if len(queue) == maxNodes {
			sub.truncated = true
			return false
		}
		admitted[id] = true
		queue = append(queue, id)
		return true
	}
	full := false
	for _, id := range placed {
		if !admit(id) {
			full = true
			break
		}
	}
	for head := 0; !full && head < len(queue); head++ {
		for _, id := range g.pprNeighbors(queue[head], walk) {
			if !admit(id) {
				full = true
				break
			}
		}
	}

	sub.ids = slices.Sorted(maps.Keys(admitted))
	sub.index = make(map[string]int, len(sub.ids))
	for i, id := range sub.ids {
		sub.index[id] = i
	}
	sub.arcs = make([][]pprArc, len(sub.ids))
	sub.degree = make([]float64, len(sub.ids))
	sub.carried = map[types.RelationID]bool{}
	for i, id := range sub.ids {
		var arcs []pprArc
		add := func(e types.KnowledgeEdge, other string) {
			weight := walk[e.Relation]
			j, ok := sub.index[other]
			if weight == 0 || !ok || other == id {
				return
			}
			arcs = append(arcs, pprArc{to: j, weight: weight})
			sub.carried[e.Relation] = true
		}
		for _, e := range g.out[id] {
			add(e, e.Target)
		}
		for _, e := range g.in[id] {
			add(e, e.Source)
		}
		slices.SortStableFunc(arcs, func(a, b pprArc) int { return cmp.Compare(a.to, b.to) })
		merged := arcs[:0]
		for _, a := range arcs {
			if n := len(merged); n > 0 && merged[n-1].to == a.to {
				merged[n-1].weight += a.weight
				continue
			}
			merged = append(merged, a)
		}
		sub.arcs[i] = merged
		for _, a := range merged {
			sub.degree[i] += a.weight
		}
	}
	return sub
}

// pprNeighbors returns id's neighbors over the walked relations, either direction, sorted.
func (g *Graph) pprNeighbors(id string, walk map[types.RelationID]float64) []string {
	var out []string
	for _, e := range g.out[id] {
		if walk[e.Relation] > 0 && e.Target != id {
			out = append(out, e.Target)
		}
	}
	for _, e := range g.in[id] {
		if walk[e.Relation] > 0 && e.Source != id {
			out = append(out, e.Source)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// pprColumn is one seed's share of the restart vector: its node and normalized weight.
type pprColumn struct {
	id   string
	node int
	mass float64
}

// pprColumns returns one column per admitted seed with a positive weight, in id order.
func pprColumns(seeds map[string]float64, sub pprGraph) ([]pprColumn, error) {
	var cols []pprColumn
	total := 0.0
	for _, id := range slices.Sorted(maps.Keys(seeds)) {
		node, ok := sub.index[id]
		if !ok || seeds[id] == 0 {
			continue
		}
		cols = append(cols, pprColumn{id: id, node: node, mass: seeds[id]})
		total += seeds[id]
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("ppr seeds: every walked seed weighs 0, so no node can be ranked")
	}
	for c := range cols {
		cols[c].mass /= total
	}
	return cols, nil
}

// iterate runs power iteration with one column per seed, p[i*len(cols)+c] being seed c's
// share of node i's score. The iteration is linear in the restart vector, so the columns
// sum to the combined score exactly and each one is that seed's contribution. A node with
// no arcs returns its mass to the column's own seed, which keeps every column's total at
// its seed's mass.
func (sub pprGraph) iterate(cols []pprColumn, w textrank.PPRWeights) []float64 {
	n, k := len(sub.ids), len(cols)
	p := make([]float64, n*k)
	for c, col := range cols {
		p[col.node*k+c] = col.mass
	}
	next := make([]float64, n*k)
	dangling := make([]float64, k)
	walk := 1 - w.Restart
	for range w.Iterations {
		clear(next)
		clear(dangling)
		for i := range n {
			row := next[i*k : (i+1)*k]
			for _, a := range sub.arcs[i] {
				f := walk * a.weight / sub.degree[a.to]
				for c, v := range p[a.to*k : (a.to+1)*k] {
					row[c] += f * v
				}
			}
			if sub.degree[i] == 0 {
				for c, v := range p[i*k : (i+1)*k] {
					dangling[c] += v
				}
			}
		}
		for c, col := range cols {
			next[col.node*k+c] += w.Restart*col.mass + walk*dangling[c]
		}
		delta := 0.0
		for i := range n {
			delta += math.Abs(sumFloats(next[i*k:(i+1)*k]) - sumFloats(p[i*k:(i+1)*k]))
		}
		p, next = next, p
		if delta < w.Tolerance {
			break
		}
	}
	return p
}

func (sub pprGraph) scores(cols []pprColumn, p []float64) []PPRScore {
	k := len(cols)
	var out []PPRScore
	for i, id := range sub.ids {
		row := p[i*k : (i+1)*k]
		score := sumFloats(row)
		if score <= 0 {
			continue
		}
		var top []int
		for c, v := range row {
			if v > 0 {
				top = append(top, c)
			}
		}
		slices.SortFunc(top, func(a, b int) int {
			if c := cmp.Compare(row[b], row[a]); c != 0 {
				return c
			}
			return cmp.Compare(cols[a].id, cols[b].id)
		})
		seeds := make([]string, 0, min(len(top), pprSeedsPerScore))
		for _, c := range top[:min(len(top), pprSeedsPerScore)] {
			seeds = append(seeds, cols[c].id)
		}
		out = append(out, PPRScore{ID: id, Score: score, Seeds: seeds})
	}
	slices.SortFunc(out, func(a, b PPRScore) int {
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}

func sumFloats(values []float64) float64 {
	total := 0.0
	for _, v := range values {
		total += v
	}
	return total
}
