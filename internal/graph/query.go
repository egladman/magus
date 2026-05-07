package graph

import (
	"cmp"
	"context"
	"math"
	"slices"
	"time"
)

// Reachable reports whether u can reach v (O(1) after closure build).
func (g *Graph) Reachable(u, v ID) bool {
	return g.closure().Reachable(u, v)
}

// ReverseClosure returns every node that can reach any seed, sorted by ID.
// Uses BFS for small graphs and the bitset closure for large ones.
func (g *Graph) ReverseClosure(dst []ID, seeds []ID) []ID {
	start := time.Now()

	var (
		result   []ID
		strategy string
	)
	if int(g.n) < closureThreshold {
		strategy = "bfs"
		result = g.reverseClosureBFS(dst, seeds)
	} else {
		strategy = "bitset"
		cl := g.closure()
		result = cl.ReverseClosure(dst, seeds, g.n)
	}

	g.obs.OnQuery(QueryEvent{
		Op:          "reverse_closure",
		Nodes:       int(g.n),
		Seeds:       len(seeds),
		Strategy:    strategy,
		ResultCount: len(result),
		Duration:    time.Since(start),
	})
	return result
}

func (g *Graph) reverseClosureBFS(dst []ID, seeds []ID) []ID {
	visited := make([]bool, g.n)
	stack := make([]ID, 0, len(seeds))
	for _, s := range seeds {
		if s >= 0 && int(s) < int(g.n) && !visited[s] {
			visited[s] = true
			stack = append(stack, s)
		}
	}
	for len(stack) > 0 {
		n := len(stack) - 1
		cur := stack[n]
		stack = stack[:n]
		dst = append(dst, cur)
		lo, hi := g.revOff[cur], g.revOff[cur+1]
		for i := lo; i < hi; i++ {
			p := ID(g.revT[i])
			if !visited[p] {
				visited[p] = true
				stack = append(stack, p)
			}
		}
	}
	slices.Sort(dst)
	return dst
}

// BlastRadius returns, for each node, the count of nodes that can transitively reach it.
func (g *Graph) BlastRadius() []int32 {
	start := time.Now()

	cl := g.closure()
	result := cl.BlastRadius()

	g.obs.OnQuery(QueryEvent{
		Op:          "blast_radius",
		Nodes:       int(g.n),
		Strategy:    "bitset",
		ResultCount: len(result),
		Duration:    time.Since(start),
	})
	return result
}

// NCCD computes Normalized CCD = CCD / CCD(balanced binary tree of same size).
func (g *Graph) NCCD() float64 {
	start := time.Now()
	n := int(g.n)

	defer func() {
		g.obs.OnQuery(QueryEvent{
			Op:       "nccd",
			Nodes:    n,
			Strategy: "bitset",
			Duration: time.Since(start),
		})
	}()

	if n == 0 {
		return 0
	}
	cl := g.closure()
	ccd := cl.CCD()
	bbtCCD := float64(n+1)*math.Log2(float64(n+1)) - float64(n)
	if bbtCCD <= 0 {
		return 0
	}
	return float64(ccd) / bbtCCD
}

// AffectedPath is one chain from a seed node to a target node,
// in seed-first order.
type AffectedPath struct {
	Seed  ID
	Chain []ID
}

// PathsFromSeeds returns the shortest seed→target chain for each reachable seed, sorted by seed ID.
func (g *Graph) PathsFromSeeds(target ID, seeds []ID, out []AffectedPath) []AffectedPath {
	out = out[:0]
	if target < 0 || int(target) >= int(g.n) {
		return out
	}
	start := time.Now()

	seedSet := make([]bool, g.n)
	for _, s := range seeds {
		if s >= 0 && int(s) < int(g.n) {
			seedSet[s] = true
		}
	}

	parent := make([]ID, g.n)
	for i := range parent {
		parent[i] = NoID
	}
	visited := make([]bool, g.n)
	visited[target] = true

	queue := make([]ID, 0, 16)
	queue = append(queue, target)

	for head := 0; head < len(queue); head++ {
		cur := queue[head]

		if seedSet[cur] {
			chain := make([]ID, 0, 8)
			for c := cur; c != NoID; c = parent[c] {
				chain = append(chain, c)
			}
			out = append(out, AffectedPath{Seed: cur, Chain: chain})
		}

		lo, hi := g.fwdOff[cur], g.fwdOff[cur+1]
		for i := lo; i < hi; i++ {
			dep := ID(g.fwdT[i])
			if !visited[dep] {
				visited[dep] = true
				parent[dep] = cur
				queue = append(queue, dep)
			}
		}
	}

	slices.SortFunc(out, func(a, b AffectedPath) int { return cmp.Compare(a.Seed, b.Seed) })

	g.obs.OnQuery(QueryEvent{
		Op:          "paths_from_seeds",
		Nodes:       int(g.n),
		Seeds:       len(seeds),
		Strategy:    "bfs",
		ResultCount: len(out),
		Duration:    time.Since(start),
	})
	return out
}

// NearCycle describes an ordered pair where adding the edge From→To
// would create a cycle of length ≤ maxDepth.
type NearCycle struct {
	From, To ID
	BackPath []ID // [To, ..., From]
}

// NearCycles returns pairs where adding From→To would close a cycle of length ≤ maxDepth.
func (g *Graph) NearCycles(ctx context.Context, maxDepth int) []NearCycle {
	start := time.Now()

	if maxDepth <= 0 {
		return nil
	}

	type state struct {
		node  ID
		depth int
		path  []ID
	}

	var results []NearCycle
	seen := map[[2]ID]struct{}{}
	visited := make([]bool, g.n)
	queue := make([]state, 0, 16)

	for nodeA := ID(0); int(nodeA) < int(g.n); nodeA++ {
		if ctx != nil && ctx.Err() != nil {
			g.obs.OnError(ctx.Err())
			break
		}
		clear(visited)
		visited[nodeA] = true
		queue = queue[:0]
		queue = append(queue, state{node: nodeA, depth: 0, path: []ID{nodeA}})

		for head := 0; head < len(queue); head++ {
			cur := queue[head]

			if cur.depth >= maxDepth-1 {
				continue
			}

			lo, hi := g.revOff[cur.node], g.revOff[cur.node+1]
			for i := lo; i < hi; i++ {
				predNode := ID(g.revT[i])
				if visited[predNode] {
					continue
				}
				visited[predNode] = true

				newPath := make([]ID, len(cur.path)+1)
				newPath[0] = predNode
				copy(newPath[1:], cur.path)

				queue = append(queue, state{
					node:  predNode,
					depth: cur.depth + 1,
					path:  newPath,
				})

				pair := [2]ID{nodeA, predNode}
				if _, ok := seen[pair]; !ok {
					seen[pair] = struct{}{}
					results = append(results, NearCycle{
						From:     nodeA,
						To:       predNode,
						BackPath: newPath,
					})
				}
			}
		}
	}

	slices.SortFunc(results, func(a, b NearCycle) int {
		if c := cmp.Compare(a.From, b.From); c != 0 {
			return c
		}
		if c := cmp.Compare(a.To, b.To); c != 0 {
			return c
		}
		return cmp.Compare(len(a.BackPath), len(b.BackPath))
	})

	g.obs.OnQuery(QueryEvent{
		Op:          "near_cycles",
		Nodes:       int(g.n),
		Strategy:    "bfs",
		ResultCount: len(results),
		Duration:    time.Since(start),
	})
	return results
}
