package knowledge

import (
	"maps"
	"slices"

	"github.com/egladman/magus/types"
)

// packageGraph is which package imports which, keyed by namespace symbol ID.
type packageGraph map[string]map[string]bool

// packageDeps reads the imports the SCIP index already recorded. An indexer emits the
// package path in an import statement as a reference to the namespace symbol, and every
// file in a package defines that package's namespace symbol, so a file that defines A and
// references B is A importing B. No language's import syntax is parsed here.
//
// Calls add the imports an indexer might leave implicit: a symbol in A calling one
// declared in B cannot compile without A importing B.
//
// Test files are left out. A test may import what its package cannot (Go's external test
// packages exist for exactly that), so its imports do not constrain where non-test code
// can move.
func (g *Graph) packageDeps() packageGraph {
	g.ensureAdj()
	deps := packageGraph{}
	add := func(from, to string) {
		if from == "" || to == "" || from == to {
			return
		}
		if deps[from] == nil {
			deps[from] = map[string]bool{}
		}
		deps[from][to] = true
	}
	fileNS := g.fileNamespaces()
	for id, n := range g.nodes {
		switch n.Kind {
		case types.KindFile:
			if isTestSource(n.Source) {
				continue
			}
			for _, e := range g.out[id] {
				if e.Relation != types.RelationReferences || !g.isNamespace(e.Target) {
					continue
				}
				for _, from := range fileNS[id] {
					add(from, e.Target)
				}
			}
		case types.KindSymbol:
			if isTestSource(n.Source) {
				continue
			}
			for _, e := range g.out[id] {
				if e.Relation == types.RelationCalls {
					add(n.Attrs[attrNamespace], g.nodes[e.Target].Attrs[attrNamespace])
				}
			}
		}
	}
	return deps
}

// fileNamespaces maps each file node ID to the namespaces it defines: its package.
func (g *Graph) fileNamespaces() map[string][]string {
	g.ensureAdj()
	out := map[string][]string{}
	for id, n := range g.nodes {
		if !g.isNamespace(id) {
			continue
		}
		for _, e := range g.in[id] {
			if e.Relation == types.RelationDefines {
				out[e.Source] = append(out[e.Source], n.ID)
			}
		}
	}
	return out
}

// isNamespace reports whether id is a namespace symbol: one declared in itself.
func (g *Graph) isNamespace(id string) bool {
	n, ok := g.nodes[id]
	return ok && n.Kind == types.KindSymbol && n.Attrs[attrNamespace] == id
}

// reaches reports whether from imports to, directly or transitively.
func (d packageGraph) reaches(from, to string) bool {
	return from == to || d.closure(from)[to]
}

// placement decides where a helper shared by members can live without an import cycle.
//
// A home H works when two things hold. Every member package can import H: that closes a
// cycle only if H already reaches the member. And H can import every package whose symbols
// the helper calls: that closes a cycle only if one of those already reaches H.
//
// Candidates are tried cheapest first. A member's own package adds no package at all; a
// package every member already imports adds no import edge to any member; a new leaf
// package adds one package and works unless a callee already reaches a member, in which
// case no home can call the callees and still be importable, and the group is blocked
// until code moves.
func (d packageGraph) placement(members, callees []string) (types.DuplicationPlacement, string) {
	works := func(home string) bool {
		for _, m := range members {
			if m != home && d.reaches(home, m) {
				return false
			}
		}
		for _, c := range callees {
			if c != home && d.reaches(c, home) {
				return false
			}
		}
		return true
	}

	for _, m := range members {
		if works(m) {
			return types.PlacementMember, m
		}
	}

	var common map[string]bool
	for _, m := range members {
		reach := d.closure(m)
		if common == nil {
			common = reach
			continue
		}
		for p := range common {
			if !reach[p] {
				delete(common, p)
			}
		}
	}
	// Sorted so the same group names the same home on every run.
	for _, c := range slices.Sorted(maps.Keys(common)) {
		if works(c) {
			return types.PlacementDependency, c
		}
	}

	for _, c := range callees {
		for _, m := range members {
			if d.reaches(c, m) {
				return types.PlacementBlocked, ""
			}
		}
	}
	return types.PlacementNewPackage, ""
}

// closure is every package from imports, directly or transitively, excluding itself.
func (d packageGraph) closure(from string) map[string]bool {
	out := map[string]bool{}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for next := range d[cur] {
			if !out[next] && next != from {
				out[next] = true
				queue = append(queue, next)
			}
		}
	}
	return out
}
