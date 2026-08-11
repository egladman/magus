package symbols

import (
	"fmt"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/egladman/magus/types"
)

// monikerV1/V2 are the same symbol (example.com/foo Bar type) at two package
// versions; ParseIndex must collapse them to one ID (version stripped).
const (
	monikerV1 = "scip-go gomod example.com/foo v1 Bar#"
	monikerV2 = "scip-go gomod example.com/foo v2 Bar#"
)

func marshalIndex(tb testing.TB, idx *scip.Index) []byte {
	tb.Helper()
	b, err := proto.Marshal(idx)
	require.NoError(tb, err)
	return b
}

func TestParseIndexDefsRefsAndDedup(t *testing.T) {
	idx := &scip.Index{Documents: []*scip.Document{
		{
			RelativePath: "pkg/foo/foo.go",
			Language:     "go",
			Symbols:      []*scip.SymbolInformation{{Symbol: monikerV1, DisplayName: "Bar", Kind: scip.SymbolInformation_Type}},
			Occurrences: []*scip.Occurrence{
				{Symbol: monikerV1, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{10, 5, 8}},
				{Symbol: "local 3", Range: []int32{0, 0, 1}}, // local -> skipped
			},
		},
		{
			RelativePath: "pkg/baz/baz.go",
			Language:     "go",
			Occurrences: []*scip.Occurrence{
				{Symbol: monikerV1, Range: []int32{4, 0, 3}}, // ref, line 5
				{Symbol: monikerV2, Range: []int32{7, 0, 3}}, // same symbol, other version -> merges; line 8
			},
		},
	}}

	syms, err := ParseIndex(t.Context(), marshalIndex(t, idx), "", "")
	require.NoError(t, err)
	require.Len(t, syms, 1, "v1 and v2 collapse to one symbol")

	s := syms[0]
	assert.Equal(t, "gomod example.com/foo Bar#", s.Key, "manager kept, version stripped")
	assert.Equal(t, "Bar", s.Label, "display name from SymbolInformation")
	assert.Equal(t, "go", s.Language)
	assert.Equal(t, "pkg/foo/foo.go:11", s.Source, "1-based definition line")
	assert.Equal(t, []string{"pkg/foo/foo.go"}, s.Defs)

	require.Len(t, s.Refs, 1, "both refs are in one file -> one per-file entry")
	assert.Equal(t, "pkg/baz/baz.go", s.Refs[0].Path)
	assert.Equal(t, 2, s.Refs[0].Count, "per-file occurrence count")
	assert.Equal(t, []int{5, 8}, s.Refs[0].Lines)
}

// TestParseIndexTypedRange guards the fix for modern indexers: they set the typed
// range oneof and NOT the deprecated packed `range` field, so reading `range` alone
// would report line 0 everywhere. SourceRange must resolve the typed form.
func TestParseIndexTypedRange(t *testing.T) {
	defRange := scip.Range{Start: scip.Position{Line: 10, Character: 5}, End: scip.Position{Line: 10, Character: 8}}
	idx := &scip.Index{Documents: []*scip.Document{{
		RelativePath: "pkg/foo/foo.go",
		Occurrences: []*scip.Occurrence{
			{Symbol: monikerV1, SymbolRoles: int32(scip.SymbolRole_Definition), TypedRange: defRange.AsTypedRange()},
		},
	}}}
	syms, err := ParseIndex(t.Context(), marshalIndex(t, idx), "", "")
	require.NoError(t, err)
	require.Len(t, syms, 1)
	assert.Equal(t, "pkg/foo/foo.go:11", syms[0].Source, "typed range resolved to the 1-based line, not 0")
}

func TestParseIndexSkipsLocalAndUnparsable(t *testing.T) {
	idx := &scip.Index{Documents: []*scip.Document{{
		RelativePath: "a.go",
		Occurrences: []*scip.Occurrence{
			{Symbol: "local 1", Range: []int32{0, 0, 1}},
			{Symbol: "", Range: []int32{1, 0, 1}},
			{Symbol: "not a valid moniker", Range: []int32{2, 0, 1}},
		},
	}}}
	syms, err := ParseIndex(t.Context(), marshalIndex(t, idx), "", "")
	require.NoError(t, err)
	assert.Empty(t, syms, "local, empty, and unparsable monikers all skipped")
}

