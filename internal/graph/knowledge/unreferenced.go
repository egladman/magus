package knowledge

import (
	"cmp"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// namespaceSuffix ends a SCIP moniker naming a package or namespace, per the descriptor
// grammar (`<namespace> ::= <name> '/'`). Node IDs end in the moniker's descriptor path,
// so the suffix is readable straight off the ID.
//
// The grammar, not SymbolInformation.Kind, because Kind is optional and scip-typescript
// sets it on nothing - the same trap that silently produced no call edges for an entire
// language. Testing the attr here would exclude packages for Go and list every namespace
// for TypeScript, which is the inconsistency this lens exists on the other side of.
const namespaceSuffix = "/"

// Unreferenced lists the symbols this workspace defines and nothing in it names.
//
// A symbol qualifies when nothing outside its own definition file reaches it: no call
// from a symbol defined elsewhere, and no reference from another file.
//
// Both clauses are needed and they are deliberately symmetric on same-file use. The calls
// edge is the sharp one - it names the caller, so an unreferenced function is genuinely
// uncalled rather than merely unmentioned. The file-reference clause covers what a call
// edge cannot be: a struct, a field, a constant, which are referenced and never called.
// Applying the same-file rule to only one of them would hide every function used once in
// its own file while listing every type in exactly that position.
//
// This is a measurement, not a verdict. The graph cannot see reflection, interface
// dispatch, a call site in a file no indexer covered, a build tag that was off when the
// index ran, or any consumer outside this workspace. It also cannot see a call from a
// package-level initializer, which has no enclosing range for the attribution to land in.
// The caller is responsible for carrying that caveat to the reader, which is why the CLI
// output states it and why the result rides a coverage verdict.
func (g *Graph) Unreferenced() []types.UnreferencedEntry {
	g.ensureAdj()
	var out []types.UnreferencedEntry
	for id, n := range g.nodes {
		if n.Kind != types.KindSymbol {
			continue
		}
		// A package is not a callable, and nothing in this model references one: an
		// import is a file->file edge, not a symbol reference. Listing packages would
		// report every package in the workspace as unreferenced, which measures the
		// model's shape rather than the code's.
		if strings.HasSuffix(id, namespaceSuffix) {
			continue
		}
		defFiles := g.definingFiles(id)
		if len(defFiles) == 0 {
			// A symbol with no definition in this workspace is a dependency's, seen only
			// because something here referenced it. It is not ours to call dead.
			continue
		}
		if g.referencedElsewhere(id, defFiles) {
			continue
		}
		out = append(out, types.UnreferencedEntry{
			ID:       id,
			Label:    n.Label,
			Source:   n.Source,
			Kind:     n.Attrs["symbol_kind"],
			Language: n.Attrs["language"],
		})
	}
	// Sorted by ID: the lens is a checklist someone works through, and a list that
	// reorders between runs cannot be diffed or resumed.
	slices.SortFunc(out, func(a, b types.UnreferencedEntry) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// definingFiles returns the file node IDs that define the symbol.
func (g *Graph) definingFiles(id string) map[string]bool {
	files := map[string]bool{}
	for _, e := range g.in[id] {
		if e.Relation == types.RelationDefines {
			files[e.Source] = true
		}
	}
	return files
}

// referencedElsewhere reports whether anything outside the symbol's own definition files
// reaches it: a call from a symbol defined elsewhere, or a reference from another file.
func (g *Graph) referencedElsewhere(id string, defFiles map[string]bool) bool {
	for _, e := range g.in[id] {
		switch e.Relation {
		case types.RelationCalls:
			// The caller is a symbol, so compare where IT is defined against where this
			// one is - otherwise a helper called once from its own file reads as used
			// while a type used in exactly that position does not.
			if !g.definedWithin(e.Source, defFiles) {
				return true
			}
		case types.RelationReferences:
			if !defFiles[e.Source] {
				return true
			}
		}
	}
	return false
}

// definedWithin reports whether every file defining the symbol is among files. A symbol
// magus cannot place (no defines edge) counts as outside, so an unplaceable caller keeps
// its callee off the list rather than silently adding it.
func (g *Graph) definedWithin(id string, files map[string]bool) bool {
	defs := g.definingFiles(id)
	if len(defs) == 0 {
		return false
	}
	for f := range defs {
		if !files[f] {
			return false
		}
	}
	return true
}
