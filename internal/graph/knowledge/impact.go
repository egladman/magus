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

// SymbolSpan is one symbol's identity and the lines it occupies in its defining file.
//
// EndLine is 0 when the indexer emitted no enclosing range, which several do not. That is the
// honest answer rather than a guessed extent, and a caller expanding a hunk to "the whole symbol"
// has to decide what to do with a start and no end rather than being handed a fabricated one.
type SymbolSpan struct {
	ID        string
	Label     string
	StartLine int
	EndLine   int
}

// SymbolAt returns the symbol whose definition encloses a 1-based line of a workspace-relative
// file, by the nearest-preceding-definition rule.
//
// The exported form of a lookup this package had written twice, unexported and single-purpose:
// once to attribute coverage blocks and once inside the SCIP parser to attribute calls. Both
// answered the same question, and neither could be asked from outside - so the review surface,
// which wants "which function is this hunk in", had no way to find out.
//
// Nearest-preceding rather than range-containment BECAUSE the end line is often missing. A
// containment test would answer "no symbol" for every indexer that emits no enclosing range,
// which is the commoner case and the one where a reader still wants an answer. Where EndLine IS
// known a caller can check it and decide; where it is not, this still names the declaration the
// line belongs to.
//
// The zero value means no symbol covers the line: a file with no ingested symbols, a line above
// the first definition, or a graph whose symbol shards were never merged. All three are "magus
// does not know", never "this line belongs to nothing".
func (g *Graph) SymbolAt(relPath string, line int) SymbolSpan {
	g.ensureAdj()
	var best SymbolSpan
	for _, e := range g.out[fileID(relPath)] {
		if e.Relation != types.RelationDefines {
			continue
		}
		sn, ok := g.node(e.Target)
		if !ok || sn.Kind != types.KindSymbol {
			continue
		}
		_, start, ok := splitPathLine(sn.Source)
		if !ok || start > line || start < best.StartLine {
			continue
		}
		best = SymbolSpan{ID: sn.ID, Label: sn.Label, StartLine: start, EndLine: defEndLine(sn)}
	}
	return best
}

// defEndLine reads AttrDefEndLine, or 0 where the indexer emitted no enclosing range.
//
// An unparseable value is 0 for the same reason a missing one is: the attr bounds a symbol, and a
// bound magus cannot read is a bound it does not have.
func defEndLine(n types.KnowledgeNode) int {
	end, err := strconv.Atoi(n.Attrs[AttrDefEndLine])
	if err != nil {
		return 0
	}
	return end
}
