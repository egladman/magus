package knowledge

import (
	"strconv"
	"strings"

	"github.com/egladman/magus/types"
)

// Symbol ingestion is EXTRACTED, never inferred: the symbol nodes and their
// defines/references edges come straight from a SCIP index a per-language indexer
// produced. This assembler is types-only - the SCIP parsing lives in internal/symbols
// and reaches here as neutral types.KnowledgeSymbol records - so internal/knowledge
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
// count and capped lines in its provenance). Edges connect to file nodes the buzz
// shard defines by the same rel path; a record with neither a def nor a ref in the
// workspace still yields its node so an explain has something to land on.
func assembleSymbols(project string, syms []types.KnowledgeSymbol) Shard {
	s := Shard{Name: SymbolsShardName(project)}
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
		s.Nodes = append(s.Nodes, types.KnowledgeNode{
			ID:     sID,
			Kind:   types.KindSymbol,
			Label:  sym.Label,
			Source: sym.Source,
			Attrs:  nilIfEmpty(attrs),
		})
		for _, def := range sym.Defs {
			s.Edges = append(s.Edges, extractedEdge(fileID(def), sID, types.RelationDefines, def))
		}
		for _, ref := range sym.Refs {
			s.Edges = append(s.Edges, extractedEdge(fileID(ref.Path), sID, types.RelationReferences, refProvenance(ref)))
		}
	}
	return s
}

// refProvenance encodes a reference's occurrence count and capped line list into the
// edge provenance string, e.g. "scip count=3 lines=10,20". This is HUMAN-FACING
// provenance (how every edge carries its origin), not a machine-readable field:
// KnowledgeEdge has only a flat Provenance string, no attr map. Making the count
// programmatically queryable would mean adding structured attrs to KnowledgeEdge - a
// wire-schema change touching every edge - which is deliberately out of scope here.
func refProvenance(ref types.KnowledgeSymbolRef) string {
	var b strings.Builder
	b.WriteString("scip count=")
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