func TestParseIndexRefLineCap(t *testing.T) {
	occs := make([]*scip.Occurrence, 0, MaxRefLines+5)
	for i := 0; i < MaxRefLines+5; i++ {
		occs = append(occs, &scip.Occurrence{Symbol: monikerV1, Range: []int32{int32(i), 0, 1}})
	}
	idx := &scip.Index{Documents: []*scip.Document{{RelativePath: "big.go", Occurrences: occs}}}

	syms, err := ParseIndex(t.Context(), marshalIndex(t, idx), "", "")
	require.NoError(t, err)
	require.Len(t, syms, 1)
	assert.Equal(t, MaxRefLines+5, syms[0].Refs[0].Count, "count is exact")
	assert.Len(t, syms[0].Refs[0].Lines, MaxRefLines, "lines are capped")
}

// An indexer that reports no language leaves every symbol unlabelled, which silently
// empties `magus query language:<lang>` for that whole ecosystem - scip-typescript sets
// Document.Language on nothing. The spell's declared language is what magus already used
// to decide the project was symbol-capable, so it fills the gap.
func TestParseIndexFallsBackToDeclaredLanguage(t *testing.T) {
	idx := &scip.Index{Documents: []*scip.Document{{
		RelativePath: "src/main.ts",
		Language:     "", // what scip-typescript emits
		Occurrences: []*scip.Occurrence{
			{Symbol: monikerV1, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{0, 0, 3}},
		},
	}}}
	syms, err := ParseIndex(t.Context(), marshalIndex(t, idx), "", "typescript")
	require.NoError(t, err)
	require.Len(t, syms, 1)
	assert.Equal(t, "typescript", syms[0].Language)
}

// The document wins when it says something: an index may legitimately span languages, so
// the declaration fills a gap rather than overriding a fact.
func TestParseIndexDocumentLanguageBeatsDeclared(t *testing.T) {
	idx := &scip.Index{Documents: []*scip.Document{{
		RelativePath: "a.go",
		Language:     "Go", // and casing is canonicalized, so `language:go` matches
		Occurrences: []*scip.Occurrence{
			{Symbol: monikerV1, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{0, 0, 3}},
		},
	}}}
	syms, err := ParseIndex(t.Context(), marshalIndex(t, idx), "", "typescript")
	require.NoError(t, err)
	require.Len(t, syms, 1)
	assert.Equal(t, "go", syms[0].Language)
}

// No language anywhere stays empty rather than inventing one.
func TestParseIndexNoLanguageAnywhere(t *testing.T) {
	idx := &scip.Index{Documents: []*scip.Document{{
		RelativePath: "a.txt",
		Occurrences: []*scip.Occurrence{
			{Symbol: monikerV1, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{0, 0, 3}},
		},
	}}}
	syms, err := ParseIndex(t.Context(), marshalIndex(t, idx), "", "")
	require.NoError(t, err)
	require.Len(t, syms, 1)
	assert.Empty(t, syms[0].Language)
}

func TestParseIndexBadBytes(t *testing.T) {
	_, err := ParseIndex(t.Context(), []byte("not a protobuf"), "", "")
	assert.Error(t, err)
}

