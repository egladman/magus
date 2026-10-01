package knowledge

import (
	"cmp"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/types"
)

// Symbol ingestion is EXTRACTED, never inferred: the symbol nodes and their
// defines/references edges come straight from a SCIP index a per-language indexer
// produced. This assembler is types-only (the SCIP parsing lives in internal/symbols
// and reaches here as neutral types.KnowledgeSymbol records), so internal/graph/knowledge
// stays free of the SCIP dependency. Per the scale plan, symbol shards are per
// project and, once wired, loaded lazily; this file only builds the shard.

// symbolsShardSuffix names a project's symbol shard: "<project>@symbols". The "@"
// keeps it out of the plain-project namespace and marks it as lazily loaded. A project's
// symbols defined below its own directory are split into one shard per defining directory,
// "<project>@symbols:<dir>" (see splitSymbolShard).
const symbolsShardSuffix = "@symbols"

// symbolsShardName returns the shard name for a project's ingested symbols.
func symbolsShardName(project string) string { return project + symbolsShardSuffix }

// isSymbolsShard reports whether a shard name is a symbol shard, a project's base shard or
// one of its directory shards: the shards excluded from the default (non-symbol-seeded)
// load path.
func isSymbolsShard(name string) bool {
	return strings.HasSuffix(name, symbolsShardSuffix) || strings.Contains(name, symbolsShardSuffix+":")
}

// symbolsShardKey is a symbol shard's name less the suffix: the project for its base
// shard, "<project>:<dir>" for a directory shard.
func symbolsShardKey(name string) string { return strings.Replace(name, symbolsShardSuffix, "", 1) }

// symbolsShardProject is the project a symbol shard belongs to.
func symbolsShardProject(name string) string {
	return name[:strings.Index(name, symbolsShardSuffix)]
}

// splitSymbolShard partitions one project's assembled symbol shard by where each symbol is
// defined, so a lookup that knows its symbol decodes that directory's shard rather than the
// project's: in this repository the root project's single shard was 106 MB.
//
// A symbol goes to the shard of the directory holding its definition, together with every
// edge that ends at it (its defines, its references, the calls into it), which is exactly
// what `refs` reads. Symbols defined at the project's top level or not in the workspace at
// all, the file and directory nodes, and every other edge stay in the base shard, so a
// small project keeps one shard named as before.
//
// The split is a partition of the deduplicated shard: no node or edge lands in two shards,
// so merging every part rebuilds the unsplit shard exactly, whatever order they merge in.
func splitSymbolShard(project string, sh Shard) []Shard {
	g := NewGraph()
	g.Merge(sh.Nodes, sh.Edges)
	dirOf := map[string]string{}
	for _, n := range g.Nodes() {
		if n.Kind != types.KindSymbol {
			continue
		}
		if file, _, ok := splitPathLine(n.Source); ok {
			if d := path.Dir(file); d != project && d != "." {
				dirOf[n.ID] = d
			}
		}
	}
	// Each part takes its nodes and edges in the order the merged graph lists them, so a
	// part is already in canonical form and fingerprinting it need not merge it again.
	parts := map[string]*Shard{"": {Name: sh.Name, canonical: true}}
	part := func(dir string) *Shard {
		p := parts[dir]
		if p == nil {
			p = &Shard{Name: sh.Name + ":" + dir, canonical: true}
			parts[dir] = p
		}
		return p
	}
	for _, n := range g.Nodes() {
		p := part(dirOf[n.ID])
		p.Nodes = append(p.Nodes, n)
	}
	for _, e := range g.Edges() {
		dir, ok := dirOf[e.Target]
		if !ok {
			dir = dirOf[e.Source]
		}
		p := part(dir)
		p.Edges = append(p.Edges, e)
	}
	out := make([]Shard, 0, len(parts))
	for _, dir := range slices.Sorted(maps.Keys(parts)) {
		out = append(out, *parts[dir])
	}
	return out
}

