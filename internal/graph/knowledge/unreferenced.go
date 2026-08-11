package knowledge

import (
	"cmp"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// symbolKindPackage is the SCIP classifier for a package/namespace node, stored on the
// symbol node's symbol_kind attr. Compared as a string rather than through the scip
// bindings on purpose: this package is a types-only leaf and stays free of that import.
const symbolKindPackage = "Package"

// Unreferenced lists the symbols this workspace defines and nothing in it names.
//
// A symbol qualifies when both hold:
//
//   - no other symbol calls it, and
//   - no file references it except the one(s) that define it.
//
// Both clauses are needed, and they cover different things. The calls edge is the sharp
// one: it names the caller, so an unreferenced Function is genuinely uncalled rather than
// merely unmentioned outside its file. The file-reference clause covers everything a call
// edge cannot be - a struct, a field, a constant - because those are referenced, never
// called, and dropping the clause would report every type in the workspace.
//
// Self-references are excluded rather than counted: a symbol used only inside the file
// that declares it is exactly the case worth surfacing, and counting that use would hide
// every one of them.
//
// This is a measurement, not a verdict. The graph cannot see reflection, interface
// dispatch, a call site in a file no indexer covered, a build tag that was off when the
// index ran, or any consumer outside this workspace. The caller is responsible for
// carrying that caveat to the reader, which is why the CLI output states it and why the
// result rides a coverage verdict.
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
		if n.Attrs["symbol_kind"] == symbolKindPackage {
			continue
		}
		defFiles, defined := definingFiles(g, id)
		if !defined {
			// A symbol with no definition in this workspace is a dependency's, seen only
			// because something here referenced it. It is not ours to call dead.
			continue
		}
		if namedBySomethingElse(g, id, defFiles) {
			continue
		}
		out = append(out, types.UnreferencedEntry{
			ID:       id,
			Label:    n.Label,
			Source:   n.Source,
			Kind:     n.Attrs["symbol_kind"],
			Language: n.Attrs["language"],
			Project:  projectOfSource(n.Source),
		})
	}
	// Sorted by ID: the lens is a checklist someone works through, and a list that
	// reorders between runs cannot be diffed or resumed.
	slices.SortFunc(out, func(a, b types.UnreferencedEntry) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// definingFiles returns the file node IDs that define the symbol, and whether any do.
func definingFiles(g *Graph, id string) (map[string]bool, bool) {
	files := map[string]bool{}
	for _, e := range g.in[id] {
		if e.Relation == types.RelationDefines {
			files[e.Source] = true
		}
	}
	return files, len(files) > 0
}

// namedBySomethingElse reports whether anything other than the symbol's own definition
// sites reaches it: a call from another symbol, or a reference from another file.
func namedBySomethingElse(g *Graph, id string, defFiles map[string]bool) bool {
	for _, e := range g.in[id] {
		switch e.Relation {
		case types.RelationCalls:
			return true
		case types.RelationReferences:
			if !defFiles[e.Source] {
				return true
			}
		}
	}
	return false
}

// projectOfSource extracts the leading directory of a "path:line" source, as a coarse
// grouping key for the report. It is presentation only - the graph's own project edges
// are authoritative - so a source with no directory yields "".
func projectOfSource(source string) string {
	path, _, _ := strings.Cut(source, ":")
	dir, _, found := strings.Cut(path, "/")
	if !found {
		return ""
	}
	return dir
}