// TestParseIndexRebasesProjectPaths: a nested project's index emits paths relative to
// its own root; ParseIndex joins them onto the project path so they are workspace-
// relative and land on the same file nodes the rest of the graph uses.
func TestParseIndexRebasesProjectPaths(t *testing.T) {
	idx := &scip.Index{Documents: []*scip.Document{{
		RelativePath: "compiler.go", // indexer-relative, project is gopherbuzz
		Language:     "go",
		Occurrences: []*scip.Occurrence{
			{Symbol: monikerV1, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{0, 0, 3}},
			{Symbol: monikerV1, Range: []int32{4, 0, 3}, EnclosingRange: nil},
		},
	}}}
	syms, err := ParseIndex(t.Context(), marshalIndex(t, idx), "gopherbuzz", "")
	require.NoError(t, err)
	require.Len(t, syms, 1)
	assert.Equal(t, []string{"gopherbuzz/compiler.go"}, syms[0].Defs, "def path rebased under the project")
	assert.Equal(t, "gopherbuzz/compiler.go:1", syms[0].Source, "source path rebased under the project")
}

// callerMoniker/calleeMoniker are two distinct symbols used by the call-attribution
// tests: caller has a body, callee is invoked from inside it.
const (
	callerMoniker = "scip-go gomod example.com/foo v1 Caller()."
	calleeMoniker = "scip-go gomod example.com/foo v1 Callee()."
	fieldMoniker  = "scip-go gomod example.com/foo v1 Holder#field."
)

// fnDoc builds a document whose caller definition spans lines 0-9 and whose callee is
// defined on line 20, so a reference placed inside the caller's span attributes to it.
func fnDoc(refs ...*scip.Occurrence) *scip.Document {
	occs := []*scip.Occurrence{
		{
			Symbol:      callerMoniker,
			SymbolRoles: int32(scip.SymbolRole_Definition),
			Range:       []int32{0, 5, 11},
			// The packed 4-element form is what scip-go emits: [startLine, startChar,
			// endLine, endChar]. The typed oneof is never set, so reading it would find
			// nothing - that is the whole point of going through EnclosingSourceRange.
			EnclosingRange: []int32{0, 0, 9, 1},
		},
		{Symbol: calleeMoniker, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{20, 5, 11}},
		{Symbol: fieldMoniker, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{30, 5, 11}},
	}
	return &scip.Document{
		RelativePath: "pkg/foo/foo.go",
		Language:     "go",
		Symbols: []*scip.SymbolInformation{
			{Symbol: callerMoniker, DisplayName: "Caller", Kind: scip.SymbolInformation_Function},
			{Symbol: calleeMoniker, DisplayName: "Callee", Kind: scip.SymbolInformation_Function},
			{Symbol: fieldMoniker, DisplayName: "field", Kind: scip.SymbolInformation_Field},
		},
		Occurrences: append(occs, refs...),
	}
}

func callsOf(t *testing.T, syms []types.KnowledgeSymbol, key string) []types.KnowledgeSymbolCall {
	t.Helper()
	for _, s := range syms {
		if s.Key == key {
			return s.Calls
		}
	}
	t.Fatalf("no symbol %q in %d parsed", key, len(syms))
	return nil
}

// A reference inside the caller's enclosing range is attributed to it, and repeated
// occurrences collapse into one entry with a count.
func TestParseIndexAttributesCalls(t *testing.T) {
	idx := &scip.Index{Documents: []*scip.Document{fnDoc(
		&scip.Occurrence{Symbol: calleeMoniker, Range: []int32{3, 2, 8}},
		&scip.Occurrence{Symbol: calleeMoniker, Range: []int32{5, 2, 8}},
	)}}
	syms, err := ParseIndex(t.Context(), marshalIndex(t, idx), "", "")
	require.NoError(t, err)

	want := []types.KnowledgeSymbolCall{{Key: "gomod example.com/foo Callee().", Count: 2}}
	assert.Equal(t, want, callsOf(t, syms, "gomod example.com/foo Caller()."))
	assert.Empty(t, callsOf(t, syms, "gomod example.com/foo Callee()."), "the callee calls nothing")
}

