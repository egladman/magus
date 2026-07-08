// Package symbols distills a SCIP index file into the language-agnostic
// types.KnowledgeSymbol shape the knowledge graph ingests. It is the ONE place that
// imports the SCIP bindings: magus never parses source code itself - a per-language
// indexer (scip-go, scip-typescript, ...) emits the index, and this reads it - so
// adding a language is a build-config change, never a magus code change. Keeping the
// scip dependency here leaves internal/knowledge a types-only leaf.
package symbols

import (
	"sort"
	"strconv"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/egladman/magus/types"
)

// MaxRefLines caps the occurrence lines recorded per (file, symbol) reference, so a
// symbol used thousands of times in one file keeps a bounded provenance list.
const MaxRefLines = 20

// ReadIndex parses a SCIP index (protobuf bytes) into per-symbol records. Occurrence
// granularity is collapsed to per (file, symbol): one Defs/Refs entry per file, with
// a count and a capped line list, so a hot symbol yields at most one edge per file
// rather than one per occurrence. `local N` symbols (index-scoped, not stable across
// builds) are skipped; the version segment is dropped from the ID so a dependency
// bump does not churn every symbol node. Output is sorted for deterministic assembly.
func ReadIndex(data []byte) ([]types.KnowledgeSymbol, error) {
	var idx scip.Index
	if err := proto.Unmarshal(data, &idx); err != nil {
		return nil, err
	}

	// SymbolInformation (display name, kind) can live in any document; index it by
	// moniker so a symbol referenced before its defining document is still named.
	infoByMoniker := map[string]*scip.SymbolInformation{}
	for _, doc := range idx.Documents {
		for _, si := range doc.Symbols {
			infoByMoniker[si.Symbol] = si
		}
	}

	type acc struct {
		sym  types.KnowledgeSymbol
		defs map[string]bool                      // def file -> seen
		refs map[string]*types.KnowledgeSymbolRef // ref file -> tally
	}
	byID := map[string]*acc{}
	var order []string

	for _, doc := range idx.Documents {
		for _, occ := range doc.Occurrences {
			moniker := occ.Symbol
			if moniker == "" || scip.IsLocalSymbol(moniker) {
				continue
			}
			id, label, ok := parseMoniker(moniker)
			if !ok {
				continue
			}
			a := byID[id]
			if a == nil {
				a = &acc{
					sym:  types.KnowledgeSymbol{ID: id, Moniker: moniker, Label: label, Language: doc.Language},
					defs: map[string]bool{},
					refs: map[string]*types.KnowledgeSymbolRef{},
				}
				if si := infoByMoniker[moniker]; si != nil {
					if si.DisplayName != "" {
						a.sym.Label = si.DisplayName
					}
					a.sym.Kind = si.Kind.String()
				}
				byID[id] = a
				order = append(order, id)
			}
			line := occurrenceLine(occ)
			if occ.SymbolRoles&int32(scip.SymbolRole_Definition) != 0 {
				if !a.defs[doc.RelativePath] {
					a.defs[doc.RelativePath] = true
				}
				if a.sym.Source == "" {
					a.sym.Source = doc.RelativePath + ":" + strconv.Itoa(line)
				}
			} else {
				r := a.refs[doc.RelativePath]
				if r == nil {
					r = &types.KnowledgeSymbolRef{Path: doc.RelativePath}
					a.refs[doc.RelativePath] = r
				}
				r.Count++
				if len(r.Lines) < MaxRefLines {
					r.Lines = append(r.Lines, line)
				}
			}
		}
	}

	out := make([]types.KnowledgeSymbol, 0, len(order))
	for _, id := range order {
		a := byID[id]
		a.sym.Defs = sortedKeys(a.defs)
		a.sym.Refs = sortedRefs(a.refs)
		out = append(out, a.sym)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// parseMoniker turns a SCIP moniker into a stable, version-free node ID and a
// display label. The ID is the package name plus the descriptor path, deliberately
// excluding the package version so a dependency bump does not rename every symbol.
// A local or unparseable moniker yields ok=false (the caller skips it).
func parseMoniker(moniker string) (id, label string, ok bool) {
	sym, err := scip.ParseSymbol(moniker)
	if err != nil || sym.Package == nil {
		return "", "", false
	}
	desc := scip.DescriptorOnlyFormatter.FormatSymbol(sym)
	id = sym.Package.Name + " " + desc
	if n := len(sym.Descriptors); n > 0 {
		label = sym.Descriptors[n-1].Name
	}
	return id, label, true
}

// occurrenceLine returns the 1-based start line of an occurrence (SCIP ranges are
// 0-based [startLine, startChar, ...]), or 0 when the range is absent.
func occurrenceLine(occ *scip.Occurrence) int {
	if len(occ.Range) == 0 {
		return 0
	}
	return int(occ.Range[0]) + 1
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedRefs(m map[string]*types.KnowledgeSymbolRef) []types.KnowledgeSymbolRef {
	if len(m) == 0 {
		return nil
	}
	out := make([]types.KnowledgeSymbolRef, 0, len(m))
	for _, r := range m {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
