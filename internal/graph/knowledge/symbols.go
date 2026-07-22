package knowledge

import (
	"strconv"
	"strings"

	"github.com/egladman/magus/types"
)

// Symbol ingestion is EXTRACTED, never inferred: the symbol nodes and their
// defines/references edges come straight from a SCIP index a per-language indexer
// produced. This assembler is types-only - the SCIP parsing lives in internal/symbols
// and reaches here as neutral types.KnowledgeSymbol records - so internal/graph/knowledge
// stays free of the SCIP dependency. Per the scale plan, symbol shards are per
// project and, once wired, loaded lazily; this file only builds the shard.

// symbolsShardSuffix names a project's symbol shard: "<project>@symbols". The "@"
// keeps it out of the plain-project namespace and marks it as lazily loaded.
const symbolsShardSuffix = "@symbols"

// SymbolsShardName returns the shard name for a project's ingested symbols.
func SymbolsShardName(project string) string { return project + symbolsShardSuffix }

// IsSymbolsShard reports whether a shard name is a per-project symbol shard - the
// shards excluded from the default (non-symbol-seeded) load path.
func IsSymbolsShard(name string) bool { return strings.HasSuffix(name, symbolsShardSuffix) }

// assembleSymbols builds one project's symbol shard from the ingested records: a
// symbol node per record, a `defines` edge from each defining file, and a
// `references` edge from each using file (one per file, carrying the occurrence
// count and capped lines in its provenance). It also materializes a `file` node for
// every path the index touched - so a SCIP-indexed source file is a browsable node
// the def/ref edges land on, not a dangling ID - and links each to the project that
// owns it (longest-prefix over the full project list, so a cross-project reference
// file is parented to its own project, not this shard's). File nodes and their
// project links ride in this lazy shard, so they surface on symbol-seeded queries;
// AddNode/AddEdge dedup by ID, so a file two indexes share merges cleanly. A record
// with neither a def nor a ref in the workspace still yields its node so an explain
// has something to land on.
func assembleSymbols(project string, syms []types.KnowledgeSymbol, projects []types.TargetGraphProject) Shard {
	s := Shard{Name: SymbolsShardName(project)}
	seenFiles := map[string]bool{}
	noteFile := func(path, language string) {
		if path == "" || seenFiles[path] {
			return
		}
		seenFiles[path] = true
		var attrs map[string]string
		if language != "" {
			attrs = map[string]string{"language": language}
		}
		s.Nodes = append(s.Nodes, types.KnowledgeNode{ID: fileID(path), Kind: types.KindFile, Label: path, Source: path, Attrs: attrs})
		if owner, ok := owningProjectPath(path, projects); ok {
			dn, de := containsChain(owner, path, fileID(path))
			s.Nodes = append(s.Nodes, dn...)
			s.Edges = append(s.Edges, de...)
		}
	}
	for _, sym := range syms {
		sID := symbolID(sym.Key)
		attrs := map[string]string{}
		if sym.Language != "" {
			attrs["language"] = sym.Language
		}
		if sym.SymbolKind != "" {
			attrs["symbol_kind"] = sym.SymbolKind
		}
		if sym.Moniker != "" {
			attrs["moniker"] = sym.Moniker
		}
		// Tested-by lens: how many referencing files are tests. Derived from the same
		// SCIP reference edges (no new data), so it rides this deterministic shard rather
		// than the observed coverage overlay. Absent (0) means no test directly names the
		// symbol - a coverage-independent hint that a symbol may be under-tested.
		if n := testRefCount(sym.Refs); n > 0 {
			attrs[AttrTestRefs] = strconv.Itoa(n)
		}
		s.Nodes = append(s.Nodes, types.KnowledgeNode{
			ID:     sID,
			Kind:   types.KindSymbol,
			Label:  sym.Label,
			Source: sym.Source,
			Attrs:  nilIfEmpty(attrs),
		})
		for _, def := range sym.Defs {
			noteFile(def, sym.Language)
			s.Edges = append(s.Edges, extractedEdge(fileID(def), sID, types.RelationDefines, def))
		}
		for _, ref := range sym.Refs {
			noteFile(ref.Path, sym.Language)
			s.Edges = append(s.Edges, extractedEdge(fileID(ref.Path), sID, types.RelationReferences, refProvenance(ref)))
		}
	}
	return s
}

// testRefCount counts the referencing files that are Go test files (path ends in
// "_test.go"). One entry per file (SCIP collapses a file's occurrences), so this is the
// number of distinct test files that name the symbol, not the raw occurrence count.
func testRefCount(refs []types.KnowledgeSymbolRef) int {
	n := 0
	for _, ref := range refs {
		if strings.HasSuffix(ref.Path, "_test.go") {
			n++
		}
	}
	return n
}

// refProvenance encodes a reference's occurrence count and capped line list into the
// edge provenance string, e.g. "scip count=3 lines=10,20". KnowledgeEdge has only a
// flat Provenance string (no attr map), so `magus refs` reads the count/lines back
// with parseRefProvenance - the two are kept together so the format has one home
// rather than being defined implicitly at the write site.
const refProvenancePrefix = "scip "

func refProvenance(ref types.KnowledgeSymbolRef) string {
	var b strings.Builder
	b.WriteString(refProvenancePrefix)
	b.WriteString("count=")
	b.WriteString(strconv.Itoa(ref.Count))
	if len(ref.Lines) > 0 {
		b.WriteString(" lines=")
		for i, ln := range ref.Lines {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.Itoa(ln))
		}
	}
	return b.String()
}

// parseRefProvenance decodes a refProvenance string back into its count and lines.
// ok=false for any provenance that is not this format (e.g. a defines edge, whose
// provenance is the plain file path), so a caller can tell a reference edge from the rest.
func parseRefProvenance(prov string) (count int, lines []int, ok bool) {
	rest, found := strings.CutPrefix(prov, refProvenancePrefix)
	if !found {
		return 0, nil, false
	}
	for _, field := range strings.Fields(rest) {
		switch {
		case strings.HasPrefix(field, "count="):
			count, _ = strconv.Atoi(strings.TrimPrefix(field, "count="))
		case strings.HasPrefix(field, "lines="):
			for _, s := range strings.Split(strings.TrimPrefix(field, "lines="), ",") {
				if n, err := strconv.Atoi(s); err == nil {
					lines = append(lines, n)
				}
			}
		}
	}
	return count, lines, true
}
