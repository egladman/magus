package knowledge

import (
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/types"
)

// Coverage ingestion is OBSERVED, not extracted: it parses the Go coverage profile
// magus produces (`magus run coverage` writes coverage.out) and folds a per-file (and
// per-symbol) covered-statement ratio onto the file/symbol nodes SCIP already minted.
// It never churns the deterministic @symbols shards it annotates - the ratio is
// volatile local run data, so it rides an isolated @coverage shard, re-derived each
// build and excluded from remote export, exactly as the @runtime timing overlay does.
// This mirrors the run-history pattern: an overlay of partial typed nodes whose attrs
// merge onto the real nodes order-independently once the symbol shards are loaded.

// CoverageShardName is the isolated shard holding the observed coverage overlay. Like
// @symbols it is omitted from the default graph (its merge targets - Go file and symbol
// nodes - are themselves lazy) and pulled in alongside the symbol shards; like @runtime
// it is local-only, never pushed to a remote.
const CoverageShardName = "@coverage"

// IsCoverageShard reports whether name is the observed coverage overlay shard.
func IsCoverageShard(name string) bool { return name == CoverageShardName }

// CoverageBlock is one line-range record from a Go coverage profile: the statement
// count the range holds and whether the run hit it. Retained per file (not just
// aggregated) so coverage can be attributed to individual symbols by line range.
type CoverageBlock struct {
	StartLine int
	EndLine   int
	NumStmt   int
	Hits      int
}

// FileCoverage is one source file's parsed coverage: its workspace-relative path, the
// covered/total statement totals (the file-level ratio), and the raw blocks (for
// symbol-level attribution). Covered counts statements the run hit at least once.
type FileCoverage struct {
	Path    string
	Covered int
	Total   int
	Blocks  []CoverageBlock
}

// ParseCoverage parses a Go coverage profile into per-file coverage, keyed by
// workspace-relative path. Profile lines are module-qualified
// ("github.com/egladman/magus/internal/foo.go:12.2,14.16 3 1"), so modulePath (the
// go.mod module path) is stripped to recover the workspace-relative path the file and
// symbol nodes use. A line outside modulePath (a nested module, or a std/vendored path)
// is dropped rather than mis-attributed. Malformed lines are skipped, never fatal:
// coverage is best-effort enrichment. The result is sorted by path for deterministic
// assembly. A profile that covers nothing yields nil.
func ParseCoverage(profile []byte, modulePath string) []FileCoverage {
	prefix := modulePath
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	byPath := map[string]*FileCoverage{}
	for _, raw := range strings.Split(string(profile), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		path, blk, ok := parseCoverageLine(line)
		if !ok {
			continue
		}
		rel, ok := strings.CutPrefix(path, prefix)
		if !ok || prefix == "" {
			// prefix == "" means we could not learn the module path; without it the
			// module-qualified path never matches a node, so drop rather than guess.
			continue
		}
		fc := byPath[rel]
		if fc == nil {
			fc = &FileCoverage{Path: rel}
			byPath[rel] = fc
		}
		fc.Total += blk.NumStmt
		if blk.Hits > 0 {
			fc.Covered += blk.NumStmt
		}
		fc.Blocks = append(fc.Blocks, blk)
	}
	if len(byPath) == 0 {
		return nil
	}
	out := make([]FileCoverage, 0, len(byPath))
	for _, fc := range byPath {
		out = append(out, *fc)
	}
	slices.SortFunc(out, func(a, b FileCoverage) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// parseCoverageLine decodes one profile record "<path>:<sl>.<sc>,<el>.<ec> <n> <h>"
// into its path and block. ok=false for any line that does not match the shape, so a
// malformed or truncated profile degrades to fewer records, never a panic.
func parseCoverageLine(line string) (path string, blk CoverageBlock, ok bool) {
	// Split off the two trailing space-separated integers (numStmt, hitCount) first, so
	// a path containing a space (rare, but legal on disk) does not confuse the parse.
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return "", CoverageBlock{}, false
	}
	hits, err := strconv.Atoi(fields[len(fields)-1])
	if err != nil {
		return "", CoverageBlock{}, false
	}
	numStmt, err := strconv.Atoi(fields[len(fields)-2])
	if err != nil {
		return "", CoverageBlock{}, false
	}
	loc := strings.Join(fields[:len(fields)-2], " ")
	colon := strings.LastIndexByte(loc, ':')
	if colon < 0 {
		return "", CoverageBlock{}, false
	}
	path = loc[:colon]
	// The range is "startLine.startCol,endLine.endCol"; only the line numbers matter for
	// symbol attribution, so parse the line halves and ignore the columns.
	rng := loc[colon+1:]
	startPart, endPart, found := strings.Cut(rng, ",")
	if !found {
		return "", CoverageBlock{}, false
	}
	startLine, ok1 := lineOf(startPart)
	endLine, ok2 := lineOf(endPart)
	if !ok1 || !ok2 {
		return "", CoverageBlock{}, false
	}
	return path, CoverageBlock{StartLine: startLine, EndLine: endLine, NumStmt: numStmt, Hits: hits}, true
}

// lineOf pulls the line number out of a "line.col" half of a coverage range.
func lineOf(part string) (int, bool) {
	lineStr, _, _ := strings.Cut(part, ".")
	n, err := strconv.Atoi(lineStr)
	if err != nil {
		return 0, false
	}
	return n, true
}

// coverageAttrs renders the covered/total ratio into the attr map shared by file and
// symbol nodes. An empty map (total == 0) means "no instrumented statements", so no
// node is emitted - a file with zero statements is not the same as a covered one.
func coverageAttrs(covered, total int) map[string]string {
	if total == 0 {
		return nil
	}
	ratio := float64(covered) / float64(total)
	return map[string]string{
		AttrCoverage:     strconv.FormatFloat(ratio, 'f', 2, 64),
		AttrCoveredStmts: strconv.Itoa(covered),
		AttrTotalStmts:   strconv.Itoa(total),
	}
}

// assembleCoverage builds the isolated @coverage overlay: a partial file node per
// covered file carrying the file-level ratio, and - when the file's symbols are known
// from the SCIP ingestion - a partial symbol node per function carrying its own ratio.
// Symbol attribution is a nearest-preceding-definition heuristic: each coverage block is
// credited to the symbol with the greatest definition line at or before the block's
// start line (the enclosing declaration), so a block above the first symbol (imports,
// package-level init) contributes only to the file total. It is best-effort - SCIP gives
// a symbol's definition line, not its body's end, so a package-level statement wedged
// between two functions can be mis-credited - but it is exact for the common case of
// top-level functions and methods, which is what "which function lacks coverage" needs.
// All nodes are partial (ID + kind + attrs): they merge onto the real file/symbol nodes
// the @symbols shards define, whichever shard the loader reaches first.
func assembleCoverage(cov []FileCoverage, symbols map[string][]types.KnowledgeSymbol) Shard {
	s := Shard{Name: CoverageShardName}
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
		tallies[i].total += blk.NumStmt
		if blk.Hits > 0 {
			tallies[i].covered += blk.NumStmt
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