// assembleSymbols builds one project's symbol shard from the ingested records: a
// symbol node per record, a `defines` edge from each defining file, a
// `references` edge from each using file (one per file, carrying the occurrence
// count and capped lines in its provenance), and a `calls` edge to each callee the
// record's body invokes. A callee always has a node in THIS shard (the call was found
// through a reference occurrence in this project's index, so the parse minted a record
// for it and the node loop above emitted it), which is what lets the derived xref route
// call queries with no changes of its own. It also materializes a `file` node for
// every path the index touched (so a SCIP-indexed source file is a browsable node
// the def/ref edges land on, not a dangling ID) and links each to the project that
// owns it (longest-prefix over the full project list, so a cross-project reference
// file is parented to its own project, not this shard's). File nodes and their
// project links ride in this lazy shard, so they surface on symbol-seeded queries;
// AddNode/AddEdge dedup by ID, so a file two indexes share merges cleanly. A record
// with neither a def nor a ref in the workspace still yields its node so an explain
// has something to land on.
func assembleSymbols(project string, syms []types.KnowledgeSymbol, projects []types.TargetGraphProject) Shard {
	s := Shard{Name: symbolsShardName(project)}
	seenFiles := map[string]bool{}
	// A file several symbols define is sized by each of them from one read, so the first
	// record that carries a size is as good as any.
	sizes := map[string][2]int{}
	for _, sym := range syms {
		if sym.SourceLines == 0 && sym.SourceBytes == 0 {
			continue
		}
		if path, _, ok := strings.Cut(sym.Source, ":"); ok {
			sizes[path] = [2]int{sym.SourceLines, sym.SourceBytes}
		}
	}
	noteFile := func(path, language string) {
		if path == "" || seenFiles[path] {
			return
		}
		seenFiles[path] = true
		var attrs map[string]string
		if language != "" {
			attrs = map[string]string{attrLanguage: language}
		}
		if size, ok := sizes[path]; ok {
			attrs = fileSizeAttrs(attrs, size[0], size[1])
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
			attrs[attrLanguage] = sym.Language
		}
		if sym.SymbolKind != "" {
			attrs[attrSymbolKind] = sym.SymbolKind
		}
		if sym.Moniker != "" {
			attrs["moniker"] = sym.Moniker
		}
		// Tested-by lens: how many referencing files are tests. Derived from the same
		// SCIP reference edges (no new data), so it rides this deterministic shard rather
		// than the observed coverage overlay. Absent (0) means no test directly names the
		// symbol, a coverage-independent hint that a symbol may be under-tested.
		if n := testRefCount(sym.Refs); n > 0 {
			attrs[attrTestRefs] = strconv.Itoa(n)
		}
		if sym.DefEndLine > 0 {
			attrs[attrDefEndLine] = strconv.Itoa(sym.DefEndLine)
		}
		if sym.Namespace != "" {
			attrs[attrNamespace] = symbolID(sym.Namespace)
		}
		if sym.Signature != "" {
			attrs[AttrSignature] = sym.Signature
		}
		if sym.BodyDigest != "" {
			attrs[AttrBodyDigest] = sym.BodyDigest
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
		for _, c := range sym.Calls {
			s.Edges = append(s.Edges, extractedEdge(sID, symbolID(c.Key), types.RelationCalls, callProvenance(c)))
		}
	}
	return s
}

// assembleSymbolShards builds one symbol shard per project in symbols, in sorted project
// order, and stores in each the dir -imports-> dir edges foldImports read from that
// project's index. A project whose index yields no node gets no shard.
func assembleSymbolShards(symbols map[string][]types.KnowledgeSymbol, projects []types.TargetGraphProject) []Shard {
	imports := foldImports(symbols)
	var out []Shard
	for _, project := range slices.Sorted(maps.Keys(symbols)) {
		s := assembleSymbols(project, symbols[project], projects)
		if len(s.Nodes) == 0 {
			continue
		}
		s.Nodes = append(s.Nodes, imports[project].nodes...)
		s.Edges = append(s.Edges, imports[project].edges...)
		out = append(out, s)
	}
	return out
}

// foldedImports is one project's share of foldImports: the edges its index recorded and a
// dir node per endpoint carrying the package's language, so an edge never outlives its
// endpoints when only some symbol shards are loaded.
type foldedImports struct {
	nodes []types.KnowledgeNode
	edges []types.KnowledgeEdge
}

// foldImports turns the package imports every index recorded into dir -imports-> dir
// edges, keyed by the project whose index read each import. An indexer emits an import as
// a reference to the package's namespace symbol, which every file of a package defines,
// so a file defining A and referencing B is A importing B. No import syntax is parsed.
//
// A namespace's package is the lowest directory holding a non-test file that defines it,
// read across every index, since an import's target is often in another project's index.
// Test files are left out (a test may import what its package cannot), as are
// self-imports and namespaces no workspace file defines. Each edge carries AttrLanguage
// and, as provenance, the lexically first source it was read from.
func foldImports(symbols map[string][]types.KnowledgeSymbol) map[string]foldedImports {
	nsDir := map[string]string{}
	nsLang := map[string]string{}
	fileNS := map[string]map[string]bool{}
	symNS := map[string]string{}
	for _, syms := range symbols {
		for _, sym := range syms {
			if sym.Namespace != "" {
				symNS[sym.Key] = sym.Namespace
			}
			if sym.Key == "" || sym.Namespace != sym.Key {
				continue
			}
			if l := sym.Language; l != "" && (nsLang[sym.Key] == "" || l < nsLang[sym.Key]) {
				nsLang[sym.Key] = l
			}
			for _, def := range sym.Defs {
				if isTestSource(def) {
					continue
				}
				if fileNS[def] == nil {
					fileNS[def] = map[string]bool{}
				}
				fileNS[def][sym.Key] = true
				if d, cur := path.Dir(def), nsDir[sym.Key]; cur == "" || d < cur {
					nsDir[sym.Key] = d
				}
			}
		}
	}

	type fold struct{ lang, evidence string }
	found := map[string]map[[2]string]fold{}
	add := func(project, from, to, evidence string) {
		fd, td := nsDir[from], nsDir[to]
		if fd == "" || td == "" || fd == td {
			return
		}
		k := [2]string{fd, td}
		if found[project] == nil {
			found[project] = map[[2]string]fold{}
		}
		f, seen := found[project][k]
		if lang := nsLang[from]; !seen || lang != "" && (f.lang == "" || lang < f.lang) {
			f.lang = lang
		}
		if !seen || evidence < f.evidence {
			f.evidence = evidence
		}
		found[project][k] = f
	}
	for project, syms := range symbols {
		for _, sym := range syms {
			if sym.Key != "" && sym.Namespace == sym.Key {
				for _, ref := range sym.Refs {
					if isTestSource(ref.Path) {
						continue
					}
					for from := range fileNS[ref.Path] {
						add(project, from, sym.Key, ref.Path)
					}
				}
			}
			if sym.Namespace == "" || isTestSource(sym.Source) {
				continue
			}
			for _, c := range sym.Calls {
				add(project, sym.Namespace, symNS[c.Key], sym.Source)
			}
		}
	}

	dirLang := map[string]string{}
	for ns, d := range nsDir {
		if l := nsLang[ns]; l != "" && (dirLang[d] == "" || l < dirLang[d]) {
			dirLang[d] = l
		}
	}
	out := make(map[string]foldedImports, len(found))
	for project, pairs := range found {
		var fi foldedImports
		ends := map[string]bool{}
		for _, k := range slices.SortedFunc(maps.Keys(pairs), func(a, b [2]string) int {
			return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1]))
		}) {
			f := pairs[k]
			e := extractedEdge(dirID(k[0]), dirID(k[1]), types.RelationImports, f.evidence)
			if f.lang != "" {
				e.Attrs = map[string]string{types.AttrLanguage: f.lang}
			}
			fi.edges = append(fi.edges, e)
			ends[k[0]], ends[k[1]] = true, true
		}
		for _, d := range slices.Sorted(maps.Keys(ends)) {
			n := types.KnowledgeNode{ID: dirID(d), Kind: types.KindDir, Label: d, Source: d}
			if dirLang[d] != "" {
				n.Attrs = map[string]string{types.AttrLanguage: dirLang[d]}
			}
			fi.nodes = append(fi.nodes, n)
		}
		out[project] = fi
	}
	return out
}