// A reference outside every enclosing range - a package-level declaration, an import -
// belongs to no body and must not be attributed to whichever definition happens to be
// nearest.
func TestParseIndexCallsIgnoresOccurrencesOutsideAnyBody(t *testing.T) {
	idx := &scip.Index{Documents: []*scip.Document{fnDoc(
		&scip.Occurrence{Symbol: calleeMoniker, Range: []int32{15, 2, 8}},
	)}}
	syms, err := ParseIndex(t.Context(), marshalIndex(t, idx), "", "")
	require.NoError(t, err)
	assert.Empty(t, callsOf(t, syms, "gomod example.com/foo Caller()."))
}

// An enclosing range spans the whole declaration, signature included, so most occurrences
// inside it are types and fields rather than calls. Only a callable callee earns the edge.
func TestParseIndexCallsSkipsNonCallableCallee(t *testing.T) {
	idx := &scip.Index{Documents: []*scip.Document{fnDoc(
		&scip.Occurrence{Symbol: fieldMoniker, Range: []int32{3, 2, 8}},
	)}}
	syms, err := ParseIndex(t.Context(), marshalIndex(t, idx), "", "")
	require.NoError(t, err)
	assert.Empty(t, callsOf(t, syms, "gomod example.com/foo Caller()."), "a field reference is not a call")
}

// Recursion would mint a source==target edge, which the graph's edge key cannot express
// and no reader wants.
func TestParseIndexCallsDropsSelfEdge(t *testing.T) {
	idx := &scip.Index{Documents: []*scip.Document{fnDoc(
		&scip.Occurrence{Symbol: callerMoniker, Range: []int32{3, 2, 8}},
	)}}
	syms, err := ParseIndex(t.Context(), marshalIndex(t, idx), "", "")
	require.NoError(t, err)
	assert.Empty(t, callsOf(t, syms, "gomod example.com/foo Caller()."))
}

// A callee the workspace never defines has nothing to navigate to, so it gets no edge -
// its usage is still recorded by the referencing file's `references` edge.
func TestParseIndexCallsSkipsCalleeDefinedElsewhere(t *testing.T) {
	const external = "scip-go gomod example.com/dep v1 Helper()."
	doc := fnDoc(&scip.Occurrence{Symbol: external, Range: []int32{3, 2, 8}})
	doc.Symbols = append(doc.Symbols, &scip.SymbolInformation{
		Symbol: external, DisplayName: "Helper", Kind: scip.SymbolInformation_Function,
	})
	idx := &scip.Index{Documents: []*scip.Document{doc}}

	syms, err := ParseIndex(t.Context(), marshalIndex(t, idx), "", "")
	require.NoError(t, err)
	assert.Empty(t, callsOf(t, syms, "gomod example.com/foo Caller()."))
}

// Nesting is what the innermost-wins walk exists for. Go cannot produce it (a FuncDecl
// never nests), but scip-typescript does on every closure and class method, and this
// package is written to be indexer-agnostic.
func TestParseIndexCallsAttributesToInnermostBody(t *testing.T) {
	const outer = "scip-go gomod example.com/foo v1 Outer()."
	const inner = "scip-go gomod example.com/foo v1 Inner()."
	idx := &scip.Index{Documents: []*scip.Document{{
		RelativePath: "pkg/foo/foo.go",
		Language:     "go",
		Symbols: []*scip.SymbolInformation{
			{Symbol: outer, DisplayName: "Outer", Kind: scip.SymbolInformation_Function},
			{Symbol: inner, DisplayName: "Inner", Kind: scip.SymbolInformation_Function},
			{Symbol: calleeMoniker, DisplayName: "Callee", Kind: scip.SymbolInformation_Function},
		},
		Occurrences: []*scip.Occurrence{
			{Symbol: outer, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{0, 5, 10}, EnclosingRange: []int32{0, 0, 20, 1}},
			{Symbol: inner, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{5, 5, 10}, EnclosingRange: []int32{5, 0, 10, 1}},
			{Symbol: calleeMoniker, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{30, 5, 11}},
			{Symbol: calleeMoniker, Range: []int32{7, 2, 8}},  // inside inner, so inside outer too
			{Symbol: calleeMoniker, Range: []int32{15, 2, 8}}, // inside outer only
		},
	}}}
	syms, err := ParseIndex(t.Context(), marshalIndex(t, idx), "", "")
	require.NoError(t, err)

	want := []types.KnowledgeSymbolCall{{Key: "gomod example.com/foo Callee().", Count: 1}}
	assert.Equal(t, want, callsOf(t, syms, "gomod example.com/foo Inner()."), "innermost body wins")
	assert.Equal(t, want, callsOf(t, syms, "gomod example.com/foo Outer()."), "outer keeps only what inner does not enclose")
}

