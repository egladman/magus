package knowledge

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/coverage"
	"github.com/egladman/magus/types"
)

// Coverage ingestion is OBSERVED, not extracted: it parses the Go coverage profile
// magus produces (`magus run test .` writes .magus/coverage.out) and folds a per-file (and
// per-symbol) covered-statement ratio onto the file/symbol nodes SCIP already minted.
// It never churns the deterministic @symbols shards it annotates: the ratio is
// volatile local run data, so it rides an isolated @coverage shard, re-derived each
// build and excluded from remote export, exactly as the @runtime timing overlay does.
// This mirrors the run-history pattern: an overlay of partial typed nodes whose attrs
// merge onto the real nodes order-independently once the symbol shards are loaded.

// coverageShardName is the isolated shard holding the observed coverage overlay. Like
// @symbols it is omitted from the default graph (its merge targets, Go file and symbol
// nodes, are themselves lazy) and pulled in alongside the symbol shards; like @runtime
// it is local-only, never pushed to a remote.
const coverageShardName = "@coverage"

// isCoverageShard reports whether name is the observed coverage overlay shard.
func isCoverageShard(name string) bool { return name == coverageShardName }

// FileCoverage is one source file's parsed coverage: its workspace-relative path, the
// covered/total statement totals (the file-level ratio), and the raw blocks (for
// symbol-level attribution). It has [coverage.File]'s shape, so one converts to the other.
type FileCoverage struct {
	Path    string
	Covered int
	Total   int
	Blocks  []coverage.Block
}

// ParseCoverage parses a Go coverage profile into per-file coverage as
// [coverage.ParseGoProfile] reads it. A profile that covers nothing yields nil.
func ParseCoverage(profile []byte, modulePath string) []FileCoverage {
	files := coverage.ParseGoProfile(profile, modulePath)
	if files == nil {
		return nil
	}
	out := make([]FileCoverage, len(files))
	for i, f := range files {
		out[i] = FileCoverage(f)
	}
	return out
}

// coverageAttrs renders the covered/total ratio into the attr map shared by file and
// symbol nodes. An empty map (total == 0) means "no instrumented statements", so no
// node is emitted; a file with zero statements is not the same as a covered one.
func coverageAttrs(covered, total int) map[string]string {
	if total == 0 {
		return nil
	}
	ratio := float64(covered) / float64(total)
	return map[string]string{
		attrCoverage:     strconv.FormatFloat(ratio, 'f', 2, 64),
		AttrCoveredStmts: strconv.Itoa(covered),
		AttrTotalStmts:   strconv.Itoa(total),
	}
}

// assembleCoverage builds the isolated @coverage overlay: a partial file node per
// covered file carrying the file-level ratio, and (when the file's symbols are known
// from the SCIP ingestion) a partial symbol node per function carrying its own ratio.
// Symbol attribution is a nearest-preceding-definition heuristic: each coverage block is
// credited to the symbol with the greatest definition line at or before the block's
// start line (the enclosing declaration), so a block above the first symbol (imports,
// package-level init) contributes only to the file total. It is best-effort (SCIP gives
// a symbol's definition line, not its body's end, so a package-level statement wedged
// between two functions can be mis-credited), but it is exact for the common case of
// top-level functions and methods, which is what "which function lacks coverage" needs.
// All nodes are partial (ID + kind + attrs): they merge onto the real file/symbol nodes
// the @symbols shards define, whichever shard the loader reaches first.
func assembleCoverage(cov []FileCoverage, symbols map[string][]types.KnowledgeSymbol) Shard {
	s := Shard{Name: coverageShardName}
	if len(cov) == 0 {
		return s
	}
	symsByFile := symbolsByDefFile(symbols)
	for _, fc := range cov {
		if attrs := coverageAttrs(fc.Covered, fc.Total); attrs != nil {
			s.Nodes = append(s.Nodes, types.KnowledgeNode{
				ID:    fileID(fc.Path),
				Kind:  types.KindFile,
				Label: fc.Path,
				Attrs: attrs,
			})
		}
		s.Nodes = append(s.Nodes, symbolCoverage(fc, symsByFile[fc.Path])...)
	}
	return s
}

