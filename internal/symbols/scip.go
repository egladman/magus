// Package symbols distills a SCIP index file into the language-agnostic
// types.KnowledgeSymbol shape the knowledge graph ingests. It is the ONE place that
// imports the SCIP bindings: magus never parses source code itself - a per-language
// indexer (scip-go, scip-typescript, ...) emits the index, and this reads it - so
// adding a language is a build-config change, never a magus code change. Keeping the
// scip dependency here leaves internal/graph/knowledge a types-only leaf.
package symbols

import (
	"cmp"
	"context"
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

// isCallableSuffix reports whether a SCIP descriptor suffix names something that can be
// invoked. An indexer's enclosing range spans a whole declaration, signature included, so
// the occurrences inside it are every symbol the definition mentions - not just the ones
// it calls. Measured over this repo's own index, only 26.4% of the in-workspace
// occurrences attributed that way are callables: 37.4% are struct fields, 15.4% types,
// 11.8% packages. Emitting all of them under the name "calls" would be wrong roughly three
// times in four. A field or type reference is still recorded, at file granularity, by the
// `references` edge.
//
// The suffix comes from SCIP's descriptor grammar - part of the moniker, which parseMoniker
// already decodes - rather than from SymbolInformation.Kind. Kind is OPTIONAL, and an
// indexer that omits it would silently produce no calls at all: scip-typescript 0.4.0 sets
// UnspecifiedKind on all 9137 of this workspace's console symbols, while its monikers carry
// 8440 Method descriptors. Reading the grammar keeps the rule language-neutral and
// independent of what any one indexer chooses to populate.
func isCallableSuffix(suffix scip.Descriptor_Suffix) bool {
	// Macro counts because a macro invocation is a call in the languages that have them
	// (Rust macro_rules!, C #define); Type, Term, Namespace, Parameter, and Meta do not.
	return suffix == scip.Descriptor_Method || suffix == scip.Descriptor_Macro
}

// enclosingDef is one definition's body span and the symbol key it attributes occurrences to.
//
// optimization: maxEnd is the greatest End among this entry and every earlier one, filled
// as a running prefix maximum after the sort. It bounds the backward walk in enclosingKey:
// without it, an occurrence that sits inside no body at all - a package-level var, an
// import - scans back through every definition in the document, making the parse
// O(occurrences x definitions). Most occurrences are that case, so the guard is what keeps
// attribution proportional to nesting depth rather than to document size.
// measured: BenchmarkParseIndex/enclosing=on funcsPerDoc 8 -> 512 (benchstat, n=10).
type enclosingDef struct {
	rng    scip.Range
	maxEnd scip.Position
	key    string
}

// enclosingKey returns the key of the innermost definition body containing pos.
//
// encl must be sorted by start ascending then end DESCENDING, with maxEnd filled. That
// ordering is what makes a backward walk find the innermost match first: a nested body
// starts at or after its parent, and when two share a start the longer (outer) one sorts
// first, so the walk reaches the inner one before it.
//
// Positions are compared in full (line and character), never on the collapsed line
// occurrenceLine returns. A line-granular test is wrong wherever a body starts or ends on
// a line it shares with another - impossible for a Go top-level func, routine for a
// one-line TypeScript arrow function.
func enclosingKey(encl []enclosingDef, pos scip.Position) (string, bool) {
	// The first entry starting strictly after pos bounds the candidates: everything that
	// could contain pos begins at or before it.
	i, _ := slices.BinarySearchFunc(encl, pos, func(e enclosingDef, p scip.Position) int {
		if e.rng.Start.Compare(p) <= 0 {
			return -1
		}
		return 1
	})
	for j := i - 1; j >= 0 && encl[j].maxEnd.Compare(pos) > 0; j-- {
		if encl[j].rng.Contains(pos) {
			return encl[j].key, true
		}
	}
	return "", false
}

// sortedEnclosing collects the definition occurrences in one document that carry an
// enclosing range, sorted and prefix-maxed into the exact shape enclosingKey requires.
// The name states that invariant because enclosingKey depends on it silently and would
// return wrong answers, not errors, without it.
//
// buf is reused across documents (truncated, not appended to) so the whole parse costs
// one amortized allocation rather than one per document.
func sortedEnclosing(buf []enclosingDef, doc *scip.Document) []enclosingDef {
	dst := buf[:0]
	for _, occ := range doc.Occurrences {
		if occ.SymbolRoles&int32(scip.SymbolRole_Definition) == 0 {
			continue
		}
		// EnclosingSourceRange, not the EnclosingRange field: scip-go emits only the
		// deprecated packed form, so reading the typed oneof directly would find nothing.
		r, ok := occ.EnclosingSourceRange()
		if !ok {
			continue
		}
		if occ.Symbol == "" || scip.IsLocalSymbol(occ.Symbol) {
			continue
		}
		info, ok := parseMoniker(occ.Symbol)
		if !ok {
			continue
		}
		dst = append(dst, enclosingDef{rng: r, key: info.Key})
	}
	slices.SortFunc(dst, func(a, b enclosingDef) int {
		if c := a.rng.Start.Compare(b.rng.Start); c != 0 {
			return c
		}
		return b.rng.End.Compare(a.rng.End) // outer (longer) first on a shared start
	})
	for i := range dst {
		dst[i].maxEnd = dst[i].rng.End
		if i > 0 && dst[i-1].maxEnd.Compare(dst[i].maxEnd) > 0 {
			dst[i].maxEnd = dst[i-1].maxEnd
		}
	}
	return dst
}

// ParseIndex decodes a SCIP index (protobuf bytes) into per-symbol records - it does
// no I/O, the caller supplies the bytes. Occurrence granularity is collapsed to per
// (file, symbol): one Defs/Refs entry per file, with a count and a capped line list,
// so a hot symbol yields at most one edge per file rather than one per occurrence.
// `local N` symbols (index-scoped, not stable across builds) are skipped; the version
// segment is dropped from the key so a dependency bump does not churn every symbol
// node. Output is sorted by key for deterministic assembly.
//
// Calls are attributed the same way, from the same occurrences: a reference that falls
// inside a definition's enclosing range was written in that definition's body, so the
// enclosing symbol is the caller. Collapsed per (caller, callee), restricted to callees
// the workspace defines and that are callable at all (see isCallableSuffix). An indexer that
// emits no enclosing ranges yields no calls, which is the honest result - nothing here
// reconstructs a call from source text.
//
// declaredLanguage is the language the producing spell adapts, used when the index does
// not say. SCIP makes Document.Language optional and scip-typescript sets it on nothing,
// so reading the index alone leaves every TypeScript symbol unlabeled and
// `magus query language:typescript` empty while `language:go` works. The document wins
// when it has a value - an index may legitimately span languages - and the declaration
// fills the gap rather than overriding it.
//
// projectPath is the ingested project's workspace-relative path. SCIP document paths are
// relative to the INDEXER's root (the project dir, where magus runs the indexer), so
// each is joined onto projectPath to become workspace-relative - the same spine the buzz
// file nodes and project->file edges use, so a nested project's symbols land on the right
// files instead of dangling at the workspace root. projectPath "" or "." leaves paths
// unchanged (the root project's paths are already workspace-relative).
func ParseIndex(ctx context.Context, data []byte, projectPath, declaredLanguage string) ([]types.KnowledgeSymbol, error) {
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
			if info, ok := parseMoniker(si.Symbol); ok {
				infoByKey[info.Key] = si
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

	// calls is caller key -> callee key -> attributed occurrence count. It lives here
	// rather than on acc because a callee can be seen before the enclosing definition's
	// own occurrence has created that accumulator; folding it in at output time makes the
	// result independent of occurrence order within a document.
	//
	// It therefore buffers callees the workspace does not define, which sortedCalls drops
	// at output. Filtering earlier is not possible - a callee's own definition may appear
	// in a later document - and measured on this repo's index the buffering is 34728 pairs
	// held to keep 12909, roughly 1-2 MB against a parse that already allocates 14 MB. A
	// two-pass restructure would halve that and double the document walk; the numbers do
	// not justify it.
	calls := map[string]map[string]int{}
	var encl []enclosingDef

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
		// Canonicalize once per document: indexers disagree on casing ("Go" vs "go",
		// "TypeScript"), so lowercasing lines it up with the spells' declared values and
		// makes `language:` a reliable join across both.
		docLanguage := strings.ToLower(strings.TrimSpace(doc.Language))
		if docLanguage == "" {
			docLanguage = strings.ToLower(strings.TrimSpace(declaredLanguage))
		}
		encl = sortedEnclosing(encl, doc)
		for _, occ := range doc.Occurrences {
			moniker := occ.Symbol
			if moniker == "" || scip.IsLocalSymbol(moniker) {
				continue
			}
			info, ok := parseMoniker(moniker)
			if !ok {
				continue
			}
			key := info.Key
			a := byKey[key]
			if a == nil {
				a = &acc{
					sym:  types.KnowledgeSymbol{Key: key, Moniker: moniker, Label: info.Label, Language: docLanguage},
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
					// The body's extent, from the same occurrence, so the pair bounds the
					// definition. EnclosingSourceRange (not the EnclosingRange field) for
					// the reason given at sortedEnclosing: scip-go emits only the
					// deprecated packed form. Absent for indexers that emit no enclosing
					// range, and 0 is how that says so.
					if r, ok := occ.EnclosingSourceRange(); ok && r.End.Line >= 0 {
						a.sym.DefEndLine = int(r.End.Line) + 1
					}
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
				attributeCall(calls, encl, occ, key, info.Callable)
			}
		}
	}

	// The set of keys this workspace defines, resolved once: a callee's own definition
	// may sit in any document, so membership is only knowable after the whole walk.
	defined := make(map[string]bool, len(byKey))
	for k, a := range byKey {
		if len(a.defs) > 0 {
			defined[k] = true
		}
	}

	out := make([]types.KnowledgeSymbol, 0, len(byKey))
	for _, a := range byKey {
		a.sym.Defs = sortedKeys(a.defs)
		a.sym.Refs = sortedRefs(a.refs)
		a.sym.Calls = sortedCalls(calls[a.sym.Key], defined)
		out = append(out, a.sym)
	}
	// byKey iteration is unordered; the sort is what makes the output deterministic.
	slices.SortFunc(out, func(x, y types.KnowledgeSymbol) int { return cmp.Compare(x.Key, y.Key) })
	if skipped > 0 {
		// Dropping a dependency document is normal scip-go output, not a fault - but an
		// index filtered to nothing looks exactly like a scip target that never ran, and
		// the caller logs nothing for an empty result. The count is what tells those
		// apart when an indexer's root does not match what magus assumed.
		slog.DebugContext(ctx, "symbols: skipped documents outside the workspace",
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

// monikerInfo is what a parsed SCIP moniker yields: the stable node key, a display label,
// and whether the symbol can be called.
//
// A record rather than a widening return tuple. The parse outcome and the symbol's own
// properties are different kinds of fact, and returning them as adjacent bools put
// `callable` and `ok` side by side on a path that already reads `key, _, _, ok :=` -
// where transposing them compiles, vets, and silently reclassifies every unparsable
// moniker as merely uncallable.
type monikerInfo struct {
	Key      string
	Label    string
	Callable bool
}

// parseMoniker turns a SCIP moniker into a stable, version-free node key and a display
// label. The key is the package manager and name plus the descriptor path, deliberately
// excluding the package VERSION so a dependency bump does not rename every symbol - but
// including the manager so two ecosystems that share a package name (npm foo vs gomod
// foo) do not collide. A local or unparsable moniker yields ok=false (the caller skips it).
//
// Callability rides along rather than being asked for separately because this already
// holds the parsed descriptors and the reference loop calls it for every occurrence; a
// second ParseSymbol per occurrence would put a full moniker parse on the hot path.
func parseMoniker(moniker string) (info monikerInfo, ok bool) {
	sym, err := scip.ParseSymbol(moniker)
	if err != nil || sym.Package == nil {
		return monikerInfo{}, false
	}
	desc := scip.DescriptorOnlyFormatter.FormatSymbol(sym)
	pkg := strings.TrimSpace(sym.Package.Manager + " " + sym.Package.Name)
	info.Key = strings.TrimSpace(pkg + " " + desc)
	if n := len(sym.Descriptors); n > 0 {
		info.Label = sym.Descriptors[n-1].Name
		info.Callable = isCallableSuffix(sym.Descriptors[n-1].Suffix)
	}
	return info, true
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

// attributeCall records one reference occurrence as a call from whichever definition
// body encloses it, when the callee is something that can be called. Occurrences outside
// every body (a package-level declaration, an import) and self-references (recursion,
// which would mint a source==target edge) are dropped.
func attributeCall(calls map[string]map[string]int, encl []enclosingDef, occ *scip.Occurrence, calleeKey string, callable bool) {
	if len(encl) == 0 || !callable {
		return
	}
	r, ok := occ.SourceRange()
	if !ok {
		return
	}
	caller, ok := enclosingKey(encl, r.Start)
	if !ok || caller == calleeKey {
		return
	}
	m := calls[caller]
	if m == nil {
		m = map[string]int{}
		calls[caller] = m
	}
	m[calleeKey]++
}

// sortedCalls turns one caller's tallied callees into a deterministic slice, dropping any
// callee not in defined. Sorted by key, like sortedRefs, so a shard's bytes do not churn
// on map iteration order.
//
// A callee outside the workspace gets no call edge: it has no definition to navigate to,
// its usage is already recorded by the referencing file's `references` edge, and including
// it would make the hottest stdlib helpers the graph's top god nodes.
func sortedCalls(m map[string]int, defined map[string]bool) []types.KnowledgeSymbolCall {
	if len(m) == 0 {
		return nil
	}
	out := make([]types.KnowledgeSymbolCall, 0, len(m))
	for k, n := range m {
		if !defined[k] {
			continue
		}
		out = append(out, types.KnowledgeSymbolCall{Key: k, Count: n})
	}
	if len(out) == 0 {
		return nil
	}
	slices.SortFunc(out, func(x, y types.KnowledgeSymbolCall) int { return cmp.Compare(x.Key, y.Key) })
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