// An indexer that emits no enclosing ranges gives no calls rather than a guess.
func TestParseIndexNoEnclosingRangeYieldsNoCalls(t *testing.T) {
	doc := fnDoc(&scip.Occurrence{Symbol: calleeMoniker, Range: []int32{3, 2, 8}})
	//nolint:staticcheck // the deprecated packed field is the ONE scip-go emits; clearing
	// the typed oneof instead would test a shape no real index has.
	doc.Occurrences[0].EnclosingRange = nil
	idx := &scip.Index{Documents: []*scip.Document{doc}}

	syms, err := ParseIndex(t.Context(), marshalIndex(t, idx), "", "")
	require.NoError(t, err)
	for _, s := range syms {
		assert.Empty(t, s.Calls, "%s", s.Key)
	}
}

// syntheticIndex builds a marshalled index of docs documents, each holding funcsPerDoc
// function definitions with refsPerFunc references inside their bodies, plus a block of
// package-level references that sit inside no body at all.
//
// That last block is the point of the fixture, not padding: an occurrence enclosed by
// nothing is the case that would scan every definition in the document if the backward
// walk were unbounded, and it is the majority case in real code. enclosing controls
// whether the definitions carry an enclosing range, so the two sub-benchmarks isolate
// exactly what attribution costs.
func syntheticIndex(tb testing.TB, docs, funcsPerDoc, refsPerFunc int, enclosing bool) []byte {
	tb.Helper()
	const callee = "scip-go gomod example.com/bench v1 Callee()."
	idx := &scip.Index{}
	for d := range docs {
		span := int32(refsPerFunc + 2)
		doc := &scip.Document{
			RelativePath: fmt.Sprintf("pkg/p%d/f.go", d),
			Language:     "go",
			Symbols:      []*scip.SymbolInformation{{Symbol: callee, DisplayName: "Callee", Kind: scip.SymbolInformation_Function}},
			Occurrences: []*scip.Occurrence{
				{Symbol: callee, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{0, 0, 6}},
			},
		}
		for f := range funcsPerDoc {
			start := int32(f+1) * span
			sym := fmt.Sprintf("scip-go gomod example.com/bench v1 Fn%d().", f)
			doc.Symbols = append(doc.Symbols, &scip.SymbolInformation{
				Symbol: sym, DisplayName: fmt.Sprintf("Fn%d", f), Kind: scip.SymbolInformation_Function,
			})
			def := &scip.Occurrence{Symbol: sym, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{start, 5, 9}}
			if enclosing {
				//nolint:staticcheck // see above: real indexes set the packed field, not the oneof.
				def.EnclosingRange = []int32{start, 0, start + span - 1, 1}
			}
			doc.Occurrences = append(doc.Occurrences, def)
			for r := range refsPerFunc {
				doc.Occurrences = append(doc.Occurrences, &scip.Occurrence{
					Symbol: callee, Range: []int32{start + int32(r) + 1, 2, 8},
				})
			}
		}
		// Package-level references, past every body: enclosed by nothing.
		tail := int32(funcsPerDoc+1) * span
		for r := range refsPerFunc {
			doc.Occurrences = append(doc.Occurrences, &scip.Occurrence{
				Symbol: callee, Range: []int32{tail + int32(r), 0, 6},
			})
		}
		idx.Documents = append(idx.Documents, doc)
	}
	return marshalIndex(tb, idx)
}

