// Package symbols distills a SCIP index file into the language-agnostic
// types.KnowledgeSymbol shape the knowledge graph ingests. It is the ONE place that
// imports the SCIP bindings: magus never parses source code itself - a per-language
// indexer (scip-go, scip-typescript, ...) emits the index, and this reads it - so
// adding a language is a build-config change, never a magus code change. Keeping the
// scip dependency here leaves internal/graph/knowledge a types-only leaf.
package symbols

import (
	"cmp"
	"log/slog"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/egladman/magus/types"
)

// MaxRefLines caps the occurrence lines recorded per (file, symbol) reference, so a
// symbol used thousands of times in one file keeps a bounded provenance list.
const MaxRefLines = 20

// ParseIndex decodes a SCIP index (protobuf bytes) into per-symbol records - it does
// no I/O, the caller supplies the bytes. Occurrence granularity is collapsed to per
// (file, symbol): one Defs/Refs entry per file, with a count and a capped line list,
// so a hot symbol yields at most one edge per file rather than one per occurrence.
// `local N` symbols (index-scoped, not stable across builds) are skipped; the version
// segment is dropped from the key so a dependency bump does not churn every symbol
// node. Output is sorted by key for deterministic assembly.
//
// projectPath is the ingested project's workspace-relative path. SCIP document paths are
// relative to the INDEXER's root (the project dir, where magus runs the indexer), so
// each is joined onto projectPath to become workspace-relative - the same spine the buzz
// file nodes and project->file edges use, so a nested project's symbols land on the right
// files instead of dangling at the workspace root. projectPath "" or "." leaves paths
// unchanged (the root project's paths are already workspace-relative).
func ParseIndex(data []byte, projectPath string) ([]types.KnowledgeSymbol, error) {
	var idx scip.Index
	if err := proto.Unmarshal(data, &idx); err != nil {
		return nil, err
	}

	// SymbolInformation (display name, kind) can live in any document; index it by
	// the version-stripped KEY (not the raw moniker) so a symbol whose definition and
	// first-seen reference carry different-version monikers is still named.
	infoByKey := map[string]*scip.SymbolInformation{}
	for _, doc := range idx.Documents {
		for _, si := range doc.Symbols {
			if key, _, ok := parseMoniker(si.Symbol); ok {
				infoByKey[key] = si
			}
		}
	}

	type acc struct {
		sym  types.KnowledgeSymbol
		defs map[string]bool                      // set of defining files
		refs map[string]*types.KnowledgeSymbolRef // ref file -> tally
	}
	byKey := map[string]*acc{}
	skipped := 0

	for _, doc := range idx.Documents {
		// Rebase the indexer-relative document path onto the workspace once per document.
		// A document outside the workspace (a dependency the indexer resolved into the
		// module or build cache) is skipped entirely: none of its occurrences describe
		// code this workspace owns.
		docPath, ok := workspacePath(projectPath, doc.RelativePath)
		if !ok {
			skipped++
			continue
		}
		for _, occ := range doc.Occurrences {
			moniker := occ.Symbol
			if moniker == "" || scip.IsLocalSymbol(moniker) {
				continue
			}
			key, label, ok := parseMoniker(moniker)
			if !ok {
				continue
			}
			a := byKey[key]
			if a == nil {
				a = &acc{
					// Canonicalize the language: indexers disagree on casing ("Go" vs "go",
					// "TypeScript"), so lowercasing lines it up with the spells' declared
					// values and makes `language:` a reliable join across both.
					sym:  types.KnowledgeSymbol{Key: key, Moniker: moniker, Label: label, Language: strings.ToLower(strings.TrimSpace(doc.Language))},
					defs: map[string]bool{},
					refs: map[string]*types.KnowledgeSymbolRef{},
				}
				if si := infoByKey[key]; si != nil {
					if si.DisplayName != "" {
						a.sym.Label = si.DisplayName
					}
					a.sym.SymbolKind = si.Kind.String()
				}
				byKey[key] = a
			}
			line := occurrenceLine(occ)
			if occ.SymbolRoles&int32(scip.SymbolRole_Definition) != 0 {
				a.defs[docPath] = true
				// First definition seen (in document then occurrence order, both stable
				// slices) wins the Source; later defs still add their defines edge.
				if a.sym.Source == "" {
					a.sym.Source = docPath + ":" + strconv.Itoa(line)
				}
			} else {
				r := a.refs[docPath]
				if r == nil {
					r = &types.KnowledgeSymbolRef{Path: docPath}
					a.refs[docPath] = r
				}
				r.Count++
				if len(r.Lines) < MaxRefLines {
					r.Lines = append(r.Lines, line)
				}
			}
		}
	}

	out := make([]types.KnowledgeSymbol, 0, len(byKey))
	for _, a := range byKey {
		a.sym.Defs = sortedKeys(a.defs)
		a.sym.Refs = sortedRefs(a.refs)
		out = append(out, a.sym)
	}
	// byKey iteration is unordered; the sort is what makes the output deterministic.
	slices.SortFunc(out, func(x, y types.KnowledgeSymbol) int { return cmp.Compare(x.Key, y.Key) })
	if skipped > 0 {
		// Dropping a dependency document is normal scip-go output, not a fault - but an
		// index filtered to nothing looks exactly like a scip target that never ran, and
		// the caller logs nothing for an empty result. The count is what tells those
		// apart when an indexer's root does not match what magus assumed.
		slog.Debug("symbols: skipped documents outside the workspace",
			slog.String("project", projectPath),
			slog.Int("skipped", skipped),
			slog.Int("kept", len(idx.Documents)-skipped))
	}
	return out, nil
}

