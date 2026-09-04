package cache

import (
	"fmt"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// TargetNode is one target's declared file footprint, the unit order derivation
// works on: a batch step's own target, or a chain member that step composes.
// Reads and Writes are workspace-rooted doublestar globs, resolved the same way
// the cache key resolves them (buildStep), so what orders and what hashes agree.
type TargetNode struct {
	Project string
	Target  string
	// Steps are the node keys (DepKey) of the batch steps whose execution runs this
	// target. Usually one; a target composed by several steps' chains carries all of
	// them, and derivation treats an edge as ordered only when every owner is.
	Steps []string
	// Reads is the target's source globs; Writes its output plus in-place-update
	// globs. Updates count as writes here even though describe.go derives no
	// DependsOn edge from them: within one batch, ordering a reader after an
	// in-place editor can only over-order, and the motivating chain
	// (changelog-generate -> content-generate) is declared exactly that way.
	Reads  []string
	Writes []string
	// DeclaredReads is false when Reads fell back to the project baseline because the
	// target names no ctx.readsFiles. Edges into such a reader are weak: the baseline
	// is a whole-project over-approximation, so a cycle through a weak edge is an
	// artifact to drop, not an authoring error to report.
	DeclaredReads  bool
	DeclaredWrites bool
	// IgnoreDirs are dir names the reader's source walk prunes (vendor, gen, ...).
	// A fallback reader provably never sees files under them, so writes landing
	// wholly inside one derive no edge.
	IgnoreDirs []string
}

// Key returns the node's scheduling identity, shared with the step barrier.
func (n TargetNode) Key() string { return DepKey(n.Project, n.Target) }

// DerivedEdge records that Writer's declared writes intersect Reader's declared
// reads, so Reader's result depends on running after Writer. Indices into
// DerivedOrder.Nodes.
type DerivedEdge struct {
	Writer, Reader int
	// weak marks an edge whose writer or reader side is a baseline fallback rather
	// than an explicit declaration; weak edges yield when they close a cycle.
	weak bool
	// Ordered reports the batch schedule honors this edge: the writer's step(s) all
	// run strictly before the reader's, via a coarse DependsOn edge or a derived
	// RunAfter edge. An unordered edge is a settling candidate for the caller.
	Ordered bool
}

// DerivedOrder is the result of DeriveTargetOrder: the target-granular
// writer-before-reader edges a batch implies, and the step-level ordering that
// enforces the enforceable subset.
type DerivedOrder struct {
	Nodes []TargetNode
	Edges []DerivedEdge
	// RunAfter maps a step's node key to the step node keys it must wait for,
	// beyond its coarse DependsOn. RunAll's barrier waits these exactly.
	RunAfter map[string][]string
}

// DeriveTargetOrder derives cross-step, target-granular ordering from declared
// footprints. steps are the batch RunAll will schedule; nodes are the targets it
// runs (each step's own target plus chain members), with resolved globs.
//
// An edge is derived when one node's writes can touch a path another node's reads
// can match, the two never run inside the same step, and they are not the same
// target (a target reading what it writes is not an edge). Glob intersection is
// conservative: uncertainty derives the edge, which can only over-order.
//
// A cycle among edges that are all explicitly declared is an authoring error and
// is returned, naming the targets. A cycle through a weak (baseline-fallback)
// edge drops the weak edges instead. Edges whose direction the step schedule can
// honor become RunAfter entries; the rest are marked unordered for the caller to
// settle after the batch. Coarse DependsOn edges always win over derived ones:
// project-level ordering (and the affected set) is never widened or narrowed here.
func DeriveTargetOrder(steps []Step, nodes []TargetNode) (*DerivedOrder, error) {
	d := &DerivedOrder{Nodes: nodes, RunAfter: map[string][]string{}}

	sharesStep := func(a, b TargetNode) bool {
		return slices.ContainsFunc(a.Steps, func(s string) bool { return slices.Contains(b.Steps, s) })
	}
	for w := range nodes {
		for r := range nodes {
			if w == r || nodes[w].Key() == nodes[r].Key() {
				continue
			}
			// Same-step pairs are the body's own ctx.needs sequencing, which stays
			// untouched; only cross-step order is magus's to derive.
			if sharesStep(nodes[w], nodes[r]) {
				continue
			}
			if !footprintsIntersect(nodes[w], nodes[r]) {
				continue
			}
			d.Edges = append(d.Edges, DerivedEdge{
				Writer: w, Reader: r,
				weak: !nodes[w].DeclaredWrites || !nodes[r].DeclaredReads,
			})
		}
	}

	if err := d.resolveFineCycles(); err != nil {
		return nil, err
	}
	d.projectOntoSteps(steps)
	return d, nil
}

// TopoNodes returns every node index in dependency order: each edge's writer
// before its reader. Valid once DeriveTargetOrder returned, whose cycle
// resolution guarantees the edge set is acyclic. Deterministic.
func (d *DerivedOrder) TopoNodes() []int {
	indeg := make([]int, len(d.Nodes))
	for _, e := range d.Edges {
		indeg[e.Reader]++
	}
	var ready, out []int
	for n := range d.Nodes {
		if indeg[n] == 0 {
			ready = append(ready, n)
		}
	}
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		out = append(out, n)
		for _, e := range d.Edges {
			if e.Writer != n {
				continue
			}
			if indeg[e.Reader]--; indeg[e.Reader] == 0 {
				ready = append(ready, e.Reader)
			}
		}
	}
	return out
}