// testRefCount counts the referencing files that are test files. One entry per file (SCIP
// collapses a file's occurrences), so this is the number of distinct test files that name
// the symbol, not the raw occurrence count.
func testRefCount(refs []types.KnowledgeSymbolRef) int {
	n := 0
	for _, ref := range refs {
		if isTestSource(ref.Path) {
			n++
		}
	}
	return n
}

// testSuffixes and testPrefixes are the naming conventions that mark a file as a test in
// the languages a SCIP indexer covers. By NAME, because the graph is language-agnostic and
// a name is the one thing every language's convention agrees can carry it.
var (
	testSuffixes = []string{"_test.go", ".test.ts", ".test.tsx", ".test.js", ".spec.ts", ".spec.tsx", ".spec.js", "_test.py"}
	testPrefixes = []string{"test_"}
)

// isTestSource reports whether a path (or a symbol Source, "<path>:<line>") is a test
// file. One predicate for every caller in this package, so the Go-only suffix check that
// used to live inline in testRefCount cannot drift from the rule the lenses apply.
func isTestSource(source string) bool {
	path, _, _ := strings.Cut(source, ":")
	base := path[strings.LastIndex(path, "/")+1:]
	for _, s := range testSuffixes {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	for _, p := range testPrefixes {
		if strings.HasPrefix(base, p) && strings.HasSuffix(base, ".py") {
			return true
		}
	}
	return false
}

// refProvenance encodes a reference's occurrence count and capped line list into the
// edge provenance string, e.g. "scip count=3 lines=10,20". KnowledgeEdge has only a
// flat Provenance string (no attr map), so `magus refs` reads the count/lines back
// with parseRefProvenance; the two are kept together so the format has one home
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

// callProvenance encodes a call edge's attributed occurrence count in the same
// "scip count=N" shape refProvenance uses, so parseRefProvenance decodes both and there
// is one SCIP provenance format rather than two near-identical ones. It carries no lines:
// the call sites are already on the caller file's `references` edge, and repeating them
// per (caller, callee) pair would be pure shard weight. What distinguishes a call from a
// reference is the RELATION and the endpoint kinds, never the provenance.
func callProvenance(c types.KnowledgeSymbolCall) string {
	return refProvenancePrefix + "count=" + strconv.Itoa(c.Count)
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