// BenchmarkParseIndex guards the attribution walk against going quadratic. The claim it
// defends: per-reference cost grows with nesting depth and log(definitions), not with the
// document's definition count - so sweeping funcsPerDoc by 64x must not cost anything
// close to 64x per reference.
func BenchmarkParseIndex(b *testing.B) {
	for _, enclosing := range []bool{false, true} {
		for _, funcs := range []int{8, 64, 512} {
			name := fmt.Sprintf("enclosing=off/funcsPerDoc=%d", funcs)
			if enclosing {
				name = fmt.Sprintf("enclosing=on/funcsPerDoc=%d", funcs)
			}
			data := syntheticIndex(b, 4, funcs, 8, enclosing)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := ParseIndex(b.Context(), data, ".", ""); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func TestParseMonikerStripsVersion(t *testing.T) {
	id1, label, _, ok := parseMoniker(monikerV1)
	require.True(t, ok)
	id2, _, _, ok := parseMoniker(monikerV2)
	require.True(t, ok)
	assert.Equal(t, id1, id2, "the two versions share one ID")
	assert.Equal(t, "Bar", label)

	_, _, _, ok = parseMoniker("local 5")
	assert.False(t, ok)
}

// Callability is read off SCIP's descriptor grammar, not off the optional
// SymbolInformation.Kind. The grammar is normative in the spec (`<method> ::= <name> '('
// (<method-disambiguator>)? ').'`), so it holds for every conforming indexer, while Kind
// is optional and routinely unset - scip-typescript populates it on none of this
// workspace's 9137 console symbols, so a kind-based rule produced no calls at all for
// TypeScript while looking like it worked.
//
// The cases below cover every indexer magus declares a spell for, plus two taken verbatim
// from the scip bindings' own parser fixtures. This is the guard against a new indexer
// silently contributing no call edges: if one of these stops classifying, the language it
// stands for has gone dark.
//
// Note the dependency this does NOT add. parseMoniker already requires the grammar - it
// reads Descriptors[n-1].Name for the label - so a moniker that does not conform fails
// ParseSymbol, yields ok=false, and the symbol never enters the graph at all. The suffix
// is a field of a structure ingestion already requires to be well formed.
func TestParseMonikerReportsCallableFromDescriptor(t *testing.T) {
	for _, tc := range []struct {
		name     string
		moniker  string
		callable bool
	}{
		{"go func", "scip-go gomod example.com/foo v1 Caller().", true},
		{"go method", "scip-go gomod example.com/foo v1 Holder#Method().", true},
		{"go struct", "scip-go gomod example.com/foo v1 Bar#", false},
		{"go field", "scip-go gomod example.com/foo v1 Holder#field.", false},
		{"go package", "scip-go gomod example.com/foo v1 `example.com/foo/pkg`/", false},

		{"typescript method", "scip-typescript npm console 0.0.1 `src/main.ts`/render().", true},
		{"typescript term", "scip-typescript npm console 0.0.1 `src/main.ts`/config.", false},
		{"typescript type", "scip-typescript npm console 0.0.1 `src/main.ts`/Props#", false},

		{"python function", "scip-python python mypkg 1.0 mymodule/handler().", true},
		{"python class", "scip-python python mypkg 1.0 mymodule/Handler#", false},
		{"python attribute", "scip-python python mypkg 1.0 mymodule/Handler#value.", false},

		{"rust function", "rust-analyzer cargo mycrate 0.1.0 mymod/run().", true},
		{"rust struct", "rust-analyzer cargo mycrate 0.1.0 mymod/Config#", false},
		// Verbatim from the scip bindings' parser fixtures. A macro invocation IS a call
		// in the languages that have them, which is why Macro joins Method.
		{"rust macro", "rust-analyzer cargo std 1.0.0 macros/println!", true},
		{"cxx method with disambiguator", "cxx . todo-pkg todo-version gfx/Rect#x(455f465bc33b4cdf).", true},

		// A method's PARAMETER is not callable: the last descriptor decides, not the path.
		{"parameter of a method", "scip-go gomod example.com/foo v1 Caller().(arg)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, callable, ok := parseMoniker(tc.moniker)
			require.True(t, ok, "moniker should parse")
			assert.Equal(t, tc.callable, callable)
		})
	}
}

// TestWorkspacePath pins the containment rule on indexer output.
//
// scip-go records occurrences in the packages it resolved, so a document path can point
// into the module or build cache. Those paths flowed into the knowledge graph as real
// nodes: a committed graph carried 93 of them, bottoming out in one developer's
// ~/Library/Caches/go-build shards.
func TestWorkspacePath(t *testing.T) {
	tests := []struct {
		name    string
		project string
		rel     string
		want    string
		wantOK  bool
	}{
		{name: "root project path", project: ".", rel: "pkg/foo.go", want: "pkg/foo.go", wantOK: true},
		{name: "empty project path", project: "", rel: "./pkg/foo.go", want: "pkg/foo.go", wantOK: true},
		{name: "nested project rebases", project: "libs/gopherbuzz", rel: "pool.go", want: "libs/gopherbuzz/pool.go", wantOK: true},
		{name: "nested project cleans", project: "libs/gopherbuzz", rel: "./sub/../pool.go", want: "libs/gopherbuzz/pool.go", wantOK: true},

		{name: "root project escape", project: ".", rel: "../../../../../Library/Caches/go-build/01/x", wantOK: false},
		{name: "bare parent", project: ".", rel: "..", wantOK: false},
		{name: "absolute path", project: ".", rel: "/Users/someone/go/pkg/mod/x.go", wantOK: false},
		{name: "empty", project: ".", rel: "", wantOK: false},
		{name: "dot", project: ".", rel: ".", wantOK: false},

		// The ".." count here is WITHIN the project's depth, which is what makes it the
		// interesting case: validating after path.Join cancels it against the project
		// path and yields "internal/interp/x.go" with nothing left to detect, silently
		// re-attributing a document to a project that never owned it. A fixture that
		// overshoots the depth (four ".." here) is rejected either way and proves
		// nothing.
		{name: "nested project escape within its own depth", project: "libs/gopherbuzz", rel: "../../internal/interp/x.go", wantOK: false},
		{name: "nested project escape past its depth", project: "libs/gopherbuzz", rel: "../../../../escape.go", wantOK: false},
		{name: "nested project escape by one", project: "libs/gopherbuzz", rel: "../../../escape.go", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := workspacePath(tt.project, tt.rel)
			assert.Equal(t, tt.wantOK, ok)
			if !tt.wantOK {
				assert.Empty(t, got, "a refused path returns no path, so a caller ignoring ok cannot use one")
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestParseIndexSkipsEscapingDocuments checks the whole ingest, not just the helper: a
// dependency document the indexer resolved outside the workspace contributes nothing,
// while the in-workspace document beside it is unaffected.
func TestParseIndexSkipsEscapingDocuments(t *testing.T) {
	idx := &scip.Index{Documents: []*scip.Document{
		{
			RelativePath: "../../../../../Library/Caches/go-build/01/dep.go",
			Language:     "go",
			Occurrences: []*scip.Occurrence{
				{Symbol: monikerV2, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{0, 0, 3}},
			},
		},
		{
			RelativePath: "pkg/foo.go",
			Language:     "go",
			Occurrences: []*scip.Occurrence{
				{Symbol: monikerV1, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{7, 0, 3}},
			},
		},
	}}

	syms, err := ParseIndex(t.Context(), marshalIndex(t, idx), ".", "")
	require.NoError(t, err)
	require.Len(t, syms, 1, "only the in-workspace document contributes")
	assert.Equal(t, []string{"pkg/foo.go"}, syms[0].Defs)
	for _, s := range syms {
		assert.NotContains(t, s.Source, "..", "no symbol sources outside the workspace")
		for _, d := range s.Defs {
			assert.NotContains(t, d, "..")
		}
		for _, r := range s.Refs {
			assert.NotContains(t, r.Path, "..")
		}
	}
}