// footprintsIntersect reports whether w's writes can produce a path r's reads
// match. For a fallback reader, writes confined to the reader's pruned dirs are
// invisible to it and derive nothing.
func footprintsIntersect(w, r TargetNode) bool {
	for _, wg := range w.Writes {
		if !r.DeclaredReads && underIgnoredDir(wg, r.IgnoreDirs) {
			continue
		}
		for _, rg := range r.Reads {
			if globsOverlap(wg, rg) {
				return true
			}
		}
	}
	return false
}

// underIgnoredDir reports whether every path glob can match lies inside one of
// the named directories. Only the glob's leading literal segments are decidable;
// a meta segment before any match means "cannot prove", so the answer is false
// and the edge stays (the conservative direction).
func underIgnoredDir(glob string, ignore []string) bool {
	if len(ignore) == 0 {
		return false
	}
	for seg := range strings.SplitSeq(glob, "/") {
		if isMetaSegment(seg) {
			return false
		}
		if slices.Contains(ignore, seg) {
			return true
		}
	}
	return false
}

func isMetaSegment(seg string) bool { return strings.ContainsAny(seg, "*?[{") }

// globsOverlap conservatively reports whether two doublestar globs can match a
// common path. False only when provable: a literal path one side rejects,
// diverging literal prefixes, or incompatible literal filename suffixes.
// Everything else answers true; over-ordering is safe, a missed edge is not.
func globsOverlap(a, b string) bool {
	aMeta, bMeta := isMetaSegment(a), isMetaSegment(b)
	switch {
	case !aMeta && !bMeta:
		return a == b
	case !aMeta:
		ok, err := doublestar.Match(b, a)
		return ok || err != nil
	case !bMeta:
		ok, err := doublestar.Match(a, b)
		return ok || err != nil
	}
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if isMetaSegment(as[i]) || isMetaSegment(bs[i]) {
			break
		}
		if as[i] != bs[i] {
			return false
		}
		// Both consumed a literal segment; a shorter glob than the other's prefix
		// cannot match a longer path, unless it still has segments to offer.
		if i == len(as)-1 || i == len(bs)-1 {
			return i == len(as)-1 && i == len(bs)-1
		}
	}
	sa, sb := literalSuffix(as[len(as)-1]), literalSuffix(bs[len(bs)-1])
	if as[len(as)-1] == "**" || bs[len(bs)-1] == "**" {
		return true
	}
	return strings.HasSuffix(sa, sb) || strings.HasSuffix(sb, sa)
}

// literalSuffix returns the literal tail of one glob segment: everything after
// the last metacharacter ("" when the segment ends in one).
func literalSuffix(seg string) string {
	if i := strings.LastIndexAny(seg, "*?[]{}"); i >= 0 {
		return seg[i+1:]
	}
	return seg
}

// resolveFineCycles removes weak edges that close cycles and reports a cycle
// whose every edge is declared, naming the targets in order.
func (d *DerivedOrder) resolveFineCycles() error {
	for {
		cycle := d.findCycle()
		if cycle == nil {
			return nil
		}
		weak := false
		for _, ei := range cycle {
			if d.Edges[ei].weak {
				weak = true
			}
		}
		if !weak {
			hops := make([]string, 0, len(cycle)+1)
			for _, ei := range cycle {
				hops = append(hops, displayKey(d.Nodes[d.Edges[ei].Writer].Key()))
			}
			hops = append(hops, displayKey(d.Nodes[d.Edges[cycle[0]].Writer].Key()))
			return fmt.Errorf("cache: derived target ordering: declared writes and reads form a cycle: %s; every target both writes what another reads and reads what it writes, so no order can run writers first", strings.Join(hops, " -> "))
		}
		// Descending index order: deleting shifts everything after the hole.
		drop := slices.Clone(cycle)
		slices.Sort(drop)
		for _, ei := range slices.Backward(drop) {
			if d.Edges[ei].weak {
				d.Edges = slices.Delete(d.Edges, ei, ei+1)
			}
		}
	}
}

