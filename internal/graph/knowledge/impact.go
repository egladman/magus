package knowledge

import (
	"cmp"
	"slices"
	"strconv"

	"github.com/egladman/magus/types"
)

// FileFacts is the impact overlay for one workspace-relative source file: the symbols
// it defines (each with how widely it is referenced and its observed coverage) and the
// file-level coverage. It is the read surface `magus affected --impact` folds onto its
// blast radius - callers (from the SCIP reference edges the @symbols shards carry) and
// coverage (from the @coverage overlay) for exactly the files a changeset touched. The
// zero value (no coverage, no symbols) is the honest answer for a file with no ingested
// symbol node, so the caller degrades gracefully rather than treating it as an error.
type FileFacts struct {
	// Coverage is the file-level observed coverage, or nil when the @coverage overlay
	// does not cover this file (no `magus run coverage`, or the file has no statements).
	Coverage *CoverageFacts
	// Symbols are the symbols defined in the file, sorted by descending reference count
	// then ID, so the most-referenced (highest-blast-radius) symbol leads.
	Symbols []SymbolFacts
}

// SymbolFacts is one symbol defined in a changed file: its identity, how many
// references and distinct referencing files the symbol index recorded for it (the
// caller spread), and its own observed coverage when a profile is loaded.
type SymbolFacts struct {
	ID        string
	Label     string
	RefCount  int
	FileCount int
	Coverage  *CoverageFacts
}

// CoverageFacts is a covered/total statement tally and its ratio (0..1), read back from
// the coverage attrs the @coverage overlay folded onto a file or symbol node.
type CoverageFacts struct {
	Ratio   float64
	Covered int
	Total   int
}

// FileFacts returns the caller and coverage overlay for a workspace-relative source
// file. It walks the file node's outgoing `defines` edges to the symbols declared in
// it, tallies each symbol's incoming SCIP `references` edges (occurrence count and
// distinct files), and reads the coverage attrs the @coverage overlay merged onto the
// file and symbol nodes. A file with no symbol node (no SCIP index ingested, or a
// non-code file) yields the zero value. Callers must have merged the @symbols and
// @coverage shards (KnowledgeGraphWithSymbols / MergeWorkspaceSymbols) first; on a
// symbol-free graph every file yields the zero value.
func (g *Graph) FileFacts(relPath string) FileFacts {
	g.ensureAdj()
	var ff FileFacts
	fid := fileID(relPath)
	if fn, ok := g.node(fid); ok {
		ff.Coverage = coverageOf(fn)
	}
	for _, e := range g.out[fid] {
		if e.Relation != types.RelationDefines {
			continue
		}
		sn, ok := g.node(e.Target)
		if !ok || sn.Kind != types.KindSymbol {
			continue
		}
		sf := SymbolFacts{ID: sn.ID, Label: sn.Label, Coverage: coverageOf(sn)}
		for _, in := range g.in[sn.ID] {
			if in.Relation != types.RelationReferences {
				continue
			}
			// Only a SCIP-ingested reference edge carries the count provenance; a
			// non-SCIP references edge (e.g. charm->target) is skipped rather than
			// counted as a phantom count-0 caller, mirroring Graph.Refs.
			count, _, ok := parseRefProvenance(in.Provenance)
			if !ok {
				continue
			}
			sf.RefCount += count
			sf.FileCount++
		}
		ff.Symbols = append(ff.Symbols, sf)
	}
	slices.SortFunc(ff.Symbols, func(a, b SymbolFacts) int {
		if c := cmp.Compare(b.RefCount, a.RefCount); c != 0 { // descending: widest reach first
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return ff
}

// coverageOf reads the covered/total ratio the @coverage overlay folded onto a node,
// returning nil when the node carries no coverage attr (the file/symbol was not in the
// profile). It is the read counterpart to coverageAttrs.
func coverageOf(n types.KnowledgeNode) *CoverageFacts {
	raw, ok := n.Attrs[AttrCoverage]
	if !ok {
		return nil
	}
	ratio, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil
	}
	covered, _ := strconv.Atoi(n.Attrs[AttrCoveredStmts])
	total, _ := strconv.Atoi(n.Attrs[AttrTotalStmts])
	return &CoverageFacts{Ratio: ratio, Covered: covered, Total: total}
}