// workspacePath rebases an indexer-relative document path onto the workspace by joining
// it under the project's workspace-relative path, and reports whether the document lives
// in the workspace at all.
//
// This is the ONE place an externally-supplied path enters the knowledge graph: every
// other path-bearing node comes from magus walking its own tree. So the check belongs
// here and nowhere downstream.
//
// ok is false for a document outside the workspace, and the caller drops it. An indexer
// routinely emits those: scip-go records occurrences in the packages it resolved, so a
// document path can point into the module or build cache. Rebasing one produced paths
// like "../../../../../Library/Caches/go-build/01/abc", which became real graph nodes -
// a committed graph carried 93 of them, describing a layout that existed on one laptop.
// The graph is committed, published, and shared through the remote cache, so dropping is
// the whole answer rather than clamping: a file outside the workspace is not the
// workspace's to describe.
//
// rel is validated BEFORE it is joined, which is load-bearing. path.Join cleans, so
// joining first cancels ".." against the project path instead of rejecting it:
// path.Join("libs/gopherbuzz", "../../internal/x.go") is "internal/x.go", which has no
// ".." left to find. That silently re-attributes a document to an unrelated project
// rather than dropping it, and it only shows up when the ".." count is within the
// project's own depth.
func workspacePath(projectPath, rel string) (string, bool) {
	cleaned := path.Clean(rel)
	if !workspaceRelative(cleaned) {
		return "", false
	}
	if projectPath == "" || projectPath == "." {
		return cleaned, true
	}
	return path.Join(projectPath, cleaned), true
}

// workspaceRelative reports whether p is a relative path that stays inside the tree it is
// relative to. It takes an already-cleaned path, so a ".." can only appear as a leading
// segment that Clean could not resolve.
func workspaceRelative(p string) bool {
	if p == "" || p == "." || path.IsAbs(p) {
		return false
	}
	return !slices.Contains(strings.Split(p, "/"), "..")
}

// parseMoniker turns a SCIP moniker into a stable, version-free node key and a
// display label. The key is the package manager and name plus the descriptor path,
// deliberately excluding the package VERSION so a dependency bump does not rename
// every symbol - but including the manager so two ecosystems that share a package
// name (npm foo vs gomod foo) do not collide. A local or unparseable moniker yields
// ok=false (the caller skips it).
func parseMoniker(moniker string) (key, label string, ok bool) {
	sym, err := scip.ParseSymbol(moniker)
	if err != nil || sym.Package == nil {
		return "", "", false
	}
	desc := scip.DescriptorOnlyFormatter.FormatSymbol(sym)
	pkg := strings.TrimSpace(sym.Package.Manager + " " + sym.Package.Name)
	key = strings.TrimSpace(pkg + " " + desc)
	if n := len(sym.Descriptors); n > 0 {
		label = sym.Descriptors[n-1].Name
	}
	return key, label, true
}

// occurrenceLine returns the 1-based start line of an occurrence, or 0 when absent.
// It reads SourceRange (which resolves both the modern typed_range and the deprecated
// packed range) - reading the deprecated field alone would report 0 for every
// occurrence a current indexer emits. A negative start (malformed index) clamps to 0.
func occurrenceLine(occ *scip.Occurrence) int {
	r, ok := occ.SourceRange()
	if !ok || r.Start.Line < 0 {
		return 0
	}
	return int(r.Start.Line) + 1
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
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
	slices.SortFunc(out, func(x, y types.KnowledgeSymbolRef) int { return cmp.Compare(x.Path, y.Path) })
	return out
}