// findCycle returns the edge indices of one cycle, in walk order, or nil.
// Deterministic: adjacency follows edge insertion order.
func (d *DerivedOrder) findCycle() []int {
	adj := map[int][]int{}
	for ei, e := range d.Edges {
		adj[e.Writer] = append(adj[e.Writer], ei)
	}
	const (
		white = iota
		grey
		black
	)
	color := map[int]int{}
	var stack []int
	var visit func(n int) []int
	visit = func(n int) []int {
		color[n] = grey
		for _, ei := range adj[n] {
			m := d.Edges[ei].Reader
			switch color[m] {
			case grey:
				for i, p := range stack {
					if d.Edges[p].Writer == m {
						return append(append([]int(nil), stack[i:]...), ei)
					}
				}
				return append(append([]int(nil), stack...), ei)
			case white:
				stack = append(stack, ei)
				if cyc := visit(m); cyc != nil {
					return cyc
				}
				stack = stack[:len(stack)-1]
			}
		}
		color[n] = black
		return nil
	}
	for n := range d.Nodes {
		if color[n] != white {
			continue
		}
		if cyc := visit(n); cyc != nil {
			return cyc
		}
	}
	return nil
}

// projectOntoSteps turns fine edges into step-level RunAfter ordering where the
// combined step graph stays acyclic, and marks the rest unordered. Coarse
// DependsOn edges are never dropped: where a derived direction conflicts with
// them (the entangled shape hand-wiring used to be the only answer for), the
// fine edge is left to post-batch settling instead of deadlocking the barrier.
func (d *DerivedOrder) projectOntoSteps(steps []Step) {
	inScope := make(map[string]bool, len(steps))
	for _, s := range steps {
		inScope[stepKey(s)] = true
	}
	// coarse holds today's barrier edges (upstream -> dependent, same target).
	coarse := map[string][]string{}
	for _, s := range steps {
		for _, dep := range s.DependsOn {
			k := DepKey(dep, s.Target)
			if inScope[k] && k != stepKey(s) {
				coarse[k] = append(coarse[k], stepKey(s))
			}
		}
	}

	type stepEdge struct{ from, to string }
	induced := map[stepEdge][]int{}
	for ei, e := range d.Edges {
		for _, sw := range d.Nodes[e.Writer].Steps {
			for _, sr := range d.Nodes[e.Reader].Steps {
				if sw != sr && inScope[sw] && inScope[sr] {
					induced[stepEdge{sw, sr}] = append(induced[stepEdge{sw, sr}], ei)
				}
			}
		}
	}

	// Admit induced edges one at a time, keeping the combined graph acyclic.
	// Deterministic order so the same batch always schedules the same way.
	kept := map[stepEdge]bool{}
	reachable := func(from, to string) bool {
		seen := map[string]bool{from: true}
		queue := []string{from}
		for len(queue) > 0 {
			n := queue[0]
			queue = queue[1:]
			if n == to {
				return true
			}
			next := slices.Clone(coarse[n])
			for e := range kept {
				if e.from == n {
					next = append(next, e.to)
				}
			}
			for _, m := range next {
				if !seen[m] {
					seen[m] = true
					queue = append(queue, m)
				}
			}
		}
		return false
	}
	edges := make([]stepEdge, 0, len(induced))
	for e := range induced {
		edges = append(edges, e)
	}
	slices.SortFunc(edges, func(a, b stepEdge) int {
		if a.from != b.from {
			return strings.Compare(a.from, b.from)
		}
		return strings.Compare(a.to, b.to)
	})
	for _, e := range edges {
		if !reachable(e.to, e.from) {
			kept[e] = true
		}
	}

	for ei := range d.Edges {
		e := &d.Edges[ei]
		e.Ordered = true
		for _, sw := range d.Nodes[e.Writer].Steps {
			for _, sr := range d.Nodes[e.Reader].Steps {
				if sw == sr || !inScope[sw] || !inScope[sr] {
					continue
				}
				if !kept[stepEdge{sw, sr}] && !reachable(sw, sr) {
					e.Ordered = false
				}
			}
		}
	}
	for e := range kept {
		if !slices.Contains(d.RunAfter[e.to], e.from) {
			d.RunAfter[e.to] = append(d.RunAfter[e.to], e.from)
		}
	}
	for k := range d.RunAfter {
		slices.Sort(d.RunAfter[k])
	}
}