// fileSymbolDef pairs a symbol with the definition line it was declared on, for the
// nearest-preceding-definition attribution.
type fileSymbolDef struct {
	key  string
	line int
}

// symbolsByDefFile groups every ingested symbol by the file it is defined in, parsing
// the "path:line" Source into a def line. A symbol with no Source (reference-only) is
// skipped: coverage attaches to a definition, not a use site.
func symbolsByDefFile(symbols map[string][]types.KnowledgeSymbol) map[string][]fileSymbolDef {
	out := map[string][]fileSymbolDef{}
	for _, syms := range symbols {
		for _, sym := range syms {
			path, line, ok := splitPathLine(sym.Source)
			if !ok {
				continue
			}
			out[path] = append(out[path], fileSymbolDef{key: sym.Key, line: line})
		}
	}
	for path := range out {
		slices.SortFunc(out[path], func(a, b fileSymbolDef) int { return a.line - b.line })
	}
	return out
}

// symbolCoverage attributes a file's coverage blocks to its defined symbols by the
// nearest-preceding-definition rule and emits one partial symbol node per symbol that
// owns at least one instrumented statement. defs must be sorted by line ascending.
func symbolCoverage(fc FileCoverage, defs []fileSymbolDef) []types.KnowledgeNode {
	if len(defs) == 0 {
		return nil
	}
	type tally struct{ covered, total int }
	tallies := make([]tally, len(defs))
	for _, blk := range fc.Blocks {
		i := enclosingDef(defs, blk.StartLine)
		if i < 0 {
			continue // above the first definition: file-level only
		}
		tallies[i].total += blk.Units
		if blk.Hits > 0 {
			tallies[i].covered += blk.Units
		}
	}
	var out []types.KnowledgeNode
	for i, t := range tallies {
		attrs := coverageAttrs(t.covered, t.total)
		if attrs == nil {
			continue
		}
		out = append(out, types.KnowledgeNode{
			ID:    symbolID(defs[i].key),
			Kind:  types.KindSymbol,
			Attrs: attrs,
		})
	}
	return out
}

// enclosingDef returns the index of the symbol whose definition line is the greatest at
// or before startLine (the declaration a block belongs to), or -1 when the block sits
// above every definition. defs must be sorted by line ascending.
func enclosingDef(defs []fileSymbolDef, startLine int) int {
	found := -1
	for i, d := range defs {
		if d.line > startLine {
			break
		}
		found = i
	}
	return found
}

// coverageGapError is why a profile that omits whole directories is left out.
func coverageGapError(gaps []string) error {
	shown := gaps
	if len(shown) > 5 {
		shown = shown[:5]
	}
	return fmt.Errorf("coverage profile omits %d director(ies) with indexed functions, e.g. %s: "+
		"a narrower run wrote it, so run `magus run test .` unnarrowed or delete .magus/coverage.out",
		len(gaps), strings.Join(shown, ", "))
}

// CoverageGaps names directories with Go functions the profile has no file for.
// A narrower run wrote such a profile, so a caller refuses it. Types and
// constants hold no statements and never count.
func CoverageGaps(cov []FileCoverage, symbols map[string][]types.KnowledgeSymbol) []string {
	profiled := map[string]bool{}
	for _, fc := range cov {
		profiled[path.Dir(fc.Path)] = true
	}
	seen := map[string]bool{}
	for _, syms := range symbols {
		for _, sym := range syms {
			if !strings.EqualFold(sym.SymbolKind, "function") && !strings.EqualFold(sym.SymbolKind, "method") {
				continue
			}
			file, _, ok := splitPathLine(sym.Source)
			if !ok || !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
				continue
			}
			if dir := path.Dir(file); !profiled[dir] {
				seen[dir] = true
			}
		}
	}
	gaps := make([]string, 0, len(seen))
	for dir := range seen {
		gaps = append(gaps, dir)
	}
	slices.Sort(gaps)
	return gaps
}
